package sbrest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nimbusxr/axx/core"
	"github.com/nimbusxr/axx/packs/rest"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/labtest"
	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
	"github.com/nimbusxr/stormbreaker/protocol"
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

// target is the service under load: it records what it received.
type target struct {
	mu       sync.Mutex
	paths    []string
	bodies   []map[string]any
	headers  []string
	queries  []string
	refuseAt int
}

func (tg *target) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.paths = append(tg.paths, r.Method+" "+r.URL.Path)
	tg.bodies = append(tg.bodies, m)
	tg.headers = append(tg.headers, r.Header.Get("X-Requested-By"))
	tg.queries = append(tg.queries, r.URL.RawQuery)
	if tg.refuseAt > 0 && len(tg.paths) == tg.refuseAt {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusAccepted)
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
		a.List = append(a.List, core.Arg{Value: v, Raw: toRaw(v), Present: v != nil})
	}
	return a
}

func toRaw(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// world is a run open on a lab orchestrator, a parcels dispatcher that sees what the run
// declared, and a REST service to send to.
type world struct {
	lab      *labtest.Lab
	sc       *core.Scenario
	sink     *sink
	target   *target
	run      *sbcore.Run
	prepared *sync.Map
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{lab: labtest.New(t), sink: &sink{}, target: &target{}, prepared: &sync.Map{}}
	w.lab.Start("parcels", "east", []string{"delivery"}, dispatcher.Hooks{
		Prepare: func(ctx context.Context, _ *protocol.Run, fp dispatcher.Footprint, _ dispatcher.Report) error {
			for c, err := range fp(ctx, protocol.FootprintFilter{}) {
				if err != nil {
					return err
				}
				w.prepared.Store(c.Locator+" "+c.Identity, true)
			}
			return nil
		},
	})
	srv := httptest.NewServer(w.target)
	t.Cleanup(srv.Close)
	root, _ := filepath.Abs("testdata/root")
	cfg, _ := json.Marshal(sbcore.Config{URL: w.lab.URL, Token: labtest.Token, Fixtures: root})
	suite := core.NewSuite(core.SuiteOptions{PackConfig: map[string]json.RawMessage{sbcore.Name: cfg}, Interpolate: func(s string) string { return s }})
	w.sc = core.NewScenario(context.Background(), core.ScenarioInfo{Name: "load", Tags: []string{"@stormbreaker-group/delivery", "@stormbreaker-requires/parcels"}}, suite, w.sink)
	for _, h := range sbcore.Pack().Manifest().Hooks {
		if h.Phase == core.BeforeScenario {
			if err := h.Run(w.sc); err != nil {
				t.Fatal(err)
			}
		}
	}
	w.run, _ = sbcore.Current(w.sc)
	if err := rest.Context(w.sc).AddService(&rest.Service{Name: "parcels", URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestAStreamIsDeclaredThenSentOpenModel(t *testing.T) {
	w := newWorld(t)
	if err := step(t, "stormbreaker.rest.stream")(w.sc, args("parcels", "post", "/parcels/{reference}", nil)); err != nil {
		t.Fatal(err)
	}
	for id, a := range map[string]core.Args{
		"stormbreaker.rest.stream.path-variables":   args("parcels", "path"),
		"stormbreaker.rest.stream.headers":          args("parcels", "headers"),
		"stormbreaker.rest.stream.query-parameters": args("parcels", "query"),
		"stormbreaker.rest.stream.query-parameter":  args("parcels", "region", "eu west"),
	} {
		if err := step(t, id)(w.sc, a); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	// Binding declared all twenty, at the service and path, before anything was sent.
	run, _ := w.lab.Client.Run(w.lab.Ctx, w.run.ID())
	if run.Claims != 20 || len(run.Streams) != 1 || run.Streams[0] != (protocol.StreamRef{Name: "parcels", Seed: w.run.ID()}) {
		t.Fatalf("after binding: claims %d, streams %+v", run.Claims, run.Streams)
	}
	if err := step(t, stepExecute)(w.sc, args("parcels", 400)); err != nil {
		t.Fatal(err)
	}
	if err := step(t, "stormbreaker.rest.no-failures")(w.sc, core.Args{}); err != nil {
		t.Error(err)
	}
	if err := step(t, "stormbreaker.rest.client-quantile")(w.sc, args("p99", 5.0)); err != nil {
		t.Error(err)
	}

	w.target.mu.Lock()
	defer w.target.mu.Unlock()
	if len(w.target.paths) != 20 {
		t.Fatalf("the service received %d requests", len(w.target.paths))
	}
	for i, p := range w.target.paths {
		id, _ := w.target.bodies[i]["reference"].(string)
		if p != "POST /parcels/"+id || w.target.headers[i] != "load-suite" || w.target.queries[i] != "region=eu+west&source=stormbreaker" {
			t.Errorf("request %d: %s, body id %q, header %q, query %q", i, p, id, w.target.headers[i], w.target.queries[i])
		}
		// Nothing was sent that the dispatcher had not seen declared.
		if _, ok := w.prepared.Load("/parcels/{reference} " + id); !ok {
			t.Errorf("%s was sent before the dispatcher saw it declared", id)
		}
	}
	view := string(w.sink.attachments["stormbreaker-rest client view: parcels"])
	if !strings.Contains(view, `"sent": 20`) || !strings.Contains(view, `"202": 20`) {
		t.Errorf("the client view:\n%s", view)
	}
	// The load is in the run's last progress, for whoever watched it go.
	w.sc.SetStatus("passed")
	if err := w.sc.Close(); err != nil {
		t.Fatal(err)
	}
	p, err := w.lab.Client.Progress(w.lab.Ctx, w.run.ID())
	if err != nil || len(p.Load) != 1 {
		t.Fatalf("the run's progress: %+v, %v", p, err)
	}
	if l := p.Load[0]; l.Stream != "parcels" || l.Protocol != "rest" || l.Target != "parcels" || l.Rate != 400 || l.Planned != 20 || l.Sent != 20 || l.Failed != 0 || !l.Done || l.Latency == nil {
		t.Errorf("the load in the run's progress: %+v", l)
	}
}

func TestFailedRequestsFailTheCheck(t *testing.T) {
	w := newWorld(t)
	w.target.refuseAt = 3
	if err := sbcore.Ready(w.sc); err != nil {
		t.Fatal(err)
	}
	if err := step(t, stepSend)(w.sc, args(10, "get", "/ping", 1000, nil)); err != nil {
		t.Fatal(err)
	}
	err := step(t, "stormbreaker.rest.no-failures")(w.sc, core.Args{})
	if !core.IsAssertion(err) || !strings.Contains(err.Error(), "1 of 10 requests of the last load failed; the first: GET /ping answered 503") {
		t.Errorf("one refusal: %v", err)
	}
	if err := step(t, "stormbreaker.rest.client-quantile")(w.sc, args("p99", 0.0)); !core.IsAssertion(err) {
		t.Errorf("a p99 over a limit of nothing: %v", err)
	}
}

func TestHandWrittenRequestsAreDeclaredBeforeTheyAreSent(t *testing.T) {
	w := newWorld(t)
	svc, _ := rest.Context(w.sc).Service("parcels")
	req := svc.AddRequest(http.MethodPost, "/parcels")
	payload := `{"reference":"hand-1","pieces":[{"id":"p-1"},{"id":"p-2"}]}`
	req.Payload = &payload
	if err := step(t, "stormbreaker.rest.identified")(w.sc, args(nil, "$.reference,pieces[*].id")); err != nil {
		t.Fatal(err)
	}
	claims, err := w.lab.Client.Run(w.lab.Ctx, w.run.ID())
	if err != nil || claims.Claims != 3 {
		t.Fatalf("after identifying: %d claims, %v", claims.Claims, err)
	}
	// The observer runs again before a traffic step, and declares only what is new.
	more := `{"reference":"hand-2"}`
	svc.AddRequest(http.MethodPost, "/parcels").Payload = &more
	w.sc.SetStep(&core.StepInfo{Definition: "rest.execute"})
	for _, h := range sbcore.Pack().Manifest().Hooks {
		if h.Phase == core.BeforeStep {
			if err := h.Run(w.sc); err != nil {
				t.Fatal(err)
			}
		}
	}
	var got []string
	for c, err := range w.lab.Client.Footprint(w.lab.Ctx, w.run.ID(), protocol.FootprintFilter{}) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, c.Locator+"="+c.Identity)
	}
	want := []string{"/parcels:$.reference=hand-1", "/parcels:pieces[*].id=p-1", "/parcels:pieces[*].id=p-2", "/parcels:$.reference=hand-2"}
	if !slices.Equal(got, want) {
		t.Errorf("the footprint: %v, want %v", got, want)
	}
}

func TestAddressesSayWhatIsMissing(t *testing.T) {
	w := newWorld(t)
	if err := step(t, "stormbreaker.rest.stream")(w.sc, args("parcels", "post", "/parcels/{trackingId}", nil)); err != nil {
		t.Fatal(err)
	}
	err := step(t, stepExecute)(w.sc, args("parcels", 100))
	if err == nil || !strings.Contains(err.Error(), "takes no path variables from its records") {
		t.Errorf("a variable with no metadata: %v", err)
	}
	if err := step(t, "stormbreaker.rest.stream.path-variables")(w.sc, args("parcels", "path")); err != nil {
		t.Fatal(err)
	}
	err = step(t, stepExecute)(w.sc, args("parcels", 100))
	if err == nil || !strings.Contains(err.Error(), "the path metadata of parcel#standard has no trackingId") {
		t.Errorf("a variable the metadata lacks: %v", err)
	}
	if err := step(t, "stormbreaker.rest.stream.path-variable")(w.sc, args("parcels", "trackingId", "t/1 x")); err != nil {
		t.Fatal(err)
	}
	if err := step(t, stepExecute)(w.sc, args("parcels", 1000)); err != nil {
		t.Fatal(err)
	}
	w.target.mu.Lock()
	defer w.target.mu.Unlock()
	if len(w.target.paths) == 0 || w.target.paths[0] != "POST /parcels/t/1 x" {
		t.Errorf("a path variable set by a step: %v", w.target.paths[:min(1, len(w.target.paths))])
	}
	if err := step(t, "stormbreaker.rest.stream.headers")(w.sc, args("nothing", "headers")); err == nil {
		t.Error("a stream that is not bound was accepted")
	}
}
