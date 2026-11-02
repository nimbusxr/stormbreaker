// Package sdk is the dispatcher-sdk for Go: what a team writes to take part in stormbreaker runs.
//
// A dispatcher runs next to a team's app, one per app per environment. It prepares the team's
// systems before a run's traffic, and cleans up what the run created after it. The SDK owns the
// protocol (registering with the orchestrator, signed notifications, leases, prepare rounds,
// status reports); the team writes two hooks:
//
//	func main() {
//		sdk.Main(sdk.Hooks{
//			Prepare: func(ctx context.Context, run *sdk.Run, declare func(sdk.Entry) error) error { … },
//			Cleanup: func(ctx context.Context, run *sdk.Run, remove func(sdk.Removal) error) (bool, error) { … },
//		})
//	}
//
// Both hooks read the run's footprint, the identities claimed so far, with Run.Footprint or
// Run.Identities, a page at a time, and report what they declare or remove as they go: the SDK
// sends it in chunks, so a hook holds neither, however large the run.
//
// A hook that walks only what it derives from (From, At) reads only that, and is not run again
// while nothing new is reported there:
//
//	for c, err := range run.Footprint(ctx, sdk.From(sdk.Suite), sdk.At("parcels", "/parcels")) { … }
package sdk

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"iter"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/protocol"
)

// ErrBlocked, wrapped by a hook's error, says the hook cannot succeed: the dispatcher reports
// itself blocked, and a run that requires it fails at once rather than waiting out its readiness
// timeout. Any other error is reported, and the hook is tried again on the next pass.
//
//	return fmt.Errorf("%w: the admin API refuses our credentials", sdk.ErrBlocked)
var ErrBlocked = dispatcher.ErrBlocked

// Claim is one identity at one place in a store, as a dispatcher instance (or the suite) claims it.
type Claim = protocol.Claim

// Suite is the name the suite's own claims are made under: the identities its streams create.
const Suite = protocol.Suite

// Only narrows a walk of the footprint to what a hook derives from. The orchestrator reads and
// sends only the claims it keeps, and a round that read only narrowed walks is not run again
// while nothing new is reported where it read: its declaration, a pure function of what it read,
// would be the same.
type Only func(*protocol.FootprintFilter)

// From keeps the claims of the named dispatchers; Suite is the suite's.
func From(dispatchers ...string) Only {
	return func(f *protocol.FootprintFilter) { f.Dispatchers = append(f.Dispatchers, dispatchers...) }
}

// At keeps the claims at one place: a store, and where in it. An empty locator keeps the whole
// store.
func At(store, locator string) Only {
	return func(f *protocol.FootprintFilter) { f.Store, f.Locator = store, locator }
}

func filter(only []Only) protocol.FootprintFilter {
	var f protocol.FootprintFilter
	for _, o := range only {
		o(&f)
	}
	return f
}

// Run is a run as the hooks see it.
type Run struct {
	ID    string
	Group string
	// Streams are the streams the run generates records from, with their seeds.
	Streams []protocol.StreamRef
	// WindowFrom and WindowTo bound the run's traffic; WindowTo is nil until the traffic is done.
	WindowFrom time.Time
	WindowTo   *time.Time
	// Failed and Abandoned say why a run is being cleaned up early.
	Failed, Abandoned bool

	footprint dispatcher.Footprint
	self      string // the dispatcher's name
}

// Footprint walks the run's footprint, every claim in the order it was made (or only those
// From and At keep), a page at a time: nothing holds it whole, however large the run. Each call
// starts from the beginning. A claim can appear more than once.
func (r *Run) Footprint(ctx context.Context, only ...Only) iter.Seq2[Claim, error] {
	return r.footprint(ctx, filter(only))
}

// Identities walks the identities claimed at one place, a page at a time, holding none of them:
// an identity claimed more than once there (by several instances, or reported again) comes more
// than once, so what a hook does with each must be idempotent, as it must be anyway.
//
// It reads only that place's claims (At), and narrowed further by only (From).
func (r *Run) Identities(ctx context.Context, store, locator string, only ...Only) iter.Seq2[string, error] {
	f := filter(only)
	f.Store, f.Locator = store, locator
	return func(yield func(string, error) bool) {
		for c, err := range r.footprint(ctx, f) {
			if err != nil {
				yield("", err)
				return
			}
			if !yield(c.Identity, nil) {
				return
			}
		}
	}
}

// Unanswered walks what this dispatcher still has to answer for at one place: the identities it
// declared there that no instance of it has yet reported removed-verified or clean-by, each once,
// in the order they were first declared. A cleanup that works from it after a restart, or on its
// next pass, does neither again what it already did nor report it twice: what the dispatcher has
// done is in the footprint, not in its memory.
//
// It holds the place's identities while it walks, a few tens of bytes each: work from
// Identities, which holds none, where a place holds more than memory allows.
func (r *Run) Unanswered(ctx context.Context, store, locator string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		answered := map[string]bool{}
		var order []string
		for c, err := range r.footprint(ctx, protocol.FootprintFilter{Dispatchers: []string{r.self}, Store: store, Locator: locator}) {
			if err != nil {
				yield("", err)
				return
			}
			if _, seen := answered[c.Identity]; !seen {
				order = append(order, c.Identity)
			}
			// The latest statement stands: a removal answers a declaration, and a declaration
			// made again after it is open once more.
			answered[c.Identity] = c.Resolution != protocol.ResolutionExpected
		}
		for _, id := range order {
			if !answered[id] && !yield(id, nil) {
				return
			}
		}
	}
}

// Entry is identities at one place: a store, and where in it.
type Entry struct {
	Store, Locator string
	Identities     []string
}

// Removal is identities a cleanup hook removed and checked gone, or, with CleanBy (a date,
// YYYY-MM-DD), promises gone by then: a store where removing is not possible now (a compacted
// topic, an aggregate) answers with when it will be clean.
type Removal struct {
	Store, Locator string
	Identities     []string
	CleanBy        string
}

// Hooks are what a team's dispatcher does. A nil hook does nothing: the dispatcher reports
// prepared, or completed, straight away.
type Hooks struct {
	// Prepare declares, through declare, what the team's systems will create from the run's
	// footprint, and may prepare for it with idempotent writes for identities already in the
	// footprint. Call declare as often as you like, with as many identities as you like.
	//
	// The declaration must be a pure function of the footprint, made in the same order each
	// time: rounds run until one declares nothing the run has not heard, so minting new
	// identities in reaction to its own declarations never settles.
	Prepare func(ctx context.Context, run *Run, declare func(Entry) error) error
	// Cleanup removes what is the team's (and only what the team's systems created), proves it,
	// and reports it through remove as it goes. It returns whether nothing is left to do; until
	// then it runs again on each pass. It must be idempotent: it runs again whenever the
	// footprint grows during cleanup.
	Cleanup func(ctx context.Context, run *Run, remove func(Removal) error) (complete bool, err error)
}

func view(run *protocol.Run, fp dispatcher.Footprint, self string) *Run {
	return &Run{
		ID: run.ID, Group: run.Group, Streams: run.Streams, WindowFrom: run.WindowFrom, WindowTo: run.WindowTo,
		Failed: run.Failed, Abandoned: run.Abandoned, footprint: fp, self: self,
	}
}

// runtime adapts the hooks to the runtime's, for the dispatcher named self.
func (h Hooks) runtime(self string) dispatcher.Hooks {
	var rt dispatcher.Hooks
	if h.Prepare != nil {
		rt.Prepare = func(ctx context.Context, run *protocol.Run, fp dispatcher.Footprint, declare dispatcher.Report) error {
			return h.Prepare(ctx, view(run, fp, self), func(e Entry) error {
				return declare(protocol.FootprintEntry{Store: e.Store, Locator: e.Locator, Identities: e.Identities, Resolution: protocol.ResolutionExpected})
			})
		}
	}
	if h.Cleanup != nil {
		rt.Cleanup = func(ctx context.Context, run *protocol.Run, fp dispatcher.Footprint, report dispatcher.Report) (bool, error) {
			return h.Cleanup(ctx, view(run, fp, self), func(r Removal) error {
				e := protocol.FootprintEntry{Store: r.Store, Locator: r.Locator, Identities: r.Identities, Resolution: protocol.ResolutionRemovedVerified}
				if r.CleanBy != "" {
					if _, err := time.Parse(time.DateOnly, r.CleanBy); err != nil {
						return fmt.Errorf("%w: a removal's CleanBy is a date (YYYY-MM-DD), not %q", ErrBlocked, r.CleanBy)
					}
					e.Resolution, e.CleanBy = protocol.ResolutionCleanBy, r.CleanBy
				}
				return report(e)
			})
		}
	}
	return rt
}

// Config is how a dispatcher runs. ConfigFromEnv reads it from the environment.
type Config struct {
	// Name is the dispatcher's, e.g. "tracking"; Instance is this instance's, stable across
	// restarts: its region or partition, never its pod's.
	Name, Instance, Team string
	// CallbackURL is where the orchestrator reaches this instance: its address and /events.
	CallbackURL string
	// Secret signs notifications: "whsec_" and the key in base64. Empty makes a fresh one.
	Secret string
	// Orchestrator is the orchestrator's URL; Token its bearer token, if it asks for one.
	Orchestrator, Token string
	// Groups are the groups the dispatcher joins.
	Groups []string
	// Addr is where it listens (":8090").
	Addr string
	// Reassert is how often it renews its lease and reports afresh (10s).
	Reassert time.Duration
	// Logger, nil for JSON on standard error.
	Logger *slog.Logger
}

// ConfigFromEnv reads a dispatcher's configuration from the environment:
//
//	STORMBREAKER_DISPATCHER      the dispatcher's name (required)
//	STORMBREAKER_INSTANCE        this instance's name, stable across restarts and rollouts (required): its region or partition
//	STORMBREAKER_TEAM            the team that owns it (required)
//	STORMBREAKER_CALLBACK_URL    where the orchestrator reaches it (required), e.g. http://tracking-dispatcher:8090/events
//	STORMBREAKER_SECRET          the notifications' signing secret (default: a fresh one)
//	STORMBREAKER_ORCHESTRATOR    the orchestrator's URL (required)
//	STORMBREAKER_TOKEN           the orchestrator's bearer token
//	STORMBREAKER_GROUPS          the groups to join, comma-separated
//	STORMBREAKER_ADDR            where to listen (default :8090)
//	STORMBREAKER_REASSERT        how often to report afresh (default 10s)
func ConfigFromEnv() (Config, error) {
	get := os.Getenv
	c := Config{
		Name: get("STORMBREAKER_DISPATCHER"), Instance: get("STORMBREAKER_INSTANCE"), Team: get("STORMBREAKER_TEAM"),
		CallbackURL: get("STORMBREAKER_CALLBACK_URL"), Secret: get("STORMBREAKER_SECRET"),
		Orchestrator: get("STORMBREAKER_ORCHESTRATOR"), Token: get("STORMBREAKER_TOKEN"), Addr: get("STORMBREAKER_ADDR"),
	}
	for _, g := range strings.Split(get("STORMBREAKER_GROUPS"), ",") {
		if g = strings.TrimSpace(g); g != "" {
			c.Groups = append(c.Groups, g)
		}
	}
	if s := get("STORMBREAKER_REASSERT"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return c, fmt.Errorf("STORMBREAKER_REASSERT: %w", err)
		}
		c.Reassert = d
	}
	var missing []string
	for name, v := range map[string]string{
		"STORMBREAKER_DISPATCHER": c.Name, "STORMBREAKER_INSTANCE": c.Instance, "STORMBREAKER_TEAM": c.Team,
		"STORMBREAKER_CALLBACK_URL": c.CallbackURL, "STORMBREAKER_ORCHESTRATOR": c.Orchestrator,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		err := fmt.Errorf("set %s", strings.Join(missing, ", "))
		if c.Instance == "" {
			// An instance named for its pod is not on the runs opened before it started: a rollout
			// mid-run fails the run, and leaves that instance's data behind (ADR 0021).
			err = fmt.Errorf("%w (STORMBREAKER_INSTANCE is this instance's stable name, such as its region or partition: never its pod's)", err)
		}
		return c, err
	}
	return c, nil
}

// Serve runs a dispatcher with hooks until ctx is done.
func Serve(ctx context.Context, cfg Config, hooks Hooks) error {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	return dispatcher.Serve(ctx, dispatcher.Config{
		Name: cfg.Name, Instance: cfg.Instance, Team: cfg.Team, CallbackURL: cfg.CallbackURL, Secret: cfg.Secret,
		Orchestrator: cfg.Orchestrator, Token: cfg.Token, Groups: cfg.Groups, Addr: cfg.Addr, Reassert: cfg.Reassert,
		Logger: cfg.Logger,
	}, hooks.runtime(cfg.Name))
}

// Main runs a dispatcher with hooks, configured from the environment (see ConfigFromEnv), until
// it is interrupted or terminated, and exits.
func Main(hooks Hooks) {
	flag.Parse()
	cfg, err := ConfigFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatcher:", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = Serve(ctx, cfg, hooks)
	stop()
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "dispatcher:", err)
		os.Exit(1)
	}
}
