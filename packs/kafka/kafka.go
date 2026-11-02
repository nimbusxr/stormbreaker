// Package sbkafka is the stormbreaker-kafka pack: load through a topic. A stream's records become
// whole Kafka records (key, value, headers), published at a declared arrival rate through a Kafka
// service axx's kafka pack declared, serialized as its publish steps serialize events: Avro with
// its Schema Registry when the record's factory is Avro, text otherwise. Events drafted by hand
// can join a run too: their identities are declared before they are published.
package sbkafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nimbusxr/axx/core"
	"github.com/nimbusxr/axx/packs/kafka"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/nimbusxr/stormbreaker/arrival"
	"github.com/nimbusxr/stormbreaker/internal/jsonpath"
	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
	"github.com/nimbusxr/stormbreaker/streams"
)

// Name is the pack's name.
const Name = "stormbreaker-kafka"

// Config is the pack's section of axx.yaml.
type Config struct {
	// MaxInFlight is how many events may be in flight before the publish stops, failing: the
	// cluster is not keeping up (default 50,000).
	MaxInFlight int64 `json:"maxInFlight"`
	// MaxDrift is how late events may leave their due time, for five seconds in a row, before the
	// publish stops, failing: the driver is not keeping up (default 1s; "off" turns it off).
	MaxDrift string `json:"maxDrift"`
}

type settings struct {
	maxInFlight int64
	maxDrift    time.Duration
}

func load(s *core.Suite) (*settings, error) {
	return core.Cached(s, Name+".settings", func() (*settings, error) {
		var c Config
		if err := s.PackConfig(Name, &c); err != nil {
			return nil, err
		}
		st := &settings{maxInFlight: c.MaxInFlight}
		switch v := s.Interpolate(c.MaxDrift); v {
		case "":
		case "off":
			st.maxDrift = -1
		default:
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return nil, fmt.Errorf("packs.%s.maxDrift is a duration, like 1s, or off, not %q", Name, v)
			}
			st.maxDrift = d
		}
		return st, nil
	})
}

var configSchema = json.RawMessage(`{
  "type": "object",
  "description": "How stormbreaker-kafka publishes its load.",
  "properties": {
    "maxInFlight": {"type": "integer", "description": "How many events may be in flight before the publish stops, failing because the cluster is not keeping up (default 50000)."},
    "maxDrift": {"type": "string", "description": "How late events may leave their due time, for five seconds in a row, before the publish stops, failing because the driver is not keeping up (default 1s; off turns it off)."}
  },
  "additionalProperties": false
}`)

// binding is a stream bound to a topic.
type binding struct {
	name, topic       string
	service           *kafka.Service
	stream            *streams.Stream
	keyMeta, headMeta string
}

type observer struct {
	topic, kind, path string // kind: payload or header
}

type scenarioContext struct {
	mu        sync.Mutex
	bindings  map[string]*binding
	observers []observer
	observing bool
	last      *arrival.Result
}

var stateKey = core.NewStateKey(Name, func(*core.Scenario) *scenarioContext {
	return &scenarioContext{bindings: map[string]*binding{}}
}, nil)

func (c *scenarioContext) binding(name string) (*binding, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.bindings[name]
	if !ok {
		return nil, fmt.Errorf("no %s stream is bound; bind it with \"the %s stream of <topic> kafka events\"", name, name)
	}
	return b, nil
}

const (
	stepPublish       = "stormbreaker.kafka.stream.publish"
	stepPublishSchema = "stormbreaker.kafka.stream.publish.schema"
	serviceSuffix     = "[[ on the {word} kafka service]]"
)

// Pack returns the stormbreaker-kafka pack.
func Pack() core.Pack {
	sbcore.Traffic(stepPublish, stepPublishSchema)
	return pack{}
}

type pack struct{}

func (pack) Manifest() core.Manifest {
	return core.Manifest{
		Name: Name, Namespace: Name, Requires: []string{"kafka"}, ConfigSchema: configSchema,
		Doc: "Load through a topic: a stream's records become whole Kafka records (key, value, headers), published at a " +
			"declared arrival rate through a Kafka service axx's kafka pack declared, serialized as its publish steps serialize: " +
			"Avro, with the Schema Registry, when the record's factory is Avro; text otherwise. Builds on stormbreaker-core " +
			"(list it in axx-packs.yaml): binding a stream declares the identities it will create before anything is published.",
		Steps: steps(),
	}
}

func steps() []core.StepDef {
	return []core.StepDef{
		{
			ID: "stormbreaker.kafka.stream", Keyword: "Given",
			Expr: "the {word} stream of {word} kafka events" + serviceSuffix,
			Doc: "Bind a stream (`<name>.stream.yaml`) to a topic: each record is an event, its data the value. Binding " +
				"declares every identity the stream will create, at the topic, before anything is published.",
			Examples: []string{"Given the scans stream of depot-scans kafka events"},
			Run:      bindStream,
		},
		{
			ID: "stormbreaker.kafka.stream.keys", Keyword: "Given",
			Expr: "the {word} stream kafka event keys are the {word} metadata",
			Doc:  "Take each event's key from a metadata entry of its record. Without one, an event's key is its record's first identity.",
			Run: func(sc *core.Scenario, a core.Args) error {
				b, err := stateKey.Of(sc).binding(a.String(0))
				if err == nil {
					b.keyMeta = a.String(1)
				}
				return err
			},
		},
		{
			ID: "stormbreaker.kafka.stream.headers", Keyword: "Given",
			Expr: "the {word} stream kafka event headers are the {word} metadata",
			Doc:  "Take each event's headers from a metadata entry of its record: a map, by header name.",
			Run: func(sc *core.Scenario, a core.Args) error {
				b, err := stateKey.Of(sc).binding(a.String(0))
				if err == nil {
					b.headMeta = a.String(1)
				}
				return err
			},
		},
		{
			ID: "stormbreaker.kafka.identified.payload", Keyword: "Given",
			Expr: "the {word} kafka events are identified by the payload {word}",
			Doc:  "Declare the identities in the payloads of the events drafted by hand for a topic before each is published: the values at the JSON paths given, comma-separated.",
			Run:  func(sc *core.Scenario, a core.Args) error { return identified(sc, a.String(0), "payload", a.String(1)) },
		},
		{
			ID: "stormbreaker.kafka.identified.header", Keyword: "Given",
			Expr: "the {word} kafka events are identified by the header {word}",
			Doc:  "Declare the identities in a header of the events drafted by hand for a topic before each is published.",
			Run:  func(sc *core.Scenario, a core.Args) error { return identified(sc, a.String(0), "header", a.String(1)) },
		},
		{
			ID: stepPublish, Keyword: "When",
			Expr:     "the {word} stream is published at {int} events per second",
			Doc:      "Publish the stream's events open-model: event i is due at i/rate seconds, however the ones before it are doing.",
			Examples: []string{"When the scans stream is published at 25 events per second"},
			Run:      func(sc *core.Scenario, a core.Args) error { return publish(sc, a.String(0), "", float64(a.Int(1))) },
		},
		{
			ID: stepPublishSchema, Keyword: "When",
			Expr:     "the {word} stream is published using schema {filepath} at {int} events per second",
			Doc:      "Publish the stream's events as values of an Avro schema, whatever their factory says.",
			Examples: []string{"When the scans stream is published using schema schemas/depot-scan.avsc at 25 events per second"},
			Run: func(sc *core.Scenario, a core.Args) error {
				return publish(sc, a.String(0), a.String(1), float64(a.Int(2)))
			},
		},
		{
			ID: "stormbreaker.kafka.caught-up", Keyword: "Then",
			Expr: "the {word} consumer group catches up on the {word} topic within {duration}" + serviceSuffix,
			Doc: "Wait until a consumer group has consumed everything published to a topic (its lag is zero on every partition), " +
				"so that what is downstream has settled before it is judged, and before cleanup. Fails when it has not caught up in time.",
			Examples: []string{"Then the tracking consumer group catches up on the parcel-events topic within 60s"},
			Run:      caughtUp,
		},
		{
			ID: "stormbreaker.kafka.no-failures", Keyword: "Then",
			Expr:     "the publish completes with no failed events",
			Doc:      "Check that no event of the last publish failed.",
			Examples: []string{"Then the publish completes with no failed events"},
			Run:      noFailures,
		},
	}
}

func currentRun(sc *core.Scenario) (*sbcore.Run, error) {
	r, ok := sbcore.Current(sc)
	if !ok {
		return nil, fmt.Errorf("a stream needs a stormbreaker run: tag the scenario @%s<group>, with %s in axx-packs.yaml", sbcore.TagGroup, sbcore.Name)
	}
	return r, nil
}

func service(sc *core.Scenario, a core.Args, i int) (*kafka.Service, error) {
	if a.Present(i) {
		return kafka.Context(sc).Service(a.String(i))
	}
	return kafka.Context(sc).Service()
}

func bindStream(sc *core.Scenario, a core.Args) error {
	r, err := currentRun(sc)
	if err != nil {
		return err
	}
	svc, err := service(sc, a, 2)
	if err != nil {
		return err
	}
	name, topic := a.String(0), a.String(1)
	s, err := r.Bind(sc.Context(), name, topic, "value")
	if err != nil {
		return err
	}
	c := stateKey.Of(sc)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.bindings[name]; ok {
		return fmt.Errorf("the %s stream is already bound", name)
	}
	c.bindings[name] = &binding{name: name, topic: topic, service: svc, stream: s}
	return nil
}

func identified(sc *core.Scenario, topic, kind, list string) error {
	r, err := currentRun(sc)
	if err != nil {
		return err
	}
	c := stateKey.Of(sc)
	c.mu.Lock()
	for _, p := range jsonpath.Paths(list) {
		c.observers = append(c.observers, observer{topic: topic, kind: kind, path: p})
	}
	first := !c.observing
	c.observing = true
	c.mu.Unlock()
	if first {
		sbcore.Observe(sc, func(r *sbcore.Run) error { return observe(sc, r) })
	}
	return observe(sc, r)
}

// observe declares the identities in the events drafted by hand: at the topic, by the payload
// path or header that names them.
func observe(sc *core.Scenario, r *sbcore.Run) error {
	c := stateKey.Of(sc)
	c.mu.Lock()
	obs := append([]observer(nil), c.observers...)
	c.mu.Unlock()
	for _, svc := range kafka.Context(sc).Services() {
		for _, tc := range svc.Topics() {
			for _, o := range obs {
				if o.topic != tc.Topic {
					continue
				}
				for _, ev := range tc.Events() {
					var ids []string
					switch o.kind {
					case "payload":
						found, err := jsonpath.Read([]byte(ev.Payload), o.path)
						if err != nil {
							return err
						}
						ids = found
					default:
						for _, h := range ev.Headers {
							if h.Key == o.path && h.Value != nil {
								ids = append(ids, *h.Value)
							}
						}
					}
					if err := r.DeclareObserved(sc.Context(), tc.Topic, o.kind+":"+o.path, ids); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// encoder turns records into Kafka records for one binding.
type encoder struct {
	b        *binding
	ser      *kafka.Serializer
	fixtures string
	schema   string // an explicit schema's text; "" for each record's own

	mu      sync.Mutex
	schemas map[string]string
}

func (e *encoder) schemaOf(rec *streams.Record) (string, error) {
	if e.schema != "" {
		return e.schema, nil
	}
	if rec.Family != "avro" {
		return "", nil
	}
	ref, _, _ := strings.Cut(rec.Schema, "#")
	if ref == "" {
		return "", fmt.Errorf("%s is an Avro fixture with no schema", rec.Ref)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.schemas[ref]; ok {
		return s, nil
	}
	b, err := os.ReadFile(filepath.Join(e.fixtures, filepath.FromSlash(ref)))
	if err != nil {
		return "", err
	}
	e.schemas[ref] = string(b)
	return string(b), nil
}

func (e *encoder) record(ctx context.Context, rec *streams.Record) (*kgo.Record, error) {
	kr := &kgo.Record{Topic: e.b.topic}
	key := rec.Identity()
	if e.b.keyMeta != "" {
		v := rec.Metadata.Get(e.b.keyMeta)
		switch v.(type) {
		case nil:
			return nil, fmt.Errorf("the %s metadata of %s holds no key", e.b.keyMeta, rec.Ref)
		case map[string]any, []any:
			return nil, fmt.Errorf("the %s metadata of %s is not a key a topic takes: keys are text (Avro record keys are not supported yet)", e.b.keyMeta, rec.Ref)
		}
		key = fmt.Sprint(v)
	}
	k, err := e.ser.Key(ctx, key)
	if err != nil {
		return nil, err
	}
	kr.Key = k
	schema, err := e.schemaOf(rec)
	if err != nil {
		return nil, err
	}
	// The family decides, not whether a schema is declared: a JSON factory with a JSON Schema
	// publishes JSON.
	if schema != "" {
		if kr.Value, err = e.ser.AvroJSON(ctx, schema, rec.Data.CompactJSON()); err != nil {
			return nil, fmt.Errorf("%s key %d: %w", rec.Ref, rec.Key, err)
		}
	} else {
		kr.Value = rec.Data.CompactJSON()
	}
	if e.b.headMeta != "" {
		v := rec.Metadata.Get(e.b.headMeta)
		if v != nil {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("the %s metadata of %s is not a map of headers", e.b.headMeta, rec.Ref)
			}
			names := make([]string, 0, len(m))
			for n := range m {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				kr.Headers = append(kr.Headers, kgo.RecordHeader{Key: n, Value: []byte(fmt.Sprint(m[n]))})
			}
		}
	}
	return kr, nil
}

func publish(sc *core.Scenario, name, schemaFile string, rate float64) error {
	r, err := currentRun(sc)
	if err != nil {
		return err
	}
	b, err := stateKey.Of(sc).binding(name)
	if err != nil {
		return err
	}
	if err := sbcore.Ready(sc); err != nil {
		return err
	}
	st, err := load(sc.Suite())
	if err != nil {
		return err
	}
	cl, err := b.service.Producer(sc, b.topic)
	if err != nil {
		return err
	}
	ser, err := b.service.Serializer(sc, b.topic)
	if err != nil {
		return err
	}
	enc := &encoder{b: b, ser: ser, fixtures: r.Fixtures(), schemas: map[string]string{}}
	if schemaFile != "" {
		path, err := sc.ResolvePath(schemaFile)
		if err != nil {
			return err
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		enc.schema = string(text)
	}
	ctx := sc.Context()
	records := func(yield func(*kgo.Record, error) bool) {
		for rec, err := range b.stream.All() {
			if err != nil {
				yield(nil, err)
				return
			}
			kr, err := enc.record(ctx, rec)
			if !yield(kr, err) || err != nil {
				return
			}
		}
	}
	// A producer connects to the partitions' leaders, and an Avro serializer registers its schema,
	// on their first event: the warm start does both before the rate applies, so a cold producer
	// neither drifts nor fills the in-flight bound in its first moments.
	live := &arrival.Live{}
	r.Load(name, sbcore.Arrivals(name, "kafka", b.topic, rate, b.stream.Records(), live))
	res, err := arrival.Run(ctx, rate, records, func(ctx context.Context, kr *kgo.Record) error {
		return cl.ProduceSync(ctx, kr).FirstErr()
	}, arrival.Options{MaxInFlight: st.maxInFlight, MaxDrift: st.maxDrift, Warm: true, Live: live})
	if res != nil {
		c := stateKey.Of(sc)
		c.mu.Lock()
		c.last = res
		c.mu.Unlock()
		body, _ := json.MarshalIndent(map[string]any{
			"stream": name, "topic": b.topic, "ratePerSecond": rate, "sent": res.Sent, "failed": res.Failed,
			"elapsed": res.Elapsed.Round(time.Millisecond).String(), "latencySeconds": res.Latency.Summary(),
			"arrivalDriftSeconds": res.Drift.Summary(), "firstError": res.FirstError,
		}, "", "  ")
		sc.Attach("application/json", body, "stormbreaker-kafka client view: "+name)
		sc.Log("stormbreaker-kafka: %s to %s: %d sent at %g/s, %d failed", name, b.topic, res.Sent, rate, res.Failed)
	}
	return err
}

// caughtUp waits for a consumer group's lag on a topic to reach zero.
func caughtUp(sc *core.Scenario, a core.Args) error {
	group, topic, patience := a.String(0), a.String(1), a.Value(2).(time.Duration)
	svc, err := service(sc, a, 3)
	if err != nil {
		return err
	}
	opts, err := svc.ProducerOpts(sc, topic)
	if err != nil {
		return err
	}
	cl, err := kgo.NewClient(opts...)
	if err != nil {
		return err
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	ctx, cancel := context.WithTimeout(sc.Context(), patience)
	defer cancel()
	// The lag is the topic's end offsets less the group's committed ones; a partition the group
	// committed nothing on has all of its records still to consume.
	var lag int64 = -1
	for {
		ends, err := adm.ListEndOffsets(ctx, topic)
		if err == nil {
			committed, ferr := adm.FetchOffsets(ctx, group)
			if errors.Is(ferr, kerr.GroupIDNotFound) {
				committed, ferr = kadm.OffsetResponses{}, nil // a group that never committed has consumed nothing
			}
			if ferr == nil {
				lag = 0
				ends.Each(func(o kadm.ListedOffset) {
					c, ok := committed.Lookup(topic, o.Partition)
					if !ok || c.At < 0 {
						lag += o.Offset
						return
					}
					lag += max(o.Offset-c.At, 0)
				})
				if lag == 0 {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return core.Fail(fmt.Sprintf("the %s consumer group did not catch up on %s within %s", group, topic, patience), "a lag of 0", fmt.Sprintf("a lag of %d", lag))
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func noFailures(sc *core.Scenario, _ core.Args) error {
	c := stateKey.Of(sc)
	c.mu.Lock()
	res := c.last
	c.mu.Unlock()
	if res == nil {
		return errors.New("no stream was published in this scenario")
	}
	if res.Failed > 0 {
		return core.Fail(fmt.Sprintf("%d of %d events of the last publish failed; the first: %s", res.Failed, res.Sent, res.FirstError), "0 failed", fmt.Sprintf("%d failed", res.Failed))
	}
	return nil
}
