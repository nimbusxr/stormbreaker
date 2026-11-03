package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

func TestBelowTheReserveGrowthIsRefusedAndCleanupGoesOn(t *testing.T) {
	e := newEnv(t)
	if _, ok := e.o.room(); !ok {
		t.Skip("this platform can't say how much room its volume has, so the orchestrator keeps no reserve")
	}
	e.register("delivery", "parcels/east")
	run := e.open("delivery")
	e.footprint(run.ID, "suite/suite", expected("parcels", "/parcels", "p-1"))

	// The volume falls below the reserve.
	e.o.cfg.DiskReserve = 1 << 62
	refused := decode[protocol.Problem](t, e.expect(http.StatusInsufficientStorage, http.MethodPost, "/runs/"+run.ID+"/footprint/suite?instance=suite",
		protocol.FootprintReport{Entries: []protocol.FootprintEntry{expected("parcels", "/parcels", "p-2")}}))
	if !strings.Contains(refused.Detail, "below its reserve") || !strings.Contains(refused.Detail, "removals, statuses and closing still go through") {
		t.Errorf("the refusal: %+v", refused)
	}
	e.expect(http.StatusInsufficientStorage, http.MethodPost, "/runs", protocol.OpenRun{Initiator: "s", Group: "elsewhere"})
	// What answers for the store still goes through, and the run closes.
	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		e.publish(run.ID, ev)
	}
	e.footprint(run.ID, "parcels/east", removed("parcels", "/parcels", "p-1"))
	e.status(run.ID, "parcels/east", protocol.StatusCompleted)
	if r := e.run(run.ID); r.State != protocol.StateClosed {
		t.Errorf("below the reserve, cleanup did not finish: %+v", r)
	}
}

func TestPruningDropsIdentitiesFirstAndRunsLater(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := e.open("delivery")
	e.footprint(run.ID, "suite/suite", expected("parcels", "/parcels", "p-1", "p-2", "p-3"))
	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		e.publish(run.ID, ev)
	}
	e.status(run.ID, "parcels/east", protocol.StatusCompleted)

	// Past the footprint retention: the identities go, the journal's counts stay.
	e.clock.advance(e.o.cfg.FootprintRetention + time.Minute)
	if _, err := e.o.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	gone := decode[protocol.Problem](t, e.expect(http.StatusGone, http.MethodGet, "/runs/"+run.ID+"/footprint/claims", nil))
	if !strings.Contains(gone.Detail, "the footprint retention") {
		t.Errorf("reading pruned identities: %+v", gone)
	}
	e.expect(http.StatusGone, http.MethodGet, "/runs/"+run.ID+"/footprint", nil)
	j := e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/journal", nil).Body.String()
	if !strings.Contains(j, "parcels:/parcels") || !strings.Contains(j, "its identities are dropped") {
		t.Errorf("the journal after the identities went:\n%s", j)
	}
	var blobs int
	if err := e.o.db.QueryRow(`select count(*) from footprint_blobs`).Scan(&blobs); err != nil || blobs != 0 {
		t.Errorf("%d blobs left, %v", blobs, err)
	}

	// Past the retention: the run goes with everything recorded about it.
	e.clock.advance(e.o.cfg.Retention)
	if n, err := e.o.Prune(context.Background()); err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	e.expect(http.StatusNotFound, http.MethodGet, "/runs/"+run.ID, nil)
}

func TestMetricsCountWhatTheOrchestratorHolds(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := e.open("delivery")
	e.footprint(run.ID, "suite/suite", expected("parcels", "/parcels", "p-1", "p-2"))
	body := e.expect(http.StatusOK, http.MethodGet, "/metrics", nil).Body.String()
	for _, want := range []string{
		`stormbreaker_runs{state="opened"} 1`,
		"stormbreaker_runs_holding 1",
		"stormbreaker_claims_open 2",
		`stormbreaker_dispatcher_instances{lease="live"} 1`,
		"# TYPE stormbreaker_store_bytes gauge",
		"stormbreaker_volume_reserve_bytes 1073741824",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the metrics lack %q:\n%s", want, body)
		}
	}
}

func TestTheListOfRunsIsNewestFirstAndFiltered(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	e.register("audit", "audit/one")
	first := e.open("delivery")
	e.clock.advance(time.Second)
	second := e.open("audit")
	all := decode[protocol.RunList](t, e.expect(http.StatusOK, http.MethodGet, "/runs", nil))
	if len(all.Runs) != 2 || all.Runs[0].ID != second.ID || all.Runs[1].ID != first.ID || !all.Runs[0].Holds {
		t.Fatalf("all runs: %+v", all)
	}
	audit := decode[protocol.RunList](t, e.expect(http.StatusOK, http.MethodGet, "/runs?group=audit&state=opened", nil))
	if len(audit.Runs) != 1 || audit.Runs[0].ID != second.ID {
		t.Errorf("audit's opened runs: %+v", audit)
	}
	page := decode[protocol.RunList](t, e.expect(http.StatusOK, http.MethodGet, "/runs?limit=1", nil))
	if len(page.Runs) != 1 || page.Before == 0 {
		t.Fatalf("a page of one: %+v", page)
	}
	next := decode[protocol.RunList](t, e.expect(http.StatusOK, http.MethodGet, fmt.Sprintf("/runs?limit=1&before=%d", page.Before), nil))
	if len(next.Runs) != 1 || next.Runs[0].ID != first.ID || next.Before != 0 {
		t.Errorf("the next page: %+v", next)
	}
	e.expect(http.StatusBadRequest, http.MethodGet, "/runs?limit=0", nil)
}
