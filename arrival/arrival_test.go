package arrival

import (
	"context"
	"errors"
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestArrivalsKeepTheirRate(t *testing.T) {
	var mu sync.Mutex
	var at []time.Time
	res, err := Run(context.Background(), 200, Count(100), func(context.Context, int64) error {
		mu.Lock()
		at = append(at, time.Now())
		mu.Unlock()
		return nil
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 100 || res.Failed != 0 {
		t.Fatalf("sent %d, failed %d", res.Sent, res.Failed)
	}
	// 100 arrivals at 200 a second span about half a second.
	span := at[len(at)-1].Sub(at[0])
	if span < 450*time.Millisecond || span > 700*time.Millisecond {
		t.Errorf("100 arrivals at 200/s took %s", span)
	}
}

func TestASlowServiceGetsTheSameArrivals(t *testing.T) {
	// Open model: sends that take a second each do not slow the arrivals down.
	var inFlight, peak atomic.Int64
	start := time.Now()
	res, err := Run(context.Background(), 100, Count(50), func(ctx context.Context, _ int64) error {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(time.Second)
		inFlight.Add(-1)
		return nil
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 50 || peak.Load() < 40 {
		t.Errorf("sent %d with at most %d in flight; a closed loop would have had one", res.Sent, peak.Load())
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("50 arrivals at 100/s with one-second sends took %s", elapsed)
	}
	if res.Latency.Quantile(0.5) < 900*time.Millisecond {
		t.Errorf("the client view's p50 is %s", res.Latency.Quantile(0.5))
	}
}

func TestFailuresAndTimeoutsAreCounted(t *testing.T) {
	res, err := Run(context.Background(), 1000, Count(10), func(ctx context.Context, i int64) error {
		switch i {
		case 3:
			return errors.New("503 Service Unavailable")
		case 7:
			<-ctx.Done() // never answers: the timeout ends it
			return ctx.Err()
		}
		return nil
	}, Options{Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent != 10 || res.Failed != 2 || res.FirstError == "" {
		t.Errorf("sent %d, failed %d, first error %q", res.Sent, res.Failed, res.FirstError)
	}
}

func TestAFailingSourceStopsTheArrivals(t *testing.T) {
	boom := errors.New("the stream cannot generate key 5")
	items := func(yield func(int, error) bool) {
		for i := range 10 {
			if i == 5 {
				yield(0, boom)
				return
			}
			if !yield(i, nil) {
				return
			}
		}
	}
	res, err := Run(context.Background(), 1000, iter.Seq2[int, error](items), func(context.Context, int) error { return nil }, Options{})
	if !errors.Is(err, boom) || res.Sent != 5 {
		t.Errorf("sent %d, err %v", res.Sent, err)
	}
}

func TestCancellingStopsTheArrivals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res, err := Run(ctx, 10, Count(1000), func(context.Context, int64) error { return nil }, Options{})
	if !errors.Is(err, context.DeadlineExceeded) || res.Sent > 5 {
		t.Errorf("sent %d, err %v", res.Sent, err)
	}
	if _, err := Run(context.Background(), 0, Count(1), func(context.Context, int64) error { return nil }, Options{}); err == nil {
		t.Error("a rate of zero was accepted")
	}
}

func TestAnOverloadedTargetStopsTheRun(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	res, err := Run(context.Background(), 1000, Count(10_000), func(ctx context.Context, _ int64) error {
		select { // a target that never answers in time
		case <-release:
		case <-ctx.Done():
		}
		return ctx.Err()
	}, Options{MaxInFlight: 100, Timeout: 2 * time.Second})
	if !errors.Is(err, ErrOverloaded) || res.Sent != 100 {
		t.Errorf("sent %d, err %v", res.Sent, err)
	}
}

func TestADriverThatFallsBehindStopsTheRun(t *testing.T) {
	// Arrivals that cannot be made ready in time: each takes 5ms, at 1,000 a second.
	slow := func(yield func(int64, error) bool) {
		for i := range int64(10_000) {
			time.Sleep(5 * time.Millisecond)
			if !yield(i, nil) {
				return
			}
		}
	}
	start := time.Now()
	res, err := Run(context.Background(), 1000, slow, func(context.Context, int64) error { return nil },
		Options{MaxDrift: 50 * time.Millisecond, DriftFor: 200 * time.Millisecond})
	if !errors.Is(err, ErrBehind) || res.Sent == 0 || time.Since(start) > 3*time.Second {
		t.Errorf("sent %d in %s, err %v", res.Sent, time.Since(start), err)
	}
	// A late moment that recovers does not stop it, and the guardrail can be turned off.
	if _, err := Run(context.Background(), 1000, Count(200), func(context.Context, int64) error { return nil },
		Options{MaxDrift: -1}); err != nil {
		t.Errorf("with the guardrail off: %v", err)
	}
}

func TestAWarmStartSendsTheFirstArrivalBeforeTheClock(t *testing.T) {
	var first time.Time
	var n atomic.Int64
	res, err := Run(context.Background(), 1000, Count(20), func(context.Context, int64) error {
		if n.Add(1) == 1 {
			first = time.Now()
			time.Sleep(300 * time.Millisecond) // a cold client connecting
		}
		return nil
	}, Options{Warm: true})
	if err != nil || res.Sent != 20 || time.Since(first) < 300*time.Millisecond {
		t.Fatalf("sent %d, err %v", res.Sent, err)
	}
	// The cold first send made nothing after it late.
	if p := res.Drift.Quantile(1); p > 100*time.Millisecond {
		t.Errorf("the slowest arrival left %s late", p)
	}
}

func TestHistogramQuantiles(t *testing.T) {
	h := NewHistogram()
	if h.Quantile(0.99) != 0 {
		t.Error("an empty histogram has a quantile")
	}
	for i := 1; i <= 1000; i++ {
		h.Record(time.Duration(i) * time.Millisecond)
	}
	for q, want := range map[float64]time.Duration{0.5: 500 * time.Millisecond, 0.9: 900 * time.Millisecond, 0.99: 990 * time.Millisecond} {
		got := h.Quantile(q)
		if got < want || float64(got) > float64(want)*1.1 {
			t.Errorf("p%g = %s, want %s to 10%% over", q*100, got, want)
		}
	}
	if h.Max() != time.Second || h.Quantile(1) != time.Second || h.Count() != 1000 {
		t.Errorf("max %s, p100 %s, count %d", h.Max(), h.Quantile(1), h.Count())
	}
	h.Record(2 * time.Hour)
	if h.Quantile(1) != 2*time.Hour {
		t.Errorf("past the top bucket: %s", h.Quantile(1))
	}
}

// BenchmarkArrivalDrift is the rate-fidelity check: 100,000 arrivals a second, sent nowhere, and
// how late they left. go test -bench ArrivalDrift -benchtime 1x ./arrival
func BenchmarkArrivalDrift(b *testing.B) {
	for range b.N {
		res, err := Run(context.Background(), 100_000, Count(200_000), func(context.Context, int64) error { return nil }, Options{})
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(res.Sent)/res.Elapsed.Seconds(), "arrivals/s")
		b.ReportMetric(float64(res.Drift.Quantile(0.5).Microseconds()), "p50-drift-µs")
		b.ReportMetric(float64(res.Drift.Quantile(0.99).Microseconds()), "p99-drift-µs")
		b.ReportMetric(float64(res.Drift.Max().Microseconds()), "max-drift-µs")
	}
}

func TestALiveRunCanBeFollowedAsItGoes(t *testing.T) {
	live := &Live{}
	release := make(chan struct{})
	var seen sync.WaitGroup
	seen.Add(1)
	go func() {
		defer seen.Done()
		for live.InFlight() < 5 {
			time.Sleep(time.Millisecond)
		}
		if live.Sent() < 5 || live.Done() || live.Latency() == nil {
			t.Errorf("mid-run: sent %d, done %v", live.Sent(), live.Done())
		}
		close(release)
	}()
	res, err := Run(context.Background(), 1000, Count(10), func(_ context.Context, i int64) error {
		<-release
		if i%5 == 0 {
			return errors.New("refused")
		}
		return nil
	}, Options{Live: live})
	seen.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if !live.Done() || live.Sent() != res.Sent || live.Failed() != 2 || live.InFlight() != 0 || live.Latency().Count() != 10 {
		t.Fatalf("after the run: sent %d of %d, failed %d, in flight %d, done %v", live.Sent(), res.Sent, live.Failed(), live.InFlight(), live.Done())
	}
}
