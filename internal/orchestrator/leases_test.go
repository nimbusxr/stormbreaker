package orchestrator

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// A renewal that changes nothing but the lease is held in memory and written with the others,
// so thousands of instances renewing do not each cost a commit; the lease reads as renewed at
// once, and everything that decides on a lease writes the renewals first (ADR 0025).
func TestRenewalsAreWrittenTogether(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	reg := protocol.Registration{Name: "parcels", Instance: "east", Team: "team-parcels", CallbackURL: "http://parcels-east.invalid/events", Secret: e.secret()}
	e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", reg)
	e.expect(http.StatusNoContent, http.MethodPut, "/groups/delivery/dispatchers/parcels", nil)
	writes := e.o.writes.n.Load()

	// Renewals within the lease the database has: none of them writes, and the lease reads as the
	// last's. (Running, the orchestrator writes held renewals every second; one whose written
	// lease has ended is written at once.)
	for range 4 {
		e.clock.advance(e.o.cfg.Lease / 5)
		e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", reg)
	}
	if n := e.o.writes.n.Load() - writes; n != 0 {
		t.Errorf("4 renewals made %d writes, want none", n)
	}
	ds := decode[[]protocol.Dispatcher](t, e.expect(http.StatusOK, http.MethodGet, "/dispatchers", nil))
	if want := e.clock.now().Add(e.o.cfg.Lease); len(ds) != 1 || !ds[0].LeaseExpiresAt.Equal(want) {
		t.Errorf("the lease reads %+v, want it to end at %s", ds, want)
	}

	// The database's lease has ended, the renewed one has not: a sweep writes the renewals before
	// it decides who lapsed, and the instance stays on its run.
	run := e.open("delivery", "parcels")
	e.clock.advance(e.o.cfg.Lease / 2)
	e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", reg)
	e.clock.advance(e.o.cfg.Lease * 3 / 4)
	e.keepAlive(run.ID)
	if err := e.o.EnforceReadiness(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.run(run.ID); got.Failed || got.Dispatchers[0].State == protocol.InstanceUnreachable {
		t.Errorf("a renewed instance was taken for gone: %+v", got)
	}

	// A flush writes every renewal held, in one transaction.
	e.clock.advance(time.Second)
	e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", reg)
	before := e.o.writes.n.Load()
	if err := e.o.flushLeases(ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.o.writes.n.Load() - before; n != 1 {
		t.Errorf("the flush made %d writes, want 1", n)
	}
	var stored int64
	if err := e.o.ro.QueryRowContext(ctx, `select lease_expires_at from dispatchers where name = 'parcels' and instance = 'east'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if want := millis(e.clock.now().Add(e.o.cfg.Lease)); stored != want {
		t.Errorf("the database has the lease ending at %d, want %d", stored, want)
	}

	// A registration that changes anything else is written at once.
	reg.CallbackURL = "http://parcels-east-2.invalid/events"
	before = e.o.writes.n.Load()
	e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", reg)
	if n := e.o.writes.n.Load() - before; n != 1 {
		t.Errorf("a new callback made %d writes, want 1", n)
	}
	// Joining a group it is in writes nothing.
	before = e.o.writes.n.Load()
	e.expect(http.StatusNoContent, http.MethodPut, "/groups/delivery/dispatchers/parcels", nil)
	if n := e.o.writes.n.Load() - before; n != 0 {
		t.Errorf("joining again made %d writes, want none", n)
	}
}
