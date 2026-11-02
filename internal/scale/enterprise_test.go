//go:build scale

package scale

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/orchestrator"
	"github.com/nimbusxr/stormbreaker/protocol"
)

// TestEnterprisePeak is a large enterprise's shared staging environment at its busiest hour, on
// one orchestrator (docs: explanations/scale, "In an enterprise"):
//
//   - 2,000 services take part, each with a dispatcher in 3 regions, and 100 more in integration
//     groups: 6,300 dispatcher instances, renewing their leases and reading the event feed.
//   - Every deploy to staging runs a smoke run: the deployed service and the one downstream of
//     it, 1,000 identities, 2 minutes of traffic. At 4 deploys a service a working day and a
//     peak hour three times the average, that is 2,400 smoke runs in the hour: 40 a minute.
//   - Integration runs, one a minute: ten services in three tiers, 10,000 identities, 5 minutes.
//   - A load run beside them: 100,000 requests a second for 10 minutes, 60 million identities.
//
// The suites poll as stormbreaker-core does (the gate every 100 ms, close every 500 ms) and
// heartbeat every 10 s. Every hook walks only what it derives from. The orchestrator is this
// process's, or, for numbers worth quoting, its own process elsewhere: STORMBREAKER_SCALE_ORCHESTRATOR
// is its URL, and the dispatchers listen on STORMBREAKER_SCALE_LISTEN and tell it to call them at
// STORMBREAKER_SCALE_ADVERTISE (scripts/scale-enterprise.sh runs it so, in containers).
func TestEnterprisePeak(t *testing.T) {
	p := enterpriseProfile()
	e := newEnterprise(t)
	defer e.close()

	// The environment's dispatchers: smoke services in pairs, integration services in groups of
	// ten in three tiers, and the load run's ledger.
	began := time.Now()
	for i := range p.smokeServices {
		me := fmt.Sprintf("svc-%04d", i)
		from := protocol.FootprintFilter{Dispatchers: []string{protocol.Suite}, Store: "smoke"}
		if i%2 == 1 { // the downstream of the pair derives from its upstream
			from = protocol.FootprintFilter{Dispatchers: []string{fmt.Sprintf("svc-%04d", i-1)}}
		}
		e.team(me, []string{fmt.Sprintf("smoke-%04d", i/2)}, from, p.regions)
	}
	for i := range p.integrationGroups * 10 {
		me, g := fmt.Sprintf("int-%04d", i), i/10
		from := protocol.FootprintFilter{Dispatchers: []string{protocol.Suite}, Store: "integration"}
		if tier := (i % 10) * 3 / 10; tier > 0 { // a team of the tier before
			from = protocol.FootprintFilter{Dispatchers: []string{fmt.Sprintf("int-%04d", g*10+((tier-1)*10+2)/3)}}
		}
		e.team(me, []string{fmt.Sprintf("integration-%02d", g)}, from, p.regions)
	}
	if p.loadIdentities > 0 {
		e.team("ledger", []string{"load"}, protocol.FootprintFilter{Dispatchers: []string{protocol.Suite}, Store: "load"}, p.regions)
	}
	instances := e.instances()
	watch := e.client()
	for {
		ds, err := watch.Dispatchers(e.ctx)
		if err == nil && len(ds) == instances {
			break
		}
		if time.Since(began) > 10*time.Minute {
			t.Fatalf("%d of %d instances registered after 10 minutes: %v", len(ds), instances, err)
		}
		time.Sleep(time.Second)
	}
	registered := time.Since(began)

	// The environment at rest: every instance renewing its lease and reading the feed.
	time.Sleep(15 * time.Second)
	idleFrom := e.metrics()
	time.Sleep(p.idle)
	idle := e.metrics().since(idleFrom)

	// The peak hour, or as much of it as p.peak.
	var (
		wg                            sync.WaitGroup
		smoke, integration, load      = newRunStats("smoke"), newRunStats("integration"), newRunStats("load")
		open, mostOpen                atomic.Int64
		smokeGroups, integrationGroup = p.smokeServices / 2, 0
	)
	launch := func(st *runStats, fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if n := open.Add(1); n > mostOpen.Load() {
				mostOpen.Store(n)
			}
			defer open.Add(-1)
			fn()
		}()
	}
	peakFrom := e.metrics()
	peakBegan := time.Now()
	deadline := peakBegan.Add(p.peak)
	var arrivals sync.WaitGroup
	every := func(rate float64, fn func(n int)) {
		if rate <= 0 {
			return
		}
		arrivals.Add(1)
		go func() {
			defer arrivals.Done()
			gap := time.Duration(float64(time.Minute) / rate)
			for n := 0; time.Now().Before(deadline); n++ {
				fn(n)
				time.Sleep(gap)
			}
		}()
	}
	every(p.smokeRate, func(n int) {
		g := n % smokeGroups
		launch(smoke, func() {
			e.run(smoke, fmt.Sprintf("smoke-%04d", g), []string{fmt.Sprintf("svc-%04d", 2*g), fmt.Sprintf("svc-%04d", 2*g+1)},
				"smoke", fmt.Sprintf("s%d-", n), p.smokeIdentities, p.smokeTraffic)
		})
	})
	every(p.integrationRate, func(n int) {
		g := integrationGroup % p.integrationGroups
		integrationGroup++
		requires := make([]string, 10)
		for k := range requires {
			requires[k] = fmt.Sprintf("int-%04d", g*10+k)
		}
		launch(integration, func() {
			e.run(integration, fmt.Sprintf("integration-%02d", g), requires, "integration", fmt.Sprintf("i%d-", n), p.integrationIdentities, p.integrationTraffic)
		})
	})
	if p.loadIdentities > 0 {
		arrivals.Add(1)
		go func() {
			defer arrivals.Done()
			time.Sleep(p.loadAfter)
			launch(load, func() { e.run(load, "load", []string{"ledger"}, "load", "l-", p.loadIdentities, p.loadTraffic) })
		}()
	}
	arrivals.Wait()
	peak := e.metrics().since(peakFrom)
	peakWall := time.Since(peakBegan)
	wg.Wait()
	drained := time.Since(peakBegan)
	end := e.metrics()

	t.Logf("instances                 %d, registered in %s", instances, registered.Round(time.Millisecond))
	t.Logf("at rest, for %s:", p.idle)
	idle.log(t, p.idle)
	t.Logf("the peak, for %s (all runs done %s after it began; at most %d open at once):", peakWall.Round(time.Second), drained.Round(time.Second), mostOpen.Load())
	peak.log(t, peakWall)
	for _, st := range []*runStats{smoke, integration, load} {
		st.log(t)
	}
	t.Logf("requests to the orchestrator, by route, as their callers waited:")
	e.timed.report(t)
	t.Logf("database                  %s", mb(uint64(end.v["stormbreaker_store_bytes"])))
	if n := smoke.failed.Load() + integration.failed.Load() + load.failed.Load(); n > 0 {
		t.Errorf("%d runs did not close clean", n)
	}
}

type profile struct {
	smokeServices, regions, integrationGroups                int
	smokeIdentities, integrationIdentities, loadIdentities   int
	smokeRate, integrationRate                               float64
	smokeTraffic, integrationTraffic, loadTraffic, loadAfter time.Duration
	idle, peak                                               time.Duration
}

func enterpriseProfile() profile {
	dur := func(name string, def time.Duration) time.Duration {
		if d, err := time.ParseDuration(os.Getenv(name)); err == nil {
			return d
		}
		return def
	}
	rate := func(name string, def float64) float64 {
		if f, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil {
			return f
		}
		return def
	}
	return profile{
		smokeServices:         envInt("STORMBREAKER_SCALE_SMOKE_SERVICES", 2000),
		regions:               envInt("STORMBREAKER_SCALE_REGIONS", 3),
		integrationGroups:     envInt("STORMBREAKER_SCALE_INTEGRATION_GROUPS", 10),
		smokeIdentities:       envInt("STORMBREAKER_SCALE_SMOKE_IDENTITIES", 1000),
		integrationIdentities: envInt("STORMBREAKER_SCALE_INTEGRATION_IDENTITIES", 10_000),
		loadIdentities:        envInt("STORMBREAKER_SCALE_LOAD_IDENTITIES", 60_000_000),
		smokeRate:             rate("STORMBREAKER_SCALE_SMOKE_RATE", 40),
		integrationRate:       rate("STORMBREAKER_SCALE_INTEGRATION_RATE", 1),
		smokeTraffic:          dur("STORMBREAKER_SCALE_SMOKE_TRAFFIC", 2*time.Minute),
		integrationTraffic:    dur("STORMBREAKER_SCALE_INTEGRATION_TRAFFIC", 5*time.Minute),
		loadTraffic:           dur("STORMBREAKER_SCALE_LOAD_TRAFFIC", 10*time.Minute),
		loadAfter:             dur("STORMBREAKER_SCALE_LOAD_AFTER", 2*time.Minute),
		idle:                  dur("STORMBREAKER_SCALE_IDLE", time.Minute),
		peak:                  dur("STORMBREAKER_SCALE_PEAK", 15*time.Minute),
	}
}

// enterprise is an orchestrator, this process's or another's, and the dispatcher instances and
// suites that use it.
type enterprise struct {
	t                 *testing.T
	ctx               context.Context
	cancel            context.CancelFunc
	url, token        string
	listen, advertise string
	timed             *routes
	mu                sync.Mutex
	stops             []func()
	n                 int
}

func newEnterprise(t *testing.T) *enterprise {
	ctx, cancel := context.WithCancel(context.Background())
	e := &enterprise{
		t: t, ctx: ctx, cancel: cancel, token: "tok", timed: &routes{by: map[string]*latencies{}},
		url: os.Getenv("STORMBREAKER_SCALE_ORCHESTRATOR"), listen: "127.0.0.1", advertise: "127.0.0.1",
	}
	if v := os.Getenv("STORMBREAKER_SCALE_TOKEN"); v != "" {
		e.token = v
	}
	if v := os.Getenv("STORMBREAKER_SCALE_LISTEN"); v != "" {
		e.listen = v
	}
	if v := os.Getenv("STORMBREAKER_SCALE_ADVERTISE"); v != "" {
		e.advertise = v
	}
	if e.url == "" {
		o, err := orchestrator.Open(ctx, orchestrator.Config{DB: filepath.Join(t.TempDir(), "orchestrator.db"), Token: e.token},
			orchestrator.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(o.Handler())
		served := make(chan struct{})
		go func() { defer close(served); o.Serve(ctx) }()
		e.url = srv.URL
		e.stops = append(e.stops, func() { cancel(); <-served; srv.Close(); o.Close() })
	}
	return e
}

func (e *enterprise) close() {
	e.cancel()
	e.mu.Lock()
	stops := e.stops
	e.mu.Unlock()
	for i := len(stops) - 1; i >= 0; i-- {
		stops[i]()
	}
}

func (e *enterprise) instances() int { e.mu.Lock(); defer e.mu.Unlock(); return e.n }

func (e *enterprise) client() *protocol.Client {
	c := protocol.NewClient(e.url, e.token)
	c.HTTP = &http.Client{Timeout: time.Minute, Transport: e.timed.transport()}
	return c
}

// team starts a dispatcher with an instance in each region. Each instance prepares, for the
// identities whose traffic landed in its region, one of its own from what from keeps, and cleans
// up its own.
func (e *enterprise) team(me string, groups []string, from protocol.FootprintFilter, regions int) {
	for r := range regions {
		instance := fmt.Sprintf("region-%d", r)
		own := protocol.FootprintFilter{Dispatchers: []string{me}, Store: me + "-db"}
		e.start(me, instance, groups, dispatcher.Hooks{
			Prepare: func(ctx context.Context, _ *protocol.Run, fp dispatcher.Footprint, declare dispatcher.Report) error {
				for c, err := range fp(ctx, from) {
					if err != nil {
						return err
					}
					if region(c.Identity, regions) != r {
						continue
					}
					root := c.Identity[strings.LastIndexByte(c.Identity, '/')+1:]
					if err := declare(protocol.FootprintEntry{Store: me + "-db", Locator: "items", Identities: []string{me + "/" + root}}); err != nil {
						return err
					}
				}
				return nil
			},
			Cleanup: func(ctx context.Context, _ *protocol.Run, fp dispatcher.Footprint, report dispatcher.Report) (bool, error) {
				for c, err := range fp(ctx, own) {
					if err != nil {
						return false, err
					}
					if c.Instance != instance || c.Resolution != protocol.ResolutionExpected {
						continue
					}
					if err := report(protocol.FootprintEntry{Store: c.Store, Locator: c.Locator, Identities: []string{c.Identity}, Resolution: protocol.ResolutionRemovedVerified}); err != nil {
						return false, err
					}
				}
				return true, nil
			},
		})
	}
}

// start runs a dispatcher instance, listening on its own port as a pod would.
func (e *enterprise) start(name, instance string, groups []string, hooks dispatcher.Hooks) {
	ln, err := net.Listen("tcp", net.JoinHostPort(e.listen, "0"))
	if err != nil {
		e.t.Fatal(err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	d, err := dispatcher.New(dispatcher.Config{
		Name: name, Instance: instance, Team: name, CallbackURL: "http://" + net.JoinHostPort(e.advertise, port) + "/events",
		Orchestrator: e.url, Token: e.token, Groups: groups, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		HTTP: &http.Client{Timeout: time.Minute, Transport: e.timed.transport()},
	}, hooks)
	if err != nil {
		e.t.Fatal(err)
	}
	srv := &http.Server{Handler: d.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	ctx, stop := context.WithCancel(e.ctx)
	done := make(chan struct{})
	go func() { defer close(done); d.Run(ctx) }()
	e.mu.Lock()
	e.stops = append(e.stops, func() { stop(); <-done; _ = srv.Close() })
	e.n++
	e.mu.Unlock()
}

// run is a suite's run, as stormbreaker-core drives one: open (waiting its turn while another run
// holds the group), declare, wait for the gate, send traffic, and wait for every dispatcher to
// clean up.
func (e *enterprise) run(st *runStats, group string, requires []string, store, prefix string, identities int, traffic time.Duration) {
	ctx := e.ctx
	c := e.client()
	st.started.Add(1)
	asked := time.Now()
	var run *protocol.Run
	for {
		r, err := c.OpenRun(ctx, protocol.OpenRun{Initiator: st.name, Group: group, Requires: requires})
		if err == nil {
			run = r
			break
		}
		if protocol.ConflictOf(err) == "" || time.Since(asked) > 30*time.Minute {
			st.fail("open: " + err.Error())
			return
		}
		time.Sleep(time.Second)
	}
	opened := time.Now()
	st.turns.add(opened.Sub(asked))
	beat, stopBeat := context.WithCancel(ctx)
	defer stopBeat()
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-beat.Done():
				return
			case <-t.C:
				_ = c.Heartbeat(beat, run.ID)
			}
		}
	}()
	ids := make([]string, 0, min(identities, 25_000))
	for k := range identities {
		ids = append(ids, prefix+uuidish(k))
		if len(ids) == cap(ids) || k == identities-1 {
			if _, err := c.ReportFootprint(ctx, run.ID, protocol.Suite, "", protocol.FootprintReport{Entries: []protocol.FootprintEntry{
				{Store: store, Locator: "/" + store, Identities: ids, Resolution: protocol.ResolutionExpected},
			}}); err != nil {
				st.fail("declare: " + err.Error())
				return
			}
			ids = ids[:0]
		}
	}
	st.declares.add(time.Since(opened))
	wait := func(every time.Duration, ok func(*protocol.Run) bool) bool {
		for {
			r, err := c.Run(ctx, run.ID)
			if err != nil {
				st.fail("view: " + err.Error())
				return false
			}
			if ok(r) {
				return true
			}
			if r.Done() || r.Failed || r.Abandoned {
				st.fail(fmt.Sprintf("%s: %s (failed %v: %s, abandoned %v)", run.ID, r.State, r.Failed, r.FailReason, r.Abandoned))
				return false
			}
			time.Sleep(every)
		}
	}
	if !wait(100*time.Millisecond, func(r *protocol.Run) bool { return r.Gate.Open }) {
		return
	}
	st.gates.add(time.Since(opened))
	time.Sleep(traffic)
	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		if _, err := c.Publish(ctx, run.ID, ev); err != nil {
			st.fail("publish: " + err.Error())
			return
		}
	}
	cleanup := time.Now()
	if !wait(500*time.Millisecond, func(r *protocol.Run) bool { return r.Done() }) {
		return
	}
	st.closes.add(time.Since(cleanup))
	st.closed.Add(1)
}

type runStats struct {
	name                           string
	started, closed, failed        atomic.Int64
	turns, declares, gates, closes *latencies
	mu                             sync.Mutex
	failures                       []string
}

func newRunStats(name string) *runStats {
	return &runStats{name: name, turns: &latencies{}, declares: &latencies{}, gates: &latencies{}, closes: &latencies{}}
}

func (s *runStats) fail(why string) {
	s.failed.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.failures) < 5 {
		s.failures = append(s.failures, why)
	}
}

func (s *runStats) log(t *testing.T) {
	if s.started.Load() == 0 {
		return
	}
	t.Logf("%s runs: %d started, %d closed clean, %d failed", s.name, s.started.Load(), s.closed.Load(), s.failed.Load())
	t.Logf("  waited for its turn      %s", s.turns)
	t.Logf("  declared                 %s", s.declares)
	t.Logf("  gate open after opening  %s", s.gates)
	t.Logf("  closed after cleanup     %s", s.closes)
	for _, f := range s.failures {
		t.Logf("  failed: %s", f)
	}
}

// scrape is the orchestrator's metrics at a moment.
type scrape struct {
	at time.Time
	v  map[string]float64
}

func (e *enterprise) metrics() scrape {
	s := scrape{at: time.Now(), v: map[string]float64{}}
	req, _ := http.NewRequestWithContext(e.ctx, http.MethodGet, e.url+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Logf("metrics: %v", err)
		return s
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, " ")
		if f, err := strconv.ParseFloat(value, 64); ok && err == nil {
			s.v[name] = f
		}
	}
	return s
}

// writerUse is what the orchestrator's writer did between two scrapes.
type writerUse struct{ writes, busy, wait float64 }

func (s scrape) since(from scrape) writerUse {
	d := func(k string) float64 { return s.v[k] - from.v[k] }
	return writerUse{writes: d("stormbreaker_writes_total"), busy: d("stormbreaker_writer_busy_seconds_total"), wait: d("stormbreaker_writer_wait_seconds_total")}
}

func (w writerUse) log(t *testing.T, over time.Duration) {
	secs := over.Seconds()
	perWrite := 0.0
	if w.writes > 0 {
		perWrite = w.wait / w.writes * 1000
	}
	t.Logf("  writes                   %.0f a second", w.writes/secs)
	t.Logf("  writer busy              %.0f%% of the time", 100*w.busy/secs)
	t.Logf("  wait for the writer      %.1f ms a write, on average", perWrite)
}
