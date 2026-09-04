// Package jobs provides a domain-neutral durable background-job lifecycle.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultRetention      = 14 * 24 * time.Hour
	DefaultCleanupTimeout = 5 * time.Minute

	ErrorCodeJobFailed     = "job_failed"
	ErrorCodeCleanupFailed = "cleanup_failed"
)

var (
	ErrNotFound          = errors.New("job not found")
	ErrStoreRequired     = errors.New("job store is required")
	ErrInvalidRequest    = errors.New("job request is invalid")
	ErrWorkerUnavailable = errors.New("job worker is unavailable")
	ErrAlreadyExists     = errors.New("job already exists")
)

type ID string

type Status string

const (
	StatusPending     Status = "pending"
	StatusRunning     Status = "running"
	StatusCancelling  Status = "cancelling"
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusInterrupted Status = "interrupted"
)

func (s Status) IsTerminal() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusInterrupted:
		return true
	default:
		return false
	}
}

type StepStatus string

const (
	StepStarted   StepStatus = "started"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
)

// Step is safe progress metadata supplied by a domain adapter. Code and
// Subject are identifiers, not free-form output or provider payloads.
type Step struct {
	Sequence  uint64     `json:"sequence"`
	Code      string     `json:"code"`
	Subject   string     `json:"subject,omitempty"`
	Status    StepStatus `json:"status"`
	Attempt   int        `json:"attempt,omitempty"`
	ErrorCode string     `json:"error_code,omitempty"`
	At        time.Time  `json:"at"`
}

// Job contains only generic lifecycle and safe progress metadata.
type Job struct {
	ID         ID         `json:"id"`
	Kind       string     `json:"kind"`
	Target     string     `json:"target"`
	Status     Status     `json:"status"`
	Steps      []Step     `json:"steps,omitempty"`
	ErrorCode  string     `json:"error_code,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	// ErrorDetail is intentionally never serialized or populated by Runner.
	// It exists only to make accidental unsafe persistence visible to tests and
	// embedders inspecting a value in-process.
	ErrorDetail string `json:"-"`
}

type Request struct {
	ID     ID
	Kind   string
	Target string
}

type Store interface {
	Put(context.Context, Job) error
	Get(context.Context, ID) (Job, error)
	List(context.Context) ([]Job, error)
	Delete(context.Context, ID) error
}

type Reporter interface {
	Record(context.Context, Step) error

	// JobID returns the ID of the job this reporter reports progress for.
	// Purely generic identity -- it lets a domain handler correlate its own
	// (domain-rich) observability calls to the job without pkg/jobs itself
	// needing to know anything about pools, agents, or providers.
	JobID() ID
}

type Handler interface {
	Run(context.Context, Reporter) error
	Cleanup(context.Context, Reporter) error
}

type HandlerFuncs struct {
	RunFunc     func(context.Context, Reporter) error
	CleanupFunc func(context.Context, Reporter) error
}

func (h HandlerFuncs) Run(ctx context.Context, reporter Reporter) error {
	if h.RunFunc == nil {
		return nil
	}
	return h.RunFunc(ctx, reporter)
}

func (h HandlerFuncs) Cleanup(ctx context.Context, reporter Reporter) error {
	if h.CleanupFunc == nil {
		return nil
	}
	return h.CleanupFunc(ctx, reporter)
}

// Failure carries a stable safe code without persisting an error string.
type Failure struct{ Code string }

func (f *Failure) Error() string {
	if f == nil || strings.TrimSpace(f.Code) == "" {
		return ErrorCodeJobFailed
	}
	return f.Code
}

type TargetBusyError struct {
	Target      string
	ActiveJobID ID
}

func (e *TargetBusyError) Error() string {
	return fmt.Sprintf("job target %q is busy with job %q", e.Target, e.ActiveJobID)
}

type Config struct {
	Store          Store
	Retention      time.Duration
	CleanupTimeout time.Duration
	Now            func() time.Time
	NewID          func() ID
}

type worker struct {
	cancel  context.CancelFunc
	handler Handler
}

type Runner struct {
	store          Store
	retention      time.Duration
	cleanupTimeout time.Duration
	now            func() time.Time
	newID          func() ID

	mu      sync.Mutex
	active  map[string]ID
	workers map[ID]worker
}

func NewRunner(cfg Config) (*Runner, error) {
	if cfg.Store == nil {
		return nil, ErrStoreRequired
	}
	if cfg.Retention <= 0 {
		cfg.Retention = DefaultRetention
	}
	if cfg.CleanupTimeout <= 0 {
		cfg.CleanupTimeout = DefaultCleanupTimeout
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.NewID == nil {
		cfg.NewID = func() ID { return ID(uuid.NewString()) }
	}
	runner := &Runner{
		store:          cfg.Store,
		retention:      cfg.Retention,
		cleanupTimeout: cfg.CleanupTimeout,
		now:            cfg.Now,
		newID:          cfg.NewID,
		active:         make(map[string]ID),
		workers:        make(map[ID]worker),
	}
	if err := runner.interruptPersisted(context.Background()); err != nil {
		return nil, err
	}
	return runner, nil
}

func (r *Runner) Submit(ctx context.Context, request Request, handler Handler) (Job, error) {
	request.Kind = strings.TrimSpace(request.Kind)
	request.Target = strings.TrimSpace(request.Target)
	if request.Kind == "" || request.Target == "" || handler == nil {
		return Job{}, ErrInvalidRequest
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if activeID, ok := r.active[request.Target]; ok {
		return Job{}, &TargetBusyError{Target: request.Target, ActiveJobID: activeID}
	}
	now := r.currentTime()
	id := request.ID
	if id == "" {
		id = r.newID()
	}
	job := Job{ID: id, Kind: request.Kind, Target: request.Target, Status: StatusPending, CreatedAt: now}
	if job.ID == "" {
		return Job{}, fmt.Errorf("%w: generated job ID is empty", ErrInvalidRequest)
	}
	if _, err := r.store.Get(ctx, job.ID); err == nil {
		return Job{}, fmt.Errorf("%w: %s", ErrAlreadyExists, job.ID)
	} else if !errors.Is(err, ErrNotFound) {
		return Job{}, err
	}
	if err := r.store.Put(ctx, job); err != nil {
		return Job{}, err
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	r.active[request.Target] = job.ID
	r.workers[job.ID] = worker{cancel: cancel, handler: handler}
	go r.run(workerCtx, job.ID)
	return cloneJob(job), nil
}

func (r *Runner) Get(ctx context.Context, id ID) (Job, error) {
	return r.store.Get(ctx, id)
}

func (r *Runner) Cancel(ctx context.Context, id ID) (Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, err := r.store.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if job.Status.IsTerminal() {
		return job, nil
	}
	activeWorker, ok := r.workers[id]
	if !ok {
		return Job{}, ErrWorkerUnavailable
	}
	job.Status = StatusCancelling
	if err := r.store.Put(ctx, job); err != nil {
		return Job{}, err
	}
	activeWorker.cancel()
	return cloneJob(job), nil
}

func (r *Runner) Prune(ctx context.Context) error {
	jobs, err := r.store.List(ctx)
	if err != nil {
		return err
	}
	cutoff := r.currentTime().Add(-r.retention)
	for _, job := range jobs {
		if !job.Status.IsTerminal() || job.FinishedAt == nil || !job.FinishedAt.Before(cutoff) {
			continue
		}
		if err := r.store.Delete(ctx, job.ID); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return nil
}

func (r *Runner) run(ctx context.Context, id ID) {
	r.mu.Lock()
	job, err := r.store.Get(context.Background(), id)
	activeWorker, ok := r.workers[id]
	if err != nil || !ok {
		r.mu.Unlock()
		return
	}
	now := r.currentTime()
	job.Status = StatusRunning
	job.StartedAt = &now
	if err := r.store.Put(context.Background(), job); err != nil {
		r.releaseLocked(job)
		r.mu.Unlock()
		return
	}
	r.mu.Unlock()

	reporter := &jobReporter{runner: r, id: id}
	runErr := activeWorker.handler.Run(ctx, reporter)

	r.mu.Lock()
	job, err = r.store.Get(context.Background(), id)
	if err != nil {
		r.releaseByIDLocked(id)
		r.mu.Unlock()
		return
	}
	wasCancelled := job.Status == StatusCancelling
	r.mu.Unlock()

	if wasCancelled {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), r.cleanupTimeout)
		cleanupErr := activeWorker.handler.Cleanup(cleanupCtx, reporter)
		cancel()
		if cleanupErr != nil {
			r.finish(id, StatusFailed, ErrorCodeCleanupFailed)
			return
		}
		r.finish(id, StatusCancelled, "")
		return
	}
	if runErr != nil {
		r.finish(id, StatusFailed, safeFailureCode(runErr))
		return
	}
	r.finish(id, StatusSucceeded, "")
}

func (r *Runner) finish(id ID, status Status, errorCode string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, err := r.store.Get(context.Background(), id)
	if err != nil {
		r.releaseByIDLocked(id)
		return
	}
	now := r.currentTime()
	job.Status = status
	job.ErrorCode = errorCode
	job.ErrorDetail = ""
	job.FinishedAt = &now
	if err := r.store.Put(context.Background(), job); err != nil {
		return
	}
	r.releaseLocked(job)
}

func (r *Runner) interruptPersisted(ctx context.Context) error {
	persisted, err := r.store.List(ctx)
	if err != nil {
		return err
	}
	now := r.currentTime()
	for _, job := range persisted {
		if job.Status != StatusPending && job.Status != StatusRunning && job.Status != StatusCancelling {
			continue
		}
		job.Status = StatusInterrupted
		job.ErrorCode = "server_restarted"
		job.ErrorDetail = ""
		job.FinishedAt = &now
		if err := r.store.Put(ctx, job); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) releaseLocked(job Job) {
	delete(r.active, job.Target)
	r.releaseByIDLocked(job.ID)
}

func (r *Runner) releaseByIDLocked(id ID) {
	if activeWorker, ok := r.workers[id]; ok {
		activeWorker.cancel()
	}
	delete(r.workers, id)
}

func (r *Runner) currentTime() time.Time {
	return r.now().UTC()
}

type jobReporter struct {
	runner *Runner
	id     ID
}

func (r *jobReporter) JobID() ID {
	return r.id
}

func (r *jobReporter) Record(ctx context.Context, step Step) error {
	step.Code = strings.TrimSpace(step.Code)
	step.Subject = strings.TrimSpace(step.Subject)
	step.ErrorCode = strings.TrimSpace(step.ErrorCode)
	if step.Code == "" || step.Status == "" {
		return errors.New("job progress step code and status are required")
	}
	r.runner.mu.Lock()
	defer r.runner.mu.Unlock()
	job, err := r.runner.store.Get(ctx, r.id)
	if err != nil {
		return err
	}
	step.Sequence = uint64(len(job.Steps) + 1)
	step.At = r.runner.currentTime()
	job.Steps = append(job.Steps, step)
	return r.runner.store.Put(ctx, job)
}

func safeFailureCode(err error) string {
	var failure *Failure
	if errors.As(err, &failure) && strings.TrimSpace(failure.Code) != "" {
		return strings.TrimSpace(failure.Code)
	}
	return ErrorCodeJobFailed
}

func cloneJob(job Job) Job {
	job.Steps = append([]Step(nil), job.Steps...)
	return job
}
