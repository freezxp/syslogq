package storage

import (
	"context"
	"errors"
	"time"

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

// Writer is the storage write contract: the ingest write path plus a health
// probe.
type Writer interface {
	WriteLogs(ctx context.Context, batch []model.LogEntry) error
	Health(ctx context.Context) error
}

// Reader is the Phase 3 read contract for the query API. Filter strings
// arrive pre-compiled by internal/query — the single place LogsQL syntax is
// produced — so adapters only append their own pipes, never splice user
// text.
type Reader interface {
	// Search returns raw rows (field → value) newest-first.
	Search(ctx context.Context, p SearchParams) ([]map[string]string, error)
	// Count returns the number of matching rows.
	Count(ctx context.Context, p RangeParams) (int64, error)
	// CountByTime returns per-bucket counts, buckets aligned to epoch
	// multiples of step. Buckets with no data are omitted; callers zero-fill.
	CountByTime(ctx context.Context, p RangeParams, step time.Duration) ([]BucketCount, error)
	// FieldNames returns field names present in the range with row counts.
	FieldNames(ctx context.Context, p RangeParams) ([]ValueCount, error)
	// FieldValues returns up to limit values of field with match counts,
	// highest first. The empty value counts rows lacking the field.
	FieldValues(ctx context.Context, p RangeParams, field string, limit int) ([]ValueCount, error)
}

// RangeParams scopes a read. Filter is a compiled query.Compile output, ""
// matches everything. Zero Start/End mean "no bound".
type RangeParams struct {
	Filter string
	Start  time.Time
	End    time.Time
}

// SearchParams adds pagination to RangeParams.
type SearchParams struct {
	RangeParams
	Offset int
	Limit  int
}

// ValueCount is one facet value with its match count.
type ValueCount struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// BucketCount is one time bucket's count.
type BucketCount struct {
	Time  time.Time `json:"time"`
	Count int64     `json:"count"`
}
