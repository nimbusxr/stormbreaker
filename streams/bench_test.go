package streams

import (
	"testing"
	"time"
)

// BenchmarkGenerate is how fast one goroutine generates records of a stream like a load test's:
// go test -run xxx -bench Generate -benchtime 20000x ./streams
func BenchmarkGenerate(b *testing.B) {
	def, err := Parse("bench", []byte("count: 100000000\nmix: [{fixture: bare/bare#no-identity, weight: 1}]"))
	if err != nil {
		b.Fatal(err)
	}
	s, err := Open(def, root, Options{Seed: "run-1", Run: "run-1", Window: time.Now()})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	n := 0
	for r, err := range s.Range(0, int64(b.N)) {
		if err != nil {
			b.Fatal(err)
		}
		_ = r.Data.CompactJSON()
		n++
	}
	b.ReportMetric(float64(n)/b.Elapsed().Seconds(), "records/s")
}

// BenchmarkGenerateSeries is the lifecycle stream: two-message series, refs and expressions.
func BenchmarkGenerateSeries(b *testing.B) {
	def, err := Parse("bench", []byte("count: 100000000\nmix: [{series: [parcel#standard, parcel-collection#booked], weight: 1}]"))
	if err != nil {
		b.Fatal(err)
	}
	s, err := Open(def, root, Options{Seed: "run-1", Run: "run-1", Window: time.Now(), Functions: functions()})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	n := 0
	for r, err := range s.Range(0, int64(b.N)) {
		if err != nil {
			b.Fatal(err)
		}
		_ = r.Data.CompactJSON()
		n++
	}
	b.ReportMetric(float64(n)/b.Elapsed().Seconds(), "records/s")
}

// BenchmarkIdentities is how fast a stream's identities are generated for declaring, on every CPU.
func BenchmarkIdentities(b *testing.B) {
	def, err := Parse("bench", []byte("count: 2000000\nmix: [{fixture: bare/bare#no-identity, weight: 1}]"))
	if err != nil {
		b.Fatal(err)
	}
	s, err := Open(def, root, Options{Seed: "run-1", Run: "run-1", Window: time.Now()})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	n := 0
	for range b.N {
		got, err := s.Identities(25_000, func([]string) error { return nil })
		if err != nil {
			b.Fatal(err)
		}
		n += got
	}
	b.ReportMetric(float64(n)/b.Elapsed().Seconds(), "identities/s")
}
