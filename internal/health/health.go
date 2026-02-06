package health

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Status represents the health status of a component.
type Status string

const (
	StatusUp   Status = "up"
	StatusDown Status = "down"
)

// ComponentCheck represents the health of a single component.
type ComponentCheck struct {
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

// Response is the JSON body returned by health endpoints.
type Response struct {
	Status     Status                    `json:"status"`
	Components map[string]ComponentCheck `json:"components,omitempty"`
	Timestamp  string                    `json:"timestamp"`
}

// Checker provides liveness and readiness probes.
// Components register themselves and report their status.
type Checker struct {
	mu              sync.RWMutex
	readinessChecks map[string]CheckFunc
	shuttingDown    atomic.Bool
}

// CheckFunc returns nil if the component is healthy, or an error describing the issue.
type CheckFunc func() error

// New creates a new health Checker.
func New() *Checker {
	return &Checker{
		readinessChecks: make(map[string]CheckFunc),
	}
}

// RegisterReadiness registers a named readiness check.
// The check is called on each /ready request.
func (c *Checker) RegisterReadiness(name string, check CheckFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readinessChecks[name] = check
}

// SetShuttingDown marks the instance as shutting down.
// After this, both /live and /ready return 503.
func (c *Checker) SetShuttingDown() {
	c.shuttingDown.Store(true)
}

// LiveHandler returns an http.HandlerFunc for the /live endpoint.
// Liveness checks that the process is running and not in shutdown.
func (c *Checker) LiveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c.shuttingDown.Load() {
			writeJSON(w, http.StatusServiceUnavailable, Response{
				Status:    StatusDown,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Components: map[string]ComponentCheck{
					"process": {Status: StatusDown, Message: "shutting down"},
				},
			})
			return
		}

		writeJSON(w, http.StatusOK, Response{
			Status:    StatusUp,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// ReadyHandler returns an http.HandlerFunc for the /ready endpoint.
// Readiness runs all registered checks; if any fail, the response is 503.
func (c *Checker) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c.shuttingDown.Load() {
			writeJSON(w, http.StatusServiceUnavailable, Response{
				Status:    StatusDown,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Components: map[string]ComponentCheck{
					"process": {Status: StatusDown, Message: "shutting down"},
				},
			})
			return
		}

		c.mu.RLock()
		checks := make(map[string]CheckFunc, len(c.readinessChecks))
		for k, v := range c.readinessChecks {
			checks[k] = v
		}
		c.mu.RUnlock()

		overall := StatusUp
		components := make(map[string]ComponentCheck, len(checks))

		for name, check := range checks {
			if err := check(); err != nil {
				overall = StatusDown
				components[name] = ComponentCheck{Status: StatusDown, Message: err.Error()}
			} else {
				components[name] = ComponentCheck{Status: StatusUp}
			}
		}

		code := http.StatusOK
		if overall == StatusDown {
			code = http.StatusServiceUnavailable
		}

		writeJSON(w, code, Response{
			Status:     overall,
			Components: components,
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
		})
	}
}

func writeJSON(w http.ResponseWriter, code int, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}
