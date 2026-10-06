package store

import (
	"context"
	"time"
)

// schemaV6: what apply created in the user's repos (each round branch and
// worktree), kept apart from the rounds so pruning never forgets one, and
// crossed off when sift clean removes it. The branches already applied are
// recorded from the applies table.
const schemaV6 = `
CREATE TABLE created (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  kind       TEXT NOT NULL,
  round_id   INTEGER NOT NULL,
  repo       TEXT NOT NULL,
  name       TEXT NOT NULL,
  base       TEXT NOT NULL DEFAULT '',
  commit_sha TEXT NOT NULL DEFAULT '',
  pushed     INTEGER NOT NULL DEFAULT 0,
  pr         TEXT NOT NULL DEFAULT '',
  at         INTEGER NOT NULL,
  removed_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO created(kind, round_id, repo, name, base, pushed, pr, at)
  SELECT 'branch', round_id, repo, branch, base,
    CASE WHEN pr != '' OR detail LIKE '%branch is pushed%' THEN 1 ELSE 0 END, pr, at
  FROM applies WHERE state IN ('pr', 'branch') AND branch != '';
`

// Created is one thing apply made in a repo: a round branch (Name is the
// branch) or a worktree (Name is its path).
type Created struct {
	ID    int64
	Kind  string // branch or worktree
	Round int64
	Repo  string // the primary clone
	Name  string
	// Base is the ref the branch was cut from; Commit the commit apply
	// made on it ("" for a branch recorded before commits were).
	Base   string
	Commit string
	Pushed bool
	PR     string
	At     time.Time
}

// AddCreated records something apply made and returns its id.
func (s *Store) AddCreated(ctx context.Context, c Created) (int64, error) {
	if c.At.IsZero() {
		c.At = time.Now()
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO created(kind, round_id, repo, name, base, commit_sha, pushed, pr, at) VALUES (?,?,?,?,?,?,?,?,?)`,
		c.Kind, c.Round, c.Repo, c.Name, c.Base, c.Commit, c.Pushed, c.PR, c.At.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// PushedCreated records that a branch was pushed, and its pull request
// when one was opened ("" leaves it).
func (s *Store) PushedCreated(ctx context.Context, id int64, pr string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE created SET pushed = 1, pr = CASE WHEN ? != '' THEN ? ELSE pr END WHERE id = ?`, pr, pr, id)
	return err
}

// RemovedCreated crosses a record off: what it names is gone.
func (s *Store) RemovedCreated(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE created SET removed_at = ? WHERE id = ?`, time.Now().UnixMilli(), id)
	return err
}

// Created lists what apply made that is not crossed off yet, oldest first.
func (s *Store) Created(ctx context.Context) ([]Created, error) {
	rs, err := s.db.QueryContext(ctx, `SELECT id, kind, round_id, repo, name, base, commit_sha, pushed, pr, at
		FROM created WHERE removed_at = 0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	var out []Created
	for rs.Next() {
		var c Created
		var at int64
		if err := rs.Scan(&c.ID, &c.Kind, &c.Round, &c.Repo, &c.Name, &c.Base, &c.Commit, &c.Pushed, &c.PR, &at); err != nil {
			return nil, err
		}
		c.At = time.UnixMilli(at)
		out = append(out, c)
	}
	return out, rs.Err()
}
