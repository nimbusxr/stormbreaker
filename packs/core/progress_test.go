package sbcore

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nimbusxr/axx/core"

	"github.com/nimbusxr/stormbreaker/internal/labtest"
	"github.com/nimbusxr/stormbreaker/protocol"
)

func TestARunSaysHowItGoes(t *testing.T) {
	lab := labtest.New(t)
	var prepared sync.Map
	lab.Start("tracking", "shared", []string{"delivery"}, tracking(&prepared))
	cfg, _ := json.Marshal(Config{URL: lab.URL, Token: labtest.Token, Readiness: "5s", Close: "10s", Progress: "20ms"})
	s := core.NewSuite(core.SuiteOptions{PackConfig: map[string]json.RawMessage{Name: cfg}, Interpolate: func(s string) string { return s }, Logger: labtest.Quiet})
	sc := scenario(s, &sink{}, "@stormbreaker-group/delivery", "@stormbreaker-requires/tracking")
	sc.SetProgress([]core.StepProgress{
		{StepInfo: core.StepInfo{Keyword: "Given", Text: "a run for delivery"}, Status: "passed"},
		{StepInfo: core.StepInfo{Keyword: "When", Text: "the parcels stream goes at 50 a second"}, Status: "running"},
	})
	if err := hook(t, "stormbreaker.run.open")(sc); err != nil {
		t.Fatal(err)
	}
	r, _ := Current(sc)
	var sent atomic.Int64
	r.Load("parcels", func() protocol.LoadProgress {
		return protocol.LoadProgress{Stream: "parcels", Protocol: "rest", Target: "parcels", Rate: 50, Planned: 100, Sent: sent.Load()}
	})
	r.Check(protocol.CheckResult{Check: "p99 under 50 ms", Source: "parcels", Status: "waiting"})

	// The progress goes out as it changes.
	sent.Store(40)
	var live protocol.Progress
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p, err := lab.Client.Progress(lab.Ctx, r.ID()); err == nil && len(p.Load) == 1 && p.Load[0].Sent == 40 {
			live = p
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if live.Scenario != "a load run" || live.Feature != "load.feature:3" || len(live.Steps) != 2 || live.Steps[1].Status != "running" || live.Final {
		t.Fatalf("the live progress: %+v", live)
	}
	if len(live.Checks) != 1 || live.Checks[0].Status != "waiting" {
		t.Fatalf("the live checks: %+v", live.Checks)
	}

	// The last goes out before the run closes, with the scenario's status, and stays.
	sc.UpdateProgress(1, func(p *core.StepProgress) { p.Status = "passed" })
	r.Check(protocol.CheckResult{Check: "p99 under 50 ms", Source: "parcels", Status: "passed", Found: "12 ms"})
	sent.Store(100)
	sc.SetStatus("passed")
	if err := sc.Close(); err != nil {
		t.Fatal(err)
	}
	final, err := lab.Client.Progress(lab.Ctx, r.ID())
	if err != nil || !final.Final || final.Status != "passed" || final.Load[0].Sent != 100 || final.Checks[0].Found != "12 ms" || len(final.Checks) != 1 {
		t.Fatalf("the final progress: %+v, %v", final, err)
	}
}
