// Package sbcore is the stormbreaker-core pack: it ties a scenario to a run on the
// orchestrator. A scenario tagged @stormbreaker-group/<group> opens a run for that group before
// its first step; the run's readiness gate holds traffic until the group's dispatchers have
// prepared; and when the scenario ends the run goes through cleanup, and the scenario passes only
// when every dispatcher cleaned up. The run's journal goes into the report.
//
// The other stormbreaker packs build on its Go API: Current, Run.Declare, Run.AddStream,
// Run.Ready, Observe, and Run.Load and Run.Check, which add to the progress the run sends.
package sbcore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nimbusxr/axx/core"
	"github.com/nimbusxr/axx/fixtures"

	"github.com/nimbusxr/stormbreaker/protocol"
	"github.com/nimbusxr/stormbreaker/streams"
)

// Name is the pack's name, and its section of axx.yaml.
const Name = "stormbreaker-core"

// Tags that tie a scenario to a run.
const (
	// TagGroup opens a run for a group: @stormbreaker-group/delivery.
	TagGroup = "stormbreaker-group/"
	// TagRequires names the dispatchers the run's gate waits for: @stormbreaker-requires/parcels,tracking.
	TagRequires = "stormbreaker-requires/"
)

// DeclareBatch is how many identities one footprint report carries at most.
const DeclareBatch = 25_000

// Config is the pack's section of axx.yaml.
type Config struct {
	// URL is the orchestrator's.
	URL string `json:"url"`
	// Token is its bearer token, if it asks for one.
	Token string `json:"token"`
	// Readiness is how long a scenario waits at the readiness gate (default 120s).
	Readiness string `json:"readiness"`
	// Close is how long a scenario waits for its run to close after cleanup opens (default 180s).
	Close string `json:"close"`
	// Turn is how long a scenario waits to open its run while another run holds the dispatchers
	// its group needs (default 30m): runs never overlap on a dispatcher (ADR 0015).
	Turn string `json:"turn"`
	// Fixtures is the directory of the fixture factories and the streams, relative to axx.yaml:
	// axx.yaml's fixtures.baseDir, if it sets one (default: the directory of axx.yaml).
	Fixtures string `json:"fixtures"`
	// LintLimit is the most keys a stream may have for Bind to lint it (default 1,000,000):
	// linting holds every identity.
	LintLimit int64 `json:"lintLimit"`
	// Progress is how often the run's progress goes to the orchestrator, for the UI (default
	// 1s; 0 sends only the last): ADR 0027.
	Progress string `json:"progress"`
}

type settings struct {
	url, token        string
	readiness, close_ time.Duration
	turn              time.Duration
	heartbeat, poll   time.Duration
	progress          time.Duration
	fixtures          string
	lintLimit         int64
}

func load(s *core.Suite) (*settings, error) {
	return core.Cached(s, Name+".settings", func() (*settings, error) {
		var c Config
		if err := s.PackConfig(Name, &c); err != nil {
			return nil, err
		}
		st := &settings{
			url: s.Interpolate(c.URL), token: s.Interpolate(c.Token),
			readiness: 120 * time.Second, close_: 180 * time.Second, turn: 30 * time.Minute, heartbeat: 10 * time.Second, poll: 100 * time.Millisecond,
			progress: time.Second,
			fixtures: s.ProjectDir(), lintLimit: 1_000_000,
		}
		if f := s.Interpolate(c.Fixtures); f != "" {
			st.fixtures = f
			if !filepath.IsAbs(f) {
				st.fixtures = filepath.Join(s.ProjectDir(), f)
			}
		}
		if c.LintLimit > 0 {
			st.lintLimit = c.LintLimit
		}
		for _, d := range []struct {
			name, v string
			into    *time.Duration
		}{{"readiness", c.Readiness, &st.readiness}, {"close", c.Close, &st.close_}, {"turn", c.Turn, &st.turn}, {"progress", c.Progress, &st.progress}} {
			if v := s.Interpolate(d.v); v != "" {
				parsed, err := time.ParseDuration(v)
				if err != nil {
					return nil, fmt.Errorf("packs.%s.%s: %w", Name, d.name, err)
				}
				*d.into = parsed
			}
		}
		return st, nil
	})
}

// Run is a scenario's run on the orchestrator.
type Run struct {
	sc       *core.Scenario
	client   *protocol.Client
	settings *settings
	id       string
	group    string
	from     time.Time

	mu        sync.Mutex
	dirty     bool
	observers []func(*Run) error
	observed  map[string]bool
	gone      bool
	stop      context.CancelFunc
	beats     sync.WaitGroup
	// What the other packs add to the run's progress: their streams' load, and their checks.
	loads     map[string]func() protocol.LoadProgress
	loadOrder []string
	checks    []protocol.CheckResult
}

var runKey = core.NewStateKey[runSlot](Name, func(*core.Scenario) *runSlot { return &runSlot{} }, nil)

type runSlot struct{ run *Run }

// Current is the scenario's run, if it has one: it is tagged @stormbreaker-group/<group>.
func Current(sc *core.Scenario) (*Run, bool) {
	slot, ok := runKey.Peek(sc)
	if !ok || slot.run == nil {
		return nil, false
	}
	return slot.run, true
}

// ID is the run's id; it is also the seed of the streams the run generates records from.
func (r *Run) ID() string { return r.id }

// Group is the group the run is for.
func (r *Run) Group() string { return r.group }

// WindowFrom is when the run's window opened.
func (r *Run) WindowFrom() time.Time { return r.from }

// Fixtures is the directory of the fixture factories and the streams.
func (r *Run) Fixtures() string { return r.settings.fixtures }

// Client is the orchestrator's client the run uses.
func (r *Run) Client() *protocol.Client { return r.client }

// Declare reports identities the run will create at a place, in batches, as the suite's
// footprint. The readiness gate then waits for the dispatchers to see them before the next
// traffic.
func (r *Run) Declare(ctx context.Context, store, locator string, identities []string) error {
	for len(identities) > 0 {
		n := min(len(identities), DeclareBatch)
		if _, err := r.client.ReportFootprint(ctx, r.id, protocol.Suite, protocol.Suite, protocol.FootprintReport{Entries: []protocol.FootprintEntry{{
			Store: store, Locator: locator, Identities: identities[:n], Resolution: protocol.ResolutionExpected,
		}}}); err != nil {
			return fmt.Errorf("declare the run's identities at %s:%s: %w", store, locator, err)
		}
		identities = identities[n:]
		r.mu.Lock()
		r.dirty = true
		r.mu.Unlock()
	}
	return nil
}

// DeclareObserved declares the identities an observer found at a place that the run has not
// declared there before: what a scenario staged by hand is read before every traffic step, and
// only what is new is reported.
func (r *Run) DeclareObserved(ctx context.Context, store, locator string, identities []string) error {
	r.mu.Lock()
	if r.observed == nil {
		r.observed = map[string]bool{}
	}
	var fresh []string
	for _, id := range identities {
		k := store + "\x00" + locator + "\x00" + id
		if !r.observed[k] {
			r.observed[k] = true
			fresh = append(fresh, id)
		}
	}
	r.mu.Unlock()
	if len(fresh) == 0 {
		return nil
	}
	return r.Declare(ctx, store, locator, fresh)
}

// AddStream records a stream the run generates records from, and its seed.
func (r *Run) AddStream(ctx context.Context, name, seed string) error {
	return r.client.AddStream(ctx, r.id, protocol.StreamRef{Name: name, Seed: seed})
}

// Bind opens the named stream (<name>.stream.yaml, under the fixtures directory) for the run:
// keyed on the run, whose id is its seed, with relative times anchored at the run's window. It
// records the stream on the run, checks that no two keys claim the same identity (for streams of
// up to lintLimit keys), and declares every identity the stream will create at store/locator,
// before any of it is sent.
func (r *Run) Bind(ctx context.Context, name, store, locator string) (*streams.Stream, error) {
	path, err := streams.Find(r.settings.fixtures, name)
	if err != nil {
		return nil, err
	}
	def, err := streams.Load(path)
	if err != nil {
		return nil, err
	}
	lib, err := core.Cached(r.sc.Suite(), Name+".fixtures:"+r.settings.fixtures, func() (*fixtures.Library, error) {
		return streams.LoadLibrary(r.settings.fixtures)
	})
	if err != nil {
		return nil, err
	}
	s, err := streams.OpenWith(def, lib, streams.Options{Seed: r.id, Run: r.id, Window: r.from})
	if err != nil {
		return nil, err
	}
	if err := r.AddStream(ctx, name, r.id); err != nil {
		return nil, err
	}
	if def.Count <= r.settings.lintLimit {
		if err := s.Lint(); err != nil {
			return nil, err
		}
	} else {
		r.sc.Log("stormbreaker: stream %s has %d keys, more than packs.%s.lintLimit (%d): its identities were not checked for collisions",
			name, def.Count, Name, r.settings.lintLimit)
	}
	n, err := s.Identities(DeclareBatch, func(ids []string) error { return r.Declare(ctx, store, locator, ids) })
	if err != nil {
		return nil, err
	}
	r.sc.Log("stormbreaker: stream %s declares %d identities at %s:%s", name, n, store, locator)
	return s, nil
}

// Observe registers fn to run before each step that sends traffic, to declare identities found
// in what the scenario staged by hand (a request's payload, an event) before it is sent.
func Observe(sc *core.Scenario, fn func(*Run) error) {
	if r, ok := Current(sc); ok {
		r.mu.Lock()
		r.observers = append(r.observers, fn)
		r.mu.Unlock()
	}
}

// Ready waits at the run's readiness gate when the run declared identities since it last
// passed it. A run that fails, or does not open its gate in time, fails the step.
func (r *Run) Ready(ctx context.Context) error {
	r.mu.Lock()
	dirty := r.dirty
	r.mu.Unlock()
	if !dirty {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, r.settings.readiness)
	defer cancel()
	for {
		run, err := r.client.Run(ctx, r.id)
		if err != nil && ctx.Err() == nil {
			return fmt.Errorf("read run %s: %w", r.id, err)
		}
		if run != nil && err == nil {
			switch {
			case run.Failed:
				return core.Fail(fmt.Sprintf("run %s failed at its readiness gate", r.id), "the gate to open", run.FailReason)
			case run.Abandoned:
				return core.Failf("run %s was abandoned while waiting at its readiness gate", r.id)
			case run.Gate.Open:
				r.mu.Lock()
				r.dirty = false
				r.mu.Unlock()
				return nil
			}
		}
		select {
		case <-ctx.Done():
			waiting := "nothing it reported"
			if run != nil && len(run.Gate.Waiting) > 0 {
				waiting = strings.Join(run.Gate.Waiting, "; ")
			}
			return core.Fail(fmt.Sprintf("run %s's readiness gate did not open within %s", r.id, r.settings.readiness), "the gate to open", "waiting on "+waiting)
		case <-time.After(r.settings.poll):
		}
	}
}

// Ready waits at the readiness gate of the scenario's run, if it has one.
func Ready(sc *core.Scenario) error {
	r, ok := Current(sc)
	if !ok {
		return nil
	}
	return r.Ready(sc.Context())
}

// Pack returns the stormbreaker-core pack.
func Pack() core.Pack { return pack{} }

type pack struct{}

func (pack) Manifest() core.Manifest {
	return core.Manifest{
		Name: Name, Namespace: Name,
		Doc: "Ties a scenario to a run on the stormbreaker orchestrator. A scenario tagged `@stormbreaker-group/<group>` opens a run " +
			"for that group before its first step (`@stormbreaker-requires/<dispatcher>,...` names the dispatchers its readiness " +
			"gate waits for). Traffic waits at the gate until the group's dispatchers have prepared for what the run declared. When " +
			"the scenario passes, the run goes through cleanup, and the scenario passes only when every dispatcher cleaned up; when " +
			"it fails, the run is abandoned and cleaned up all the same. The run's journal goes into the report.\n\n" +
			"Configure the orchestrator in axx.yaml: `packs: {stormbreaker-core: {url: ..., token: ...}}`.",
		ConfigSchema: configSchema,
		Hooks: []core.Hook{
			{ID: "stormbreaker.run.open", Phase: core.BeforeScenario, Order: -100, Run: open},
			{ID: "stormbreaker.run.gate", Phase: core.BeforeStep, Run: beforeStep},
		},
		Steps: steps(),
	}
}

var configSchema = json.RawMessage(`{
  "type": "object",
  "description": "The stormbreaker orchestrator the scenarios' runs are opened on.",
  "properties": {
    "url": {"type": "string", "description": "The orchestrator's URL; it can use ${env:...}."},
    "token": {"type": "string", "description": "Its bearer token, if it asks for one; it can use ${env:...}."},
    "readiness": {"type": "string", "description": "How long a scenario waits at its run's readiness gate (default 120s)."},
    "close": {"type": "string", "description": "How long a scenario waits for its run to close after cleanup opens (default 180s)."},
    "turn": {"type": "string", "description": "How long a scenario waits to open its run while another run holds the dispatchers its group needs (default 30m): runs never overlap on a dispatcher."},
    "fixtures": {"type": "string", "description": "The directory of the fixture factories and the streams, relative to axx.yaml (default: its directory; set it to fixtures.baseDir if axx.yaml sets one)."},
    "lintLimit": {"type": "integer", "description": "The most keys a stream may have for its identities to be checked for collisions when it binds (default 1000000)."},
    "progress": {"type": "string", "description": "How often the run's progress (its steps, load and checks) goes to the orchestrator, for whoever watches it in the UI (default 1s; 0 sends only the last)."}
  },
  "required": ["url"],
  "additionalProperties": false
}`)

// tagged reads the scenario's run tags: its group, and the dispatchers it requires.
func tagged(tags []string) (group string, requires []string) {
	for _, t := range tags {
		t = strings.TrimPrefix(t, "@")
		if g, ok := strings.CutPrefix(t, TagGroup); ok {
			group = g
		}
		if r, ok := strings.CutPrefix(t, TagRequires); ok {
			for _, name := range strings.Split(r, ",") {
				if name = strings.TrimSpace(name); name != "" {
					requires = append(requires, name)
				}
			}
		}
	}
	return group, requires
}

// open opens the run of a tagged scenario, starts its heartbeat, and arranges its close.
func open(sc *core.Scenario) error {
	group, requires := tagged(sc.Tags)
	if group == "" {
		if len(requires) > 0 {
			return fmt.Errorf("@%s needs @%s<group> too", TagRequires, TagGroup)
		}
		return nil
	}
	st, err := load(sc.Suite())
	if err != nil {
		return err
	}
	if st.url == "" {
		return fmt.Errorf("the scenario is tagged @%s%s, but axx.yaml gives no packs.%s.url for the orchestrator", TagGroup, group, Name)
	}
	client := protocol.NewClient(st.url, st.token)
	run, err := openInTurn(sc, client, st, protocol.OpenRun{
		Initiator: fmt.Sprintf("axx %s:%d %s", sc.URI, sc.Line, sc.Name), Group: group, Requires: requires,
	})
	if err != nil {
		return fmt.Errorf("open a run for group %s: %w", group, err)
	}
	// A new run waits at its gate: its required dispatchers have not prepared yet, whether or not
	// the suite declares anything.
	r := &Run{sc: sc, client: client, settings: st, id: run.ID, group: group, from: run.WindowFrom, dirty: true}
	runKey.Of(sc).run = r
	sc.Log("stormbreaker run %s opened for group %s", r.id, group)
	sc.Describe("stormbreaker", func() any { return map[string]any{"run": r.id, "group": group, "requires": requires} })

	ctx, cancel := context.WithCancel(context.Background())
	r.stop = cancel
	r.beats.Add(1)
	go r.heartbeat(ctx)
	if st.progress > 0 {
		r.beats.Add(1)
		go r.report(ctx)
	}
	sc.OnClose("stormbreaker run "+r.id, r.close)
	return nil
}

// turnEvery is how often a scenario waiting for its turn asks again.
var turnEvery = 2 * time.Second

// openInTurn opens the run, waiting while another run holds the dispatchers its group needs, up
// to the turn setting: runs never overlap on a dispatcher (ADR 0015). An orchestrator restarting
// meanwhile is waited out too.
func openInTurn(sc *core.Scenario, client *protocol.Client, st *settings, req protocol.OpenRun) (*protocol.Run, error) {
	deadline := time.Now().Add(st.turn)
	waiting := ""
	for {
		var run *protocol.Run
		err := throughRestarts(sc.Context(), func() error {
			var err error
			run, err = client.OpenRun(sc.Context(), req)
			return err
		})
		holder := protocol.ConflictOf(err)
		if holder == "" {
			if err == nil && waiting != "" {
				sc.Log("stormbreaker: run %s let go; this run opens now", waiting)
			}
			return run, err
		}
		if time.Now().Add(turnEvery).After(deadline) {
			return nil, fmt.Errorf("run %s still holds the dispatchers this run needs after %s (packs.%s.turn): %w", holder, st.turn, Name, err)
		}
		if holder != waiting {
			sc.Log("stormbreaker: run %s holds the dispatchers group %s needs; waiting for it to close (up to %s)", holder, req.Group, st.turn)
			waiting = holder
		}
		select {
		case <-sc.Context().Done():
			return nil, errors.Join(err, sc.Context().Err())
		case <-time.After(turnEvery):
		}
	}
}

// heartbeat renews the run's lease until it stops, or the orchestrator says the run is gone.
func (r *Run) heartbeat(ctx context.Context) {
	defer r.beats.Done()
	t := time.NewTicker(r.settings.heartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := r.client.Heartbeat(ctx, r.id)
			if protocol.StatusOf(err) == http.StatusGone {
				r.mu.Lock()
				r.gone = true
				r.mu.Unlock()
				return
			}
		}
	}
}

// traffic are the step definitions that send traffic: the steps the gate holds back.
var traffic = struct {
	sync.Mutex
	ids map[string]bool
}{ids: map[string]bool{
	// axx's own: a REST request executed, a Kafka event published.
	"rest.execute": true, "kafka.event.publish": true, "kafka.event.publish.schema": true,
}}

// Traffic registers step definitions (StepDef IDs) that send traffic, for the readiness gate to
// hold back until the run is ready. A pack whose steps send registers them in its Pack function.
func Traffic(definitions ...string) {
	traffic.Lock()
	defer traffic.Unlock()
	for _, d := range definitions {
		traffic.ids[d] = true
	}
}

// sendsTraffic reports whether the step about to run sends traffic. A step whose definition the
// host does not say counts as one: better to hold it at the gate than to let traffic past it.
func sendsTraffic(st *core.StepInfo) bool {
	if st == nil || st.Definition == "" {
		return true
	}
	traffic.Lock()
	defer traffic.Unlock()
	return traffic.ids[st.Definition]
}

// beforeStep, before a step that sends traffic, runs the run's observers (which declare what
// the scenario staged by hand), then waits at the gate if the run declared anything new.
func beforeStep(sc *core.Scenario) error {
	r, ok := Current(sc)
	if !ok || !sendsTraffic(sc.Step()) {
		return nil
	}
	r.mu.Lock()
	observers := append([]func(*Run) error(nil), r.observers...)
	r.mu.Unlock()
	for _, fn := range observers {
		if err := fn(r); err != nil {
			return err
		}
	}
	return r.Ready(sc.Context())
}

// restartPatience is how long a run's closing calls keep trying an orchestrator that does not
// answer: long enough for its pod to be rescheduled and its volume to move.
var restartPatience = 2 * time.Minute

// throughRestarts calls fn until it succeeds, fails for a reason of its own, or the orchestrator
// has not answered for restartPatience. Publishing a transition again is harmless (a repeat
// changes nothing, and its notification is a claim check), so a call that may have landed before
// the connection broke can be made again.
func throughRestarts(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(restartPatience)
	wait := 500 * time.Millisecond
	for {
		err := fn()
		if err == nil || !unanswered(err) || time.Now().Add(wait).After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(wait):
		}
		wait = min(wait*2, 10*time.Second)
	}
}

// unanswered is whether an orchestrator call failed for want of an orchestrator: no answer at
// all, or a gateway's in its place.
func unanswered(err error) bool {
	switch protocol.StatusOf(err) {
	case 0, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// close ends the run with its scenario. A passed scenario's run goes through cleanup and must
// close with every dispatcher completed; a failed one's is abandoned, which cleans up too.
// Either way the journal goes into the report. Its calls to the orchestrator wait out a restart.
func (r *Run) close() error {
	r.stop()
	r.beats.Wait()
	ctx := context.Background()
	defer r.attachJournal(ctx)
	r.reportFinal(ctx)
	if r.sc.Status() != "passed" {
		err := throughRestarts(ctx, func() error { _, err := r.client.Publish(ctx, r.id, protocol.EventAbandoned); return err })
		if err != nil && protocol.StatusOf(err) != http.StatusConflict {
			r.sc.Log("stormbreaker: could not abandon run %s (its lease will expire): %v", r.id, err)
		}
		return nil
	}
	if err := r.Ready(ctx); err != nil {
		return err
	}
	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		if err := throughRestarts(ctx, func() error { _, err := r.client.Publish(ctx, r.id, ev); return err }); err != nil {
			return fmt.Errorf("run %s: publish %s: %w", r.id, ev, err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, r.settings.close_)
	defer cancel()
	for {
		run, err := r.client.Run(ctx, r.id)
		if err == nil && run.Done() {
			return verdict(run)
		}
		select {
		case <-ctx.Done():
			if run == nil {
				return fmt.Errorf("run %s did not close within %s: %w", r.id, r.settings.close_, errors.Join(err, ctx.Err()))
			}
			return core.Fail(fmt.Sprintf("run %s did not close within %s of cleanup opening", r.id, r.settings.close_),
				"every dispatcher completed", pending(run))
		case <-time.After(r.settings.poll * 5):
		}
	}
}

// verdict holds a closed run to what a clean run is: it did not fail, and every dispatcher
// instance completed its cleanup.
func verdict(run *protocol.Run) error {
	if run.Failed {
		return core.Fail(fmt.Sprintf("run %s failed", run.ID), "the run to pass", run.FailReason)
	}
	if p := pending(run); p != "" {
		return core.Fail(fmt.Sprintf("run %s closed, but not every dispatcher cleaned up", run.ID), "every dispatcher completed", p)
	}
	return nil
}

func pending(run *protocol.Run) string {
	var out []string
	for _, d := range run.Dispatchers {
		if d.State != protocol.InstanceCompleted {
			s := d.Name + "/" + d.Instance + " " + d.State
			if d.Detail != "" {
				s += " (" + d.Detail + ")"
			}
			out = append(out, s)
		}
	}
	return strings.Join(out, "; ")
}

func (r *Run) attachJournal(ctx context.Context) {
	j, err := r.client.Journal(ctx, r.id)
	if err != nil {
		r.sc.Log("stormbreaker: could not read run %s's journal: %v", r.id, err)
		return
	}
	r.sc.Attach("text/plain", []byte(j), "stormbreaker journal "+r.id)
}

func steps() []core.StepDef {
	return []core.StepDef{
		{
			ID: "stormbreaker.run.ready", Keyword: "Given", Expr: "the run is ready",
			Doc: "Wait at the run's readiness gate until every instance of every dispatcher it requires has prepared for " +
				"everything the run declared. Traffic steps wait there anyway; this says so where a scenario wants to.",
			Examples: []string{"Given the run is ready"},
			Run: func(sc *core.Scenario, _ core.Args) error {
				r, err := mustRun(sc)
				if err != nil {
					return err
				}
				r.mu.Lock()
				r.dirty = true
				r.mu.Unlock()
				return r.Ready(sc.Context())
			},
		},
		{
			ID: "stormbreaker.run.traffic-complete", Keyword: "When", Expr: "the run's traffic is complete",
			Doc: "Close the run's window now: the run's traffic is done. Checks over the run, like Prometheus's, then judge " +
				"the window it closed; without this step the window closes when the scenario ends.",
			Examples: []string{"When the run's traffic is complete"},
			Run: func(sc *core.Scenario, _ core.Args) error {
				r, err := mustRun(sc)
				if err != nil {
					return err
				}
				_, err = r.client.Publish(sc.Context(), r.id, protocol.EventTrafficComplete)
				return err
			},
		},
	}
}

func mustRun(sc *core.Scenario) (*Run, error) {
	r, ok := Current(sc)
	if !ok {
		return nil, fmt.Errorf("the scenario has no stormbreaker run: tag it @%s<group>", TagGroup)
	}
	return r, nil
}
