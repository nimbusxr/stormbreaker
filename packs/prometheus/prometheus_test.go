package sbprometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nimbusxr/axx/core"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/labtest"
	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
	"github.com/nimbusxr/stormbreaker/protocol"
	"github.com/nimbusxr/stormbreaker/slo"
)

// fakePrometheus answers instant queries with fixed series, and records what it was asked.
type fakePrometheus struct {
	mu      sync.Mutex
	queries []string
	buckets map[string]float64 // le -> count
	counter float64
}

func (f *fakePrometheus) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("query")
	f.mu.Lock()
	f.queries = append(f.queries, q+" @"+r.URL.Query().Get("time"))
	f.mu.Unlock()
	type series struct {
		Metric map[string]string `json:"metric"`
		Value  [2]any            `json:"value"`
	}
	var result []series
	if strings.Contains(q, "_bucket") {
		for le, n := range f.buckets {
			result = append(result, series{Metric: map[string]string{"le": le}, Value: [2]any{1, fmt.Sprint(n)}})
		}
	} else {
		result = append(result, series{Metric: map[string]string{}, Value: [2]any{1, fmt.Sprint(f.counter)}})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": result}})
}

func step(t *testing.T, id string) core.StepDef {
	t.Helper()
	for _, s := range Pack().Manifest().Steps {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no step %s", id)
	return core.StepDef{}
}

type nopSink struct{}

func (nopSink) Log(*core.Scenario, string)                    {}
func (nopSink) Attach(*core.Scenario, string, []byte, string) {}

func TestChecksJudgeTheRunsWindowAcrossRegions(t *testing.T) {
	slo.JudgeEvery = 10 * time.Millisecond
	lab := labtest.New(t)
	lab.Start("audit", "one", []string{"delivery"}, dispatcher.Hooks{})
	east := &fakePrometheus{buckets: map[string]float64{"0.1": 100, "1": 100, "+Inf": 100}, counter: 30}
	west := &fakePrometheus{buckets: map[string]float64{"0.1": 0, "1": 100, "+Inf": 100}, counter: 20}
	eastSrv, westSrv := httptest.NewServer(east), httptest.NewServer(west)
	defer eastSrv.Close()
	defer westSrv.Close()

	cfg, _ := json.Marshal(sbcore.Config{URL: lab.URL, Token: labtest.Token})
	suite := core.NewSuite(core.SuiteOptions{PackConfig: map[string]json.RawMessage{sbcore.Name: cfg}, Interpolate: func(s string) string { return s }})
	sc := core.NewScenario(context.Background(), core.ScenarioInfo{Name: "load", Tags: []string{"@stormbreaker-group/delivery"}}, suite, nopSink{})
	for _, h := range sbcore.Pack().Manifest().Hooks {
		if h.Phase == core.BeforeScenario {
			if err := h.Run(sc); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, _ := sbcore.Current(sc)

	register := step(t, "stormbreaker.prometheus.service")
	if err := register.Run(sc, core.Args{List: []core.Arg{{Value: "metrics", Raw: "metrics", Present: true}}, Table: &core.Table{Rows: [][]string{
		{"url", eastSrv.URL + ", " + westSrv.URL + "/"}, {"minimum-window", "1s"},
	}}}); err != nil {
		t.Fatal(err)
	}

	quantile := step(t, "stormbreaker.prometheus.quantile")
	args := func(vals ...any) core.Args {
		var a core.Args
		for _, v := range vals {
			a.List = append(a.List, core.Arg{Value: v, Raw: fmt.Sprint(v), Present: v != nil})
		}
		return a
	}
	// The window is shorter than the minimum: refused, however long the patience.
	err := quantile.Run(sc, args(50*time.Millisecond, 0.99, "http_seconds", nil, 2.0))
	if !core.IsAssertion(err) || !strings.Contains(err.Error(), "shorter than metrics's minimum of 1s") {
		t.Fatalf("a window shorter than the minimum: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)
	if _, err := r.Client().Publish(sc.Context(), r.ID(), protocol.EventTrafficComplete); err != nil {
		t.Fatal(err)
	}
	a := args(time.Second, 0.99, "http_seconds", nil, 2.0)
	a.List[1].Raw = "p99"
	sc.SetStep(&core.StepInfo{Keyword: "Then", Text: "within 1s, the p99 of http_seconds is at most 2 seconds"})
	if err := quantile.Run(sc, a); err != nil {
		t.Errorf("the merged p99 under the limit: %v", err)
	}
	sc.SetStep(nil)
	a.List[4] = core.Arg{Value: 0.5, Raw: "0.5", Present: true}
	err = quantile.Run(sc, a)
	if !core.IsAssertion(err) || !strings.Contains(err.Error(), "the p99 of http_seconds over the run is 0.982s") {
		t.Errorf("the merged p99 over the limit: %v", err)
	}

	increase := step(t, "stormbreaker.prometheus.increase")
	if err := increase.Run(sc, args(time.Second, "requests_total", nil, 50.0)); err != nil {
		t.Errorf("the summed increase: %v", err)
	}
	if err := increase.Run(sc, args(50*time.Millisecond, "requests_total", nil, 51.0)); !core.IsAssertion(err) {
		t.Errorf("an increase below the minimum: %v", err)
	}

	// The verdict is in the run's progress, for whoever watches it, with what the check found.
	sc.SetStatus("passed")
	if err := sc.Close(); err != nil {
		t.Fatal(err)
	}
	p, err := r.Client().Progress(context.Background(), r.ID())
	if err != nil || len(p.Checks) != 1 {
		t.Fatalf("the run's progress: %+v, %v", p, err)
	}
	if c := p.Checks[0]; c.Check != "within 1s, the p99 of http_seconds is at most 2 seconds" || c.Source != "metrics" || c.Status != "passed" || c.Found != "0.982s" {
		t.Errorf("the check in the run's progress: %+v", c)
	}

	// Each region is asked about the run's closed window, at its end.
	run, _ := r.Client().Run(context.Background(), r.ID())
	at := fmt.Sprintf("@%.3f", float64(run.WindowTo.UnixMilli())/1000)
	east.mu.Lock()
	defer east.mu.Unlock()
	if len(east.queries) == 0 || !strings.HasPrefix(east.queries[0], "sum by (le) (increase(http_seconds_bucket[") || !strings.HasSuffix(east.queries[0], at) {
		t.Errorf("east was asked %v; want the window ending %s", east.queries, at)
	}
}

func TestAServiceSelectsItsSeriesAndAsksOneReplicaOfAPair(t *testing.T) {
	slo.JudgeEvery = 10 * time.Millisecond
	lab := labtest.New(t)
	lab.Start("audit", "one", []string{"delivery"}, dispatcher.Hooks{})
	// A region whose Prometheus is an HA pair, the first replica down; and a global view.
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	replica := &fakePrometheus{buckets: map[string]float64{"0.1": 10, "+Inf": 10}, counter: 7}
	global := &fakePrometheus{buckets: map[string]float64{"0.1": 5, "+Inf": 5}, counter: 5}
	replicaSrv, globalSrv := httptest.NewServer(replica), httptest.NewServer(global)
	defer replicaSrv.Close()
	defer globalSrv.Close()

	cfg, _ := json.Marshal(sbcore.Config{URL: lab.URL, Token: labtest.Token})
	suite := core.NewSuite(core.SuiteOptions{PackConfig: map[string]json.RawMessage{sbcore.Name: cfg}, Interpolate: func(s string) string { return s }})
	sc := core.NewScenario(context.Background(), core.ScenarioInfo{Name: "load", Tags: []string{"@stormbreaker-group/delivery"}}, suite, nopSink{})
	for _, h := range sbcore.Pack().Manifest().Hooks {
		if h.Phase == core.BeforeScenario {
			if err := h.Run(sc); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, _ := sbcore.Current(sc)
	register := step(t, "stormbreaker.prometheus.service")
	if err := register.Run(sc, core.Args{List: []core.Arg{{Value: "parcels", Raw: "parcels", Present: true}}, Table: &core.Table{Rows: [][]string{
		{"url", down.URL + " | " + replicaSrv.URL + ", " + globalSrv.URL}, {"selector", `{app="parcels", region=~"eu-.*"}`}, {"minimum-window", "1s"},
	}}}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "parcels", "{}"} {
		err := register.Run(sc, core.Args{List: []core.Arg{{Value: "x", Raw: "x", Present: true}}, Table: &core.Table{Rows: [][]string{{"url", globalSrv.URL}, {"selector", bad}}}})
		if err == nil {
			t.Errorf("the selector %q was accepted", bad)
		}
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := r.Client().Publish(sc.Context(), r.ID(), protocol.EventTrafficComplete); err != nil {
		t.Fatal(err)
	}
	increase := step(t, "stormbreaker.prometheus.increase")
	at := func(n float64) core.Args {
		return core.Args{List: []core.Arg{{Value: 200 * time.Millisecond, Raw: "200ms", Present: true}, {Value: "requests_total", Raw: "requests_total", Present: true}, {}, {Value: n, Raw: fmt.Sprint(n), Present: true}}}
	}
	// The pair counts once, from the replica that answered: 7 + 5, not 7 + 7 + 5.
	if err := increase.Run(sc, at(12)); err != nil {
		t.Errorf("the pair and the global view summed: %v", err)
	}
	if err := increase.Run(sc, at(13)); !core.IsAssertion(err) {
		t.Errorf("an increase above what the servers hold: %v", err)
	}
	global.mu.Lock()
	defer global.mu.Unlock()
	if len(global.queries) == 0 || !strings.HasPrefix(global.queries[0], `sum(increase(requests_total{app="parcels", region=~"eu-.*"}[`) {
		t.Errorf("the global view was asked %v; want the service's series only", global.queries)
	}
}

func TestChecksNeedARun(t *testing.T) {
	sc := core.NewScenario(context.Background(), core.ScenarioInfo{}, core.NewSuite(core.SuiteOptions{Interpolate: func(s string) string { return s }}), nopSink{})
	_, err := slo.RunWindow(sc, "m", time.Second)
	if err == nil || !strings.Contains(err.Error(), "needs a stormbreaker run") {
		t.Errorf("a check without a run: %v", err)
	}
	reg := step(t, "stormbreaker.prometheus.service")
	if err := reg.Run(sc, core.Args{List: []core.Arg{{Value: "m", Raw: "m", Present: true}}, Table: &core.Table{Rows: [][]string{{"uri", "x"}}}}); err == nil {
		t.Error("an unknown property was accepted")
	}
}
