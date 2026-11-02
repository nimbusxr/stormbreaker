//go:build scale

package sbkafka

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nimbusxr/axx/core"
	"github.com/nimbusxr/axx/packs/kafka"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/labtest"
	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
)

// TestPublishAtScale publishes streams through a real broker at rising rates, as JSON and as
// Avro with a real Schema Registry, and counts what reached the topic. STORMBREAKER_SCALE_RATES
// (e.g. "100000,200000") replaces the rates, each tried as JSON and as Avro.
// STORMBREAKER_SCALE_WARM publishes that many records through the same producer first.
//
//	STORMBREAKER_SCALE_KAFKA=localhost:19092 STORMBREAKER_SCALE_REGISTRY=http://localhost:18095 \
//	  go test -tags scale -run PublishAtScale -timeout 30m -v ./packs/kafka
func TestPublishAtScale(t *testing.T) {
	envInt := func(name string, def int) int {
		if v := os.Getenv(name); v != "" {
			var n int
			if _, err := fmt.Sscan(v, &n); err == nil {
				return n
			}
		}
		return def
	}
	brokers, registryURL := os.Getenv("STORMBREAKER_SCALE_KAFKA"), os.Getenv("STORMBREAKER_SCALE_REGISTRY")
	if brokers == "" {
		t.Skip("set STORMBREAKER_SCALE_KAFKA to a broker")
	}
	lab := labtest.New(t)
	lab.Start("parcels", "east", []string{"delivery"}, dispatcher.Hooks{})
	const seconds = 10
	type publish struct {
		name    string
		avro    bool
		rate    int
		fixture string
	}
	const jsonFixture, avroFixture = "parcel-registered#registered", "depot-scan#sorted"
	cases := []publish{
		{"json", false, 25_000, jsonFixture},
		{"json", false, 50_000, jsonFixture},
		{"json", false, 100_000, jsonFixture},
		{"avro", true, 50_000, avroFixture},
		{"avro", true, 100_000, avroFixture},
	}
	if s := os.Getenv("STORMBREAKER_SCALE_RATES"); s != "" {
		cases = nil
		for _, f := range strings.Split(s, ",") {
			var r int
			if _, err := fmt.Sscan(strings.TrimSpace(f), &r); err != nil {
				t.Fatalf("STORMBREAKER_SCALE_RATES: %v", err)
			}
			cases = append(cases, publish{"json", false, r, jsonFixture}, publish{"avro", true, r, avroFixture})
		}
	}
	for _, c := range cases {
		if c.avro && registryURL == "" {
			continue
		}
		count := c.rate * seconds
		topic := fmt.Sprintf("scale-%s-%d-%d", c.name, c.rate, time.Now().UnixNano())
		admin, err := kgo.NewClient(kgo.SeedBrokers(brokers))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := kadm.NewClient(admin).CreateTopic(context.Background(), 12, 1, nil, topic); err != nil {
			t.Fatal(err)
		}
		// A topic created a moment ago has no leaders yet. Publishing at once measures that wait,
		// which a topic of a real environment never has: wait for every partition to have one.
		for ready := false; !ready; {
			md, err := kadm.NewClient(admin).Metadata(context.Background(), topic)
			if err != nil {
				t.Fatal(err)
			}
			ps := md.Topics[topic].Partitions
			ready = len(ps) == 12
			for _, p := range ps {
				ready = ready && p.Err == nil && p.Leader >= 0
			}
			if !ready {
				time.Sleep(100 * time.Millisecond)
			}
		}
		root := t.TempDir()
		entries, _ := os.ReadDir("testdata/root")
		for _, e := range entries {
			b, _ := os.ReadFile(filepath.Join("testdata/root", e.Name()))
			_ = os.WriteFile(filepath.Join(root, e.Name()), b, 0o644)
		}
		_ = os.WriteFile(filepath.Join(root, "scale.stream.yaml"), fmt.Appendf(nil, "count: %d\nmix: [{fixture: %s, weight: 1}]\n", count, c.fixture), 0o644)
		warm := int64(envInt("STORMBREAKER_SCALE_WARM", 0))
		if warm > 0 {
			_ = os.WriteFile(filepath.Join(root, "warm.stream.yaml"), fmt.Appendf(nil, "count: %d\nmix: [{fixture: %s, weight: 1}]\n", warm, c.fixture), 0o644)
		}

		cfg, _ := json.Marshal(sbcore.Config{URL: lab.URL, Token: labtest.Token, Fixtures: root, Readiness: "300s"})
		suite := core.NewSuite(core.SuiteOptions{
			PackConfig: map[string]json.RawMessage{sbcore.Name: cfg}, Interpolate: func(s string) string { return s },
			ResolvePath: func(p string) (string, error) { return p, nil },
		})
		sc := core.NewScenario(context.Background(), core.ScenarioInfo{Name: "scale", Tags: []string{"@stormbreaker-group/delivery"}}, suite, &sink{})
		for _, h := range sbcore.Pack().Manifest().Hooks {
			if h.Phase == core.BeforeScenario {
				if err := h.Run(sc); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := kafka.Context(sc).AddService(&kafka.Service{Name: "events", Brokers: brokers}); err != nil {
			t.Fatal(err)
		}
		if c.avro {
			var clientStep func(*core.Scenario, core.Args) error
			for _, s := range kafka.Pack().Manifest().Steps {
				if s.ID == "kafka.client.props" {
					clientStep = s.Run
				}
			}
			if err := clientStep(sc, core.Args{
				List: []core.Arg{{Value: topic, Raw: topic, Present: true}, {}},
				Table: &core.Table{Rows: [][]string{
					{"producer.value.serializer", "io.confluent.kafka.serializers.KafkaAvroSerializer"},
					{"producer.schema.registry.url", registryURL},
				}},
			}); err != nil {
				t.Fatal(err)
			}
		}
		begin := time.Now()
		if err := step(t, "stormbreaker.kafka.stream")(sc, args("scale", topic, nil)); err != nil {
			t.Fatal(err)
		}
		bind := time.Since(begin)
		if err := step(t, "stormbreaker.kafka.stream.keys")(sc, args("scale", "key")); err != nil {
			t.Fatal(err)
		}
		// A warm-up stream first, through the same producer, when asked for: what is left is the
		// publishing rate once the producer has started (and Avro's schema is registered).
		if warm > 0 {
			if err := step(t, "stormbreaker.kafka.stream")(sc, args("warm", topic, nil)); err != nil {
				t.Fatal(err)
			}
			if err := step(t, "stormbreaker.kafka.stream.keys")(sc, args("warm", "key")); err != nil {
				t.Fatal(err)
			}
			if err := step(t, stepPublish)(sc, args("warm", 10_000)); err != nil {
				t.Fatalf("warming up: %v", err)
			}
		}
		pubErr := step(t, stepPublish)(sc, args("scale", c.rate))
		res := stateKey.Of(sc).last
		if res == nil {
			t.Fatalf("%s at %d/s: %v", c.name, c.rate, pubErr)
		}
		offsets, err := kadm.NewClient(admin).ListEndOffsets(context.Background(), topic)
		if err != nil {
			t.Fatal(err)
		}
		var landed int64
		offsets.Each(func(o kadm.ListedOffset) { landed += o.Offset })
		landed -= warm
		admin.Close()
		stopped := ""
		if pubErr != nil {
			stopped = " stopped: " + pubErr.Error()
		}
		t.Logf("%-4s %6d/s asked: %7.0f/s sent, %7d on the topic of %d, %d failed, latency p50 %8s p99 %8s, drift p99 %8s (bound in %s)%s %s",
			c.name, c.rate, float64(res.Sent)/res.Elapsed.Seconds(), landed, count, res.Failed,
			res.Latency.Quantile(0.5), res.Latency.Quantile(0.99), res.Drift.Quantile(0.99), bind.Round(time.Millisecond), res.FirstError, stopped)
		sc.SetStatus("failed")
		_ = sc.Close()
	}
}
