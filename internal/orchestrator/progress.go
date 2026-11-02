package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// A suite's progress (ADR 0027): how its scenario is going, which nothing here decides on. The
// orchestrator holds the latest in memory, and writes only the final one, with the run.

// progressLimit is the most a progress may weigh, as JSON.
const progressLimit = 64 << 10

// progressStale is how long a live progress lasts without its suite renewing it.
const progressStale = time.Minute

type progressBook struct {
	mu sync.Mutex
	by map[string]progressEntry
}

type progressEntry struct {
	p  protocol.Progress
	at time.Time
}

func (b *progressBook) set(runID string, p protocol.Progress, at time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.by == nil {
		b.by = map[string]progressEntry{}
	}
	b.by[runID] = progressEntry{p, at}
}

func (b *progressBook) get(runID string, now time.Time) (protocol.Progress, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.by[runID]
	if !ok || now.Sub(e.at) > progressStale {
		return protocol.Progress{}, false
	}
	return e.p, true
}

// sweep drops what nobody renewed for a while: the suite finished, or went away.
func (b *progressBook) sweep(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, e := range b.by {
		if now.Sub(e.at) > progressStale {
			delete(b.by, id)
		}
	}
}

func (o *Orchestrator) sweepProgress(context.Context) error {
	o.progress.sweep(o.now())
	return nil
}

// SetProgress keeps the latest progress of a run's suite. The final one is written with the
// run, and the journal says how the scenario went.
func (o *Orchestrator) SetProgress(ctx context.Context, runID string, p protocol.Progress) error {
	r, err := o.runRow(ctx, o.ro, runID)
	if err != nil {
		return err
	}
	if r.state == protocol.StateClosed {
		return refuse(http.StatusGone, "%s is closed", runID)
	}
	if blank(p.Scenario) {
		return refuse(http.StatusBadRequest, "a progress names its scenario")
	}
	p.At = o.now().UTC()
	if p.Final {
		body, err := json.Marshal(p)
		if err != nil {
			return err
		}
		err = o.tx(ctx, func(tx *sql.Tx) error {
			var had sql.NullString
			if err := tx.QueryRowContext(ctx, `select progress from runs where id = ?`, runID).Scan(&had); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `update runs set progress = ? where id = ?`, string(body), runID); err != nil {
				return err
			}
			if had.Valid {
				return nil
			}
			return o.logRun(ctx, tx, runID, "suite", summarize(p))
		})
		if err != nil {
			return err
		}
	}
	o.progress.set(runID, p, o.now())
	return nil
}

// Progress is the latest its suite said about a run: the live progress while it says it, the
// final one after.
func (o *Orchestrator) Progress(ctx context.Context, runID string) (protocol.Progress, error) {
	if p, ok := o.progress.get(runID, o.now()); ok {
		return p, nil
	}
	var body sql.NullString
	err := o.ro.QueryRowContext(ctx, `select progress from runs where id = ?`, runID).Scan(&body)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return protocol.Progress{}, refuse(http.StatusNotFound, "no run with id %s", runID)
	case err != nil:
		return protocol.Progress{}, err
	case !body.Valid:
		return protocol.Progress{}, refuse(http.StatusNotFound, "the suite of %s has said nothing about how it is going", runID)
	}
	var p protocol.Progress
	return p, json.Unmarshal([]byte(body.String), &p)
}

// summarize is the journal's line for a scenario's final progress.
func summarize(p protocol.Progress) string {
	passed := 0
	for _, s := range p.Steps {
		if s.Status == "passed" {
			passed++
		}
	}
	status := p.Status
	if status == "" {
		status = "ended"
	}
	parts := []string{fmt.Sprintf("%q %s: %d of %d steps passed", p.Scenario, status, passed, len(p.Steps))}
	for _, l := range p.Load {
		s := fmt.Sprintf("%s sent %d of %d to %s at %g/s, %d failed", l.Stream, l.Sent, l.Planned, l.Target, l.Rate, l.Failed)
		if l.Latency != nil {
			s += fmt.Sprintf(", p99 %g ms", l.Latency.P99)
		}
		parts = append(parts, s)
	}
	for _, c := range p.Checks {
		s := c.Check + ": " + c.Status
		if c.Found != "" {
			s += " (" + c.Found + ")"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "; ")
}
