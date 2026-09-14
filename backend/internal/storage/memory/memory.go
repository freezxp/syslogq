package memory

import (
	"context"
	"sync"
	"time"

	"github.com/freezxp/syslogq/internal/model"
)

// Store is an in-memory storage.Writer for tests: it records every batch,
// and can be made to fail or stall to exercise retry and drop paths.
type Store struct {
	mu      sync.Mutex
	entries []model.LogEntry
	batches int

	// Err, when non-nil, makes every write fail (after applying Delay).
	Err error
	// FailN writes fail with FailErr before writes start succeeding.
	FailN   int
	FailErr error
	// Delay stalls each write attempt.
	Delay time.Duration
}

func (s *Store) WriteLogs(_ context.Context, batch []model.LogEntry) error {
	if s.Delay > 0 {
		time.Sleep(s.Delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches++
	if s.FailN > 0 {
		s.FailN--
		return s.FailErr
	}
	if s.Err != nil {
		return s.Err
	}
	s.entries = append(s.entries, batch...)
	return nil
}

func (s *Store) Health(_ context.Context) error { return nil }

func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (s *Store) Batches() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.batches
}

func (s *Store) All() []model.LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.LogEntry, len(s.entries))
	copy(out, s.entries)
	return out
}
