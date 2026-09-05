// Package jobs runs the recurring background work through one scheduler, so each
// task's last run, failure, and status are observable.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
	"sync"
	"time"
)

type Func func(context.Context) error

type Job struct {
	Name    string
	Every   time.Duration
	OnStart bool
	Run     Func
}

type Status struct {
	Name     string    `json:"name"`
	Every    string    `json:"every"`
	Running  bool      `json:"running"`
	LastRun  time.Time `json:"lastRun,omitempty"`
	LastOK   time.Time `json:"lastOk,omitempty"`
	NextRun  time.Time `json:"nextRun,omitempty"`
	Runs     int       `json:"runs"`
	Failures int       `json:"failures"`
	LastErr  string    `json:"lastError,omitempty"`
	Took     string    `json:"took,omitempty"`
}

type entry struct {
	job Job

	mu       sync.Mutex
	running  bool
	lastRun  time.Time
	lastOK   time.Time
	nextRun  time.Time
	runs     int
	failures int
	lastErr  string
	took     time.Duration
}

type Scheduler struct {
	log *slog.Logger

	mu      sync.Mutex
	entries map[string]*entry
	order   []string
}

func New(log *slog.Logger) *Scheduler {
	return &Scheduler{log: log, entries: map[string]*entry{}}
}

func (s *Scheduler) Add(job Job) {
	if job.Run == nil || job.Name == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.entries[job.Name]; exists {
		return
	}
	s.entries[job.Name] = &entry{job: job}
	s.order = append(s.order, job.Name)
}

func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	names := append([]string(nil), s.order...)
	s.mu.Unlock()

	for _, name := range names {
		go s.loop(ctx, name)
	}
}

func (s *Scheduler) loop(ctx context.Context, name string) {
	e := s.entry(name)
	if e == nil {
		return
	}

	if e.job.OnStart {
		s.execute(ctx, e)
	}

	// Consecutive failures back off so a persistently broken job stops
	// hammering whatever it depends on.
	var consecutive int
	for {
		wait := e.job.Every
		if consecutive > 0 {
			wait = backoff(e.job.Every, consecutive)
		}

		e.mu.Lock()
		e.nextRun = time.Now().Add(wait)
		e.mu.Unlock()

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}

		if err := s.execute(ctx, e); err != nil {
			consecutive++
		} else {
			consecutive = 0
		}
	}
}

func backoff(base time.Duration, failures int) time.Duration {
	capped := math.Min(float64(failures), 5)
	wait := time.Duration(float64(base) * math.Pow(2, capped))
	if wait > 6*time.Hour {
		return 6 * time.Hour
	}
	return wait
}

func (s *Scheduler) execute(ctx context.Context, e *entry) error {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return nil
	}
	e.running, e.lastRun = true, time.Now()
	e.mu.Unlock()

	start := time.Now()
	err := safely(s.log, e.job.Name, ctx, e.job.Run)
	took := time.Since(start)

	e.mu.Lock()
	e.running, e.runs, e.took = false, e.runs+1, took
	if err != nil {
		e.failures++
		e.lastErr = err.Error()
	} else {
		e.lastErr, e.lastOK = "", time.Now()
	}
	e.mu.Unlock()

	if err != nil {
		s.log.Warn("job failed", "job", e.job.Name, "err", err, "took", took.Round(time.Millisecond))
	}
	return err
}

// A panic in one job must not take the process down with it. The stack goes to
// the log because the error alone reaches the jobs tab without a location.
func safely(log *slog.Logger, name string, ctx context.Context, run Func) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("job panicked", "job", name, "err", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return run(ctx)
}

// Trigger runs a job immediately without disturbing its schedule.
func (s *Scheduler) Trigger(ctx context.Context, name string) error {
	e := s.entry(name)
	if e == nil {
		return fmt.Errorf("no such job: %s", name)
	}
	return s.execute(ctx, e)
}

func (s *Scheduler) Status() []Status {
	s.mu.Lock()
	names := append([]string(nil), s.order...)
	s.mu.Unlock()

	out := make([]Status, 0, len(names))
	for _, name := range names {
		e := s.entry(name)
		if e == nil {
			continue
		}

		e.mu.Lock()
		st := Status{
			Name: name, Every: e.job.Every.String(),
			Running: e.running, LastRun: e.lastRun, LastOK: e.lastOK,
			NextRun: e.nextRun, Runs: e.runs, Failures: e.failures,
			LastErr: e.lastErr,
		}
		if e.took > 0 {
			st.Took = e.took.Round(time.Millisecond).String()
		}
		e.mu.Unlock()

		out = append(out, st)
	}
	return out
}

func (s *Scheduler) entry(name string) *entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[name]
}
