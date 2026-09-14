// Package health implements /health (liveness) and /ready (readiness)
// checks. See docs/architecture.md §2.5 and docs/deployment.md §7.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Checker reports one component's health. Check must be safe for concurrent
// use and must respect ctx cancellation.
type Checker interface {
	Name() string
	Check(ctx context.Context) error
}

// CheckerFunc adapts a function to Checker.
type CheckerFunc func(ctx context.Context) error

type namedChecker struct {
	name string
	fn   CheckerFunc
}

func (c namedChecker) Name() string                    { return c.name }
func (c namedChecker) Check(ctx context.Context) error { return c.fn(ctx) }

// NewChecker wraps a function with a name.
func NewChecker(name string, fn CheckerFunc) Checker {
	return namedChecker{name: name, fn: fn}
}

// Registry aggregates checkers for the /ready endpoint.
type Registry struct {
	checkers []Checker
	timeout  time.Duration
}

func NewRegistry(checkers ...Checker) *Registry {
	return &Registry{checkers: checkers, timeout: 3 * time.Second}
}

// result is the /ready response body.
type result struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Handler serves readiness: 200 when every checker passes, 503 otherwise.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ok, checks := r.Ready()
		status := "ok"
		code := http.StatusOK
		if !ok {
			status = "unavailable"
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(result{Status: status, Checks: checks})
	})
}

// Ready runs all checkers concurrently with a per-check timeout.
func (r *Registry) Ready() (bool, map[string]string) {
	type outcome struct {
		name string
		err  error
	}
	var wg sync.WaitGroup
	outcomes := make(chan outcome, len(r.checkers))
	for _, c := range r.checkers {
		wg.Add(1)
		go func(c Checker) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
			defer cancel()
			outcomes <- outcome{name: c.Name(), err: c.Check(ctx)}
		}(c)
	}
	wg.Wait()
	close(outcomes)

	checks := make(map[string]string, len(r.checkers))
	allOK := true
	for o := range outcomes {
		if o.err != nil {
			checks[o.name] = o.err.Error()
			allOK = false
		} else {
			checks[o.name] = "ok"
		}
	}
	return allOK, checks
}
