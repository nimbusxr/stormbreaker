package sbcore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nimbusxr/axx/core"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/labtest"
	"github.com/nimbusxr/stormbreaker/protocol"
)

// sink keeps a scenario's log and attachments.
type sink struct {
	mu          sync.Mutex
	logs        []string
	attachments map[string][]byte
}

func (s *sink) Log(_ *core.Scenario, msg string) {
	s.mu.Lock()
	s.logs = append(s.logs, msg)
	s.mu.Unlock()
}

func (s *sink) Attach(_ *core.Scenario, _ string, body []byte, name string) {
	s.mu.Lock()
	if s.attachments == nil {
		s.attachments = map[string][]byte{}
	}
	s.attachments[name] = body
	s.mu.Unlock()
}

func suite(t *testing.T, url string) *core.Suite {
	t.Helper()
	cfg, _ := json.Marshal(Config{URL: url, Token: labtest.Token, Readiness: "5s", Close: "10s"})
	return core.NewSuite(core.SuiteOptions{
		PackConfig:  map[string]json.RawMessage{Name: cfg},
		Interpolate: func(s string) string { return s },
		Logger:      labtest.Quiet,
	})
}

func scenario(s *core.Suite, sk *sink, tags ...string) *core.Scenario {
	return core.NewScenario(context.Background(), core.ScenarioInfo{ID: "s1", Name: "a load run", URI: "load.feature", Line: 3, Tags: tags}, s, sk)
}

// tracking prepares, for each identity the suite declared, one of its own.
func tracking(prepared *sync.Map) dispatcher.Hooks {
	return dispatcher.Hooks{
		Prepare: func(ctx context.Context, run *protocol.Run, fp dispatcher.Footprint, declare dispatcher.Report) error {
			for c, err := range fp(ctx, protocol.FootprintFilter{}) {
				if err != nil {
					return err
				}
				if c.Dispatcher == protocol.Suite {
					prepared.Store("TRK-"+c.Identity, true)
					if err := declare(protocol.FootprintEntry{Store: "tracking-db", Locator: "tracking", Identities: []string{"TRK-" + c.Identity}}); err != nil {
						return err
					}
				}
			}
			return nil
		},
		Cleanup: func(ctx context.Context, run *protocol.Run, fp dispatcher.Footprint, report dispatcher.Report) (bool, error) {
			var ids []string
			prepared.Range(func(k, _ any) bool {
				ids = append(ids, k.(string))
				prepared.Delete(k)
				return true
			})
			if len(ids) == 0 {
				return true, nil
			}
			slices.Sort(ids)
			return true, report(protocol.FootprintEntry{Store: "tracking-db", Locator: "tracking", Identities: ids, Resolution: protocol.ResolutionRemovedVerified})
		},
	}
}

func hook(t *testing.T, id string) func(*core.Scenario) error {
	t.Helper()
	for _, h := range Pack().Manifest().Hooks {
		if h.ID == id {
			return h.Run
		}
	}
	t.Fatalf("no hook %s", id)
	return nil
}

func TestAPassingScenarioCleansUpItsRun(t *testing.T) {
	lab := labtest.New(t)
	var prepared sync.Map
	lab.Start("tracking", "shared", []string{"delivery"}, tracking(&prepared))
	s := suite(t, lab.URL)
	sk := &sink{}
	sc := scenario(s, sk, "@load", "@stormbreaker-group/delivery", "@stormbreaker-requires/tracking")

	if err := hook(t, "stormbreaker.run.open")(sc); err != nil {
		t.Fatal(err)
	}
	r, ok := Current(sc)
	if !ok {
		t.Fatal("the tagged scenario has no run")
	}
	if err := r.Declare(sc.Context(), "parcels-service", "/parcels", []string{"c-1", "c-2"}); err != nil {
		t.Fatal(err)
	}
	var observed int
	Observe(sc, func(*Run) error { observed++; return nil })
	start := time.Now()
	if err := hook(t, "stormbreaker.run.gate")(sc); err != nil {
		t.Fatalf("the gate: %v", err)
	}
	if observed != 1 {
		t.Errorf("observers ran %d times before the step", observed)
	}
	if _, ok := prepared.Load("TRK-c-2"); !ok {
		t.Errorf("traffic was let through %s before tracking prepared", time.Since(start))
	}
	// Once through, the gate does not hold the next step.
	if err := hook(t, "stormbreaker.run.gate")(sc); err != nil {
		t.Fatal(err)
	}

	sc.SetStatus("passed")
	if err := sc.Close(); err != nil {
		t.Fatalf("closing a clean run: %v", err)
	}
	run, err := lab.Client.Run(lab.Ctx, r.ID())
	if err != nil || !run.Done() || run.WindowTo == nil {
		t.Fatalf("after the scenario: %+v, %v", run, err)
	}
	n := 0
	prepared.Range(func(any, any) bool { n++; return true })
	if n != 0 {
		t.Errorf("%d prepared records left behind", n)
	}
	j := string(sk.attachments["stormbreaker journal "+r.ID()])
	if !strings.Contains(j, "the run closes: every instance completed") || !strings.Contains(j, "axx load.feature:3 a load run") {
		t.Errorf("the journal attached:\n%s", j)
	}
}

func TestAFailingScenarioAbandonsItsRun(t *testing.T) {
	lab := labtest.New(t)
	var prepared sync.Map
	lab.Start("tracking", "shared", []string{"delivery"}, tracking(&prepared))
	sc := scenario(suite(t, lab.URL), &sink{}, "@stormbreaker-group/delivery")
	if err := hook(t, "stormbreaker.run.open")(sc); err != nil {
		t.Fatal(err)
	}
	r, _ := Current(sc)
	_ = r.Declare(sc.Context(), "svc", "/x", []string{"x-1"})
	if err := r.Ready(sc.Context()); err != nil {
		t.Fatal(err)
	}
	sc.SetStatus("failed")
	if err := sc.Close(); err != nil {
		t.Fatalf("closing a failed scenario's run: %v", err)
	}
	run := lab.Await(r.ID(), "the abandoned run to be cleaned up", func(r *protocol.Run) bool { return r.Done() })
	if !run.Abandoned {
		t.Errorf("the run: %+v", run)
	}
}

func TestTheGateFailsTheStepWhenARequiredDispatcherIsBlocked(t *testing.T) {
	lab := labtest.New(t)
	lab.Start("tracking", "shared", []string{"delivery"}, dispatcher.Hooks{
		Prepare: func(context.Context, *protocol.Run, dispatcher.Footprint, dispatcher.Report) error {
			return dispatcher.ErrBlocked
		},
	})
	sc := scenario(suite(t, lab.URL), &sink{}, "@stormbreaker-group/delivery", "@stormbreaker-requires/tracking")
	if err := hook(t, "stormbreaker.run.open")(sc); err != nil {
		t.Fatal(err)
	}
	err := hook(t, "stormbreaker.run.gate")(sc)
	if !core.IsAssertion(err) || !strings.Contains(err.Error(), "required dispatcher blocked: tracking/shared") {
		t.Fatalf("the gate: %v", err)
	}
}

func TestTags(t *testing.T) {
	lab := labtest.New(t)
	s := suite(t, lab.URL)
	plain := scenario(s, &sink{}, "@smoke")
	if err := hook(t, "stormbreaker.run.open")(plain); err != nil {
		t.Fatal(err)
	}
	if _, ok := Current(plain); ok {
		t.Error("an untagged scenario has a run")
	}
	if err := hook(t, "stormbreaker.run.gate")(plain); err != nil {
		t.Errorf("the gate of a scenario without a run: %v", err)
	}
	if err := hook(t, "stormbreaker.run.open")(scenario(s, &sink{}, "@stormbreaker-requires/tracking")); err == nil {
		t.Error("requires without a group was accepted")
	}
	if err := hook(t, "stormbreaker.run.open")(scenario(s, &sink{}, "@stormbreaker-group/nowhere")); err == nil || !strings.Contains(err.Error(), "no such group: nowhere") {
		t.Errorf("a group that does not exist: %v", err)
	}
	g, req := tagged([]string{"@stormbreaker-group/delivery", "@stormbreaker-requires/parcels, tracking"})
	if g != "delivery" || !slices.Equal(req, []string{"parcels", "tracking"}) {
		t.Errorf("tagged = %q %v", g, req)
	}
}

func TestStepsNeedARun(t *testing.T) {
	sc := scenario(core.NewSuite(core.SuiteOptions{}), &sink{})
	for _, st := range Pack().Manifest().Steps {
		if err := st.Run(sc, core.Args{}); err == nil || !strings.Contains(err.Error(), "tag it @stormbreaker-group/") {
			t.Errorf("%s without a run: %v", st.ID, err)
		}
	}
}

func TestTheGateHoldsBackOnlyTrafficSteps(t *testing.T) {
	lab := labtest.New(t)
	lab.Start("tracking", "shared", []string{"delivery"}, dispatcher.Hooks{
		Prepare: func(context.Context, *protocol.Run, dispatcher.Footprint, dispatcher.Report) error {
			return dispatcher.ErrBlocked
		},
	})
	sc := scenario(suite(t, lab.URL), &sink{}, "@stormbreaker-group/delivery", "@stormbreaker-requires/tracking")
	if err := hook(t, "stormbreaker.run.open")(sc); err != nil {
		t.Fatal(err)
	}
	observed := 0
	Observe(sc, func(*Run) error { observed++; return nil })
	gate := hook(t, "stormbreaker.run.gate")

	// A step that sends nothing is not held, even though the run will never be ready.
	sc.SetStep(&core.StepInfo{Text: "the parcels service with the following properties:", Pack: "rest", Definition: "rest.service"})
	if err := gate(sc); err != nil || observed != 0 {
		t.Fatalf("a step that sends nothing: %v, observers ran %d times", err, observed)
	}
	Traffic("acme.load.send")
	for _, def := range []string{"rest.execute", "acme.load.send", ""} {
		sc.SetStep(&core.StepInfo{Text: "a step", Definition: def})
		if err := gate(sc); !core.IsAssertion(err) {
			t.Errorf("a traffic step %q went through a gate that never opens: %v", def, err)
		}
	}
	if observed != 3 {
		t.Errorf("observers ran %d times before 3 traffic steps", observed)
	}
}

func TestClosingCallsWaitOutAnOrchestratorThatIsNotAnswering(t *testing.T) {
	old := restartPatience
	restartPatience = 3 * time.Second
	defer func() { restartPatience = old }()
	calls := 0
	unreachable := errors.New("dial tcp: connection refused")
	err := throughRestarts(context.Background(), func() error {
		calls++
		if calls < 3 {
			return unreachable
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Errorf("after two refused connections: %v, %d calls", err, calls)
	}
	// A refusal of the orchestrator's own is not retried.
	calls = 0
	conflict := &protocol.Problem{Status: http.StatusConflict}
	if err := throughRestarts(context.Background(), func() error { calls++; return conflict }); calls != 1 || protocol.StatusOf(err) != http.StatusConflict {
		t.Errorf("a conflict: %v, %d calls", err, calls)
	}
}

func TestAScenarioWaitsItsTurnWhileAnotherRunHoldsItsGroup(t *testing.T) {
	old := turnEvery
	turnEvery = 50 * time.Millisecond
	defer func() { turnEvery = old }()
	lab := labtest.New(t)
	lab.Start("tracking", "shared", []string{"delivery"}, dispatcher.Hooks{})
	// Another suite's run holds the group.
	other, err := lab.Client.OpenRun(lab.Ctx, protocol.OpenRun{Initiator: "another suite", Group: "delivery"})
	if err != nil {
		t.Fatal(err)
	}
	s := suite(t, lab.URL)
	sk := &sink{}
	sc := scenario(s, sk, "@stormbreaker-group/delivery")
	opened := make(chan error, 1)
	go func() { opened <- hook(t, "stormbreaker.run.open")(sc) }()
	select {
	case err := <-opened:
		t.Fatalf("the scenario opened beside a run holding its group: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	// The other run is abandoned, cleaned up and closed: the scenario's run opens.
	if _, err := lab.Client.Publish(lab.Ctx, other.ID, protocol.EventAbandoned); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("after the other run closed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the scenario did not open after the other run closed")
	}
	if !slices.ContainsFunc(sk.logs, func(l string) bool { return strings.Contains(l, "holds the dispatchers group delivery needs") }) {
		t.Errorf("the scenario's log does not say why it waited: %q", sk.logs)
	}
	sc.SetStatus("failed")
	_ = sc.Close()
}
