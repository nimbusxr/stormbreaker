package sbcore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/nimbusxr/stormbreaker/arrival"
	"github.com/nimbusxr/stormbreaker/protocol"
)

// A run's progress (ADR 0027): how its scenario is going, sent to the orchestrator about once a
// second while the run is open, for whoever watches it. Nothing waits on it: a progress that
// does not go out costs the run nothing.

// progressKeepAlive is the longest the progress goes unsent, changed or not: the orchestrator
// forgets a progress nobody renews for a minute.
const progressKeepAlive = 10 * time.Second

// Load adds a stream's load to the run's progress. fn is called each time the progress goes
// out, from another goroutine: it must be quick, and safe to call concurrently. A stream that
// reports again replaces what it reported.
func (r *Run) Load(stream string, fn func() protocol.LoadProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loads == nil {
		r.loads = map[string]func() protocol.LoadProgress{}
	}
	if _, ok := r.loads[stream]; !ok {
		r.loadOrder = append(r.loadOrder, stream)
	}
	r.loads[stream] = fn
}

// Arrivals is a Load for a run of arrivals: what live follows, of stream, sent by how (rest,
// kafka) to target at rate a second, planned records in all.
func Arrivals(stream, how, target string, rate float64, planned int64, live *arrival.Live) func() protocol.LoadProgress {
	return func() protocol.LoadProgress {
		p := protocol.LoadProgress{
			Stream: stream, Protocol: how, Target: target, Rate: rate, Planned: planned,
			Sent: live.Sent(), InFlight: live.InFlight(), Failed: live.Failed(), Done: live.Done(),
		}
		if h := live.Latency(); h != nil && h.Count() > 0 {
			ms := func(d time.Duration) float64 { return math.Round(float64(d)/float64(time.Millisecond)*10) / 10 }
			p.Latency = &protocol.Latency{P50: ms(h.Quantile(0.5)), P90: ms(h.Quantile(0.9)), P99: ms(h.Quantile(0.99)), Max: ms(h.Max())}
		}
		return p
	}
}

// Check adds a check's verdict to the run's progress: waiting while it judges, then passed or
// failed. A check reported again, by what it checked, replaces what it reported.
func (r *Run) Check(c protocol.CheckResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i := slices.IndexFunc(r.checks, func(o protocol.CheckResult) bool { return o.Check == c.Check }); i >= 0 {
		r.checks[i] = c
		return
	}
	r.checks = append(r.checks, c)
}

// progress is the run's progress as it is now.
func (r *Run) progress() protocol.Progress {
	p := protocol.Progress{Scenario: r.sc.Name, Feature: fmt.Sprintf("%s:%d", r.sc.URI, r.sc.Line), Steps: []protocol.StepProgress{}}
	for _, s := range r.sc.Progress() {
		p.Steps = append(p.Steps, protocol.StepProgress{Keyword: s.Keyword, Text: s.Text, Status: s.Status, Error: s.Error})
	}
	r.mu.Lock()
	loads := make([]func() protocol.LoadProgress, 0, len(r.loadOrder))
	for _, name := range r.loadOrder {
		loads = append(loads, r.loads[name])
	}
	p.Checks = slices.Clone(r.checks)
	r.mu.Unlock()
	for _, fn := range loads {
		p.Load = append(p.Load, fn())
	}
	return p
}

// report sends the run's progress until ctx ends: when it changes, and at least every
// progressKeepAlive. A failed send waits for the next.
func (r *Run) report(ctx context.Context) {
	defer r.beats.Done()
	t := time.NewTicker(r.settings.progress)
	defer t.Stop()
	var last []byte
	var sent time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p := r.progress()
			body, _ := json.Marshal(p)
			if bytes.Equal(body, last) && time.Since(sent) < progressKeepAlive {
				continue
			}
			err := r.client.SendProgress(ctx, r.id, p)
			if protocol.StatusOf(err) == http.StatusGone {
				return
			}
			if err == nil {
				last, sent = body, time.Now()
			}
		}
	}
}

// reportFinal sends the scenario's last progress, with its status, before the run closes.
func (r *Run) reportFinal(ctx context.Context) {
	p := r.progress()
	p.Status, p.Final = r.sc.Status(), true
	err := throughRestarts(ctx, func() error { return r.client.SendProgress(ctx, r.id, p) })
	if err != nil && protocol.StatusOf(err) != http.StatusGone {
		r.sc.Log("stormbreaker: could not send run %s's final progress: %v", r.id, err)
	}
}
