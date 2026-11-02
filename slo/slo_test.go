package slo

import (
	"math"
	"testing"
)

var inf = math.Inf(1)

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("got %g, want %g", got, want)
	}
}

func TestMergeSumsCumulativeCountsAcrossSources(t *testing.T) {
	m := Merge(map[float64]float64{0.1: 60, 0.5: 90, inf: 100}, map[float64]float64{0.1: 40, 0.5: 60, inf: 100})
	near(t, m[0.1], 100)
	near(t, m[0.5], 150)
	near(t, m[inf], 200)
}

func TestQuantileInterpolatesWithinTheWinningBucket(t *testing.T) {
	b := map[float64]float64{0.1: 50, 0.5: 100, inf: 100}
	// The median sits exactly at the 0.1 boundary; p90 is 80% of the way through the second bucket.
	q, _ := Quantile(0.5, b)
	near(t, q, 0.1)
	q, _ = Quantile(0.9, b)
	near(t, q, 0.1+0.4*0.8)
}

func TestMergingChangesTheQuantileTheWayAveragingNeverCould(t *testing.T) {
	// One fast region and one slow, with the same traffic. The honest p99 lands in the slow
	// region's range; an average of the regions' p99s would understate it.
	fast := map[float64]float64{0.1: 100, 1: 100, inf: 100}
	slow := map[float64]float64{0.1: 0, 1: 100, inf: 100}
	if q, _ := Quantile(0.99, Merge(fast, slow)); q <= 0.9 {
		t.Errorf("the merged p99 is %g; it must reflect the slow region", q)
	}
}

func TestAnEmptyWindowIsAnErrorNotZero(t *testing.T) {
	if _, err := Quantile(0.99, map[float64]float64{0.1: 0, inf: 0}); err == nil {
		t.Error("a histogram with no observations gave a quantile")
	}
	if _, err := Quantile(0.99, nil); err == nil {
		t.Error("no buckets gave a quantile")
	}
}

func TestTheInfBucketCapsTheEstimateAtTheLastFiniteBound(t *testing.T) {
	q, _ := Quantile(0.99, map[float64]float64{0.1: 10, 0.5: 20, inf: 100})
	near(t, q, 0.5)
}
