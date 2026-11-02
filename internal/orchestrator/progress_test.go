package orchestrator

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

func TestASuiteSaysHowItsRunGoes(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := e.open("delivery", "parcels")
	path := "/runs/" + run.ID + "/progress"

	e.expect(http.StatusNotFound, http.MethodGet, path, nil)
	e.expect(http.StatusNotFound, http.MethodGet, "/runs/nope/progress", nil)
	e.expect(http.StatusBadRequest, http.MethodPut, path, protocol.Progress{})

	live := protocol.Progress{
		Scenario: "parcels hold their rate", Feature: "features/load.feature:3",
		Steps: []protocol.StepProgress{{Keyword: "Given", Text: "a run", Status: "passed"}, {Keyword: "When", Text: "the load goes", Status: "running"}},
		Load:  []protocol.LoadProgress{{Stream: "parcels", Protocol: "rest", Target: "parcels", Rate: 50, Planned: 500, Sent: 120, InFlight: 3, Failed: 1, Latency: &protocol.Latency{P50: 4, P90: 9, P99: 21, Max: 40}}},
	}
	e.expect(http.StatusNoContent, http.MethodPut, path, live)
	got := decode[protocol.Progress](t, e.expect(http.StatusOK, http.MethodGet, path, nil))
	if got.Scenario != live.Scenario || len(got.Steps) != 2 || got.Load[0].Sent != 120 || !got.At.Equal(e.clock.now()) {
		t.Fatalf("the live progress: %+v", got)
	}
	// Live progress is never written: the journal does not mention it.
	if journal := e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/journal", nil).Body.String(); strings.Contains(journal, "parcels hold their rate") {
		t.Fatalf("live progress reached the journal:\n%s", journal)
	}

	// A minute unrenewed, it is gone, and the sweep forgets it.
	e.clock.advance(progressStale + time.Second)
	e.expect(http.StatusNotFound, http.MethodGet, path, nil)
	e.o.progress.sweep(e.clock.now())
	if len(e.o.progress.by) != 0 {
		t.Fatalf("the sweep kept %d", len(e.o.progress.by))
	}

	// The final one is kept with the run, and told once in the journal.
	final := live
	final.Status, final.Final = "passed", true
	final.Steps[1].Status = "passed"
	final.Load[0].Sent, final.Load[0].InFlight, final.Load[0].Done = 500, 0, true
	final.Checks = []protocol.CheckResult{{Check: "p99 under 50 ms", Source: "parcels", Status: "passed", Found: "21 ms"}}
	e.expect(http.StatusNoContent, http.MethodPut, path, final)
	e.expect(http.StatusNoContent, http.MethodPut, path, final)
	e.clock.advance(progressStale + time.Second)
	got = decode[protocol.Progress](t, e.expect(http.StatusOK, http.MethodGet, path, nil))
	if !got.Final || got.Status != "passed" || got.Checks[0].Found != "21 ms" {
		t.Fatalf("the final progress: %+v", got)
	}
	journal := e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/journal", nil).Body.String()
	if n := strings.Count(journal, `"parcels hold their rate" passed: 2 of 2 steps passed; parcels sent 500 of 500 to parcels at 50/s, 1 failed, p99 21 ms; p99 under 50 ms: passed (21 ms)`); n != 1 {
		t.Fatalf("the journal tells the final progress %d times:\n%s", n, journal)
	}
}

func TestAProgressHasItsLimits(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := e.open("delivery", "parcels")
	big := protocol.Progress{Scenario: "huge", Steps: []protocol.StepProgress{{Keyword: "Given", Text: strings.Repeat("x", progressLimit)}}}
	e.expect(http.StatusRequestEntityTooLarge, http.MethodPut, "/runs/"+run.ID+"/progress", big)
}
