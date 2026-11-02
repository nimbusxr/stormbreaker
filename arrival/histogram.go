package arrival

import (
	"encoding/json"
	"math"
	"sync/atomic"
	"time"
)

// Histogram counts durations in buckets that grow by a tenth each, from 10µs to an hour: a
// quantile read from it is within 10% of the truth. Recording is lock-free.
type Histogram struct {
	counts [buckets + 1]atomic.Int64 // the last counts what is over the top bucket
	max    atomic.Int64
	total  atomic.Int64
}

const (
	minBucket = 10 * time.Microsecond
	growth    = 1.1
	buckets   = 205 // 10µs * 1.1^204 ≈ 3,700s
)

// bounds[i] is bucket i's upper bound.
var bounds = func() [buckets]time.Duration {
	var b [buckets]time.Duration
	v := float64(minBucket)
	for i := range b {
		b[i] = time.Duration(v)
		v *= growth
	}
	return b
}()

// NewHistogram returns an empty histogram.
func NewHistogram() *Histogram { return &Histogram{} }

func bucketOf(d time.Duration) int {
	if d <= minBucket {
		return 0
	}
	i := int(math.Ceil(math.Log(float64(d)/float64(minBucket)) / math.Log(growth)))
	return min(i, buckets)
}

// Record counts one duration.
func (h *Histogram) Record(d time.Duration) {
	h.counts[bucketOf(d)].Add(1)
	h.total.Add(1)
	for {
		m := h.max.Load()
		if int64(d) <= m || h.max.CompareAndSwap(m, int64(d)) {
			return
		}
	}
}

// Count is how many durations it counted.
func (h *Histogram) Count() int64 { return h.total.Load() }

// Max is the longest duration it counted.
func (h *Histogram) Max() time.Duration { return time.Duration(h.max.Load()) }

// Quantile is the q quantile (0 to 1) of the durations counted: the upper bound of the bucket
// it falls in, never more than the longest duration counted.
func (h *Histogram) Quantile(q float64) time.Duration {
	total := h.total.Load()
	if total == 0 {
		return 0
	}
	rank := int64(math.Ceil(q * float64(total)))
	var seen int64
	for i := 0; i <= buckets; i++ {
		seen += h.counts[i].Load()
		if seen >= max(rank, 1) {
			if i == buckets {
				return h.Max()
			}
			return min(bounds[i], h.Max())
		}
	}
	return h.Max()
}

// Summary is a histogram in a report: its count and the quantiles a run is judged on, in
// seconds.
type Summary struct {
	Count int64   `json:"count"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P99   float64 `json:"p99"`
	P999  float64 `json:"p999"`
	Max   float64 `json:"max"`
}

// Summary summarizes the histogram.
func (h *Histogram) Summary() Summary {
	return Summary{
		Count: h.Count(), P50: h.Quantile(0.5).Seconds(), P90: h.Quantile(0.9).Seconds(),
		P99: h.Quantile(0.99).Seconds(), P999: h.Quantile(0.999).Seconds(), Max: h.Max().Seconds(),
	}
}

// MarshalJSON writes the histogram's summary.
func (h *Histogram) MarshalJSON() ([]byte, error) { return json.Marshal(h.Summary()) }
