package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

func TestRegistration(t *testing.T) {
	e := newEnv(t)
	reg := protocol.Registration{Name: "parcels", Instance: "east", Team: "pay", CallbackURL: "http://parcels/events", Secret: e.secret()}

	bad := reg
	bad.Secret = "change-me"
	e.expect(http.StatusBadRequest, http.MethodPost, "/dispatchers", bad)
	bad = reg
	bad.Name = protocol.Suite
	e.expect(http.StatusBadRequest, http.MethodPost, "/dispatchers", bad)

	rec := e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", reg)
	if strings.Contains(rec.Body.String(), "whsec_") {
		t.Errorf("the registration answered with its secret: %s", rec.Body.String())
	}
	d := decode[protocol.Dispatcher](t, rec)
	if want := e.clock.now().Add(30 * time.Second); !d.LeaseExpiresAt.Equal(want) {
		t.Errorf("lease expires %s, want %s", d.LeaseExpiresAt, want)
	}

	// Joining needs a live instance; the group is created on the way.
	e.expect(http.StatusNotFound, http.MethodPut, "/groups/delivery/dispatchers/tracking", nil)
	e.expect(http.StatusNoContent, http.MethodPut, "/groups/delivery/dispatchers/parcels", nil)
	groups := decode[[]protocol.Group](t, e.expect(http.StatusOK, http.MethodGet, "/groups", nil))
	if len(groups) != 1 || groups[0].Name != "delivery" || !slices.Equal(groups[0].Dispatchers, []string{"parcels"}) {
		t.Errorf("groups = %+v", groups)
	}
	if m := decode[[]string](t, e.expect(http.StatusOK, http.MethodGet, "/groups/nowhere/dispatchers", nil)); len(m) != 0 {
		t.Errorf("a group that does not exist has members %v", m)
	}
	e.expect(http.StatusNoContent, http.MethodDelete, "/groups/delivery/dispatchers/parcels", nil)
	if m := decode[[]string](t, e.expect(http.StatusOK, http.MethodGet, "/groups/delivery/dispatchers", nil)); len(m) != 0 {
		t.Errorf("after leaving, members %v", m)
	}
}

func TestOpenRunRefusals(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east", "tracking/shared")

	e.expect(http.StatusBadRequest, http.MethodPost, "/runs", protocol.OpenRun{Group: "delivery"})
	e.expect(http.StatusUnprocessableEntity, http.MethodPost, "/runs", protocol.OpenRun{Initiator: "s", Group: "nowhere"})
	e.expect(http.StatusUnprocessableEntity, http.MethodPost, "/runs", protocol.OpenRun{Initiator: "s", Group: "delivery", Requires: []string{"billing"}})

	// A required dispatcher with no live instance refuses the run, and leaves nothing behind.
	e.clock.advance(31 * time.Second)
	e.expect(http.StatusUnprocessableEntity, http.MethodPost, "/runs", protocol.OpenRun{Initiator: "s", Group: "delivery", Requires: []string{"tracking"}})
	if feed := decode[protocol.EventFeed](t, e.expect(http.StatusOK, http.MethodGet, "/events", nil)); len(feed.Events) != 0 {
		t.Errorf("a refused run left events: %+v", feed.Events)
	}
	e.expect(http.StatusNotFound, http.MethodGet, "/runs/run-nothing", nil)
}

func TestTheGateWaitsForEveryRequiredInstance(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east", "parcels/west", "tracking/shared", "audit/one")
	run := e.open("delivery", "parcels", "tracking")

	if run.State != protocol.StateOpened || run.Gate.Open {
		t.Fatalf("a new run: state %s, gate %+v", run.State, run.Gate)
	}
	if len(run.Dispatchers) != 4 {
		t.Fatalf("dispatchers on the run: %+v", run.Dispatchers)
	}
	for _, d := range run.Dispatchers {
		if d.State != protocol.InstanceNotified || d.Required != (d.Name != "audit") {
			t.Errorf("%s/%s: %+v", d.Name, d.Instance, d)
		}
	}
	if !slices.Contains(e.eventTypes(run.ID), protocol.EventOpened) {
		t.Errorf("no run.opened event")
	}

	// The suite declares what its streams will create: the footprint grows.
	e.footprint(run.ID, "suite/suite", expected("parcels-service", "/parcels", "c-1", "c-2"))
	e.status(run.ID, "parcels/east", protocol.StatusPrepared)
	e.status(run.ID, "parcels/west", protocol.StatusPrepared)
	if g := e.run(run.ID).Gate; g.Open || len(g.Waiting) != 1 || !strings.HasPrefix(g.Waiting[0], "tracking/shared") {
		t.Fatalf("with tracking still notified, gate = %+v", g)
	}

	// tracking derives identities of its own: the footprint grows again, and the gate waits for
	// every required instance to settle on it.
	e.clock.advance(time.Millisecond)
	e.footprint(run.ID, "tracking/shared", expected("tracking-db", "public.tracking", "TRK-c-1", "TRK-c-2"))
	e.status(run.ID, "tracking/shared", protocol.StatusPrepared)
	g := e.run(run.ID).Gate
	if g.Open || len(g.Waiting) != 2 {
		t.Fatalf("after the footprint grew, gate = %+v", g)
	}
	for _, w := range g.Waiting {
		if !strings.HasPrefix(w, "parcels/") || !strings.Contains(w, "settled on footprint version 1; it is at 2") {
			t.Errorf("waiting on %q", w)
		}
	}

	// A round that adds nothing settles. The optional audit dispatcher never blocks.
	e.status(run.ID, "parcels/east", protocol.StatusPrepared)
	e.status(run.ID, "parcels/west", protocol.StatusPrepared)
	if g := e.run(run.ID).Gate; !g.Open {
		t.Fatalf("every required instance settled on the footprint as it is, gate = %+v", g)
	}
	if run := e.run(run.ID); run.Claims != 4 || run.FootprintVersion != 2 {
		t.Errorf("claims = %d, want 4; footprint version %d, want 2", run.Claims, run.FootprintVersion)
	}
}

// A round reads the run, walks the footprint and reports. One that began before the footprint
// grew reports after it did, but never saw what grew: it counts for nothing.
func TestARoundThatBeganBeforeTheFootprintGrewCountsForNothing(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east", "tracking/shared")
	run := e.open("delivery", "parcels", "tracking")
	e.footprint(run.ID, "suite/suite", expected("parcels-service", "/parcels", "c-1"))
	began := e.run(run.ID).FootprintVersion // tracking starts a round here
	e.status(run.ID, "parcels/east", protocol.StatusPrepared)
	e.footprint(run.ID, "parcels/east", expected("parcels-mem", "parcels", "c-1-hold"))
	e.status(run.ID, "parcels/east", protocol.StatusPrepared)
	e.statusOn(run.ID, "tracking/shared", protocol.StatusPrepared, began) // reported late, on what it walked
	g := e.run(run.ID).Gate
	if g.Open || len(g.Waiting) != 1 || !strings.HasPrefix(g.Waiting[0], "tracking/shared settled on footprint version 1; it is at 2") {
		t.Fatalf("a round that missed the last growth opened the gate: %+v", g)
	}
	// A round still under way says nothing of what it has seen.
	e.statusOn(run.ID, "tracking/shared", protocol.StatusInProgress, 2)
	if g := e.run(run.ID).Gate; g.Open {
		t.Fatalf("a round in progress opened the gate: %+v", g)
	}
	e.status(run.ID, "tracking/shared", protocol.StatusPrepared)
	if g := e.run(run.ID).Gate; !g.Open {
		t.Fatalf("gate %+v", g)
	}
	// No round began from a version the footprint has not reached.
	e.expect(http.StatusBadRequest, http.MethodPost, "/runs/"+run.ID+"/dispatchers/tracking/status?instance=shared",
		protocol.Status{State: protocol.StatusPrepared, FootprintVersion: 3})
}

func TestOnlyNewWordGrowsTheFootprint(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := e.open("delivery", "parcels")
	count := func() int {
		n := 0
		for _, typ := range e.eventTypes(run.ID) {
			if typ == protocol.EventFootprintUpdated {
				n++
			}
		}
		return n
	}

	if !e.footprint(run.ID, "parcels/east", expected("mem", "parcels", "a", "b")) || count() != 1 {
		t.Fatalf("a first report: %d footprint updates", count())
	}
	// Saying the same thing again is the same telling: an instance that re-declares its whole
	// set every round settles, and the orchestrator says so.
	if e.footprint(run.ID, "parcels/east", expected("mem", "parcels", "a", "b")) || count() != 1 {
		t.Errorf("the same report again grew the footprint")
	}
	// A different statement is new word, even about the same identities.
	e.footprint(run.ID, "parcels/east", expected("mem", "parcels", "b", "a"))
	e.footprint(run.ID, "suite/suite", expected("mem", "parcels", "a", "b"))
	if count() != 3 {
		t.Errorf("new word from the same instance and from another party: %d updates, want 3", count())
	}
	// A report that names nothing changes nothing.
	e.footprint(run.ID, "parcels/east", expected("mem", "parcels"))
	if count() != 3 {
		t.Errorf("an empty report grew the footprint")
	}

	// Only instances on the run report, but the suite always may.
	e.expect(http.StatusNotFound, http.MethodPost, "/runs/"+run.ID+"/footprint/tracking?instance=x", protocol.FootprintReport{Entries: []protocol.FootprintEntry{expected("s", "l", "i")}})
	e.expect(http.StatusBadRequest, http.MethodPost, "/runs/"+run.ID+"/footprint/parcels", protocol.FootprintReport{})
	e.expect(http.StatusBadRequest, http.MethodPost, "/runs/"+run.ID+"/footprint/parcels?instance=east",
		protocol.FootprintReport{Entries: []protocol.FootprintEntry{{Store: "s", Locator: "l", Identities: []string{"i"}, Resolution: protocol.ResolutionCleanBy}}})
}

func TestTheFootprintSettlesOnRead(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east", "parcels/west")
	run := e.open("delivery")
	e.footprint(run.ID, "parcels/east", expected("mem", "parcels", "a", "b"))
	e.footprint(run.ID, "parcels/west", expected("mem", "parcels", "a"))
	e.footprint(run.ID, "parcels/east", removed("mem", "parcels", "a", "b"))

	// Within one instance at one place a later report owns an identity; across instances,
	// claims stand side by side.
	claims := decode[[]protocol.Claim](t, e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/footprint/claims", nil))
	if len(claims) != 3 {
		t.Errorf("settled claims: %+v", claims)
	}

	// Pages count reports, and hand them over as written.
	inPage := func(p protocol.FootprintPage) int {
		n := 0
		for _, r := range p.Reports {
			n += len(r.Identities)
		}
		return n
	}
	page := decode[protocol.FootprintPage](t, e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/footprint?reports=2", nil))
	if inPage(page) != 3 || len(page.Reports) != 2 || page.Reports[0].Dispatcher != "parcels" || page.Next == nil {
		t.Fatalf("first page: %+v", page)
	}
	page = decode[protocol.FootprintPage](t, e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/footprint?reports=2&after="+itoa64(*page.Next), nil))
	if inPage(page) != 2 || page.Next == nil {
		t.Fatalf("second page: %+v", page)
	}
	page = decode[protocol.FootprintPage](t, e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/footprint?after="+itoa64(*page.Next), nil))
	if len(page.Reports) != 0 || page.Next != nil {
		t.Fatalf("past the end: %+v", page)
	}
	e.expect(http.StatusBadRequest, http.MethodGet, "/runs/"+run.ID+"/footprint?after=x", nil)
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func TestARunMovesForward(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := e.open("delivery")

	e.expect(http.StatusBadRequest, http.MethodPost, "/runs/"+run.ID+"/events", protocol.PublishEvent{Type: protocol.EventOpened})
	e.expect(http.StatusNotFound, http.MethodPost, "/runs/run-none/events", protocol.PublishEvent{Type: protocol.EventSettled})

	e.clock.advance(5 * time.Second)
	e.publish(run.ID, protocol.EventTrafficComplete)
	got := e.run(run.ID)
	if got.State != protocol.StateTrafficComplete || got.WindowTo == nil || !got.WindowTo.Equal(e.clock.now()) {
		t.Fatalf("after traffic-complete: %+v", got)
	}
	e.publish(run.ID, protocol.EventTrafficComplete) // the current phase again: fine
	e.publish(run.ID, protocol.EventSettled)
	e.expect(http.StatusConflict, http.MethodPost, "/runs/"+run.ID+"/events", protocol.PublishEvent{Type: protocol.EventTrafficComplete})
	e.publish(run.ID, protocol.EventCleanupReady)
	if s := e.run(run.ID).State; s != protocol.StateCleanup {
		t.Fatalf("after cleanup-ready: %s", s)
	}

	// Cleanup: the instance removes what it claimed and says so.
	e.footprint(run.ID, "parcels/east", removed("mem", "parcels", "a"))
	e.status(run.ID, "parcels/east", protocol.StatusCompleted)
	if s := e.run(run.ID).State; s != protocol.StateClosed {
		t.Fatalf("every instance completed after the last growth, state %s", s)
	}
	e.expect(http.StatusConflict, http.MethodPost, "/runs/"+run.ID+"/events", protocol.PublishEvent{Type: protocol.EventCleanupReady})
	e.expect(http.StatusGone, http.MethodPost, "/runs/"+run.ID+"/heartbeat", nil)

	// The removal answered for something; it declared nothing, so it grew nothing.
	want := []string{protocol.EventOpened, protocol.EventTrafficComplete, protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady}
	if got := e.eventTypes(run.ID); !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestSomethingDeclaredDuringCleanupWaitsForEveryone(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east", "tracking/shared")
	run := e.open("delivery")
	e.publish(run.ID, protocol.EventCleanupReady)
	e.status(run.ID, "parcels/east", protocol.StatusCompleted)
	// tracking finds something it made that nobody declared: that is new, and parcels may own
	// a part of it.
	e.footprint(run.ID, "tracking/shared", expected("tracking-db", "public.tracking", "TRK-late"))
	e.footprint(run.ID, "tracking/shared", removed("tracking-db", "public.tracking", "TRK-late"))
	e.status(run.ID, "tracking/shared", protocol.StatusCompleted)
	if s := e.run(run.ID).State; s != protocol.StateCleanup {
		t.Fatalf("parcels completed before the footprint last grew; the run closed anyway (%s)", s)
	}
	e.status(run.ID, "parcels/east", protocol.StatusCompleted)
	if s := e.run(run.ID).State; s != protocol.StateClosed {
		t.Fatalf("state %s, want closed", s)
	}
}

func TestARemovalHoldsNobodyUp(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east", "tracking/shared")
	run := e.open("delivery")
	e.footprint(run.ID, "tracking/shared", expected("tracking-db", "public.tracking", "TRK-1", "TRK-2"))
	e.publish(run.ID, protocol.EventCleanupReady)
	e.status(run.ID, "parcels/east", protocol.StatusCompleted)
	// Removed in a chunk of its own: a statement tracking never made before, and no growth.
	if e.footprint(run.ID, "tracking/shared", removed("tracking-db", "public.tracking", "TRK-1")) {
		t.Error("a removal grew the footprint")
	}
	e.status(run.ID, "tracking/shared", protocol.StatusCompleted)
	if s := e.run(run.ID).State; s != protocol.StateClosed {
		t.Fatalf("state %s, want closed", s)
	}
}

func TestReadinessFailsEveryRunThatQualifies(t *testing.T) {
	e := newEnv(t)
	// Runs never overlap on a dispatcher, so these four run side by side in groups of their own.
	renew := func() {
		e.register("blocking", "parcels/east")
		e.register("staling", "tracking/shared")
		e.register("late", "labels/east")
		e.register("fine", "billing/shared")
	}
	renew()
	blocked := e.open("blocking", "parcels")
	stale := e.open("staling", "tracking")
	late := e.open("late", "labels")
	fine := e.open("fine")

	e.expect(http.StatusNoContent, http.MethodPost, "/runs/"+blocked.ID+"/dispatchers/parcels/status?instance=east",
		protocol.Status{State: protocol.StatusBlocked, Detail: "the admin door refuses"})
	e.status(stale.ID, "tracking/shared", protocol.StatusPartial) // acknowledged, then silent
	e.status(late.ID, "labels/east", protocol.StatusInProgress)
	// Keep the dispatchers' leases alive while time passes.
	e.keepAlive(late.ID, fine.ID)
	e.clock.advance(25 * time.Second)
	renew()
	e.keepAlive(late.ID, fine.ID)
	e.housekeeping()
	if r := e.run(blocked.ID); !r.Failed || r.State != protocol.StateCleanup || !strings.Contains(r.FailReason, "blocked: parcels/east (the admin door refuses)") {
		t.Errorf("blocked: %+v", r)
	}
	e.clock.advance(10 * time.Second)
	renew()
	e.keepAlive(late.ID, fine.ID)
	e.status(late.ID, "labels/east", protocol.StatusInProgress)
	e.housekeeping()
	if r := e.run(stale.ID); !r.Failed || !strings.Contains(r.FailReason, "stale: tracking/shared") {
		t.Errorf("stale: %+v", r)
	}
	for range 4 {
		e.clock.advance(25 * time.Second)
		renew()
		e.keepAlive(late.ID, fine.ID)
		e.status(late.ID, "labels/east", protocol.StatusInProgress)
	}
	e.housekeeping()
	if r := e.run(late.ID); !r.Failed || !strings.Contains(r.FailReason, "readiness timeout: labels/east") {
		t.Errorf("late: %+v", r)
	}
	if r := e.run(fine.ID); r.Failed {
		t.Errorf("a run with no required dispatchers failed: %+v", r)
	}
	if !slices.Contains(e.eventTypes(blocked.ID), protocol.EventFailed) {
		t.Errorf("no run.failed event")
	}
	e.expect(http.StatusConflict, http.MethodPost, "/runs/"+blocked.ID+"/events", protocol.PublishEvent{Type: protocol.EventTrafficComplete})
	e.expect(http.StatusGone, http.MethodPost, "/runs/"+blocked.ID+"/heartbeat", nil)

	// A failed run is cleaned up like any other, and closes.
	e.status(blocked.ID, "parcels/east", protocol.StatusCompleted)
	if r := e.run(blocked.ID); r.State != protocol.StateClosed || !r.Failed {
		t.Errorf("a failed run after cleanup: %+v", r)
	}
}

func TestASilentSuiteIsAbandonedAndStillCleanedUp(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := e.open("delivery")
	e.clock.advance(20 * time.Second)
	e.expect(http.StatusNoContent, http.MethodPost, "/runs/"+run.ID+"/heartbeat", nil)
	e.register("delivery", "parcels/east")
	e.clock.advance(20 * time.Second)
	e.register("delivery", "parcels/east")
	e.housekeeping()
	if r := e.run(run.ID); r.Abandoned {
		t.Fatalf("abandoned within its renewed lease")
	}
	e.clock.advance(15 * time.Second)
	e.register("delivery", "parcels/east")
	e.housekeeping()
	r := e.run(run.ID)
	if !r.Abandoned || r.State != protocol.StateCleanup || r.Gate.Open {
		t.Fatalf("after the lease expired: %+v", r)
	}
	e.expect(http.StatusGone, http.MethodPost, "/runs/"+run.ID+"/heartbeat", nil)
	e.status(run.ID, "parcels/east", protocol.StatusCompleted)
	if r := e.run(run.ID); r.State != protocol.StateClosed {
		t.Errorf("an abandoned run after cleanup: %s", r.State)
	}

	// A suite can give a run up itself.
	run = e.open("delivery")
	e.publish(run.ID, protocol.EventAbandoned)
	if r := e.run(run.ID); !r.Abandoned || r.State != protocol.StateCleanup {
		t.Errorf("given up: %+v", r)
	}
}

func TestAnInstanceWhoseLeaseLapsedHoldsNothingUp(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east", "parcels/west")
	run := e.open("delivery", "parcels")
	e.publish(run.ID, protocol.EventCleanupReady)
	// west scales away; east keeps renewing and completes.
	for range 2 {
		e.clock.advance(20 * time.Second)
		e.register("delivery", "parcels/east")
		e.expect(http.StatusNoContent, http.MethodPost, "/runs/"+run.ID+"/heartbeat", nil)
	}
	e.status(run.ID, "parcels/east", protocol.StatusCompleted)
	e.housekeeping()
	r := e.run(run.ID)
	if r.State != protocol.StateClosed {
		t.Fatalf("state %s, dispatchers %+v", r.State, r.Dispatchers)
	}
	for _, d := range r.Dispatchers {
		if d.Instance == "west" && (d.State != protocol.InstanceUnreachable || d.Detail != "its lease lapsed") {
			t.Errorf("west: %+v", d)
		}
	}
}

func TestTheEventFeedSkipsNothing(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := e.open("delivery")
	e.publish(run.ID, protocol.EventTrafficComplete)
	e.publish(run.ID, protocol.EventSettled)
	var seen []string
	var cursor int64
	for range 5 {
		feed := decode[protocol.EventFeed](t, e.expect(http.StatusOK, http.MethodGet, "/events?limit=1&since="+itoa64(cursor), nil))
		for _, ev := range feed.Events {
			seen = append(seen, ev.Type)
		}
		cursor = feed.Cursor
	}
	want := []string{protocol.EventOpened, protocol.EventTrafficComplete, protocol.EventSettled}
	if !slices.Equal(seen, want) {
		t.Errorf("a page at a time, the feed gave %v, want %v", seen, want)
	}
}

func TestStreamsAreRecorded(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := decode[protocol.Run](t, e.expect(http.StatusCreated, http.MethodPost, "/runs", protocol.OpenRun{
		Initiator: "s", Group: "delivery", Streams: []protocol.StreamRef{{Name: "parcels", Seed: "s1"}},
	}))
	e.expect(http.StatusNoContent, http.MethodPost, "/runs/"+run.ID+"/streams", protocol.StreamRef{Name: "collections", Seed: run.ID})
	e.expect(http.StatusNoContent, http.MethodPost, "/runs/"+run.ID+"/streams", protocol.StreamRef{Name: "collections", Seed: run.ID})
	got := e.run(run.ID).Streams
	if want := []protocol.StreamRef{{Name: "collections", Seed: run.ID}, {Name: "parcels", Seed: "s1"}}; !slices.Equal(got, want) {
		t.Errorf("streams = %+v, want %+v", got, want)
	}
}

func TestTheJournalTellsTheRun(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := e.open("delivery", "parcels")
	e.footprint(run.ID, "suite/suite", expected("parcels-service", "/parcels", "c-1"))
	rec := e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/journal", nil)
	j := rec.Body.String()
	for _, want := range []string{
		"run " + run.ID + "  group delivery",
		"parcels              east         required  notified",
		"opens a run in group delivery, requiring parcels",
		"suite/suite                  declares 1 identity at parcels-service:/parcels",
		"1 identity claimed into the footprint",
		"the readiness gate is waiting on parcels/east is notified",
	} {
		if !strings.Contains(j, want) {
			t.Errorf("the journal lacks %q:\n%s", want, j)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content type %q", ct)
	}
}

func TestTheJournalTellsABurstOfReportsOnce(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := e.open("delivery", "parcels")
	for i := range 40 {
		e.clock.advance(time.Millisecond)
		e.footprint(run.ID, "suite/suite", expected("parcels-service", "/parcels", fmt.Sprint("c-", i), fmt.Sprint("d-", i)))
	}
	e.clock.advance(time.Millisecond)
	e.status(run.ID, "parcels/east", protocol.StatusPrepared)
	e.clock.advance(time.Millisecond)
	e.footprint(run.ID, "suite/suite", expected("parcels-service", "/parcels", "late"))
	j := e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/journal", nil).Body.String()
	for _, want := range []string{
		"suite/suite                  declares 80 identities at parcels-service:/parcels, in 40 reports until +",
		"orchestrator                 the footprint grew 40 times; each time, another prepare round runs",
		"parcels/east                 reports prepared",
		// After another moment, a report is a moment of its own.
		"suite/suite                  declares 1 identity at parcels-service:/parcels\n",
		"orchestrator                 the footprint grew, so another prepare round runs",
	} {
		if !strings.Contains(j, want) {
			t.Errorf("the journal lacks %q:\n%s", want, j)
		}
	}
	if n := strings.Count(j, "declares"); n != 2 {
		t.Errorf("%d declare lines, want 2:\n%s", n, j)
	}
}

func TestPruneKeepsWhatIsInRetention(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	old := e.open("delivery")
	e.publish(old.ID, protocol.EventCleanupReady)
	e.status(old.ID, "parcels/east", protocol.StatusCompleted)
	e.clock.advance(15 * 24 * time.Hour)
	e.register("delivery", "parcels/east")
	young := e.open("delivery")
	if n, err := e.o.Prune(context.Background()); err != nil || n != 1 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	e.expect(http.StatusNotFound, http.MethodGet, "/runs/"+old.ID, nil)
	e.expect(http.StatusOK, http.MethodGet, "/runs/"+young.ID, nil)
}

func TestTheAPIAsksForItsToken(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/runs/x", "/groups", "/events"} {
		req, _ := http.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("GET %s without a token -> %d %s", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	for _, path := range []string{"/healthz", "/openapi.yaml", "/openapi.json"} {
		req, _ := http.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s without a token -> %d", path, rec.Code)
		}
	}
	e.expect(http.StatusOK, http.MethodGet, "/healthz", nil)
	e.expect(http.StatusOK, http.MethodGet, "/openapi.json", nil)
}

func TestAWalkSeesTheFootprintAsItWasWhenItBegan(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "tracking/shared")
	run := e.open("delivery")
	e.footprint(run.ID, "tracking/shared", expected("db", "tracking", "a"))
	e.footprint(run.ID, "tracking/shared", expected("db", "tracking", "b"))
	first := decode[protocol.FootprintPage](t, e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/footprint?reports=1", nil))
	if len(first.Reports) != 1 || first.Until == 0 {
		t.Fatalf("first page %+v", first)
	}
	// Reported while walking: the walk, bound by its first page's until, does not see it.
	e.footprint(run.ID, "tracking/shared", removed("db", "tracking", "a"))
	rest := decode[protocol.FootprintPage](t, e.expect(http.StatusOK, http.MethodGet,
		"/runs/"+run.ID+"/footprint?reports=10&after="+itoa64(*first.Next)+"&until="+itoa64(first.Until), nil))
	if len(rest.Reports) != 1 || rest.Reports[0].Identities[0] != "b" || rest.Until != first.Until {
		t.Errorf("the rest of the walk %+v", rest)
	}
	// A new walk sees it.
	all := decode[protocol.FootprintPage](t, e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/footprint?reports=10", nil))
	if len(all.Reports) != 3 {
		t.Errorf("a new walk %+v", all)
	}
}

// A walk narrowed to what a hook derives from reads only that, and ends where the whole walk
// would (ADR 0024).
func TestAWalkReadsOnlyWhatItAsksFor(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east", "tracking/shared")
	run := e.open("delivery")
	e.footprint(run.ID, "suite", expected("parcels", "/parcels", "p-1", "p-2"))
	e.footprint(run.ID, "parcels/east", expected("parcels-db", "public.parcels", "p-1"))
	e.footprint(run.ID, "tracking/shared", expected("tracking-db", "public.tracking", "t-1"))
	e.footprint(run.ID, "suite", expected("parcels", "/returns", "r-1"))
	e.footprint(run.ID, "parcels/east", expected("parcels-db", "public.labels", "l-1"))

	walk := func(query string) []string {
		t.Helper()
		var ids []string
		after, until := int64(0), int64(0)
		for {
			page := decode[protocol.FootprintPage](t, e.expect(http.StatusOK, http.MethodGet,
				"/runs/"+run.ID+"/footprint?reports=1&after="+itoa64(after)+"&until="+itoa64(until)+query, nil))
			for _, r := range page.Reports {
				ids = append(ids, r.Identities...)
			}
			if page.Next == nil {
				return ids
			}
			after, until = *page.Next, page.Until
		}
	}
	for query, want := range map[string][]string{
		"":                                      {"p-1", "p-2", "p-1", "t-1", "r-1", "l-1"},
		"&dispatcher=suite":                     {"p-1", "p-2", "r-1"},
		"&dispatcher=suite&dispatcher=tracking": {"p-1", "p-2", "t-1", "r-1"},
		"&dispatcher=suite&store=parcels&locator=%2Fparcels": {"p-1", "p-2"},
		"&store=parcels-db":      {"p-1", "l-1"},
		"&locator=public.labels": {"l-1"},
		"&dispatcher=nobody":     nil,
	} {
		if got := walk(query); !slices.Equal(got, want) {
			t.Errorf("walk%s: %v, want %v", query, got, want)
		}
	}

	// A narrowed walk's until is the whole footprint's, so that a walk begun before a report
	// does not see it, narrowed or not.
	first := decode[protocol.FootprintPage](t, e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/footprint?reports=1&dispatcher=tracking", nil))
	whole := decode[protocol.FootprintPage](t, e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/footprint?reports=1", nil))
	if first.Until != whole.Until {
		t.Errorf("a narrowed walk ends at %d, the whole one at %d", first.Until, whole.Until)
	}
	// Nothing new past a walk where it read is one small request away.
	past := func(query string) int {
		t.Helper()
		return len(decode[protocol.FootprintPage](t, e.expect(http.StatusOK, http.MethodGet,
			"/runs/"+run.ID+"/footprint?reports=1&after="+itoa64(whole.Until)+query, nil)).Reports)
	}
	e.footprint(run.ID, "tracking/shared", expected("tracking-db", "public.tracking", "t-2"))
	if past("&dispatcher=suite") != 0 || past("&dispatcher=tracking") != 1 {
		t.Errorf("after tracking reported: %d past the walk from the suite, %d from tracking", past("&dispatcher=suite"), past("&dispatcher=tracking"))
	}
}

func TestRunsNeverOverlapOnADispatcher(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east", "tracking/shared")
	e.register("returns", "returns/shared", "tracking/shared")
	e.register("audit", "audit/one")
	first := e.open("delivery", "parcels")
	if !first.Holds {
		t.Fatalf("an open run does not hold its group: %+v", first)
	}

	refused := func(group string) protocol.Problem {
		t.Helper()
		return decode[protocol.Problem](t, e.expect(http.StatusConflict, http.MethodPost, "/runs", protocol.OpenRun{Initiator: "another suite", Group: group}))
	}
	// The same group, and a group that shares a dispatcher, wait; a group apart does not.
	if p := refused("delivery"); p.ConflictsWith != first.ID || !strings.Contains(p.Detail, "one run at a time holds a group's dispatchers") {
		t.Errorf("the same group: %+v", p)
	}
	if p := refused("returns"); p.ConflictsWith != first.ID || !strings.Contains(p.Detail, "holds dispatchers group returns shares") {
		t.Errorf("a group sharing tracking: %+v", p)
	}
	apart := e.open("audit")

	// Once the first run closes, the group is free again.
	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		e.publish(first.ID, ev)
	}
	e.status(first.ID, "parcels/east", protocol.StatusCompleted)
	e.status(first.ID, "tracking/shared", protocol.StatusCompleted)
	if r := e.run(first.ID); r.State != protocol.StateClosed || r.Holds {
		t.Fatalf("a closed run still holds: %+v", r)
	}
	e.open("returns")
	if r := e.run(apart.ID); !r.Holds {
		t.Errorf("a run apart lost its hold: %+v", r)
	}
}

func TestACleanupThatOutlastsItsHoldStopsHoldingTheGroup(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	run := e.open("delivery", "parcels")
	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		e.publish(run.ID, ev)
	}
	// Its dispatcher stays alive, and never verifies its cleanup.
	e.expect(http.StatusConflict, http.MethodPost, "/runs", protocol.OpenRun{Initiator: "next", Group: "delivery"})
	for elapsed := time.Duration(0); elapsed <= e.o.cfg.CleanupHold; elapsed += 20 * time.Second {
		e.clock.advance(20 * time.Second)
		e.register("delivery", "parcels/east")
		e.status(run.ID, "parcels/east", protocol.StatusInProgress)
		e.housekeeping()
	}
	r := e.run(run.ID)
	if r.State != protocol.StateCleanup || r.Holds {
		t.Fatalf("past the cleanup hold: %+v", r)
	}
	if j := e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/journal", nil).Body.String(); !strings.Contains(j, "the run stops holding its group's dispatchers, so other runs may open, and it goes on cleaning up") {
		t.Errorf("the journal does not say the hold ended:\n%s", j)
	}
	e.open("delivery", "parcels")
}

func TestPartiesThatDoNotSpeakItsProtocolAreRefused(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	for body, want := range map[string]any{
		"newer than this orchestrator's": protocol.Registration{Name: "parcels", Instance: "west", Team: "parcels", CallbackURL: "http://p.invalid", Secret: e.secret(), Protocol: protocol.Version + 1},
		"did not say which protocol":     map[string]any{"name": "parcels", "instance": "west", "team": "parcels", "callbackUrl": "http://p.invalid", "secret": e.secret()},
	} {
		if p := decode[protocol.Problem](t, e.expect(http.StatusUnprocessableEntity, http.MethodPost, "/dispatchers", want)); !strings.Contains(p.Detail, body) {
			t.Errorf("%s: %+v", body, p)
		}
	}
	if p := decode[protocol.Problem](t, e.expect(http.StatusUnprocessableEntity, http.MethodPost, "/runs", map[string]any{"initiator": "old suite", "group": "delivery"})); !strings.Contains(p.Detail, "upgrade its stormbreaker packs") {
		t.Errorf("a suite that does not say: %+v", p)
	}
	if h := decode[map[string]any](t, e.expect(http.StatusOK, http.MethodGet, "/healthz", nil)); h["protocol"] != float64(protocol.Version) {
		t.Errorf("health: %v", h)
	}
}

func TestTheEventFeedByGroupStartsAtItsUnfinishedRuns(t *testing.T) {
	e := newEnv(t)
	e.register("delivery", "parcels/east")
	e.register("audit", "audit/one")
	// A delivery run that closed long ago, then one still going, and an audit run.
	old := e.open("delivery")
	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		e.publish(old.ID, ev)
	}
	e.status(old.ID, "parcels/east", protocol.StatusCompleted)
	current := e.open("delivery")
	audit := e.open("audit")

	feed := decode[protocol.EventFeed](t, e.expect(http.StatusOK, http.MethodGet, "/events?group=delivery&since=0", nil))
	if len(feed.Events) != 1 || feed.Events[0].RunID != current.ID {
		t.Fatalf("delivery's feed, from the start: %+v", feed)
	}
	all := decode[protocol.EventFeed](t, e.expect(http.StatusOK, http.MethodGet, "/events?since=0&limit=1000", nil))
	if feed.Cursor != all.Cursor {
		t.Errorf("a short page by group ends at %d; the feed ends at %d", feed.Cursor, all.Cursor)
	}
	// From the cursor on: only delivery's events.
	e.publish(audit.ID, protocol.EventTrafficComplete)
	e.publish(current.ID, protocol.EventTrafficComplete)
	next := decode[protocol.EventFeed](t, e.expect(http.StatusOK, http.MethodGet, fmt.Sprintf("/events?group=delivery&since=%d", feed.Cursor), nil))
	if len(next.Events) != 1 || next.Events[0].RunID != current.ID || next.Events[0].Type != protocol.EventTrafficComplete {
		t.Errorf("delivery's next events: %+v", next)
	}
}

func TestACleanupPastItsDeadlineClosesUnfinished(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.CleanupHold, c.CleanupDeadline = time.Minute, 5*time.Minute })
	e.register("delivery", "parcels/east", "tracking/shared")
	run := e.open("delivery", "parcels")
	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		e.publish(run.ID, ev)
	}
	e.status(run.ID, "tracking/shared", protocol.StatusCompleted)
	// parcels/east stays alive and never verifies.
	for elapsed := time.Duration(0); elapsed <= 5*time.Minute; elapsed += 20 * time.Second {
		e.clock.advance(20 * time.Second)
		e.register("delivery", "parcels/east", "tracking/shared")
		e.status(run.ID, "parcels/east", protocol.StatusInProgress)
		e.housekeeping()
	}
	r := e.run(run.ID)
	if r.State != protocol.StateClosed || !r.Unfinished || r.Holds {
		t.Fatalf("past the cleanup deadline: %+v", r)
	}
	if j := e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/journal", nil).Body.String(); !strings.Contains(j, "the run closes unfinished. Not completed: parcels/east;") {
		t.Errorf("the journal:\n%s", j)
	}
	if m := e.expect(http.StatusOK, http.MethodGet, "/metrics", nil).Body.String(); !strings.Contains(m, "stormbreaker_runs_unfinished 1") {
		t.Errorf("the metrics:\n%s", m)
	}
}
