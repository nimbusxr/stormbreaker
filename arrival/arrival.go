// Package arrival drives traffic open-model: arrival i is due at start + i/rate, whatever the
// arrivals before it are doing. A service that slows down gets the same arrivals and more of them
// in flight, as it would in production, never fewer; closed-loop throttling would hide exactly
// the slowness a load test is for. Each arrival's due time is worked out from its index, so drift
// never accumulates.
//
// It is stormbreaker's public scheduler for packs that send traffic (stormbreaker-rest and
// stormbreaker-kafka use it; a pack for another protocol can too: ADR 0018). Two guardrails stop a
// run that cannot be what it says: too many arrivals in flight (the target is not keeping up), and
// arrivals leaving late for too long (the driver is not keeping up).
package arrival

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sync"
	"sync/atomic"
	"time"
)

// LookAhead is how many arrivals are prepared ahead of their due time.
const LookAhead = 4096

// ErrOverloaded is a run of arrivals stopped because too many were in flight: the target is not
// keeping up with the rate. Open model means arrivals do not wait for it; a guardrail means the
// driver does not drown either.
var ErrOverloaded = errors.New("the target is not keeping up")

// ErrBehind is a run of arrivals stopped because they kept leaving late: the driver is not
// keeping up with the rate, so the load is no longer what was declared. Its client view would
// describe a smaller load than the one asked for.
var ErrBehind = errors.New("the driver is not keeping up")

// Options shape a run of arrivals.
type Options struct {
	// Timeout bounds each arrival's send (default 30s).
	Timeout time.Duration
	// MaxInFlight is how many arrivals may be in flight at once before the run stops with
	// ErrOverloaded (default 50,000).
	MaxInFlight int64
	// Lead is how long after Run starts the first arrival is due (default 50ms), so preparing the
	// first arrivals does not make them late.
	Lead time.Duration
	// MaxDrift is how late arrivals may leave, for longer than DriftFor in a row, before the run
	// stops with ErrBehind (default 1s). A negative value turns the guardrail off.
	MaxDrift time.Duration
	// DriftFor is how long arrivals may leave later than MaxDrift before the run stops (default 5s).
	DriftFor time.Duration
	// Warm sends the first arrival, and waits for it, before the clock starts: a client that
	// connects, or registers a schema, on its first send does that before the rate applies, so
	// its first moments neither drift nor pile up in flight. The first arrival counts like any
	// other.
	Warm bool
	// Live, if set, follows the run as it goes, for whoever shows it while it goes.
	Live *Live
}

// Live is a run of arrivals as it goes: Run keeps it up to date when Options.Live is set, and it
// is safe to read from another goroutine meanwhile.
type Live struct {
	sent, failed, inFlight atomic.Int64
	latency                atomic.Pointer[Histogram]
	done                   atomic.Bool
}

// Sent counts the arrivals sent so far; Failed those whose send failed; InFlight those sent and
// not answered yet.
func (l *Live) Sent() int64     { return l.sent.Load() }
func (l *Live) Failed() int64   { return l.failed.Load() }
func (l *Live) InFlight() int64 { return l.inFlight.Load() }

// Latency is how long the sends answered so far took: nil until the run starts.
func (l *Live) Latency() *Histogram { return l.latency.Load() }

// Done says the run has ended: every arrival sent and answered, or it stopped.
func (l *Live) Done() bool { return l.done.Load() }

// Result is how a run of arrivals went, seen from the client: the client view, which is labelled
// apart from what the service's own metrics say.
type Result struct {
	// Sent and Failed count the arrivals sent, and those whose send failed.
	Sent, Failed int64
	// Rate is the arrival rate asked for, per second; Elapsed how long the run took.
	Rate    float64
	Elapsed time.Duration
	// Latency is how long the sends took; Drift how late arrivals left after their due time.
	Latency, Drift *Histogram
	// FirstError is the first failure, for the report.
	FirstError string
}

// Run sends every item of items at rate per second, each with send in a goroutine of its own,
// and waits for them all. It stops early when ctx is done or items fails, after waiting for the
// sends already under way.
func Run[T any](ctx context.Context, rate float64, items iter.Seq2[T, error], send func(context.Context, T) error, opts Options) (*Result, error) {
	if rate <= 0 {
		return nil, errors.New("an arrival rate is above zero")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Lead <= 0 {
		opts.Lead = 50 * time.Millisecond
	}
	if opts.MaxInFlight <= 0 {
		opts.MaxInFlight = 50_000
	}
	if opts.MaxDrift == 0 {
		opts.MaxDrift = time.Second
	}
	if opts.DriftFor <= 0 {
		opts.DriftFor = 5 * time.Second
	}
	type next struct {
		item T
		err  error
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ready := make(chan next, LookAhead)
	go func() {
		defer close(ready)
		for item, err := range items {
			select {
			case ready <- next{item, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	res := &Result{Rate: rate, Latency: NewHistogram(), Drift: NewHistogram()}
	live := opts.Live
	if live == nil {
		live = &Live{}
	}
	live.latency.Store(res.Latency)
	defer live.done.Store(true)
	sent, failed, inFlight := &live.sent, &live.failed, &live.inFlight
	var firstErr sync.Once
	var wg sync.WaitGroup
	if opts.Warm {
		n, ok := <-ready
		if !ok {
			res.Elapsed = 0
			return res, nil
		}
		if n.err != nil {
			return res, n.err
		}
		sctx, stop := context.WithTimeout(ctx, opts.Timeout)
		began := time.Now()
		err := send(sctx, n.item)
		stop()
		res.Latency.Record(time.Since(began))
		res.Drift.Record(0)
		sent.Add(1)
		if err != nil {
			failed.Add(1)
			firstErr.Do(func() { res.FirstError = err.Error() })
		}
	}
	start := time.Now().Add(opts.Lead)
	interval := float64(time.Second) / rate
	var runErr error
	var i int64
	var behindSince time.Time
	for n := range ready {
		if n.err != nil {
			runErr = n.err
			break
		}
		due := start.Add(time.Duration(float64(i) * interval))
		i++
		if wait := time.Until(due); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
			}
		}
		if ctx.Err() != nil {
			runErr = ctx.Err()
			break
		}
		if n := inFlight.Load(); n >= opts.MaxInFlight {
			runErr = fmt.Errorf("%w: %d arrivals in flight at %g a second, after %s", ErrOverloaded, n, rate, time.Since(start).Round(time.Millisecond))
			break
		}
		late := max(time.Since(due), 0)
		res.Drift.Record(late)
		if opts.MaxDrift > 0 && late > opts.MaxDrift {
			if behindSince.IsZero() {
				behindSince = time.Now()
			} else if time.Since(behindSince) >= opts.DriftFor {
				runErr = fmt.Errorf("%w: arrivals left more than %s late for %s, the last %s late, at %g a second, after %s",
					ErrBehind, opts.MaxDrift, opts.DriftFor, late.Round(time.Millisecond), rate, time.Since(start).Round(time.Millisecond))
				break
			}
		} else {
			behindSince = time.Time{}
		}
		sent.Add(1)
		inFlight.Add(1)
		wg.Add(1)
		go func(item T) {
			defer wg.Done()
			defer inFlight.Add(-1)
			sctx, stop := context.WithTimeout(ctx, opts.Timeout)
			defer stop()
			began := time.Now()
			err := send(sctx, item)
			res.Latency.Record(time.Since(began))
			if err != nil {
				failed.Add(1)
				firstErr.Do(func() { res.FirstError = err.Error() })
			}
		}(n.item)
	}
	wg.Wait()
	res.Sent, res.Failed = sent.Load(), failed.Load()
	res.Elapsed = time.Since(start)
	return res, runErr
}

// Count is n arrivals, indexed from zero, for a run of arrivals with no data of their own.
func Count(n int64) iter.Seq2[int64, error] {
	return func(yield func(int64, error) bool) {
		for i := range n {
			if !yield(i, nil) {
				return
			}
		}
	}
}
