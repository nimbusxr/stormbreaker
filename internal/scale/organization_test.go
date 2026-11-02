//go:build scale

package scale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/orchestrator"
	"github.com/nimbusxr/stormbreaker/protocol"
)

func envInt(name string, def int) int {
	if s := os.Getenv(name); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return def
}

// latencies collects durations from many goroutines.
type latencies struct {
	mu sync.Mutex
	d  []time.Duration
}

func (l *latencies) add(d time.Duration) { l.mu.Lock(); l.d = append(l.d, d); l.mu.Unlock() }

// String is the count and the p50, p99 and max.
func (l *latencies) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.d) == 0 {
		return "none"
	}
	slices.Sort(l.d)
	q := func(p float64) time.Duration {
		return l.d[min(len(l.d)-1, int(p*float64(len(l.d))))].Round(time.Millisecond)
	}
	return fmt.Sprintf("%d: p50 %s, p99 %s, max %s", len(l.d), q(.5), q(.99), l.d[len(l.d)-1].Round(time.Millisecond))
}

// errorLog counts what the orchestrator logs as errors, and keeps the first few.
type errorLog struct {
	n     atomic.Int64
	mu    sync.Mutex
	first []string
}

func (e *errorLog) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelError }
func (e *errorLog) Handle(_ context.Context, r slog.Record) error {
	e.n.Add(1)
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.first) < 5 {
		msg := r.Message
		r.Attrs(func(a slog.Attr) bool { msg += " " + a.String(); return true })
		e.first = append(e.first, msg)
	}
	return nil
}
func (e *errorLog) WithAttrs([]slog.Attr) slog.Handler { return e }
func (e *errorLog) WithGroup(string) slog.Handler      { return e }

// routes times every request to the orchestrator by its route, from the caller's side: how long
// a lease renewal or a footprint page waited is what an overloaded orchestrator shows first.
type routes struct {
	mu sync.Mutex
	by map[string]*latencies
}

func (rs *routes) transport() http.RoundTripper {
	return roundTrip{rs: rs, next: protocol.Pooled(16)}
}

func (rs *routes) report(t *testing.T) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	names := make([]string, 0, len(rs.by))
	for n := range rs.by {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		t.Logf("  %-40s %s", n, rs.by[n])
	}
}

type roundTrip struct {
	rs   *routes
	next http.RoundTripper
}

// route is a request's route, its ids and names elided.
func route(method, path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := 1; i < len(parts); i++ {
		switch prev := parts[i-1]; {
		case prev == "runs", prev == "groups", prev == "dispatchers" && i > 1, prev == "footprint" && parts[i] != "claims":
			parts[i] = "{}"
		}
	}
	return method + " /" + strings.Join(parts, "/")
}

func (r roundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	route := route(req.Method, req.URL.Path)
	begin := time.Now()
	resp, err := r.next.RoundTrip(req)
	r.rs.mu.Lock()
	l, ok := r.rs.by[route]
	if !ok {
		l = &latencies{}
		r.rs.by[route] = l
	}
	r.rs.mu.Unlock()
	l.add(time.Since(begin))
	return resp, err
}

func cpuTime() time.Duration {
	var u syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &u); err != nil {
		return 0
	}
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}

// region is the region an identity's traffic lands in: every team derives in the region the
// suite's record went to, so a team's instance in that region answers for it.
func region(identity string, regions int) int {
	root := identity[strings.LastIndexByte(identity, '/')+1:]
	h := fnv.New32a()
	h.Write([]byte(root))
	return int(h.Sum32() % uint32(regions))
}

// TestOrganizationAtScale is one environment's orchestrator serving an organization: many teams'
// dispatchers, each with an instance in every region, in groups of teams that derive from each
// other in tiers, and many runs at once. Every run declares its identities, waits for its gate,
// and is cleaned up by every team it touched.
//
// The orchestrator runs with its production defaults (notifications every 100 ms by 16 workers,
// the two-minute readiness timeout) and the dispatchers with theirs (a sweep every 10 s). Each
// dispatcher instance and each suite has its own connections, as separate processes would.
//
//	STORMBREAKER_SCALE_TEAMS=500 STORMBREAKER_SCALE_RUNS=50 \
//	  go test -tags scale -run OrganizationAtScale -timeout 60m -v ./internal/scale
func TestOrganizationAtScale(t *testing.T) {
	var (
		teams   = envInt("STORMBREAKER_SCALE_TEAMS", 100)
		regions = envInt("STORMBREAKER_SCALE_REGIONS", 2)
		width   = envInt("STORMBREAKER_SCALE_GROUP", 10) // teams in a group
		depth   = envInt("STORMBREAKER_SCALE_DEPTH", 3)  // tiers in a group's cascade
		runs    = envInt("STORMBREAKER_SCALE_RUNS", 10)  // at most one a group: runs never overlap
		perRun  = envInt("STORMBREAKER_SCALE_RUN_IDENTITIES", 10_000)
		// whole walks the whole footprint in every hook, as hooks did before walks were narrowed
		// (ADR 0024): set it to measure what narrowing saves.
		whole = os.Getenv("STORMBREAKER_SCALE_WHOLE") != ""
	)
	readiness, _ := time.ParseDuration(os.Getenv("STORMBREAKER_SCALE_READINESS")) // zero: the default
	groups := (teams + width - 1) / width
	if runs > groups {
		// Runs never overlap (ADR 0015): runs at once are in groups of their own.
		t.Fatalf("%d runs at once need %d groups, and %d teams make %d: give STORMBREAKER_SCALE_TEAMS at least %d", runs, runs, teams, groups, runs*width)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errs := &errorLog{}
	db := filepath.Join(t.TempDir(), "orchestrator.db")
	o, err := orchestrator.Open(ctx, orchestrator.Config{DB: db, Token: "tok", ReadinessTimeout: readiness}, orchestrator.WithLogger(slog.New(errs)))
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	srv := httptest.NewServer(o.Handler())
	defer srv.Close()
	served := make(chan struct{})
	go func() { defer close(served); o.Serve(ctx) }()
	defer func() { cancel(); <-served }()
	timed := &routes{by: map[string]*latencies{}}
	client := func() *protocol.Client {
		c := protocol.NewClient(srv.URL, "tok")
		c.HTTP = &http.Client{Timeout: time.Minute, Transport: timed.transport()}
		return c
	}
	watch := client()

	// The teams. Team j is in group j/width, at a tier of its group's cascade: the first tier
	// derives from what the suite declares, each later tier from one team of the tier before.
	name := func(j int) string { return fmt.Sprintf("team-%03d", j) }
	group := func(j int) string { return fmt.Sprintf("group-%02d", j/width) }
	tier := func(j int) int { return (j % width) * depth / width }
	upstream := func(j int) string {
		if tier(j) == 0 {
			return protocol.Suite
		}
		base := j - j%width
		var before []int
		for i := base; i < min(base+width, teams); i++ {
			if tier(i) == tier(j)-1 {
				before = append(before, i)
			}
		}
		return name(before[j%len(before)])
	}
	notified := &latencies{}
	var stopAll []func()
	defer func() {
		for _, stop := range stopAll {
			stop()
		}
	}()
	startedAt := time.Now()
	for j := range teams {
		me, from := name(j), upstream(j)
		// Each hook walks only what it derives from: its upstream's claims, and in cleanup its own.
		fromUpstream, own := walkOnly(protocol.FootprintFilter{Dispatchers: []string{from}}), walkOnly(protocol.FootprintFilter{Dispatchers: []string{me}})
		for r := range regions {
			instance := fmt.Sprintf("region-%d", r)
			hooks := dispatcher.Hooks{
				Prepare: func(ctx context.Context, run *protocol.Run, fp dispatcher.Footprint, declare dispatcher.Report) error {
					for c, err := range fp(ctx, fromUpstream) {
						if err != nil {
							return err
						}
						if c.Dispatcher != from || region(c.Identity, regions) != r {
							continue
						}
						root := c.Identity[strings.LastIndexByte(c.Identity, '/')+1:]
						if err := declare(protocol.FootprintEntry{Store: me + "-db", Locator: "items", Identities: []string{me + "/" + root}}); err != nil {
							return err
						}
					}
					return nil
				},
				Cleanup: func(ctx context.Context, run *protocol.Run, fp dispatcher.Footprint, report dispatcher.Report) (bool, error) {
					for c, err := range fp(ctx, own) {
						if err != nil {
							return false, err
						}
						if c.Dispatcher != me || c.Instance != instance {
							continue
						}
						if err := report(protocol.FootprintEntry{Store: c.Store, Locator: c.Locator, Identities: []string{c.Identity}, Resolution: protocol.ResolutionRemovedVerified}); err != nil {
							return false, err
						}
					}
					return true, nil
				},
			}
			mux := http.NewServeMux()
			ds := httptest.NewServer(mux)
			d, err := dispatcher.New(dispatcher.Config{
				Name: me, Instance: instance, Team: me, CallbackURL: ds.URL + "/events",
				Orchestrator: srv.URL, Token: "tok", Groups: []string{group(j)}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
				HTTP: &http.Client{Timeout: time.Minute, Transport: timed.transport()},
			}, hooks)
			if err != nil {
				t.Fatal(err)
			}
			h := d.Handler()
			// How long a notification took to arrive after its event.
			mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
				if req.Method == http.MethodPost {
					body, _ := io.ReadAll(req.Body)
					var ev protocol.Event
					if json.Unmarshal(body, &ev) == nil && !ev.Time.IsZero() {
						notified.add(time.Since(ev.Time))
					}
					req.Body = io.NopCloser(bytes.NewReader(body))
				}
				h.ServeHTTP(w, req)
			})
			dctx, stop := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() { defer close(done); d.Run(dctx) }()
			stopAll = append(stopAll, func() { stop(); <-done; ds.Close() })
		}
	}
	instances := teams * regions
	for {
		ds, err := watch.Dispatchers(ctx)
		if err == nil && len(ds) == instances {
			break
		}
		if time.Since(startedAt) > 5*time.Minute {
			t.Fatalf("%d of %d instances registered after 5 minutes: %v", len(ds), instances, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	registered := time.Since(startedAt)

	heap := watchHeap()
	cpuBefore, wallBefore := cpuTime(), time.Now()
	var (
		gates, closes, views = &latencies{}, &latencies{}, &latencies{}
		failed               atomic.Int64
		failures             sync.Map
		claims               atomic.Int64
		wg                   sync.WaitGroup
	)
	// Housekeeping, timed: each phase is idempotent, so running one again beside the
	// orchestrator's own loop changes nothing but measures what a pass costs.
	sweeps := map[string]*latencies{"readiness": {}, "leases": {}, "close": {}, "fanout": {}}
	stopSweeps := make(chan struct{})
	var sweepWG sync.WaitGroup
	sweepWG.Add(1)
	go func() {
		defer sweepWG.Done()
		phases := []struct {
			name string
			fn   func(context.Context) error
		}{{"readiness", o.EnforceReadiness}, {"leases", o.EnforceLeases}, {"close", o.CloseSettled}, {"fanout", o.Deliver}}
		for {
			for _, p := range phases {
				begin := time.Now()
				_ = p.fn(ctx)
				sweeps[p.name].add(time.Since(begin))
			}
			select {
			case <-stopSweeps:
				return
			case <-time.After(time.Second):
			}
		}
	}()

	for i := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := client()
			g := i % groups
			var requires []string
			for j := g * width; j < min((g+1)*width, teams); j++ {
				requires = append(requires, name(j))
			}
			fail := func(why string) {
				failed.Add(1)
				failures.Store(i, why)
			}
			opened := time.Now()
			run, err := c.OpenRun(ctx, protocol.OpenRun{Initiator: fmt.Sprintf("suite-%d", i), Group: fmt.Sprintf("group-%02d", g), Requires: requires})
			if err != nil {
				fail("open: " + err.Error())
				return
			}
			beat, stopBeat := context.WithCancel(ctx)
			defer stopBeat()
			go func() {
				tick := time.NewTicker(10 * time.Second)
				defer tick.Stop()
				for {
					select {
					case <-beat.Done():
						return
					case <-tick.C:
						_ = c.Heartbeat(beat, run.ID)
					}
				}
			}()
			ids := make([]string, 0, min(perRun, 25_000))
			for k := range perRun {
				ids = append(ids, uuidish(i*perRun+k))
				if len(ids) == cap(ids) || k == perRun-1 {
					if _, err := c.ReportFootprint(ctx, run.ID, protocol.Suite, "", protocol.FootprintReport{Entries: []protocol.FootprintEntry{
						{Store: "parcels", Locator: "/parcels", Identities: ids, Resolution: protocol.ResolutionExpected},
					}}); err != nil {
						fail("declare: " + err.Error())
						return
					}
					ids = ids[:0]
				}
			}
			wait := func(ok func(*protocol.Run) bool) (*protocol.Run, bool) {
				for {
					begin := time.Now()
					r, err := c.Run(ctx, run.ID)
					views.add(time.Since(begin))
					if err != nil {
						fail("view: " + err.Error())
						return nil, false
					}
					if ok(r) {
						return r, true
					}
					if r.Done() || r.Failed || r.Abandoned {
						why := fmt.Sprintf("%s (failed %v: %s, abandoned %v)", r.State, r.Failed, r.FailReason, r.Abandoned)
						fail(why)
						return nil, false
					}
					time.Sleep(250 * time.Millisecond)
				}
			}
			if _, ok := wait(func(r *protocol.Run) bool { return r.Gate.Open }); !ok {
				return
			}
			gates.add(time.Since(opened))
			for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
				if _, err := c.Publish(ctx, run.ID, ev); err != nil {
					fail("publish: " + err.Error())
					return
				}
			}
			cleanupFrom := time.Now()
			r, ok := wait(func(r *protocol.Run) bool { return r.Done() })
			if !ok {
				return
			}
			closes.add(time.Since(cleanupFrom))
			claims.Add(r.Claims)
		}()
	}
	wg.Wait()
	wall := time.Since(wallBefore)
	cores := float64(cpuTime()-cpuBefore) / float64(wall)
	close(stopSweeps)
	sweepWG.Wait()
	peak := heap.end()

	t.Logf("shape                     %d teams × %d regions = %d instances, in %d groups of %d (a cascade %d deep)", teams, regions, instances, groups, width, depth)
	t.Logf("runs at once              %d, each declaring %d identities", runs, perRun)
	t.Logf("hooks walk                %s", map[bool]string{true: "the whole footprint", false: "only what they derive from"}[whole])
	t.Logf("instances registered in   %s", registered.Round(time.Millisecond))
	t.Logf("all runs done in          %s", wall.Round(time.Millisecond))
	t.Logf("gate open after opening   %s", gates)
	t.Logf("closed after cleanup      %s", closes)
	t.Logf("failed runs               %d", failed.Load())
	failures.Range(func(k, v any) bool { t.Logf("  run %v: %v", k, v); return true })
	t.Logf("notifications             %s (event to arrival)", notified)
	t.Logf("run view, as polled       %s", views)
	t.Logf("requests to the orchestrator, by route, as their callers waited:")
	timed.report(t)
	for _, p := range []string{"readiness", "leases", "close", "fanout"} {
		t.Logf("housekeeping: %-10s  %s", p, sweeps[p])
	}
	t.Logf("claims answered for       %d", claims.Load())
	t.Logf("database on disk          %s", mb(uint64(onDisk(db))))
	t.Logf("heap at its peak          %s (orchestrator, %d dispatcher instances and %d suites in one process)", mb(peak), instances, runs)
	t.Logf("CPU                       %.1f cores on average, for everything in the process", cores)
	t.Logf("orchestrator errors       %d", errs.n.Load())
	for _, e := range errs.first {
		t.Logf("  %s", e)
	}
	if failed.Load() > 0 {
		t.Errorf("%d of %d runs did not close", failed.Load(), runs)
	}
}
