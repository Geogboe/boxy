package jobs

import (
	"context"
	"fmt"
	"sync"
)

type MemoryStore struct {
	mu   sync.Mutex
	jobs map[ID]Job
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{jobs: make(map[ID]Job)}
}

func (s *MemoryStore) Put(_ context.Context, job Job) error {
	if job.ID == "" {
		return fmt.Errorf("%w: job id is required", ErrInvalidRequest)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs == nil {
		s.jobs = make(map[ID]Job)
	}
	s.jobs[job.ID] = cloneJob(job)
	return nil
}

func (s *MemoryStore) Get(_ context.Context, id ID) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return cloneJob(job), nil
}

func (s *MemoryStore) List(_ context.Context) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		result = append(result, cloneJob(job))
	}
	return result, nil
}

func (s *MemoryStore) Delete(_ context.Context, id ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return ErrNotFound
	}
	delete(s.jobs, id)
	return nil
}
