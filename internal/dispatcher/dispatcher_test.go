package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/internal/orchestrator"
	"github.com/nimbusxr/stormbreaker/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// lab is an orchestrator, really serving and notifying, that dispatchers join.
type lab struct {
	t      *testing.T
	ctx    context.Context
	url    string
	client *protocol.Client
}

func newLab(t *testing.T) *lab {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	o, err := orchestrator.Open(ctx, orchestrator.Config{
		DB: filepath.Join(t.TempDir(), "o.db"), Token: "tok", FanoutInterval: 20 * time.Millisecond,
		RetryBase: 50 * time.Millisecond, RetryMax: 200 * time.Millisecond,
	}, orchestrator.WithLogger(quiet))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(o.Handler())
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		srv.Close()
		o.Close()
	})
	return &lab{t: t, ctx: ctx, url: srv.URL, client: protocol.NewClient(srv.URL, "tok")}
}

// start runs a dispatcher instance in the lab.
func (l *lab) start(name, instance string, groups []string, hooks Hooks) *Dispatcher {
	l.t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	l.t.Cleanup(srv.Close)
	d, err := New(Config{
		Name: name, Instance: instance, Team: "team-" + name, CallbackURL: srv.URL + "/events",
		Orchestrator: l.url, Token: "tok", Groups: groups, Reassert: 100 * time.Millisecond, Logger: quiet,
	}, hooks)
	if err != nil {
		l.t.Fatal(err)
	}
	mux.Handle("/", d.Handler())
	ctx, cancel := context.WithCancel(l.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx)
	}()
	l.t.Cleanup(func() {
		cancel()
		<-done
	})
	return d
}

// await polls the run until ok, failing the test after a while.
func (l *lab) await(runID, what string, ok func(*protocol.Run) bool) *protocol.Run {
	l.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		run, err := l.client.Run(l.ctx, runID)
		if err != nil {
			l.t.Fatal(err)
		}
		if ok(run) {
			return run
		}
		if time.Now().After(deadline) {
			j, _ := l.client.Journal(l.ctx, runID)
			l.t.Fatalf("waited for %s; the run is %+v\n%s", what, run, j)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (l *lab) waitRegistered(names ...string) {
	l.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ds, err := l.client.Dispatchers(l.ctx)
		if err != nil {
			l.t.Fatal(err)
		}
		var got []string
		for _, d := range ds {
			got = append(got, d.Name+"/"+d.Instance)
		}
		missing := false
		for _, n := range names {
			if !slices.Contains(got, n) {
				missing = true
			}
		}
		if !missing {
			return
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("registered %v, want %v", got, names)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// store is a team's store: identities it holds.
type store struct {
	mu  sync.Mutex
	ids map[string]bool
}

func newStore() *store { return &store{ids: map[string]bool{}} }

func (s *store) put(id string) { s.mu.Lock(); s.ids[id] = true; s.mu.Unlock() }

func (s *store) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for id := range s.ids {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func (s *store) remove(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		delete(s.ids, id)
	}
}

// identitiesAt reads the footprint's identities at a place, sorted, once each.
func identitiesAt(ctx context.Context, fp Footprint, storeName, locator string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for c, err := range fp(ctx, protocol.FootprintFilter{}) {
		if err != nil {
			return nil, err
		}
		if c.Store == storeName && c.Locator == locator && !seen[c.Identity] {
			seen[c.Identity] = true
			out = append(out, c.Identity)
		}
	}
	slices.Sort(out)
	return out, nil
}

// teamHooks prepares, for every identity claimed at from, one in its own store (derive names
// it) and declares it at to; cleanup removes what it holds of the run's and proves it gone.
func teamHooks(s *store, from, to [2]string, derive func(string) string) Hooks {
	return Hooks{
		Prepare: func(ctx context.Context, run *protocol.Run, fp Footprint, declare Report) error {
			ids, err := identitiesAt(ctx, fp, from[0], from[1])
			if err != nil {
				return err
			}
			for _, id := range ids {
				d := derive(id)
				s.put(d)
				if err := declare(protocol.FootprintEntry{Store: to[0], Locator: to[1], Identities: []string{d}}); err != nil {
					return err
				}
			}
			return nil
		},
		Cleanup: func(ctx context.Context, run *protocol.Run, fp Footprint, report Report) (bool, error) {
			ids, err := identitiesAt(ctx, fp, to[0], to[1])
			if err != nil {
				return false, err
			}
			s.remove(ids)
			if len(ids) == 0 {
				return true, nil
			}
			return true, report(protocol.FootprintEntry{Store: to[0], Locator: to[1], Identities: ids, Resolution: protocol.ResolutionRemovedVerified})
		},
	}
}

func TestARunFromOpenToClosed(t *testing.T) {
	l := newLab(t)
	parcelsStore, trackingStore := newStore(), newStore()
	parcels := teamHooks(parcelsStore, [2]string{"parcels-service", "/parcels"}, [2]string{"parcels-mem", "parcels"}, func(id string) string { return id })
	tracking := teamHooks(trackingStore, [2]string{"parcels-mem", "parcels"}, [2]string{"tracking-db", "public.tracking"}, func(id string) string { return "TRK-" + id })
	l.start("parcels", "east", []string{"delivery"}, parcels)
	l.start("parcels", "west", []string{"delivery"}, parcels)
	l.start("tracking", "shared", []string{"delivery"}, tracking)
	l.start("audit", "one", []string{"delivery"}, Hooks{})
	l.waitRegistered("parcels/east", "parcels/west", "tracking/shared", "audit/one")
	// Every member must have joined before the run opens.
	deadline := time.Now().Add(5 * time.Second)
	for {
		groups, _ := l.client.Groups(l.ctx)
		if len(groups) == 1 && len(groups[0].Dispatchers) == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("groups %+v", groups)
		}
		time.Sleep(10 * time.Millisecond)
	}

	run, err := l.client.OpenRun(l.ctx, protocol.OpenRun{Initiator: "test", Group: "delivery", Requires: []string{"parcels", "tracking"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.client.ReportFootprint(l.ctx, run.ID, protocol.Suite, "", protocol.FootprintReport{Entries: []protocol.FootprintEntry{
		{Store: "parcels-service", Locator: "/parcels", Identities: []string{"c-1", "c-2", "c-3"}, Resolution: protocol.ResolutionExpected},
	}}); err != nil {
		t.Fatal(err)
	}
	l.await(run.ID, "the gate to open", func(r *protocol.Run) bool { return r.Gate.Open })

	// The cascade reached tracking, and nobody reported what they had not prepared.
	if got := trackingStore.list(); !slices.Equal(got, []string{"TRK-c-1", "TRK-c-2", "TRK-c-3"}) {
		t.Errorf("tracking prepared %v", got)
	}
	claims, err := identitiesAt(l.ctx, func(ctx context.Context, only protocol.FootprintFilter) iter.Seq2[protocol.Claim, error] {
		return l.client.Footprint(ctx, run.ID, only)
	}, "tracking-db", "public.tracking")
	if err != nil || len(claims) != 3 {
		t.Errorf("tracking' claims %v, %v", claims, err)
	}

	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		if _, err := l.client.Publish(l.ctx, run.ID, ev); err != nil {
			t.Fatal(err)
		}
	}
	closed := l.await(run.ID, "the run to close", func(r *protocol.Run) bool { return r.Done() })
	for _, d := range closed.Dispatchers {
		if d.State != protocol.InstanceCompleted {
			t.Errorf("%s/%s ended %s", d.Name, d.Instance, d.State)
		}
	}
	if got := append(parcelsStore.list(), trackingStore.list()...); len(got) != 0 {
		t.Errorf("left behind: %v", got)
	}
}

func TestABlockedDispatcherFailsTheRunAndOthersStillCleanUp(t *testing.T) {
	l := newLab(t)
	prepared := newStore()
	var cleaned atomic.Bool
	l.start("parcels", "east", []string{"delivery"}, Hooks{
		Prepare: func(_ context.Context, _ *protocol.Run, _ Footprint, declare Report) error {
			prepared.put("seeded")
			return declare(protocol.FootprintEntry{Store: "mem", Locator: "seed", Identities: []string{"seeded"}})
		},
		Cleanup: func(_ context.Context, _ *protocol.Run, _ Footprint, report Report) (bool, error) {
			prepared.remove([]string{"seeded"})
			cleaned.Store(true)
			return true, report(protocol.FootprintEntry{Store: "mem", Locator: "seed", Identities: []string{"seeded"}, Resolution: protocol.ResolutionRemovedVerified})
		},
	})
	l.start("tracking", "shared", []string{"delivery"}, Hooks{
		Prepare: func(context.Context, *protocol.Run, Footprint, Report) error {
			return fmt.Errorf("%w: the admin API refuses our credentials", ErrBlocked)
		},
	})
	l.waitRegistered("parcels/east", "tracking/shared")
	time.Sleep(150 * time.Millisecond) // both have joined

	run, err := l.client.OpenRun(l.ctx, protocol.OpenRun{Initiator: "test", Group: "delivery", Requires: []string{"parcels", "tracking"}})
	if err != nil {
		t.Fatal(err)
	}
	// The orchestrator's readiness sweep runs every two seconds.
	failed := l.await(run.ID, "the run to fail", func(r *protocol.Run) bool { return r.Failed })
	if !strings.Contains(failed.FailReason, "blocked: tracking/shared (prepare: blocked: the admin API refuses our credentials)") {
		t.Errorf("fail reason %q", failed.FailReason)
	}
	l.await(run.ID, "the failed run to close", func(r *protocol.Run) bool { return r.Done() })
	if !cleaned.Load() || len(prepared.list()) != 0 {
		t.Errorf("what parcels prepared for the failed run was not cleaned up")
	}
}

func TestNotificationsMustBeSigned(t *testing.T) {
	l := newLab(t)
	d := l.start("parcels", "east", nil, Hooks{})
	h := d.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(`{"id":"evt-1","runId":"run-1"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an unsigned notification -> %d", rec.Code)
	}

	body := []byte(`{"id":"evt-1","runId":"run-1","type":"run.opened"}`)
	req := httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body))
	other, _ := protocol.NewSecret()
	_ = protocol.Sign(req.Header, other, "evt-1", body, time.Now())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a notification signed with another secret -> %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/events", bytes.NewReader(body))
	_ = protocol.Sign(req.Header, d.cfg.Secret, "evt-1", body, time.Now())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("a signed notification -> %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"dispatcher":"parcels"`) {
		t.Errorf("healthz -> %d %s", rec.Code, rec.Body.String())
	}
}

func TestPassesCoalesceAndKeepOrder(t *testing.T) {
	p := newPasses()
	release := make(chan struct{})
	var mu sync.Mutex
	var log []string
	var running, maxRunning atomic.Int32
	pass := func(name string) func() {
		return func() {
			n := running.Add(1)
			for {
				m := maxRunning.Load()
				if n <= m || maxRunning.CompareAndSwap(m, n) {
					break
				}
			}
			<-release
			mu.Lock()
			log = append(log, name)
			mu.Unlock()
			running.Add(-1)
		}
	}
	p.submit("run-a", pass("first"))
	time.Sleep(20 * time.Millisecond) // the first pass is running
	for i := range 5 {
		p.submit("run-a", pass(fmt.Sprintf("queued-%d", i)))
	}
	close(release)
	p.wait()
	if len(log) != 2 || log[0] != "first" || log[1] != "queued-0" {
		t.Errorf("passes run: %v; want the running one and one queued behind it", log)
	}
	if maxRunning.Load() != 1 {
		t.Errorf("%d passes over one run ran at once", maxRunning.Load())
	}

	// Different runs go side by side.
	block := make(chan struct{})
	var both sync.WaitGroup
	both.Add(2)
	for _, id := range []string{"run-b", "run-c"} {
		p.submit(id, func() { both.Done(); <-block })
	}
	waited := make(chan struct{})
	go func() { both.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Error("passes over different runs did not run side by side")
	}
	close(block)
	p.wait()
}

func TestSeenForgetsTheOldest(t *testing.T) {
	s := newSeen(2)
	for _, id := range []string{"a", "b"} {
		if !s.add(id) {
			t.Fatalf("%s is new", id)
		}
	}
	if s.add("a") {
		t.Error("a repeat was new")
	}
	s.add("c")
	if !s.add("a") {
		t.Error("the oldest was not forgotten")
	}
}

func TestConfigIsChecked(t *testing.T) {
	ok := Config{Name: "n", Instance: "i", Team: "t", CallbackURL: "http://x/events", Orchestrator: "http://o"}
	if _, err := New(ok, Hooks{}); err != nil {
		t.Fatalf("a complete config: %v", err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Name = "" },
		func(c *Config) { c.CallbackURL = "" },
		func(c *Config) { c.Orchestrator = "" },
		func(c *Config) { c.Name = protocol.Suite },
		func(c *Config) { c.Secret = "change-me" },
	} {
		c := ok
		mutate(&c)
		if _, err := New(c, Hooks{}); err == nil {
			t.Errorf("New accepted %+v", c)
		}
	}
	if !errors.Is(fmt.Errorf("x: %w", ErrBlocked), ErrBlocked) {
		t.Error("ErrBlocked does not wrap")
	}
}

// TestACleanupThatReportsWhereItWalksEnds is a cleanup hook that walks its own claims and reports
// each removal at the same place as it goes: its walk must not find its own removals.
func TestACleanupThatReportsWhereItWalksEnds(t *testing.T) {
	l := newLab(t)
	var rounds atomic.Int64
	l.start("tracking", "shared", []string{"delivery"}, Hooks{
		Prepare: func(_ context.Context, _ *protocol.Run, _ Footprint, declare Report) error {
			for i := range 300 {
				if err := declare(protocol.FootprintEntry{Store: "db", Locator: "tracking", Identities: []string{fmt.Sprint("TRK-", i)}}); err != nil {
					return err
				}
			}
			return nil
		},
		Cleanup: func(ctx context.Context, _ *protocol.Run, fp Footprint, report Report) (bool, error) {
			rounds.Add(1)
			for c, err := range fp(ctx, protocol.FootprintFilter{}) {
				if err != nil {
					return false, err
				}
				if c.Dispatcher == "tracking" && c.Store == "db" {
					if err := report(protocol.FootprintEntry{Store: "db", Locator: "tracking", Identities: []string{c.Identity}, Resolution: protocol.ResolutionRemovedVerified}); err != nil {
						return false, err
					}
				}
			}
			return true, nil
		},
	})
	l.waitRegistered("tracking/shared")
	time.Sleep(150 * time.Millisecond)
	run, err := l.client.OpenRun(l.ctx, protocol.OpenRun{Initiator: "test", Group: "delivery", Requires: []string{"tracking"}})
	if err != nil {
		t.Fatal(err)
	}
	l.await(run.ID, "the gate", func(r *protocol.Run) bool { return r.Gate.Open })
	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		if _, err := l.client.Publish(l.ctx, run.ID, ev); err != nil {
			t.Fatal(err)
		}
	}
	closed := l.await(run.ID, "the run to close", func(r *protocol.Run) bool { return r.Done() })
	// 300 declared, once however many prepare rounds said so; one cleanup round removes them,
	// and the removals, which grow nothing, call for no other.
	if closed.Claims != 600 || rounds.Load() != 1 {
		t.Errorf("%d claims in %d cleanup rounds, want 600 in 1", closed.Claims, rounds.Load())
	}
}

func TestASettledRoundIsNotRunAgainUntilTheFootprintGrows(t *testing.T) {
	l := newLab(t)
	var rounds atomic.Int64
	l.start("tracking", "shared", []string{"delivery"}, Hooks{
		Prepare: func(ctx context.Context, _ *protocol.Run, fp Footprint, declare Report) error {
			rounds.Add(1)
			for _, err := range fp(ctx, protocol.FootprintFilter{}) { // the whole footprint
				if err != nil {
					return err
				}
			}
			return declare(protocol.FootprintEntry{Store: "db", Locator: "tracking", Identities: []string{"TRK-1"}})
		},
	})
	l.waitRegistered("tracking/shared")
	time.Sleep(150 * time.Millisecond)
	run, err := l.client.OpenRun(l.ctx, protocol.OpenRun{Initiator: "test", Group: "delivery", Requires: []string{"tracking"}})
	if err != nil {
		t.Fatal(err)
	}
	l.await(run.ID, "the gate", func(r *protocol.Run) bool { return r.Gate.Open })
	// One round declared, one found nothing new; the sweeps since have had nothing to go on.
	time.Sleep(5 * 100 * time.Millisecond)
	if n := rounds.Load(); n != 2 {
		t.Errorf("%d prepare rounds, want 2", n)
	}
	// The suite declares something: one more round, which settles on the new version.
	if _, err := l.client.ReportFootprint(l.ctx, run.ID, protocol.Suite, protocol.Suite, protocol.FootprintReport{Entries: []protocol.FootprintEntry{
		{Store: "svc", Locator: "/parcels", Identities: []string{"c-1"}, Resolution: protocol.ResolutionExpected},
	}}); err != nil {
		t.Fatal(err)
	}
	l.await(run.ID, "a round on version 2", func(r *protocol.Run) bool {
		return r.FootprintVersion == 2 && r.Dispatchers[0].FootprintVersion == 2 && r.Gate.Open
	})
	time.Sleep(3 * 100 * time.Millisecond)
	if n := rounds.Load(); n != 3 {
		t.Errorf("%d prepare rounds, want 3", n)
	}
}

// A round that walked only what it derives from is not run again while nothing new is reported
// there: another team's declarations grow the footprint, and the instance settles on the new
// version without a round (ADR 0024).
func TestARoundIsNotRunAgainUntilWhereItReadGrows(t *testing.T) {
	l := newLab(t)
	var rounds, unnarrowed atomic.Int64
	fromSuite := protocol.FootprintFilter{Dispatchers: []string{protocol.Suite}, Store: "svc", Locator: "/parcels"}
	l.start("tracking", "shared", []string{"delivery"}, Hooks{
		Prepare: func(ctx context.Context, _ *protocol.Run, fp Footprint, declare Report) error {
			rounds.Add(1)
			for c, err := range fp(ctx, fromSuite) {
				if err != nil {
					return err
				}
				if !fromSuite.Matches(c.Dispatcher, c.Store, c.Locator) {
					unnarrowed.Add(1)
				}
				if err := declare(protocol.FootprintEntry{Store: "db", Locator: "tracking", Identities: []string{"TRK-" + c.Identity}}); err != nil {
					return err
				}
			}
			return nil
		},
	})
	l.start("parcels", "east", []string{"delivery"}, Hooks{})
	l.waitRegistered("tracking/shared", "parcels/east")
	time.Sleep(150 * time.Millisecond)
	run, err := l.client.OpenRun(l.ctx, protocol.OpenRun{Initiator: "test", Group: "delivery", Requires: []string{"tracking", "parcels"}})
	if err != nil {
		t.Fatal(err)
	}
	declare := func(name, instance string, e protocol.FootprintEntry) {
		t.Helper()
		e.Resolution = protocol.ResolutionExpected
		if _, err := l.client.ReportFootprint(l.ctx, run.ID, name, instance, protocol.FootprintReport{Entries: []protocol.FootprintEntry{e}}); err != nil {
			t.Fatal(err)
		}
	}
	declare(protocol.Suite, protocol.Suite, protocol.FootprintEntry{Store: "svc", Locator: "/parcels", Identities: []string{"c-1"}})
	settledOn := func(what string) *protocol.Run {
		t.Helper()
		return l.await(run.ID, what, func(r *protocol.Run) bool {
			for _, d := range r.Dispatchers {
				if d.Name == "tracking" && d.FootprintVersion == r.FootprintVersion && d.State == protocol.StatusPrepared {
					return true
				}
			}
			return false
		})
	}
	settledOn("tracking settled on the suite's parcel")
	time.Sleep(3 * 100 * time.Millisecond)
	before := rounds.Load()

	// Another team declares, at another place: the footprint grows, where tracking reads it does
	// not, and tracking settles on the new version without a round.
	declare("parcels", "east", protocol.FootprintEntry{Store: "parcels-db", Locator: "public.parcels", Identities: []string{"p-1", "p-2"}})
	declare(protocol.Suite, protocol.Suite, protocol.FootprintEntry{Store: "svc", Locator: "/returns", Identities: []string{"r-1"}})
	grown := settledOn("tracking settled on the grown footprint")
	time.Sleep(3 * 100 * time.Millisecond)
	if n := rounds.Load(); n != before {
		t.Errorf("%d more prepare rounds for a footprint that grew elsewhere, want none", n-before)
	}

	// The suite declares where tracking reads: one more round, and its declaration with it.
	declare(protocol.Suite, protocol.Suite, protocol.FootprintEntry{Store: "svc", Locator: "/parcels", Identities: []string{"c-2"}})
	l.await(run.ID, "a version past the grown one", func(r *protocol.Run) bool { return r.FootprintVersion > grown.FootprintVersion })
	settledOn("tracking settled on the suite's second parcel")
	if rounds.Load() == before {
		t.Error("no prepare round after the suite declared where tracking reads")
	}
	if unnarrowed.Load() != 0 {
		t.Errorf("a narrowed walk saw %d claims outside its filter", unnarrowed.Load())
	}
	var tracked []string
	for c, err := range l.client.Footprint(l.ctx, run.ID, protocol.FootprintFilter{Dispatchers: []string{"tracking"}}) {
		if err != nil {
			t.Fatal(err)
		}
		tracked = append(tracked, c.Identity)
	}
	if !slices.Contains(tracked, "TRK-c-2") {
		t.Errorf("tracking declared %v; want TRK-c-2 among them", tracked)
	}
}

func TestALongRoundSaysItIsWorking(t *testing.T) {
	l := newLab(t)
	release := make(chan struct{})
	l.start("tracking", "shared", []string{"delivery"}, Hooks{
		Prepare: func(ctx context.Context, _ *protocol.Run, _ Footprint, _ Report) error {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		},
	})
	l.waitRegistered("tracking/shared")
	time.Sleep(150 * time.Millisecond)
	run, err := l.client.OpenRun(l.ctx, protocol.OpenRun{Initiator: "test", Group: "delivery", Requires: []string{"tracking"}})
	if err != nil {
		t.Fatal(err)
	}
	l.await(run.ID, "a report that the round is under way", func(r *protocol.Run) bool {
		return r.Dispatchers[0].LastStatus == protocol.StatusInProgress && strings.Contains(r.Dispatchers[0].Detail, "prepare round under way")
	})
	close(release)
	l.await(run.ID, "the gate", func(r *protocol.Run) bool { return r.Gate.Open })
}

// TestALeaseIsRenewedWhileTheFeedHangs: an orchestrator slow to answer the event feed must not
// let an instance's lease lapse, or every run that requires it fails as unreachable.
func TestALeaseIsRenewedWhileTheFeedHangs(t *testing.T) {
	var registrations atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/dispatchers":
			registrations.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"tracking","instance":"shared","team":"t","callbackUrl":"http://x","leaseExpiresAt":"2026-11-01T00:00:00Z"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/events":
			select { // the feed hangs
			case <-release:
			case <-r.Context().Done():
			}
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	defer close(release)
	d, err := New(Config{
		Name: "tracking", Instance: "shared", Team: "t", CallbackURL: "http://x/events", Orchestrator: srv.URL,
		Groups: []string{"delivery"}, Reassert: 50 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	d.Run(ctx)
	if n := registrations.Load(); n < 6 {
		t.Errorf("renewed %d times in 600 ms at a 50 ms cadence while the feed hung", n)
	}
}
