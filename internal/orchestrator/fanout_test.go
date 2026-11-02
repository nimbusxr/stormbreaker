package orchestrator

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// receiver is a dispatcher's notification endpoint that checks signatures.
type receiver struct {
	t      *testing.T
	secret string
	now    func() time.Time
	refuse bool

	mu  sync.Mutex
	got []protocol.Event
}

func (rc *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if err := protocol.Verify(r.Header, rc.secret, body, rc.now()); err != nil {
		rc.t.Errorf("a notification failed its signature check: %v", err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var ev protocol.Event
	if err := json.Unmarshal(body, &ev); err != nil {
		rc.t.Errorf("a notification is not an event: %s", body)
	}
	if r.Header.Get(protocol.HeaderID) != ev.ID {
		rc.t.Errorf("webhook-id %q is not the event's id %q", r.Header.Get(protocol.HeaderID), ev.ID)
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.refuse {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	rc.got = append(rc.got, ev)
}

func (rc *receiver) types() []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var out []string
	for _, ev := range rc.got {
		out = append(out, ev.Type)
	}
	return out
}

func (e *env) attach(group, name, instance string, rc *receiver) {
	e.t.Helper()
	srv := httptest.NewServer(rc)
	e.t.Cleanup(srv.Close)
	rc.secret = e.secret()
	rc.now = e.clock.now
	e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", protocol.Registration{
		Name: name, Instance: instance, Team: "t", CallbackURL: srv.URL + "/events", Secret: rc.secret,
	})
	e.expect(http.StatusNoContent, http.MethodPut, "/groups/"+group+"/dispatchers/"+name, nil)
}

func TestNotificationsAreSignedAndAcknowledge(t *testing.T) {
	e := newEnv(t)
	east := &receiver{t: t}
	e.attach("delivery", "parcels", "east", east)
	run := e.open("delivery", "parcels")
	e.publish(run.ID, protocol.EventTrafficComplete)

	if err := e.o.Deliver(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A notification is a claim check: the one not yet sent stands for the later event too.
	if got, want := east.types(), []string{protocol.EventOpened}; !slices.Equal(got, want) {
		t.Errorf("received %v, want %v", got, want)
	}
	if d := e.run(run.ID).Dispatchers[0]; d.State != protocol.InstanceAcknowledged {
		t.Errorf("after receiving, %+v", d)
	}
	// Delivered is delivered: nothing is sent twice.
	if err := e.o.Deliver(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(east.types()); n != 1 {
		t.Errorf("received %d notifications after a second round", n)
	}
	// Once that one went, the next event is a notification of its own.
	e.publish(run.ID, protocol.EventSettled)
	if err := e.o.Deliver(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := east.types(), []string{protocol.EventOpened, protocol.EventSettled}; !slices.Equal(got, want) {
		t.Errorf("received %v, want %v", got, want)
	}
}

func TestANotificationOnItsWayIsNotCountedOnForLaterEvents(t *testing.T) {
	e := newEnv(t)
	e.attach("delivery", "parcels", "east", &receiver{t: t})
	run := e.open("delivery", "parcels")
	// The opened notification is claimed for sending; the instance may already have read the run.
	if due, err := e.o.dueDeliveries(context.Background(), 10); err != nil || len(due) != 1 {
		t.Fatalf("claimed %v, %v", due, err)
	}
	e.publish(run.ID, protocol.EventTrafficComplete)
	e.publish(run.ID, protocol.EventSettled)
	var sending, pending int
	if err := e.o.db.QueryRow(`select count(*) filter (where status = 'sending'), count(*) filter (where status = 'pending') from deliveries`).Scan(&sending, &pending); err != nil {
		t.Fatal(err)
	}
	if sending != 1 || pending != 1 {
		t.Errorf("%d sending and %d pending: the later events want one notification of their own", sending, pending)
	}
	// A crash with a notification on its way: it goes out again after the restart.
	restarted, err := Open(context.Background(), e.o.cfg, WithClock(e.clock.now), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.db.QueryRow(`select count(*) filter (where status = 'sending'), count(*) filter (where status = 'pending') from deliveries`).Scan(&sending, &pending); err != nil {
		t.Fatal(err)
	}
	if sending != 0 || pending != 2 {
		t.Errorf("after a restart, %d sending and %d pending", sending, pending)
	}
}

func TestAnInstanceThatNeverAnswersBecomesUnreachable(t *testing.T) {
	e := newEnv(t)
	west := &receiver{t: t, refuse: true}
	e.attach("delivery", "parcels", "west", west)
	run := e.open("delivery", "parcels")

	// MaxAttempts is 3 here, with backoff from a second.
	for range 3 {
		if err := e.o.Deliver(context.Background()); err != nil {
			t.Fatal(err)
		}
		e.clock.advance(10 * time.Second)
		e.keepAlive(run.ID)
		e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", protocol.Registration{
			Name: "parcels", Instance: "west", Team: "t", CallbackURL: "http://unused.invalid", Secret: west.secret,
		})
	}
	d := e.run(run.ID).Dispatchers[0]
	if d.State != protocol.InstanceUnreachable || d.Detail != "notifications stopped reaching it" {
		t.Fatalf("after three refusals, %+v", d)
	}
	e.housekeeping()
	if r := e.run(run.ID); !r.Failed {
		t.Errorf("a required unreachable instance did not fail the run: %+v", r)
	}
	// It holds nothing up, but the journal says that what it claimed is not answered for.
	e.housekeeping()
	if j := e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/journal", nil).Body.String(); !strings.Contains(j, "the run closes with parcels/west unreachable: what it claimed is not answered for") {
		t.Errorf("the journal's close does not name the unreachable instance:\n%s", j)
	}
}

func TestAnInstanceStillReportingIsNotMadeUnreachableByFailedNotifications(t *testing.T) {
	e := newEnv(t)
	west := &receiver{t: t, refuse: true}
	e.attach("delivery", "parcels", "west", west)
	run := e.open("delivery", "parcels")

	// Its notifications never arrive, but it reads the run from the event feed and reports.
	for range 3 {
		e.status(run.ID, "parcels/west", protocol.StatusInProgress)
		if err := e.o.Deliver(context.Background()); err != nil {
			t.Fatal(err)
		}
		e.clock.advance(10 * time.Second)
		e.keepAlive(run.ID)
		e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", protocol.Registration{
			Name: "parcels", Instance: "west", Team: "t", CallbackURL: "http://unused.invalid", Secret: west.secret,
		})
	}
	if d := e.run(run.ID).Dispatchers[0]; d.State == protocol.InstanceUnreachable {
		t.Fatalf("an instance still reporting was made unreachable: %+v", d)
	}
	if j := e.expect(http.StatusOK, http.MethodGet, "/runs/"+run.ID+"/journal", nil).Body.String(); !strings.Contains(j, "but it is still reporting, so it reads the run from the event feed") {
		t.Errorf("the journal does not say why it stays reachable:\n%s", j)
	}
	e.housekeeping()
	if r := e.run(run.ID); r.Failed {
		t.Errorf("the run failed on an instance still reporting: %+v", r)
	}
}

func TestARestartedOrchestratorGivesOneLeaseBeforeSilenceCounts(t *testing.T) {
	e := newEnv(t)
	east := &receiver{t: t}
	e.attach("delivery", "parcels", "east", east)
	run := e.open("delivery", "parcels")
	e.status(run.ID, "parcels/east", protocol.StatusPrepared)

	// The orchestrator is down for two leases: nobody could renew or report.
	lease := e.o.cfg.Lease
	e.clock.advance(2 * lease)
	restarted, err := Open(context.Background(), e.o.cfg, WithClock(e.clock.now), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close() })
	e.o, e.h = restarted, restarted.Handler()
	e.housekeeping()
	r := e.run(run.ID)
	if r.Failed || r.Abandoned || r.Dispatchers[0].State != protocol.InstancePrepared {
		t.Fatalf("straight after the restart, silence during the outage counted: %+v", r)
	}

	// Renewing within the lease keeps everything as it was.
	e.clock.advance(lease / 2)
	e.keepAlive(run.ID)
	e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", protocol.Registration{
		Name: "parcels", Instance: "east", Team: "t", CallbackURL: "http://unused.invalid", Secret: east.secret,
	})
	e.clock.advance(lease / 2)
	e.housekeeping()
	if r := e.run(run.ID); r.Failed || r.Abandoned || r.Dispatchers[0].State != protocol.InstancePrepared {
		t.Fatalf("renewed after the restart, %+v", r)
	}

	// A whole lease of silence after the restart counts as it always does.
	e.clock.advance(lease + time.Second)
	e.housekeeping()
	if r := e.run(run.ID); (!r.Failed && !r.Abandoned) || r.Dispatchers[0].State != protocol.InstanceUnreachable {
		t.Errorf("silent for a lease after the restart, %+v", r)
	}
}

func TestBackoffGrowsAndIsBounded(t *testing.T) {
	o := &Orchestrator{cfg: Config{RetryBase: time.Second, RetryMax: 8 * time.Second}}
	for attempts, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 8 * time.Second, 9: 8 * time.Second} {
		for range 20 {
			got := o.backoff(attempts)
			if got < want-want/10 || got > want+want/10 {
				t.Fatalf("backoff(%d) = %s, want %s give or take a tenth", attempts, got, want)
			}
		}
	}
}
