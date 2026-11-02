package sbkafka

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/iskorotkov/avro/v2"
	"github.com/nimbusxr/axx/core"
	"github.com/nimbusxr/axx/packs/kafka"
	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/labtest"
	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
)

// registry is a Schema Registry in a test: it registers schemas under subjects and serves them
// by id, which is what the Confluent serializers ask of one.
type registry struct {
	mu       sync.Mutex
	schemas  []string
	subjects map[string]int
}

func (r *registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
	switch {
	case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/subjects/") && strings.HasSuffix(req.URL.Path, "/versions"):
		var body struct {
			Schema string `json:"schema"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		subject := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/subjects/"), "/versions")
		id := 0
		for i, s := range r.schemas {
			if s == body.Schema {
				id = i + 1
			}
		}
		if id == 0 {
			r.schemas = append(r.schemas, body.Schema)
			id = len(r.schemas)
		}
		r.subjects[subject] = id
		_ = json.NewEncoder(w).Encode(map[string]int{"id": id})
	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/schemas/ids/"):
		id, err := strconv.Atoi(strings.TrimPrefix(req.URL.Path, "/schemas/ids/"))
		if err != nil || id < 1 || id > len(r.schemas) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error_code":40403,"message":"Schema not found"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"schema": r.schemas[id-1]})
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error_code":404,"message":"not here"}`)
	}
}

func TestAnAvroStreamPublishesInTheConfluentWireFormat(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "depot-scans"))
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	reg := &registry{subjects: map[string]int{}}
	regSrv := httptest.NewServer(reg)
	defer regSrv.Close()

	lab := labtest.New(t)
	lab.Start("parcels", "east", []string{"delivery"}, dispatcher.Hooks{})
	root, _ := filepath.Abs("testdata/root")
	cfg, _ := json.Marshal(sbcore.Config{URL: lab.URL, Token: labtest.Token, Fixtures: root})
	suite := core.NewSuite(core.SuiteOptions{
		PackConfig: map[string]json.RawMessage{sbcore.Name: cfg}, Interpolate: func(s string) string { return s },
		ResolvePath: func(p string) (string, error) { return p, nil },
	})
	sc := core.NewScenario(context.Background(), core.ScenarioInfo{Name: "avro", Tags: []string{"@stormbreaker-group/delivery"}}, suite, &sink{})
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
	// The topic client, as a scenario configures it with axx's kafka step.
	var clientStep func(*core.Scenario, core.Args) error
	for _, s := range kafka.Pack().Manifest().Steps {
		if s.ID == "kafka.client.props" {
			clientStep = s.Run
		}
	}
	if err := clientStep(sc, core.Args{
		List: []core.Arg{{Value: "depot-scans", Raw: "depot-scans", Present: true}, {}},
		Table: &core.Table{Rows: [][]string{
			{"producer.value.serializer", "io.confluent.kafka.serializers.KafkaAvroSerializer"},
			{"producer.schema.registry.url", regSrv.URL},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	if err := step(t, "stormbreaker.kafka.stream")(sc, args("depot-scans", "depot-scans", nil)); err != nil {
		t.Fatal(err)
	}
	if err := step(t, "stormbreaker.kafka.stream.keys")(sc, args("depot-scans", "key")); err != nil {
		t.Fatal(err)
	}
	if err := step(t, stepPublish)(sc, args("depot-scans", 400)); err != nil {
		t.Fatal(err)
	}
	if err := step(t, "stormbreaker.kafka.no-failures")(sc, core.Args{}); err != nil {
		t.Fatal(err)
	}

	text, _ := os.ReadFile("testdata/root/depot-scan.avsc")
	schema := avro.MustParse(string(text))
	id, ok := reg.subjects["depot-scans-value"]
	if !ok {
		t.Fatalf("nothing was registered under depot-scans-value: %v", reg.subjects)
	}
	out := 0
	for _, rec := range consume(t, cluster.ListenAddrs(), "depot-scans", 40) {
		if len(rec.Value) < 5 || rec.Value[0] != 0 || int(binary.BigEndian.Uint32(rec.Value[1:5])) != id {
			t.Fatalf("not in the Confluent wire format with schema %d: % x", id, rec.Value[:min(8, len(rec.Value))])
		}
		var v map[string]any
		if err := avro.Unmarshal(schema, rec.Value[5:], &v); err != nil {
			t.Fatalf("the value does not decode with its schema: %v", err)
		}
		if v["scanId"] != string(rec.Key) {
			t.Errorf("key %q, scanId %v", rec.Key, v["scanId"])
		}
		switch v["status"] {
		case "OUT_FOR_DELIVERY":
			out++
			if v["depot"] != "DE-HAM-2" || v["weightGrams"] != 650 || v["declaredValue"] != 300.0 {
				t.Errorf("a scan out for delivery: %v", v)
			}
		case "SORTED":
			if v["depot"] != "DE-BER-1" || v["weightGrams"] != 2400 || v["declaredValue"] != 25.5 {
				t.Errorf("a sorting scan: %v", v)
			}
		default:
			t.Errorf("status %v", v["status"])
		}
	}
	if out == 0 || out == 40 {
		t.Errorf("%d of 40 scans took the weight-1 shape", out)
	}
}
