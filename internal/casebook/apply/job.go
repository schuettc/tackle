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
	Machine    string    `json:"machine"`
	Session    string    `json:"session"`
	State      JobState  `json:"state"`
	Paused     bool      `json:"paused"`
	CreatedAt  time.Time `json:"created_at"`
	ApprovedAt time.Time `json:"approved_at,omitzero"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
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
	Text         string    `json:"text,omitempty"`
	Restore      string    `json:"restore,omitempty"`
	State        StepState `json:"state"`
	Detail       string    `json:"detail,omitempty"`
	VerifiedAt   time.Time `json:"verified_at,omitzero"`
}

// NeedsYou is a card requesting Court's attention for a job (and optionally a step).
type NeedsYou struct {
	ID         int64     `json:"id"`
	JobID      int64     `json:"job_id"`
	StepID     int64     `json:"step_id"`
	Kind       string    `json:"kind"`
	Question   string    `json:"question"`
	Text       string    `json:"text"`
	State      string    `json:"state"`
	Answer     string    `json:"answer"`
	CreatedAt  time.Time `json:"created_at"`
	AnsweredAt time.Time `json:"answered_at,omitzero"`
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
// row per plan step in position order. machine identifies the host where the
// casebook daemon is running. It returns the populated Job.
func (s *Store) Create(ctx context.Context, p Plan, machine string) (Job, error) {
	now := s.Now()
	planJSON, err := json.Marshal(p)
	if err != nil {
		return Job{}, fmt.Errorf("marshal plan: %w", err)
	}

	var job Job
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO jobs(plan_json, machine, session, state, created_at) VALUES (?, ?, '', ?, ?)`,
			string(planJSON), machine, JobPlanned, ms(now),
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
			Machine:   machine,
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

// Approve transitions a job from planned to approved, setting session and
// approved_at. session may be empty only when the job has no agent-lane steps;
// if agent-lane steps exist and session is empty, an error is returned.
// Approving a job that is not in the planned state is an error.
func (s *Store) Approve(ctx context.Context, jobID int64, session string) (Job, error) {
	now := s.Now()
	err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var current JobState
		if err := tx.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id = ?`, jobID).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("job %d not found", jobID)
			}
			return err
		}
		if current != JobPlanned {
			return &ErrInvalidTransition{Kind: "job", ID: jobID, From: current, To: JobApproved}
		}
		// Check whether any agent-lane steps exist.
		if session == "" {
			var agentCount int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM steps WHERE job_id = ? AND lane = ?`, jobID, string(LaneAgent),
			).Scan(&agentCount); err != nil {
				return err
			}
			if agentCount > 0 {
				return fmt.Errorf("job %d has agent-lane steps: session must not be empty", jobID)
			}
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state = ?, session = ?, approved_at = ? WHERE id = ?`,
			JobApproved, session, ms(now), jobID)
		return err
	})
	if err != nil {
		return Job{}, err
	}
	return s.Get(ctx, jobID)
}

// SetPaused persists the paused flag on a job without changing its state.
func (s *Store) SetPaused(ctx context.Context, jobID int64, paused bool) error {
	p := 0
	if paused {
		p = 1
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET paused = ? WHERE id = ?`, p, jobID)
	return err
}

// Finish sets the terminal state (JobDone or JobFailed) on a job and records
// finished_at. Any state other than done or failed is rejected. Finish
// operates on any non-terminal job state (planned, approved, running, paused).
func (s *Store) Finish(ctx context.Context, jobID int64, state JobState) error {
	if state != JobDone && state != JobFailed {
		return fmt.Errorf("Finish: state must be done or failed, got %q", state)
	}
	now := s.Now()
	return s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var current JobState
		if err := tx.QueryRowContext(ctx, `SELECT state FROM jobs WHERE id = ?`, jobID).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("job %d not found", jobID)
			}
			return err
		}
		// Terminal states cannot be finished again.
		if current == JobDone || current == JobFailed {
			return &ErrInvalidTransition{Kind: "job", ID: jobID, From: current, To: state}
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE jobs SET state = ?, finished_at = ? WHERE id = ?`,
			state, ms(now), jobID)
		return err
	})
}

// Get returns the job with the given ID, including its steps.
func (s *Store) Get(ctx context.Context, id int64) (Job, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, machine, session, state, paused, created_at, approved_at, finished_at FROM jobs WHERE id = ?`, id)
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
		`SELECT id, machine, session, state, paused, created_at, approved_at, finished_at FROM jobs ORDER BY id`)
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
		`SELECT id, job_id, key, action, lane, command, precondition, posts, expected_tip, text, restore,
		        state, detail, verified_at
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
// It sets updated_at on every call and sets verified_at when entering verified.
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
// or (JobStep{}, false, nil) if none exist or the job is paused.
func (s *Store) NextRunnable(ctx context.Context, jobID int64, lane Lane) (JobStep, bool, error) {
	// Check if the job is paused.
	var paused int
	if err := s.DB.QueryRowContext(ctx, `SELECT paused FROM jobs WHERE id = ?`, jobID).Scan(&paused); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return JobStep{}, false, fmt.Errorf("job %d not found", jobID)
		}
		return JobStep{}, false, err
	}
	if paused != 0 {
		return JobStep{}, false, nil
	}

	row := s.DB.QueryRowContext(ctx,
		`SELECT id, job_id, key, action, lane, command, precondition, posts, expected_tip, text, restore,
		        state, detail, verified_at
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

// SetStepText sets the approved public text for a step.
func (s *Store) SetStepText(ctx context.Context, stepID int64, text string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE steps SET text = ? WHERE id = ?`, text, stepID)
	return err
}

// SetStepRestore sets the restore command for a step.
func (s *Store) SetStepRestore(ctx context.Context, stepID int64, restore string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE steps SET restore = ? WHERE id = ?`, restore, stepID)
	return err
}

// OpenNeedsYou creates a new open needs-you card for the given job.
// stepID may be 0 if the card is not associated with a specific step.
func (s *Store) OpenNeedsYou(ctx context.Context, jobID, stepID int64, kind, question, text string) (NeedsYou, error) {
	now := s.Now()
	var stepVal interface{}
	if stepID != 0 {
		stepVal = stepID
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO needs_you(job_id, step_id, kind, question, text, state, created_at)
		 VALUES (?, ?, ?, ?, ?, 'open', ?)`,
		jobID, stepVal, kind, question, text, ms(now))
	if err != nil {
		return NeedsYou{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return NeedsYou{}, err
	}
	return NeedsYou{
		ID:        id,
		JobID:     jobID,
		StepID:    stepID,
		Kind:      kind,
		Question:  question,
		Text:      text,
		State:     "open",
		CreatedAt: now,
	}, nil
}

// NeedsYouFor returns all needs-you cards for a job (any state), ordered by id.
func (s *Store) NeedsYouFor(ctx context.Context, jobID int64) ([]NeedsYou, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, job_id, COALESCE(step_id,0), kind, question, text, state, answer, created_at, answered_at
		 FROM needs_you WHERE job_id = ? ORDER BY id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNeedsYouRows(rows)
}

// OpenNeedsYouAll returns all open needs-you cards across all jobs, ordered by id.
func (s *Store) OpenNeedsYouAll(ctx context.Context) ([]NeedsYou, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, job_id, COALESCE(step_id,0), kind, question, text, state, answer, created_at, answered_at
		 FROM needs_you WHERE state = 'open' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNeedsYouRows(rows)
}

// EditNeedsYouText updates the text of an open needs-you card. It returns an
// error if the card is not in the open state.
func (s *Store) EditNeedsYouText(ctx context.Context, id int64, text string) error {
	return s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM needs_you WHERE id = ?`, id).Scan(&state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("needs_you %d not found", id)
			}
			return err
		}
		if state != "open" {
			return fmt.Errorf("needs_you %d is %q, not open", id, state)
		}
		_, err := tx.ExecContext(ctx, `UPDATE needs_you SET text = ? WHERE id = ?`, text, id)
		return err
	})
}

// AnswerNeedsYou transitions an open needs-you card to answered, recording the
// answer and answered_at. Answering a card that is not open returns an error.
func (s *Store) AnswerNeedsYou(ctx context.Context, id int64, answer string) (NeedsYou, error) {
	now := s.Now()
	err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM needs_you WHERE id = ?`, id).Scan(&state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("needs_you %d not found", id)
			}
			return err
		}
		if state != "open" {
			return fmt.Errorf("needs_you %d is already %q", id, state)
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE needs_you SET state = 'answered', answer = ?, answered_at = ? WHERE id = ?`,
			answer, ms(now), id)
		return err
	})
	if err != nil {
		return NeedsYou{}, err
	}
	// Read back the updated card.
	row := s.DB.QueryRowContext(ctx,
		`SELECT id, job_id, COALESCE(step_id,0), kind, question, text, state, answer, created_at, answered_at
		 FROM needs_you WHERE id = ?`, id)
	return scanNeedsYou(row)
}

// scanner abstracts *sql.Row and *sql.Rows for the scan helpers.
type scanner interface {
	Scan(...any) error
}

func scanJob(sc scanner) (Job, error) {
	var j Job
	var createdAt, approvedAt, finishedAt int64
	var paused int
	if err := sc.Scan(&j.ID, &j.Machine, &j.Session, &j.State, &paused, &createdAt, &approvedAt, &finishedAt); err != nil {
		return Job{}, err
	}
	j.Paused = paused != 0
	j.CreatedAt = tm(createdAt)
	j.ApprovedAt = tm(approvedAt)
	j.FinishedAt = tm(finishedAt)
	return j, nil
}

func scanStep(sc scanner) (JobStep, error) {
	var st JobStep
	var verifiedAt int64
	var posts int
	var lane string
	if err := sc.Scan(&st.ID, &st.JobID, &st.Key, &st.Action, &lane,
		&st.Command, &st.Precondition, &posts, &st.ExpectedTip,
		&st.Text, &st.Restore,
		&st.State, &st.Detail, &verifiedAt); err != nil {
		return JobStep{}, err
	}
	st.Lane = Lane(lane)
	st.Posts = posts != 0
	st.VerifiedAt = tm(verifiedAt)
	return st, nil
}

func scanNeedsYou(sc scanner) (NeedsYou, error) {
	var n NeedsYou
	var createdAt, answeredAt int64
	if err := sc.Scan(&n.ID, &n.JobID, &n.StepID, &n.Kind, &n.Question, &n.Text, &n.State, &n.Answer, &createdAt, &answeredAt); err != nil {
		return NeedsYou{}, err
	}
	n.CreatedAt = tm(createdAt)
	n.AnsweredAt = tm(answeredAt)
	return n, nil
}

func scanNeedsYouRows(rows *sql.Rows) ([]NeedsYou, error) {
	var result []NeedsYou
	for rows.Next() {
		n, err := scanNeedsYou(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}
