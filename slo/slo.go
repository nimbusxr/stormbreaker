// Package slo is what a pack that judges a run against its SLOs builds on: the run's window,
// judging a check within a patience, and merging latency histograms honestly across the sources
// that report them. stormbreaker-prometheus uses it; a pack for another metrics vendor can too
// (ADR 0018).
package slo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/nimbusxr/axx/core"

	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
)

// Window is a run's window: from its opening to when its traffic was complete, or to now while
// it is still under way.
type Window struct {
	From, To time.Time
}

// Length is how long the window is.
func (w Window) Length() time.Duration { return w.To.Sub(w.From) }

// Seconds is the window's length in whole seconds, rounded up: a range a query language takes.
func (w Window) Seconds() int64 { return int64(math.Ceil(w.Length().Seconds())) }

// RunWindow is the window of the scenario's stormbreaker run. A window shorter than minimum (a
// source's scrape or reporting interval) is refused as a failed check, because it cannot be
// judged honestly; source names what the minimum is for, in the failure.
func RunWindow(sc *core.Scenario, source string, minimum time.Duration) (Window, error) {
	r, ok := sbcore.Current(sc)
	if !ok {
		return Window{}, fmt.Errorf("a check over the run needs a stormbreaker run: tag the scenario @%s<group>, with %s in axx-packs.yaml",
			sbcore.TagGroup, sbcore.Name)
	}
	run, err := r.Client().Run(sc.Context(), r.ID())
	if err != nil {
		return Window{}, err
	}
	w := Window{From: run.WindowFrom, To: time.Now()}
	if run.WindowTo != nil {
		w.To = *run.WindowTo
	}
	if w.Length() < minimum {
		got := w.Length().Round(time.Millisecond)
		return w, core.Fail(fmt.Sprintf("the run's window is %s, shorter than %s's minimum of %s: a window shorter than the scrape "+
			"interval cannot be judged honestly", got, source, minimum), minimum.String(), got.String())
	}
	return w, nil
}

// JudgeEvery is how often Judge asks again while a check does not hold.
var JudgeEvery = 2 * time.Second

// Judge runs check until it holds or patience runs out, and then fails with check's last failure.
// Metrics arrive late (a scrape interval, an ingestion delay): a check waits that out by asking
// again, never by sleeping a guessed time first.
func Judge(ctx context.Context, patience time.Duration, check func(context.Context) error) error {
	deadline := time.Now().Add(patience)
	for {
		err := check(ctx)
		if err == nil {
			return nil
		}
		if time.Now().Add(JudgeEvery).After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(JudgeEvery):
		}
	}
}

// Merge sums cumulative histogram buckets (upper bound to count) across sources: regions, servers,
// replicas of an app. It is the only honest way to combine them; percentiles from different
// sources are never averaged.
func Merge(sources ...map[float64]float64) map[float64]float64 {
	out := map[float64]float64{}
	for _, b := range sources {
		for le, n := range b {
			out[le] += n
		}
	}
	return out
}

// Quantile interpolates the q quantile (0 to 1) of cumulative buckets within the bucket it falls
// in, as Prometheus's histogram_quantile does. A quantile in the +Inf bucket is the highest
// finite bound.
func Quantile(q float64, buckets map[float64]float64) (float64, error) {
	if len(buckets) == 0 {
		return 0, errors.New("no histogram buckets: is the metric a histogram, and was it scraped in the window?")
	}
	bounds := make([]float64, 0, len(buckets))
	for le := range buckets {
		bounds = append(bounds, le)
	}
	sort.Float64s(bounds)
	total := buckets[bounds[len(bounds)-1]]
	if total <= 0 {
		return 0, errors.New("the histogram holds no observations in the window")
	}
	rank := q * total
	prevBound, prevCount := 0.0, 0.0
	for _, le := range bounds {
		n := buckets[le]
		if n >= rank {
			if math.IsInf(le, 1) {
				return prevBound, nil
			}
			inBucket := n - prevCount
			if inBucket <= 0 {
				return le, nil
			}
			return prevBound + (le-prevBound)*((rank-prevCount)/inBucket), nil
		}
		if !math.IsInf(le, 1) {
			prevBound = le
		}
		prevCount = n
	}
	return prevBound, nil
}
