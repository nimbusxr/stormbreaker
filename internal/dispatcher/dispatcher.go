// Package dispatcher is the Go dispatcher's runtime: one per team app per environment, it owns
// the protocol so that a team only writes what to prepare and what to clean up. It registers
// with the orchestrator and renews its lease, receives signed notifications, drops repeats, falls
// back to the event feed, and runs a pass per run, in order and coalesced, that acts on the run's
// state: prepare rounds while the run is open, cleanup once it is in cleanup.
//
// Notifications are only doorbells: every pass reads the run from the orchestrator and acts on
// what it finds, so a missed, repeated or reordered notification costs nothing.
package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// ErrBlocked, wrapped by a hook's error, says the hook cannot succeed: the dispatcher reports
// itself blocked, and a run that requires it fails rather than waiting out its timeout. Any
// other error is reported and the hook is tried again.
var ErrBlocked = errors.New("blocked")

// Footprint walks a run's footprint a page at a time, the claims the filter keeps (the zero filter
// keeps them all); each call starts from the beginning. A hook that walks only what it derives
// from reads only that, and its round is not run again while nothing new is reported there
// (ADR 0024).
type Footprint func(ctx context.Context, only protocol.FootprintFilter) iter.Seq2[protocol.Claim, error]

// Report takes footprint entries from a hook as it goes: the dispatcher reports them in chunks of
// ReportChunk identities, so a hook never holds what it declares or removes.
type Report func(protocol.FootprintEntry) error

// ReportChunk is how many identities a footprint report from a dispatcher carries at most.
const ReportChunk = 25_000

// Hooks are what a team's dispatcher does. A nil hook does nothing: the dispatcher reports
// prepared, or completed, straight away.
type Hooks struct {
	// Prepare declares, through declare, what the team's systems will create from the footprint,
	// and may prepare for it with idempotent writes for identities already in the footprint. Its
	// declaration must be a pure function of the footprint it walks and the run, in the same
	// order each round, or prepare rounds never settle.
	Prepare func(ctx context.Context, run *protocol.Run, footprint Footprint, declare Report) error
	// Cleanup removes what is the team's and proves it, reporting through report: removed-verified
	// entries, or clean-by promises. complete says nothing is left to do. It must be idempotent.
	Cleanup func(ctx context.Context, run *protocol.Run, footprint Footprint, report Report) (complete bool, err error)
}

// reporter gathers a hook's entries by place and resolution, and reports each place's identities
// in chunks of ReportChunk, in the order the hook gave them: a round that says what the round
// before said makes the same chunks, which the orchestrator knows it has heard.
type reporter struct {
	ctx        context.Context
	d          *Dispatcher
	runID      string
	resolution string // what every entry is, for a prepare round; "" to take each entry's
	pending    map[[4]string][]string
	order      [][4]string
	grew       bool
	reports    int
}

func (d *Dispatcher) newReporter(ctx context.Context, runID, resolution string) *reporter {
	return &reporter{ctx: ctx, d: d, runID: runID, resolution: resolution, pending: map[[4]string][]string{}}
}

func (r *reporter) add(e protocol.FootprintEntry) error {
	res := e.Resolution
	if r.resolution != "" {
		res = r.resolution
	}
	k := [4]string{e.Store, e.Locator, res, e.CleanBy}
	if _, ok := r.pending[k]; !ok {
		r.order = append(r.order, k)
	}
	for _, id := range e.Identities {
		r.pending[k] = append(r.pending[k], id)
		if len(r.pending[k]) == ReportChunk {
			if err := r.send(k); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *reporter) send(k [4]string) error {
	ids := r.pending[k]
	if len(ids) == 0 {
		return nil
	}
	res, err := r.d.client.ReportFootprint(r.ctx, r.runID, r.d.cfg.Name, r.d.cfg.Instance, protocol.FootprintReport{Entries: []protocol.FootprintEntry{{
		Store: k[0], Locator: k[1], Identities: ids, Resolution: k[2], CleanBy: k[3],
	}}})
	if err != nil {
		return fmt.Errorf("report the footprint: %w", err)
	}
	r.grew = r.grew || res.Grew
	r.reports++
	r.pending[k] = ids[:0:0]
	return nil
}

// flush reports what is left, place by place in the order the hook first named them.
func (r *reporter) flush() error {
	for _, k := range r.order {
		if err := r.send(k); err != nil {
			return err
		}
	}
	return nil
}

// Config is how a dispatcher runs.
type Config struct {
	// Name is the dispatcher's, e.g. "tracking"; Instance is this instance's, e.g. its pod's.
	Name, Instance, Team string
	// CallbackURL is where the orchestrator reaches this instance's /events.
	CallbackURL string
	// Secret signs the notifications sent to this instance: "whsec_" and the key in base64. Empty
	// makes a fresh one at start.
	Secret string
	// Orchestrator is the orchestrator's URL, and Token its bearer token if it asks for one.
	Orchestrator, Token string
	// Groups are the groups the dispatcher joins.
	Groups []string
	// Addr is where it listens.
	Addr string
	// Reassert is how often it renews its lease, reads the event feed and goes over its runs
	// again, reporting afresh. Renewing has a clock of its own, so a slow feed never holds a
	// renewal up past the lease.
	Reassert time.Duration
	// Logger, nil for slog's default.
	Logger *slog.Logger
	// HTTP calls the orchestrator; nil for a client with a 30 second timeout.
	HTTP *http.Client
}

// Dispatcher is a running dispatcher.
type Dispatcher struct {
	cfg    Config
	hooks  Hooks
	client *protocol.Client
	log    *slog.Logger
	now    func() time.Time
	passes *passes
	seen   *seen

	mu         sync.Mutex
	registered bool
	joined     bool // it has joined its groups since it started
	renewals   int
	cursor     int64
	tracked    map[string]bool // the runs it is working on: it goes over them again on every sweep
	settled    map[string]round
}

// round is a round that settled: prepared, or cleanup completed. Until something is reported
// where its hook read, another round would only find the same, so the dispatcher says the same
// again instead of running one: at sixty million claims, a round is a walk of them all.
type round struct {
	state   string // the run's state the round was for
	inputs  string // the rest of what the hook saw of the run (inputs)
	version int64  // the footprint version it began from
	status  protocol.Status
	read    []walk // what its hook walked
}

// walk is one walk of the footprint a hook made: its filter, and the report it read up to.
type walk struct {
	only  protocol.FootprintFilter
	until int64
}

// reads records a round's walks. A hook may walk from several goroutines.
type reads struct {
	mu    sync.Mutex
	walks []walk
}

// add records a walk. Walks with the same filter keep the earliest end: whatever was reported
// past it, that walk did not see.
func (r *reads) add(only protocol.FootprintFilter, until int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, w := range r.walks {
		if slices.Equal(w.only.Dispatchers, only.Dispatchers) && w.only.Store == only.Store && w.only.Locator == only.Locator {
			r.walks[i].until = min(w.until, until)
			return
		}
	}
	r.walks = append(r.walks, walk{only: only, until: until})
}

func (r *reads) all() []walk {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.walks)
}

// inputs is what a hook sees of a run besides its footprint. A round settled on other inputs
// is no answer for these.
func inputs(run *protocol.Run) string {
	return fmt.Sprint(run.Streams, run.WindowFrom.UnixNano(), run.WindowTo, run.Failed, run.Abandoned)
}

// New returns a dispatcher. It does nothing until Run.
func New(cfg Config, hooks Hooks) (*Dispatcher, error) {
	switch {
	case cfg.Name == "", cfg.Instance == "", cfg.Team == "":
		return nil, errors.New("a dispatcher needs a name, an instance and a team")
	case cfg.CallbackURL == "":
		return nil, errors.New("a dispatcher needs the callback URL the orchestrator reaches it at")
	case cfg.Orchestrator == "":
		return nil, errors.New("a dispatcher needs the orchestrator's URL")
	case cfg.Name == protocol.Suite:
		return nil, fmt.Errorf("%q is reserved for the suite", protocol.Suite)
	}
	if cfg.Secret == "" {
		s, err := protocol.NewSecret()
		if err != nil {
			return nil, err
		}
		cfg.Secret = s
	} else if err := protocol.CheckSecret(cfg.Secret); err != nil {
		return nil, err
	}
	if cfg.Reassert <= 0 {
		cfg.Reassert = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	client := protocol.NewClient(cfg.Orchestrator, cfg.Token)
	if cfg.HTTP != nil {
		client.HTTP = cfg.HTTP
	} else {
		client.HTTP = &http.Client{Timeout: 30 * time.Second, Transport: protocol.Pooled(16)}
	}
	return &Dispatcher{
		cfg: cfg, hooks: hooks, client: client, log: cfg.Logger.With("dispatcher", cfg.Name, "instance", cfg.Instance),
		now: time.Now, passes: newPasses(), seen: newSeen(10_000), tracked: map[string]bool{},
		settled: map[string]round{},
	}, nil
}

// Handler serves the dispatcher's endpoints: POST /events for notifications, and GET /healthz.
func (d *Dispatcher) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /events", d.receive)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		d.mu.Lock()
		registered := d.registered
		d.mu.Unlock()
		fmt.Fprintf(w, `{"status":"ok","dispatcher":%q,"instance":%q,"registered":%t}`+"\n", d.cfg.Name, d.cfg.Instance, registered)
	})
	return mux
}

// receive takes a notification: checks its signature, drops a repeat, answers at once, and goes
// to look at the run.
func (d *Dispatcher) receive(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "cannot read the notification", http.StatusBadRequest)
		return
	}
	if err := protocol.Verify(r.Header, d.cfg.Secret, body, d.now()); err != nil {
		d.log.Warn("refused a notification", "err", err)
		http.Error(w, "the signature does not match", http.StatusUnauthorized)
		return
	}
	var ev protocol.Event
	if err := jsonUnmarshal(body, &ev); err != nil || ev.RunID == "" {
		http.Error(w, "a notification is an event with a runId", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
	if !d.seen.add(ev.ID) {
		return
	}
	d.log.Info("notified", "event", ev.ID, "run", ev.RunID, "type", ev.Type)
	d.Touch(ev.RunID)
}

// Touch goes to look at a run: a pass over it, unless one is already waiting to run.
func (d *Dispatcher) Touch(runID string) {
	if runID == "" {
		return
	}
	d.passes.submit(runID, func() { d.process(context.Background(), runID) })
}

// Run registers the dispatcher and keeps it going until ctx is done: it renews its lease, reads
// the event feed for what notifications missed, and goes over every run it is working on, every
// Reassert. The renewal goes on its own clock, each attempt bounded by Reassert: an orchestrator
// slow to answer the feed must not let the lease lapse, or every run that requires this instance
// fails as unreachable.
func (d *Dispatcher) Run(ctx context.Context) {
	d.renew(ctx)
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		t := time.NewTicker(d.cfg.Reassert)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				d.renew(ctx)
			}
		}
	}()
	d.sweep(ctx)
	t := time.NewTicker(d.cfg.Reassert)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			<-renewed
			d.passes.wait()
			return
		case <-t.C:
			d.sweep(ctx)
		}
	}
}

// renew registers again, which renews this instance's lease, within Reassert: an instance that
// stops doing it ages out, so a replica that scaled away holds no run up.
func (d *Dispatcher) renew(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.Reassert)
	defer cancel()
	if err := d.register(ctx); err != nil {
		if ctx.Err() == nil || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			d.log.Warn("cannot register with the orchestrator", "err", err)
		}
		return
	}
	d.mu.Lock()
	if !d.registered {
		d.log.Info("registered", "orchestrator", d.cfg.Orchestrator, "groups", strings.Join(d.cfg.Groups, ","))
	}
	d.registered = true
	d.mu.Unlock()
}

// rejoinEvery is how many renewals go by between joining the groups again: membership is the
// dispatcher's, not the instance's, and it outlives a renewal, so every renewal joining again
// would double the orchestrator's writes for nothing. Joining again now and then puts it back
// if the orchestrator lost it.
const rejoinEvery = 6

func (d *Dispatcher) register(ctx context.Context) error {
	if _, err := d.client.Register(ctx, protocol.Registration{
		Name: d.cfg.Name, Instance: d.cfg.Instance, Team: d.cfg.Team, CallbackURL: d.cfg.CallbackURL, Secret: d.cfg.Secret,
	}); err != nil {
		return err
	}
	d.mu.Lock()
	join := !d.joined || d.renewals%rejoinEvery == 0
	d.renewals++
	d.mu.Unlock()
	if !join {
		return nil
	}
	for _, g := range d.cfg.Groups {
		if err := d.client.JoinGroup(ctx, g, d.cfg.Name); err != nil {
			return err
		}
	}
	d.mu.Lock()
	d.joined = true
	d.mu.Unlock()
	return nil
}

// sweep is the event-feed backstop and the cadence of fresh reports: it reads the feed from
// where it left off, as far as it gets within Reassert, and goes over every run it saw there and
// every run it is working on.
func (d *Dispatcher) sweep(ctx context.Context) {
	d.mu.Lock()
	registered, cursor := d.registered, d.cursor
	d.mu.Unlock()
	if !registered {
		return
	}
	feedCtx, cancel := context.WithTimeout(ctx, d.cfg.Reassert)
	defer cancel()

	// One pass per run, not one per event, however many events a page has for it. The feed is its
	// groups' alone, and a dispatcher that has just started reads it from their unfinished runs, as
	// fast as the orchestrator answers: it does not wait sweeps to find a run it was working on.
	touched := map[string]bool{}
	var order []string
	for {
		feed, err := d.client.Events(feedCtx, cursor, 1000, d.cfg.Groups...)
		if err != nil {
			if ctx.Err() == nil {
				d.log.Warn("cannot read the event feed", "err", err)
			}
			break
		}
		for _, ev := range feed.Events {
			if !touched[ev.RunID] {
				touched[ev.RunID] = true
				order = append(order, ev.RunID)
			}
		}
		caughtUp := feed.Cursor == cursor || len(feed.Events) < 1000
		cursor = feed.Cursor
		if caughtUp {
			break
		}
	}
	d.mu.Lock()
	d.cursor = cursor
	for id := range d.tracked {
		if !touched[id] {
			order = append(order, id)
		}
	}
	d.mu.Unlock()
	for _, id := range order {
		d.Touch(id)
	}
}

func (d *Dispatcher) track(runID string) {
	d.mu.Lock()
	d.tracked[runID] = true
	d.mu.Unlock()
}

func (d *Dispatcher) forget(runID string) {
	d.mu.Lock()
	delete(d.tracked, runID)
	delete(d.settled, runID)
	d.mu.Unlock()
	d.passes.forget(runID)
}

// process is one pass over a run: read it, and act on its state.
func (d *Dispatcher) process(ctx context.Context, runID string) {
	run, err := d.client.Run(ctx, runID)
	if err != nil {
		if protocol.StatusOf(err) == http.StatusNotFound {
			d.forget(runID)
			return
		}
		d.log.Warn("cannot read the run", "run", runID, "err", err)
		return
	}
	if !d.onRun(run) {
		d.forget(runID)
		return
	}
	switch run.State {
	case protocol.StateOpened:
		d.prepare(ctx, run)
	case protocol.StateCleanup:
		// A failed or abandoned run is cleaned up all the same: the mess is still ours.
		d.cleanup(ctx, run)
	case protocol.StateClosed:
		d.forget(runID)
	default:
		d.track(runID)
	}
}

func (d *Dispatcher) onRun(run *protocol.Run) bool {
	for _, rd := range run.Dispatchers {
		if rd.Name == d.cfg.Name && rd.Instance == d.cfg.Instance {
			return true
		}
	}
	return false
}

// footprint is the hooks' walk of a run's footprint, a page at a time, recording each walk in rec.
func (d *Dispatcher) footprint(runID string, rec *reads) Footprint {
	return func(ctx context.Context, only protocol.FootprintFilter) iter.Seq2[protocol.Claim, error] {
		return func(yield func(protocol.Claim, error) bool) {
			var after, until int64
			for first := true; ; first = false {
				page, err := d.client.FootprintPage(ctx, runID, after, until, 64, only)
				if err != nil {
					yield(protocol.Claim{}, err)
					return
				}
				if first {
					rec.add(only, page.Until)
				}
				for _, r := range page.Reports {
					c := protocol.Claim{Dispatcher: r.Dispatcher, Instance: r.Instance, Store: r.Store, Locator: r.Locator, Resolution: r.Resolution, CleanBy: r.CleanBy}
					for _, id := range r.Identities {
						c.Identity = id
						if !yield(c, nil) {
							return
						}
					}
				}
				if page.Next == nil {
					return
				}
				after, until = *page.Next, page.Until
			}
		}
	}
}

// unread says whether something was reported, since a round, where its hook read: a report past
// the end of one of its walks, that the walk's filter keeps. One small request a walk answers
// it; an error says yes, and the round runs.
func (d *Dispatcher) unread(ctx context.Context, runID string, last round) bool {
	for _, w := range last.read {
		page, err := d.client.FootprintPage(ctx, runID, w.until, 0, 1, w.only)
		if err != nil || len(page.Reports) > 0 {
			return true
		}
	}
	return false
}

func (d *Dispatcher) report(ctx context.Context, runID string, s protocol.Status) {
	if err := d.client.ReportStatus(ctx, runID, d.cfg.Name, d.cfg.Instance, s); err != nil {
		d.log.Warn("cannot report status", "run", runID, "status", s.State, "err", err)
	}
}

// still says again what the last round on run said, if it settled and nothing has been reported
// since where its hook read, and reports whether it did. Saying it again costs a request or two,
// and puts back an instance the orchestrator stopped counting on.
//
// A footprint that grew elsewhere (another team's declarations, the instance's own) changes
// nothing the hook saw, and its declaration is a pure function of what it saw: the same round
// would declare the same, so the instance settles on the footprint's new version without one.
func (d *Dispatcher) still(ctx context.Context, run *protocol.Run) bool {
	d.mu.Lock()
	last, ok := d.settled[run.ID]
	d.mu.Unlock()
	if !ok || last.state != run.State || last.inputs != inputs(run) {
		return false
	}
	if last.version != run.FootprintVersion {
		if d.unread(ctx, run.ID, last) {
			return false
		}
		last.version, last.status.FootprintVersion = run.FootprintVersion, run.FootprintVersion
		last.status.Detail = "nothing new where the round read; settled again"
		d.mu.Lock()
		d.settled[run.ID] = last
		d.mu.Unlock()
	}
	d.report(ctx, run.ID, last.status)
	return true
}

// settle reports a round's outcome. One that settled is remembered, with what its hook read;
// one that did not leaves nothing to say again, so the next pass runs another round.
func (d *Dispatcher) settle(ctx context.Context, run *protocol.Run, s protocol.Status, read *reads) {
	s.FootprintVersion = run.FootprintVersion
	d.mu.Lock()
	if s.State == protocol.StatusPrepared || s.State == protocol.StatusCompleted {
		d.settled[run.ID] = round{state: run.State, inputs: inputs(run), version: run.FootprintVersion, status: s, read: read.all()}
	} else {
		delete(d.settled, run.ID)
	}
	d.mu.Unlock()
	d.report(ctx, run.ID, s)
}

// working reports the instance in progress, every Reassert, until the returned stop: a round
// over millions of claims outlasts the orchestrator's patience with an instance that has
// acknowledged a run and gone quiet. A round shorter than Reassert says nothing extra.
func (d *Dispatcher) working(ctx context.Context, runID, hook string) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	began := d.now()
	go func() {
		defer close(done)
		t := time.NewTicker(d.cfg.Reassert)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				detail := hook + " round under way"
				if took := d.now().Sub(began).Round(time.Second); took >= time.Second {
					detail += ", " + took.String() + " so far"
				}
				d.report(ctx, runID, protocol.Status{State: protocol.StatusInProgress, Detail: detail})
			}
		}
	}()
	return func() { cancel(); <-done }
}

// hookFailed reports a hook's error: blocked when the hook says it cannot succeed, else in
// progress, to be tried again on the next pass.
func (d *Dispatcher) hookFailed(ctx context.Context, run *protocol.Run, hook string, err error) {
	d.log.Warn("hook failed", "run", run.ID, "hook", hook, "err", err)
	if errors.Is(err, ErrBlocked) {
		d.settle(ctx, run, protocol.Status{State: protocol.StatusBlocked, Detail: hook + ": " + err.Error()}, nil)
		return
	}
	d.settle(ctx, run, protocol.Status{State: protocol.StatusInProgress, Detail: hook + " failed, trying again: " + err.Error()}, nil)
}

// prepare runs a prepare round: the hook declares, the dispatcher reports it in chunks, and a
// round whose every chunk the orchestrator had heard before adds nothing, and settles the
// instance as prepared on the footprint version the round began from. The dispatcher keeps
// nothing of what was declared: the orchestrator knows what each party has said.
func (d *Dispatcher) prepare(ctx context.Context, run *protocol.Run) {
	d.track(run.ID)
	if d.still(ctx, run) {
		return
	}
	if d.hooks.Prepare == nil {
		d.settle(ctx, run, protocol.Status{State: protocol.StatusPrepared, Detail: "no prepare hook; nothing to declare"}, &reads{})
		return
	}
	stop := d.working(ctx, run.ID, "prepare")
	r := d.newReporter(ctx, run.ID, protocol.ResolutionExpected)
	read := &reads{}
	err := d.hooks.Prepare(ctx, run, d.footprint(run.ID, read), func(e protocol.FootprintEntry) error { return r.add(e) })
	if err == nil {
		err = r.flush()
	}
	stop()
	if err != nil {
		d.hookFailed(ctx, run, "prepare", err)
		return
	}
	if !r.grew {
		d.settle(ctx, run, protocol.Status{State: protocol.StatusPrepared, Detail: "a prepare round added nothing; settled"}, read)
		return
	}
	d.settle(ctx, run, protocol.Status{State: protocol.StatusPartial, Detail: fmt.Sprintf("declared something new, in %d %s", r.reports, plural(r.reports, "report", "reports"))}, nil)
}

// cleanup runs a cleanup round: the hook removes what is the team's and proves it, and the
// dispatcher reports what it claims, in chunks, and whether it is done.
func (d *Dispatcher) cleanup(ctx context.Context, run *protocol.Run) {
	d.track(run.ID)
	if d.still(ctx, run) {
		return
	}
	if d.hooks.Cleanup == nil {
		d.settle(ctx, run, protocol.Status{State: protocol.StatusCompleted, Evidence: "no cleanup hook; nothing to remove"}, &reads{})
		return
	}
	stop := d.working(ctx, run.ID, "cleanup")
	r := d.newReporter(ctx, run.ID, "")
	read := &reads{}
	complete, err := d.hooks.Cleanup(ctx, run, d.footprint(run.ID, read), func(e protocol.FootprintEntry) error { return r.add(e) })
	if err == nil {
		err = r.flush()
	}
	stop()
	if err != nil {
		d.hookFailed(ctx, run, "cleanup", err)
		return
	}
	if !complete {
		d.settle(ctx, run, protocol.Status{State: protocol.StatusPartial, Detail: "cleanup in progress"}, nil)
		return
	}
	d.settle(ctx, run, protocol.Status{State: protocol.StatusCompleted, Evidence: "cleanup verified at " + d.now().UTC().Format(time.RFC3339)}, read)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Serve runs a dispatcher and its endpoints until ctx is done.
func Serve(ctx context.Context, cfg Config, hooks Hooks) error {
	d, err := New(cfg, hooks)
	if err != nil {
		return err
	}
	addr := cfg.Addr
	if addr == "" {
		addr = ":8090"
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: d.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	d.log.Info("dispatcher serving", "addr", ln.Addr().String(), "callback", cfg.CallbackURL)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx)
	}()
	select {
	case err := <-errc:
		cancel()
		<-done
		return err
	case <-ctx.Done():
	}
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	err = srv.Shutdown(shutdown)
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}
