package apply

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
)

// JobState is the lifecycle state of a Job.
type JobState = string

const (
	// JobPlanned is built and shown to Court, awaiting approval.
	JobPlanned JobState = "planned"
	// JobApproved is approved by Court, ready to run.
	JobApproved JobState = "approved"
	// JobRunning is actively executing steps.
	JobRunning JobState = "running"
	// JobPaused is paused mid-execution.
	JobPaused JobState = "paused"
	// JobDone is completed successfully.
	JobDone JobState = "done"
	// JobFailed is terminated due to an error.
	JobFailed JobState = "failed"
)

// StepState is the lifecycle state of a JobStep.
type StepState = string

const (
	// StepPending is not yet started.
	StepPending StepState = "pending"
	// StepRunning is currently executing.
	StepRunning StepState = "running"
	// StepReported means the command has been dispatched/reported but not yet verified.
	StepReported StepState = "reported"
	// StepVerified means execution was confirmed successful.
	StepVerified StepState = "verified"
	// StepSkipped means the step was skipped (precondition not met or already done).
	StepSkipped StepState = "skipped"
	// StepPaused means the step is paused mid-execution.
	StepPaused StepState = "paused"
	// StepFailed means the step failed.
	StepFailed StepState = "failed"
	// StepNeedsYou means the step is waiting for Court's input.
	StepNeedsYou StepState = "needs_you"
)

// Job is a persisted apply plan with execution state.
type Job struct {
	ID         int64     `json:"id"`
	State      JobState  `json:"state"`
	CreatedAt  time.Time `json:"created_at"`
	ApprovedAt time.Time `json:"approved_at,omitzero"`
	Steps      []JobStep `json:"steps"`
}

// JobStep is one persisted step within a Job.
type JobStep struct {
	ID           int64     `json:"id"`
	JobID        int64     `json:"job_id"`
	Key          string    `json:"key"`
	Action       string    `json:"action"`
	Lane         Lane      `json:"lane"`
	Command      string    `json:"command"`
	Precondition string    `json:"precondition,omitempty"`
	Posts        bool      `json:"posts,omitempty"`
	ExpectedTip  string    `json:"expected_tip,omitempty"`
	State        StepState `json:"state"`
	Detail       string    `json:"detail,omitempty"`
	VerifiedAt   time.Time `json:"verified_at,omitzero"`
}

// Store reads and writes jobs and their steps.
type Store struct {
	DB  *db.DB
	Now func() time.Time
}

// NewStore returns a Store over d.
func NewStore(d *db.DB) *Store { return &Store{DB: d, Now: time.Now} }

func ms(t time.Time) int64 { return t.UnixMilli() }

func tm(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

// validJobTransitions maps from → set of valid to-states.
var validJobTransitions = map[JobState]map[JobState]bool{
	JobPlanned:  {JobApproved: true, JobFailed: true},
	JobApproved: {JobRunning: true, JobFailed: true},
	JobRunning:  {JobPaused: true, JobDone: true, JobFailed: true},
	JobPaused:   {JobRunning: true, JobFailed: true},
	JobDone:     {},
	JobFailed:   {},
}

// validStepTransitions maps from → set of valid to-states.
var validStepTransitions = map[StepState]map[StepState]bool{
	StepPending:  {StepRunning: true, StepSkipped: true},
	StepRunning:  {StepReported: true, StepFailed: true, StepSkipped: true, StepNeedsYou: true, StepPaused: true},
	StepReported: {StepVerified: true, StepFailed: true},
	StepVerified: {},
	StepSkipped:  {},
	StepPaused:   {StepRunning: true},
	StepFailed:   {},
	StepNeedsYou: {StepRunning: true},
}

// ErrInvalidTransition is returned when a state transition is not allowed.
type ErrInvalidTransition struct {
	Kind string
	ID   int64
	From string
	To   string
}

func (e *ErrInvalidTransition) Error() string {
	return fmt.Sprintf("invalid %s transition %s→%s (id=%d)", e.Kind, e.From, e.To, e.ID)
}

// Create persists a Plan as a new Job in the "planned" state, with one step
// row per plan step. It returns the populated Job.
func (s *Store) Create(ctx context.Context, p Plan) (Job, error) {
	now := s.Now()
	planJSON, err := json.Marshal(p)
	if err != nil {
		return Job{}, fmt.Errorf("marshal plan: %w", err)
	}

	var job Job
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO jobs(plan_json, machine, session, state, created_at) VALUES (?, '', '', ?, ?)`,
			string(planJSON), JobPlanned, ms(now),
		)
		if err != nil {
			return err
		}
		jobID, err := res.LastInsertId()
		if err != nil {
			return err
		}

		steps := make([]JobStep, len(p.Steps))
		for i, ps := range p.Steps {
			posts := 0
			if ps.Posts {
				posts = 1
			}
			res2, err := tx.ExecContext(ctx,
				`INSERT INTO steps(job_id, pos, key, action, lane, command, precondition, posts, expected_tip, state, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				jobID, i, ps.Key, ps.Action, string(ps.Lane), ps.Command, ps.Precondition,
				posts, ps.ExpectedTip, StepPending, ms(now),
			)
			if err != nil {
				return err
			}
			stepID, err := res2.LastInsertId()
			if err != nil {
				return err
			}
			steps[i] = JobStep{
				ID:           stepID,
				JobID:        jobID,
				Key:          ps.Key,
				Action:       ps.Action,
				Lane:         ps.Lane,
				Command:      ps.Command,
				Precondition: ps.Precondition,
				Posts:        ps.Posts,
				ExpectedTip:  ps.ExpectedTip,
				State:        StepPending,
			}
		}

		job = Job{
			ID:        jobID,
			State:     JobPlanned,
			CreatedAt: now,
			Steps:     steps,
		}
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return job, nil
}

// Get returns the job with the given ID, including its steps.
func (s *Store) Get(ctx context.Context, id int64) (Job, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, state, created_at, approved_at FROM jobs WHERE id = ?`, id)
	job, err := scanJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, fmt.Errorf("job %d not found", id)
		}
		return Job{}, err
	}
	steps, err := s.Steps(ctx, id)
	if err != nil {
		return Job{}, err
	}
	job.Steps = steps
	return job, nil
}

// List returns all jobs ordered by id, each with its steps.
func (s *Store) List(ctx context.Context) ([]Job, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, state, created_at, approved_at FROM jobs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range jobs {
		steps, err := s.Steps(ctx, jobs[i].ID)
		if err != nil {
			return nil, err
		}
		jobs[i].Steps = steps
	}
	return jobs, nil
}

// Steps returns all steps for a job in position order.
func (s *Store) Steps(ctx context.Context, jobID int64) ([]JobStep, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, job_id, key, action, lane, command, precondition, posts, expected_tip, state, detail, verified_at
		 FROM steps WHERE job_id = ? ORDER BY pos`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []JobStep
	for rows.Next() {
		st, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		steps = append(steps, st)
	}
	return steps, rows.Err()
}

// SetStepState transitions a step to a new state, storing an optional detail.
// Invalid transitions return an *ErrInvalidTransition.
func (s *Store) SetStepState(ctx context.Context, stepID int64, state StepState, detail string) error {
	now := s.Now()
	return s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var current StepState
		if err := tx.QueryRowContext(ctx, `SELECT state FROM steps WHERE id = ?`, stepID).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("step %d not found", stepID)
			}
			return err
		}
		if !validStepTransitions[current][state] {
			return &ErrInvalidTransition{Kind: "step", ID: stepID, From: current, To: state}
		}
		verifiedAt := int64(0)
		if state == StepVerified {
			verifiedAt = ms(now)
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE steps SET state = ?, detail = ?, updated_at = ?, verified_at = ? WHERE id = ?`,
			state, detail, ms(now), verifiedAt, stepID)
		return err
	})
}

// SetJobState transitions a job to a new state.
// Invalid transitions return an *ErrInvalidTransition.
func (s *Store) SetJobState(ctx context.Context, jobID int64, state JobState) error {
	now := s.Now()
	return s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var current JobState
		if err := tx.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id = ?`, jobID).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("job %d not found", jobID)
			}
			return err
		}
		if !validJobTransitions[current][state] {
			return &ErrInvalidTransition{Kind: "job", ID: jobID, From: current, To: state}
		}

		approvedAt := int64(0)
		if state == JobApproved {
			approvedAt = ms(now)
		}
		finishedAt := int64(0)
		if state == JobDone || state == JobFailed {
			finishedAt = ms(now)
		}

		_, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state = ?, approved_at = CASE WHEN ? > 0 THEN ? ELSE approved_at END,
			 finished_at = CASE WHEN ? > 0 THEN ? ELSE finished_at END WHERE id = ?`,
			state, approvedAt, approvedAt, finishedAt, finishedAt, jobID)
		return err
	})
}

// NextRunnable returns the first pending step in the given lane for the job,
// or (JobStep{}, false, nil) if none exist.
func (s *Store) NextRunnable(ctx context.Context, jobID int64, lane Lane) (JobStep, bool, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, job_id, key, action, lane, command, precondition, posts, expected_tip, state, detail, verified_at
		 FROM steps WHERE job_id = ? AND lane = ? AND state = ? ORDER BY pos LIMIT 1`,
		jobID, string(lane), StepPending)
	st, err := scanStep(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return JobStep{}, false, nil
		}
		return JobStep{}, false, err
	}
	return st, true, nil
}

// scanner abstracts *sql.Row and *sql.Rows for the scan helpers.
type scanner interface {
	Scan(...any) error
}

func scanJob(sc scanner) (Job, error) {
	var j Job
	var createdAt, approvedAt int64
	if err := sc.Scan(&j.ID, &j.State, &createdAt, &approvedAt); err != nil {
		return Job{}, err
	}
	j.CreatedAt = tm(createdAt)
	j.ApprovedAt = tm(approvedAt)
	return j, nil
}

func scanStep(sc scanner) (JobStep, error) {
	var st JobStep
	var verifiedAt int64
	var posts int
	var lane string
	if err := sc.Scan(&st.ID, &st.JobID, &st.Key, &st.Action, &lane,
		&st.Command, &st.Precondition, &posts, &st.ExpectedTip,
		&st.State, &st.Detail, &verifiedAt); err != nil {
		return JobStep{}, err
	}
	st.Lane = Lane(lane)
	st.Posts = posts != 0
	st.VerifiedAt = tm(verifiedAt)
	return st, nil
}
