package sbkafka

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nimbusxr/axx/core"
	"github.com/nimbusxr/axx/packs/kafka"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/labtest"
	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
)

type sink struct {
	mu          sync.Mutex
	attachments map[string][]byte
}

func (*sink) Log(*core.Scenario, string) {}
func (s *sink) Attach(_ *core.Scenario, _ string, body []byte, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attachments == nil {
		s.attachments = map[string][]byte{}
	}
	s.attachments[name] = body
}

func step(t *testing.T, id string) func(*core.Scenario, core.Args) error {
	t.Helper()
	for _, s := range Pack().Manifest().Steps {
		if s.ID == id {
			return s.Run
		}
	}
	t.Fatalf("no step %s", id)
	return nil
}

func args(vals ...any) core.Args {
	var a core.Args
	for _, v := range vals {
		raw := ""
		switch x := v.(type) {
		case string:
			raw = x
		case int:
			b, _ := json.Marshal(x)
			raw = string(b)
		}
		a.List = append(a.List, core.Arg{Value: v, Raw: raw, Present: v != nil})
	}
	return a
}

func consume(t *testing.T, brokers []string, topic string, n int) []*kgo.Record {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics(topic), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out []*kgo.Record
	for len(out) < n {
		fetches := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			t.Fatalf("consumed %d of %d records", len(out), n)
		}
		out = append(out, fetches.Records()...)
	}
	return out
}

func TestAStreamIsDeclaredThenPublishedAsWholeRecords(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "parcel-events"))
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()

	lab := labtest.New(t)
	lab.Start("parcels", "east", []string{"delivery"}, dispatcher.Hooks{})
	root, _ := filepath.Abs("testdata/root")
	cfg, _ := json.Marshal(sbcore.Config{URL: lab.URL, Token: labtest.Token, Fixtures: root})
	suite := core.NewSuite(core.SuiteOptions{
		PackConfig: map[string]json.RawMessage{sbcore.Name: cfg}, Interpolate: func(s string) string { return s },
		ResolvePath: func(p string) (string, error) { return p, nil },
	})
	sk := &sink{}
	sc := core.NewScenario(context.Background(), core.ScenarioInfo{Name: "events", Tags: []string{"@stormbreaker-group/delivery"}}, suite, sk)
	for _, h := range sbcore.Pack().Manifest().Hooks {
		if h.Phase == core.BeforeScenario {
			if err := h.Run(sc); err != nil {
				t.Fatal(err)
			}
		}
	}
	defer func() {
		sc.SetStatus("passed")
		if err := sc.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := kafka.Context(sc).AddService(&kafka.Service{Name: "events", Brokers: strings.Join(cluster.ListenAddrs(), ",")}); err != nil {
		t.Fatal(err)
	}

	if err := step(t, "stormbreaker.kafka.stream")(sc, args("parcel-events", "parcel-events", nil)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"stormbreaker.kafka.stream.keys", "stormbreaker.kafka.stream.headers"} {
		meta := "key"
		if strings.HasSuffix(id, "headers") {
			meta = "headers"
		}
		if err := step(t, id)(sc, args("parcel-events", meta)); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := sbcore.Current(sc)
	run, _ := lab.Client.Run(lab.Ctx, r.ID())
	if run.Claims != 12 {
		t.Fatalf("binding declared %d identities", run.Claims)
	}
	if err := step(t, stepPublish)(sc, args("parcel-events", 500)); err != nil {
		t.Fatal(err)
	}
	if err := step(t, "stormbreaker.kafka.no-failures")(sc, core.Args{}); err != nil {
		t.Error(err)
	}

	got := consume(t, cluster.ListenAddrs(), "parcel-events", 12)
	for _, rec := range got {
		var v map[string]any
		if err := json.Unmarshal(rec.Value, &v); err != nil {
			t.Fatalf("a JSON factory's record was not published as JSON: %q", rec.Value)
		}
		if string(rec.Key) != v["reference"] {
			t.Errorf("key %q, reference %v", rec.Key, v["reference"])
		}
		headers := map[string]string{}
		for _, h := range rec.Headers {
			headers[h.Key] = string(h.Value)
		}
		if headers["source"] != "load-suite" || headers["event-type"] != "ParcelRegistered" {
			t.Errorf("headers %v", headers)
		}
	}
	if view := string(sk.attachments["stormbreaker-kafka client view: parcel-events"]); !strings.Contains(view, `"sent": 12`) {
		t.Errorf("client view:\n%s", view)
	}
}

func TestStepsNeedARunAndABinding(t *testing.T) {
	sc := core.NewScenario(context.Background(), core.ScenarioInfo{}, core.NewSuite(core.SuiteOptions{}), &sink{})
	if err := step(t, "stormbreaker.kafka.stream")(sc, args("s", "t", nil)); err == nil || !strings.Contains(err.Error(), "needs a stormbreaker run") {
		t.Errorf("binding without a run: %v", err)
	}
	if err := step(t, "stormbreaker.kafka.stream.keys")(sc, args("s", "key")); err == nil {
		t.Error("keys for a stream that is not bound")
	}
	if err := step(t, "stormbreaker.kafka.no-failures")(sc, core.Args{}); err == nil {
		t.Error("no failures with nothing published")
	}
}

func TestAConsumerGroupCatchesUp(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(2, "parcel-events"))
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	brokers := cluster.ListenAddrs()
	prod, _ := kgo.NewClient(kgo.SeedBrokers(brokers...))
	for i := range 20 {
		if err := prod.ProduceSync(context.Background(), &kgo.Record{Topic: "parcel-events", Key: []byte{byte(i)}, Value: []byte("x")}).FirstErr(); err != nil {
			t.Fatal(err)
		}
	}
	prod.Close()

	sc := core.NewScenario(context.Background(), core.ScenarioInfo{}, core.NewSuite(core.SuiteOptions{Interpolate: func(s string) string { return s }}), &sink{})
	if err := kafka.Context(sc).AddService(&kafka.Service{Name: "events", Brokers: strings.Join(brokers, ",")}); err != nil {
		t.Fatal(err)
	}
	caught := step(t, "stormbreaker.kafka.caught-up")
	if err := caught(sc, args("tracking", "parcel-events", 1500*time.Millisecond, nil)); !core.IsAssertion(err) || !strings.Contains(err.Error(), "a lag of 20") {
		t.Fatalf("a group that consumed nothing: %v", err)
	}

	cons, _ := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumerGroup("tracking"), kgo.ConsumeTopics("parcel-events"), kgo.DisableAutoCommit())
	defer cons.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for n := 0; n < 20; {
		f := cons.PollFetches(ctx)
		if ctx.Err() != nil {
			t.Fatal("consuming timed out")
		}
		n += len(f.Records())
	}
	if err := cons.CommitUncommittedOffsets(ctx); err != nil {
		t.Fatal(err)
	}
	if err := caught(sc, args("tracking", "parcel-events", 5*time.Second, nil)); err != nil {
		t.Errorf("a group that consumed everything: %v", err)
	}
}
