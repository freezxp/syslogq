package storage

import (
	"context"
	"errors"

	"github.com/freezxp/syslogq/internal/model"
)

// Typed errors mapped to stable API error codes later; adapters wrap them so
// callers can branch without parsing strings.
var (
	// ErrBackendUnavailable marks transport/status failures worth retrying.
	ErrBackendUnavailable = errors.New("storage: backend unavailable")
	// ErrBadRequest marks a rejection of the payload itself — retrying the
	// same batch cannot succeed.
	ErrBadRequest = errors.New("storage: request rejected")
	// ErrTimeout marks a storage operation that exceeded its deadline.
	ErrTimeout = errors.New("storage: operation timed out")
)

// Writer is the Phase 1 storage contract: the ingest write path plus a
// health probe. The full LogStorage interface (query/stats/facets/tail,
// docs/architecture.md §2.4) grows out of this when the query API lands in
// Phase 3; adapters then embed Writer and add the read methods.
type Writer interface {
	WriteLogs(ctx context.Context, batch []model.LogEntry) error
	Health(ctx context.Context) error
}
