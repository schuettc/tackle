// Package content reads an audited file back at its source: a repo file at
// the commit it was audited at, any other file where it resolved at the
// audit, through apply's no-symlink walk. The store keeps no file content
// (a file may hold a secret), so sift next, the review page and apply read
// it here and check it against the hash the round recorded.
package content

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/row"
)

// Max caps what is read of one file.
const Max = 2 << 20

// ErrChanged is returned when a file no longer has the content the round
// recorded.
var ErrChanged = errors.New("the file changed since the audit: run sift check again")

// Read returns f's content at the audit, checked against f.Base.
func Read(ctx context.Context, f rec.File) (string, error) {
	b, err := Raw(ctx, f.Source, f.Commit)
	if err != nil {
		return "", err
	}
	if rec.Hash(string(b)) != f.Base {
		return "", fmt.Errorf("%s: %w", f.Source.File, ErrChanged)
	}
	return string(b), nil
}

// Raw reads the file a source names as it was audited, unchecked: a repo
// file at commit (else the source's ref), any other file from disk where it
// resolved at the audit (see Moved). Up to Max+1 bytes.
func Raw(ctx context.Context, src row.Source, commit string) ([]byte, error) {
	if src.Repo != "" && src.Path != "" && (commit != "" || src.Ref != "") {
		at := commit
		if at == "" {
			at = src.Ref
		}
		b, err := discover.Git(ctx, src.Repo, "show", at+":"+src.Path).Output()
		if err != nil {
			return nil, fmt.Errorf("can't read %s at %s: %w", src.Path, at, err)
		}
		return b, nil
	}
	if src.File == "" {
		return nil, errors.New("this source has no file")
	}
	if why := Moved(src); why != "" {
		return nil, errors.New(why)
	}
	return ReadCanon(src.Canon, Max+1)
}

// Moved says why a file on disk can't be read as audited: its path is not
// clean and absolute, or it resolves somewhere other than it did then (a
// symlink, the leaf or a parent, retargeted). "" when it can be read.
func Moved(src row.Source) string {
	if src.Canon == "" {
		return "sift did not record where " + src.File + " resolved: run sift check again"
	}
	if now := row.Resolve(src.File); now != src.Canon {
		return src.File + " resolves somewhere else since the audit: run sift check again"
	}
	return ""
}

// BeforeOpen, when set (tests), runs after a file's path is validated and
// before it is opened.
var BeforeOpen func()

// ReadCanon reads up to n bytes of canon (clean, absolute, resolved at the
// audit) through an os.Root at the filesystem root, by apply's walk: every
// component is checked not to be a symlink and opened by descriptor, so a
// parent swapped after Moved's check can't redirect the read.
func ReadCanon(canon string, n int64) ([]byte, error) {
	if !filepath.IsAbs(canon) || filepath.Clean(canon) != canon {
		return nil, fmt.Errorf("%s is not a clean absolute path", canon)
	}
	top := filepath.VolumeName(canon) + string(filepath.Separator)
	root, err := os.OpenRoot(top)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if BeforeOpen != nil {
		BeforeOpen()
	}
	rel := filepath.ToSlash(strings.TrimPrefix(canon, top))
	return apply.ReadNoLink(root, rel, n)
}
