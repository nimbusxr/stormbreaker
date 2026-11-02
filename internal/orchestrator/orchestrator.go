// Package orchestrator is the stormbreaker orchestrator: one service per shared environment
// that coordinates runs. It keeps the registry of dispatchers and their leases, each run's
// readiness gate, its footprint (the append-only claims of what the run created) and its
// journal, and it notifies dispatchers of what happens to the runs of their groups.
//
// The orchestrator never blocks traffic itself: the readiness gate is a fact the suite reads.
// Every transition is written before it is fanned out, and every phase is idempotent.
package orchestrator

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// Config is how an orchestrator runs.
type Config struct {
	// DB is the SQLite database's path.
	DB string
	// Token is a bearer token with every grant: the lab's, or a lone team's. Empty asks for none,
	// unless there are Principals.
	Token string
	// Principals are the credentials the API accepts, each with its grants (ADR 0019).
	Principals []Principal
	// Lease is how long a suite's hold on a run, and a dispatcher instance's registration, last
	// without being renewed.
	Lease time.Duration
	// ReadinessTimeout is how long a run's required dispatchers have to prepare.
	ReadinessTimeout time.Duration
	// StaleAfter is how long a required instance that acknowledged a run may stay silent before
	// preparing.
	StaleAfter time.Duration
	// FanoutInterval is how often due notifications are sent.
	FanoutInterval time.Duration
	// FanoutWorkers is how many notifications are sent at once.
	FanoutWorkers int
	// DeliveryTimeout bounds one notification's request.
	DeliveryTimeout time.Duration
	// RetryBase and RetryMax bound the exponential backoff between attempts to deliver a
	// notification; MaxAttempts attempts make an instance unreachable for that run.
	RetryBase   time.Duration
	RetryMax    time.Duration
	MaxAttempts int
	// Retention is how long closed runs are kept.
	Retention time.Duration
	// CleanupHold is how long a run in cleanup keeps holding its group's dispatchers. A cleanup
	// that outlasts it goes on, but other runs may open beside it (ADR 0015).
	CleanupHold time.Duration
	// CleanupDeadline is how long a run may stay in cleanup. Past it, the run closes unfinished,
	// naming the instances that never completed (ADR 0023).
	CleanupDeadline time.Duration
	// FootprintRetention is how long a closed run keeps its identities; its journal and its
	// counts by place stay for Retention. It is at most Retention (ADR 0016).
	FootprintRetention time.Duration
	// DiskReserve is the room, in bytes, the orchestrator keeps on its volume: below it, it takes
	// no new runs and no declarations, so that a full disk never breaks a run halfway. Removals,
	// statuses and closing still go through. Zero keeps none (ADR 0016).
	DiskReserve int64
}

func (c Config) withDefaults() Config {
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	if c.DB == "" {
		c.DB = "orchestrator.db"
	}
	def(&c.Lease, 30*time.Second)
	def(&c.ReadinessTimeout, 120*time.Second)
	def(&c.StaleAfter, 30*time.Second)
	def(&c.FanoutInterval, 100*time.Millisecond)
	def(&c.DeliveryTimeout, 3*time.Second)
	def(&c.RetryBase, 500*time.Millisecond)
	def(&c.RetryMax, 10*time.Second)
	def(&c.Retention, 14*24*time.Hour)
	def(&c.CleanupHold, 15*time.Minute)
	def(&c.CleanupDeadline, 24*time.Hour)
	def(&c.FootprintRetention, 72*time.Hour)
	c.FootprintRetention = min(c.FootprintRetention, c.Retention)
	if c.DiskReserve == 0 {
		c.DiskReserve = 1 << 30
	}
	if c.FanoutWorkers <= 0 {
		c.FanoutWorkers = 16
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 10
	}
	return c
}

// Orchestrator is a running orchestrator.
type Orchestrator struct {
	cfg Config
	db  *sql.DB // the writer: one connection; every write is a transaction on it
	ro  *sql.DB // the readers: a pool that never waits for the writer
	cp  *sql.DB // the checkpointer: one connection, beside the writer
	// writes counts the writer's transactions, and the time spent in them and waiting for it.
	writes struct{ n, busy, wait atomic.Int64 }
	// leases are the dispatcher instances' renewals not yet written (ADR 0025).
	leases leaseBook
	// progress is the latest each run's suite said about how it is going (ADR 0027).
	progress progressBook
	log      *slog.Logger
	http     *http.Client
	now      func() time.Time
	// started is when this process began serving. While it was down, nobody could renew a
	// lease or report, so for one lease after it no silence counts against anyone.
	started time.Time
}

// Option configures an Orchestrator.
type Option func(*Orchestrator)

// WithLogger sets where the orchestrator logs.
func WithLogger(l *slog.Logger) Option { return func(o *Orchestrator) { o.log = l } }

// WithClock sets the orchestrator's clock (tests).
func WithClock(now func() time.Time) Option { return func(o *Orchestrator) { o.now = now } }

// WithHTTPClient sets the client notifications are sent with.
func WithHTTPClient(c *http.Client) Option { return func(o *Orchestrator) { o.http = c } }

// Open opens an orchestrator on its database, creating the database or bringing its schema up
// to date.
func Open(ctx context.Context, cfg Config, opts ...Option) (*Orchestrator, error) {
	cfg = cfg.withDefaults()
	db, ro, cp, err := openDB(ctx, cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", cfg.DB, err)
	}
	o := &Orchestrator{
		cfg: cfg, db: db, ro: ro, cp: cp, log: slog.Default(), now: time.Now,
		leases: leaseBook{holders: map[leaseKey]leaseHolder{}, pending: map[leaseKey]int64{}},
	}
	for _, opt := range opts {
		opt(o)
	}
	if o.http == nil {
		// Notifications go to every instance in the environment: keep a connection to each.
		o.http = &http.Client{Timeout: cfg.DeliveryTimeout, Transport: protocol.Pooled(2)}
	}
	o.started = o.now()
	// Notifications a crash left on their way go out again: at least once, and each a claim check.
	if _, err := db.ExecContext(ctx, `update deliveries set status = 'pending' where status = 'sending'`); err != nil {
		db.Close()
		return nil, err
	}
	return o, nil
}

// settling is whether this process started less than a lease ago. A lease that lapsed, or a
// silence that grew stale, while the orchestrator itself was down says nothing about the suite
// or the dispatcher: they get one lease to renew and report before it counts.
func (o *Orchestrator) settling() bool {
	return o.now().Before(o.started.Add(o.cfg.Lease))
}

// Close writes the renewals it holds and closes the orchestrator's database.
func (o *Orchestrator) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := o.flushLeases(ctx); err != nil {
		o.log.Warn("could not write the last lease renewals", "err", err)
	}
	if o.ro != o.db {
		_ = o.ro.Close()
		_ = o.cp.Close()
	}
	return o.db.Close()
}

// read runs fn on one of the readers, in a read transaction: everything fn reads is the
// footprint and the runs as of one commit, however many statements it takes.
func (o *Orchestrator) read(ctx context.Context, fn func(q querier) error) error {
	tx, err := o.ro.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	return fn(tx)
}

// apiError is a refusal the API answers with a problem of its status.
type apiError struct {
	status int
	detail string
	// conflictsWith is the run a refused run would overlap (ADR 0015).
	conflictsWith string
}

func (e *apiError) Error() string { return e.detail }

func refuse(status int, format string, args ...any) error {
	return &apiError{status: status, detail: fmt.Sprintf(format, args...)}
}

// speaks refuses a party that does not speak this orchestrator's protocol version: a mismatch
// would fail in ways that look like something else, so it fails here and says so (ADR 0021).
func speaks(who, upgrade string, v int) error {
	switch {
	case v == protocol.Version:
		return nil
	case v == 0:
		return refuse(http.StatusUnprocessableEntity, "%s did not say which protocol it speaks; this orchestrator speaks protocol %d: upgrade its %s", who, protocol.Version, upgrade)
	case v < protocol.Version:
		return refuse(http.StatusUnprocessableEntity, "%s speaks protocol %d, and this orchestrator speaks %d: upgrade its %s", who, v, protocol.Version, upgrade)
	default:
		return refuse(http.StatusUnprocessableEntity, "%s speaks protocol %d, newer than this orchestrator's %d: upgrade the orchestrator first", who, v, protocol.Version)
	}
}

// conflict refuses a run that would overlap the run holding the dispatchers it needs.
func conflict(holder, format string, args ...any) error {
	return &apiError{status: http.StatusConflict, detail: fmt.Sprintf(format, args...), conflictsWith: holder}
}

func (o *Orchestrator) tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	asked := time.Now()
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	began := time.Now()
	defer func() {
		o.writes.n.Add(1)
		o.writes.wait.Add(int64(began.Sub(asked)))
		o.writes.busy.Add(int64(time.Since(began)))
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func newID(prefix string, n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// --- groups and dispatchers ---

func (o *Orchestrator) createGroup(ctx context.Context, q querier, name string) error {
	_, err := q.ExecContext(ctx, `insert into run_groups (name, created_at) values (?, ?) on conflict (name) do nothing`, name, millis(o.now()))
	return err
}

// CreateGroup creates a group; creating one that exists changes nothing.
func (o *Orchestrator) CreateGroup(ctx context.Context, name string) (*protocol.Group, error) {
	if blank(name) {
		return nil, refuse(http.StatusBadRequest, "a group needs a name")
	}
	if err := o.createGroup(ctx, o.db, name); err != nil {
		return nil, err
	}
	members, err := o.members(ctx, o.db, name)
	if err != nil {
		return nil, err
	}
	return &protocol.Group{Name: name, Dispatchers: members}, nil
}

// Groups lists the groups and their dispatchers.
func (o *Orchestrator) Groups(ctx context.Context) ([]protocol.Group, error) {
	rows, err := o.ro.QueryContext(ctx, `select name from run_groups order by name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, n)
	}
	rows.Close()
	groups := []protocol.Group{}
	for _, n := range names {
		members, err := o.members(ctx, o.ro, n)
		if err != nil {
			return nil, err
		}
		groups = append(groups, protocol.Group{Name: n, Dispatchers: members})
	}
	return groups, nil
}

// exclusive refuses a run in group, with these members, while another run holds any of them: a
// run in the same group, or one whose dispatchers include a member. Runs never overlap on a
// dispatcher, so one team's load never lands in another run's window, and no dispatcher prepares
// for one run while it cleans up another's (ADR 0015).
func (o *Orchestrator) exclusive(ctx context.Context, q querier, group string, members []string) error {
	args := []any{protocol.StateClosed, group}
	in := ""
	for i, m := range members {
		if i > 0 {
			in += ", "
		}
		in += "?"
		args = append(args, m)
	}
	shared := "0"
	if in != "" {
		shared = `exists (select 1 from run_dispatchers rd where rd.run_id = r.id and rd.name in (` + in + `))`
	}
	var holder, holderGroup, state string
	err := q.QueryRowContext(ctx, `select r.id, r.group_name, r.state from runs r where r.state != ? and r.released = 0
		and (r.group_name = ? or `+shared+`) order by r.opened_at limit 1`, args...).Scan(&holder, &holderGroup, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if holderGroup == group {
		return conflict(holder, "run %s is open in group %s (%s): one run at a time holds a group's dispatchers, until it closes", holder, group, state)
	}
	return conflict(holder, "run %s in group %s (%s) holds dispatchers group %s shares: one run at a time holds a dispatcher, until it closes", holder, holderGroup, state, group)
}

func (o *Orchestrator) members(ctx context.Context, q querier, group string) ([]string, error) {
	return strings0(q.QueryContext(ctx, `select dispatcher from group_members where group_name = ? order by dispatcher`, group))
}

// Members lists a group's dispatchers: none for a group that does not exist.
func (o *Orchestrator) Members(ctx context.Context, group string) ([]string, error) {
	return o.members(ctx, o.ro, group)
}

// liveInstances are a dispatcher's instances whose leases have not expired. An instance that
// stopped renewing is gone as far as the orchestrator is concerned, so a replica that scaled
// away cannot hold a run at its gate for ever.
func (o *Orchestrator) liveInstances(ctx context.Context, q querier, name string) ([]string, error) {
	return strings0(q.QueryContext(ctx, `select instance from dispatchers where name = ? and lease_expires_at > ? order by instance`, name, millis(o.now())))
}

// Join adds a dispatcher with a live instance to a group, creating the group if it has to.
func (o *Orchestrator) Join(ctx context.Context, group, name string) error {
	// A dispatcher joins again now and then, to put back a membership the orchestrator lost:
	// one it has, with a live instance, writes nothing.
	member := func(q querier) (bool, error) {
		var in bool
		if err := q.QueryRowContext(ctx, `select exists(select 1 from group_members where group_name = ? and dispatcher = ?)`, group, name).Scan(&in); err != nil || !in {
			return true, err
		}
		live, err := o.liveInstances(ctx, q, name)
		return len(live) == 0, err
	}
	return o.decideThenWrite(ctx, member, func(tx *sql.Tx) error {
		live, err := o.liveInstances(ctx, tx, name)
		if err != nil {
			return err
		}
		if len(live) == 0 {
			return refuse(http.StatusNotFound, "no live instance of dispatcher %s: register before joining a group", name)
		}
		if err := o.createGroup(ctx, tx, group); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `insert into group_members (group_name, dispatcher) values (?, ?) on conflict do nothing`, group, name)
		return err
	})
}

// Leave takes a dispatcher out of a group; leaving one it is not in changes nothing.
func (o *Orchestrator) Leave(ctx context.Context, group, name string) error {
	_, err := o.db.ExecContext(ctx, `delete from group_members where group_name = ? and dispatcher = ?`, group, name)
	return err
}

// Register registers a dispatcher instance and grants it a lease; registering again renews it.
func (o *Orchestrator) Register(ctx context.Context, r protocol.Registration) (*protocol.Dispatcher, error) {
	switch {
	case blank(r.Name), blank(r.Instance), blank(r.Team), blank(r.CallbackURL):
		return nil, refuse(http.StatusBadRequest, "a registration needs a name, an instance, a team and a callbackUrl")
	case r.Name == protocol.Suite:
		return nil, refuse(http.StatusBadRequest, "%q is reserved for the suite's own footprint", protocol.Suite)
	}
	if err := protocol.CheckSecret(r.Secret); err != nil {
		return nil, refuse(http.StatusBadRequest, "%v", err)
	}
	if err := speaks("dispatcher "+r.Name+"/"+r.Instance, "dispatcher-sdk", r.Protocol); err != nil {
		return nil, err
	}
	now := o.now()
	lease := now.Add(o.cfg.Lease)
	k, holder := leaseKey{r.Name, r.Instance}, leaseHolder{team: r.Team, callback: r.CallbackURL, secret: r.Secret}
	answer := &protocol.Dispatcher{Name: r.Name, Instance: r.Instance, Team: r.Team, CallbackURL: r.CallbackURL, LeaseExpiresAt: fromMillis(millis(lease))}
	// A renewal that changes nothing but the lease is kept in memory, and written with the others
	// within a second (ADR 0025).
	if o.leases.renew(k, holder, millis(now), millis(lease)) {
		return answer, nil
	}
	err := o.tx(ctx, func(tx *sql.Tx) error {
		// A dispatcher's name belongs to one team: another team cannot take it over while it is
		// live. The check and the write are one transaction, so two teams racing for a name
		// cannot both pass it.
		var holder string
		switch err := tx.QueryRowContext(ctx, `select team from dispatchers where name = ? and team != ? and lease_expires_at > ? limit 1`,
			r.Name, r.Team, millis(now)).Scan(&holder); {
		case err == nil:
			return refuse(http.StatusConflict, "dispatcher %s belongs to team %s, whose instances are live: a dispatcher's name belongs to one team", r.Name, holder)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		_, err := tx.ExecContext(ctx, `insert into dispatchers (name, instance, team, callback_url, secret, created_at, lease_expires_at)
			values (?, ?, ?, ?, ?, ?, ?)
			on conflict (name, instance) do update set team = excluded.team, callback_url = excluded.callback_url,
				secret = excluded.secret, lease_expires_at = excluded.lease_expires_at`,
			r.Name, r.Instance, r.Team, r.CallbackURL, r.Secret, millis(now), millis(lease))
		return err
	})
	if err != nil {
		return nil, err
	}
	o.leases.wrote(k, holder, millis(lease))
	return answer, nil
}

// --- leases ---

// leaseBook holds the renewals of dispatcher instances' leases until they are written. Every
// instance renews every few seconds, and a renewal that changes nothing but the lease's end need
// not be on the disk before it is answered: what reads a lease to decide anything writes these
// first (flushLeases), and an orchestrator that crashes with some unwritten gives every instance
// a lease to renew in when it starts again (settling). At 6,000 instances, writing each renewal
// was 600 commits a second with no run open (ADR 0025).
type leaseBook struct {
	mu sync.Mutex
	// holders are the instances whose registration the database has, as it has them: only a
	// renewal by the same team, with the same callback and secret, is kept in memory.
	holders map[leaseKey]leaseHolder
	pending map[leaseKey]int64 // lease ends not yet written
}

type leaseKey struct{ name, instance string }

type leaseHolder struct {
	team, callback, secret string
	stored                 int64 // the lease's end as the database has it
}

func (h leaseHolder) same(o leaseHolder) bool {
	return h.team == o.team && h.callback == o.callback && h.secret == o.secret
}

// renew keeps a renewal in memory, if it changes nothing but the lease of a registration the
// database has, with a lease that has not ended.
func (b *leaseBook) renew(k leaseKey, h leaseHolder, now, until int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	known, ok := b.holders[k]
	if !ok || !known.same(h) || known.stored <= now {
		return false
	}
	b.pending[k] = max(b.pending[k], until)
	return true
}

// wrote records a registration the database now has.
func (b *leaseBook) wrote(k leaseKey, h leaseHolder, until int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	h.stored = until
	b.holders[k] = h
	if b.pending[k] <= until {
		delete(b.pending, k)
	}
}

// leaseOf is an instance's lease end as it stands, written or not.
func (b *leaseBook) leaseOf(k leaseKey, stored int64) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return max(stored, b.pending[k])
}

// flushLeases writes the renewals held in memory, all in one transaction.
func (o *Orchestrator) flushLeases(ctx context.Context) error {
	b := &o.leases
	b.mu.Lock()
	batch := b.pending
	if len(batch) == 0 {
		b.mu.Unlock()
		return nil
	}
	b.pending = map[leaseKey]int64{}
	teams := make(map[leaseKey]string, len(batch))
	for k := range batch {
		teams[k] = b.holders[k].team
	}
	b.mu.Unlock()
	err := o.tx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `update dispatchers set lease_expires_at = max(lease_expires_at, ?) where name = ? and instance = ? and team = ?`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for k, until := range batch {
			if _, err := stmt.ExecContext(ctx, until, k.name, k.instance, teams[k]); err != nil {
				return err
			}
		}
		return nil
	})
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, until := range batch {
		if err != nil {
			b.pending[k] = max(b.pending[k], until) // tried again on the next flush
			continue
		}
		if h, ok := b.holders[k]; ok && until > h.stored {
			h.stored = until
			b.holders[k] = h
		}
	}
	return err
}

// Dispatchers lists the registered dispatcher instances, without their secrets.
func (o *Orchestrator) Dispatchers(ctx context.Context) ([]protocol.Dispatcher, error) {
	rows, err := o.ro.QueryContext(ctx, `select name, instance, team, callback_url, lease_expires_at from dispatchers order by name, instance`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Dispatcher{}
	for rows.Next() {
		var d protocol.Dispatcher
		var lease int64
		if err := rows.Scan(&d.Name, &d.Instance, &d.Team, &d.CallbackURL, &lease); err != nil {
			return nil, err
		}
		d.LeaseExpiresAt = fromMillis(o.leases.leaseOf(leaseKey{d.Name, d.Instance}, lease))
		out = append(out, d)
	}
	return out, rows.Err()
}

// --- runs ---

// runRow is a run's row.
type runRow struct {
	id, group, state, initiator string
	failed, abandoned           bool
	failReason                  sql.NullString
	openedAt, windowFrom        int64
	windowTo                    sql.NullInt64
	leaseExpiresAt              int64
	footprintUpdatedAt          sql.NullInt64
	footprintVersion            int64
	claims                      int64
	cleanupAt                   sql.NullInt64
	closedAt                    sql.NullInt64
	released                    bool
	unfinished                  bool
}

// holds is whether the run still holds its group's dispatchers (ADR 0015).
func (r *runRow) holds() bool { return r.state != protocol.StateClosed && !r.released }

func (o *Orchestrator) runRow(ctx context.Context, q querier, id string) (*runRow, error) {
	var r runRow
	err := q.QueryRowContext(ctx, `select id, group_name, state, initiator, failed, fail_reason, abandoned, opened_at, window_from,
		window_to, lease_expires_at, footprint_updated_at, footprint_version, claims, cleanup_at, closed_at, released, unfinished from runs where id = ?`, id).
		Scan(&r.id, &r.group, &r.state, &r.initiator, &r.failed, &r.failReason, &r.abandoned, &r.openedAt, &r.windowFrom,
			&r.windowTo, &r.leaseExpiresAt, &r.footprintUpdatedAt, &r.footprintVersion, &r.claims, &r.cleanupAt, &r.closedAt, &r.released, &r.unfinished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, refuse(http.StatusNotFound, "no run with id %s", id)
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// runDispatcherRow is a dispatcher instance on a run.
type runDispatcherRow struct {
	name, instance, state        string
	required                     bool
	lastStatus, evidence, detail sql.NullString
	lastReportAt                 sql.NullInt64
	footprintVersion             int64
}

func (o *Orchestrator) runDispatchers(ctx context.Context, q querier, runID string) ([]runDispatcherRow, error) {
	rows, err := q.QueryContext(ctx, `select name, instance, required, state, last_status, evidence, detail, last_report_at, footprint_version
		from run_dispatchers where run_id = ? order by name, instance`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []runDispatcherRow
	for rows.Next() {
		var d runDispatcherRow
		if err := rows.Scan(&d.name, &d.instance, &d.required, &d.state, &d.lastStatus, &d.evidence, &d.detail, &d.lastReportAt, &d.footprintVersion); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// OpenRun opens a run for a group: every live instance of every dispatcher in the group is put
// on it and notified. A required dispatcher must be in the group and have a live instance.
func (o *Orchestrator) OpenRun(ctx context.Context, req protocol.OpenRun) (*protocol.Run, error) {
	if blank(req.Initiator) || blank(req.Group) {
		return nil, refuse(http.StatusBadRequest, "a run needs an initiator and a group")
	}
	if err := o.mustHaveRoom("new runs"); err != nil {
		return nil, err
	}
	for _, s := range req.Streams {
		if blank(s.Name) {
			return nil, refuse(http.StatusBadRequest, "a stream needs a name")
		}
	}
	if err := speaks("the suite", "stormbreaker packs", req.Protocol); err != nil {
		return nil, err
	}
	id := newID("run-", 4)
	err := o.tx(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRowContext(ctx, `select count(*) from run_groups where name = ?`, req.Group).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return refuse(http.StatusUnprocessableEntity, "no such group: %s", req.Group)
		}
		members, err := o.members(ctx, tx, req.Group)
		if err != nil {
			return err
		}
		for _, r := range req.Requires {
			if !slices.Contains(members, r) {
				return refuse(http.StatusUnprocessableEntity, "required dispatcher not in group %s: %s", req.Group, r)
			}
		}
		if err := o.exclusive(ctx, tx, req.Group, members); err != nil {
			return err
		}
		now := millis(o.now())
		if _, err := tx.ExecContext(ctx, `insert into runs (id, group_name, state, initiator, opened_at, window_from, lease_expires_at, footprint_updated_at)
			values (?, ?, ?, ?, ?, ?, ?, ?)`, id, req.Group, protocol.StateOpened, req.Initiator, now, now, now+o.cfg.Lease.Milliseconds(), now); err != nil {
			return err
		}
		for _, s := range req.Streams {
			if err := o.addStream(ctx, tx, id, req.Initiator, s); err != nil {
				return err
			}
		}
		for _, m := range members {
			live, err := o.liveInstances(ctx, tx, m)
			if err != nil {
				return err
			}
			required := slices.Contains(req.Requires, m)
			if len(live) == 0 && required {
				return refuse(http.StatusUnprocessableEntity, "no live instance of required dispatcher: %s", m)
			}
			for _, inst := range live {
				if _, err := tx.ExecContext(ctx, `insert into run_dispatchers (run_id, name, instance, required, state) values (?, ?, ?, ?, ?)`,
					id, m, inst, required, protocol.InstanceNotified); err != nil {
					return err
				}
			}
		}
		text := "opens a run in group " + req.Group
		if len(req.Requires) == 0 {
			text += " with no required dispatchers"
		} else {
			text += ", requiring " + strings.Join(req.Requires, ", ")
		}
		if err := o.logRun(ctx, tx, id, req.Initiator, text); err != nil {
			return err
		}
		_, err = o.appendEvent(ctx, tx, id, req.Group, protocol.EventOpened)
		return err
	})
	if err != nil {
		return nil, err
	}
	return o.Run(ctx, id)
}

// Run is a run, with its gate worked out.
func (o *Orchestrator) Run(ctx context.Context, id string) (*protocol.Run, error) {
	var run *protocol.Run
	err := o.read(ctx, func(q querier) (err error) {
		run, err = o.view(ctx, q, id)
		return err
	})
	return run, err
}

func (o *Orchestrator) view(ctx context.Context, q querier, id string) (*protocol.Run, error) {
	r, err := o.runRow(ctx, q, id)
	if err != nil {
		return nil, err
	}
	ds, err := o.runDispatchers(ctx, q, id)
	if err != nil {
		return nil, err
	}
	run := &protocol.Run{
		ID: r.id, Group: r.group, State: r.state, Initiator: r.initiator,
		Failed: r.failed, FailReason: r.failReason.String, Abandoned: r.abandoned,
		OpenedAt: fromMillis(r.openedAt), WindowFrom: fromMillis(r.windowFrom), WindowTo: nullMillis(r.windowTo),
		FootprintUpdatedAt: nullMillis(r.footprintUpdatedAt), FootprintVersion: r.footprintVersion,
		Gate: gate(r, ds), Dispatchers: []protocol.RunDispatcher{}, Streams: []protocol.StreamRef{}, Claims: r.claims, Holds: r.holds(), Unfinished: r.unfinished,
		CleanupAt: nullMillis(r.cleanupAt), ClosedAt: nullMillis(r.closedAt),
	}
	for _, d := range ds {
		run.Dispatchers = append(run.Dispatchers, protocol.RunDispatcher{
			Name: d.name, Instance: d.instance, Required: d.required, State: d.state,
			LastStatus: d.lastStatus.String, Evidence: d.evidence.String, Detail: d.detail.String, LastReportAt: nullMillis(d.lastReportAt),
			FootprintVersion: d.footprintVersion,
		})
	}
	rows, err := q.QueryContext(ctx, `select name, seed from run_streams where run_id = ? order by name`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s protocol.StreamRef
		if err := rows.Scan(&s.Name, &s.Seed); err != nil {
			return nil, err
		}
		run.Streams = append(run.Streams, s)
	}
	return run, rows.Err()
}

// gate works out the run's readiness gate. It is open when every instance of every required
// dispatcher is prepared, in a round that began from the footprint as it is now: that instance
// has seen everything there is to derive from and found nothing to add. A round that began
// before the footprint last grew counts for nothing, however late it reports, because it cannot
// have seen what grew. That is a fact, not a timer, so the gate needs no settle window that load
// would get wrong. Every instance answers for itself: only it knows what it prepared.
func gate(r *runRow, ds []runDispatcherRow) protocol.Gate {
	if r.failed || r.abandoned {
		return protocol.Gate{Open: false}
	}
	var waiting []string
	for _, d := range ds {
		if !d.required {
			continue
		}
		who := d.name + "/" + d.instance
		switch {
		case d.state != protocol.InstancePrepared && d.state != protocol.InstanceCompleted:
			waiting = append(waiting, who+" is "+d.state)
		case d.footprintVersion < r.footprintVersion:
			waiting = append(waiting, fmt.Sprintf("%s settled on footprint version %d; it is at %d", who, d.footprintVersion, r.footprintVersion))
		}
	}
	return protocol.Gate{Open: len(waiting) == 0, Waiting: waiting}
}

// order is where a state sits in a run's progress.
var order = map[string]int{
	protocol.StateOpened: 0, protocol.StateTrafficComplete: 1, protocol.StateSettled: 2,
	protocol.StateCleanup: 3, protocol.StateClosed: 4,
}

// Publish publishes a lifecycle event the suite sends: traffic-complete, settled,
// cleanup-ready, or abandoned. A run only moves forward; publishing its current phase again
// notifies again and changes nothing else.
func (o *Orchestrator) Publish(ctx context.Context, runID, eventType string) (*protocol.Event, error) {
	var ev *protocol.Event
	err := o.tx(ctx, func(tx *sql.Tx) error {
		r, err := o.runRow(ctx, tx, runID)
		if err != nil {
			return err
		}
		if r.failed || r.abandoned || r.state == protocol.StateClosed {
			why := r.state
			switch {
			case r.failed:
				why = "failed (" + r.failReason.String + ")"
			case r.abandoned:
				why = "abandoned"
			}
			return refuse(http.StatusConflict, "%s cannot follow on %s: it is %s", eventType, runID, why)
		}
		var next string
		switch eventType {
		case protocol.EventTrafficComplete:
			next = protocol.StateTrafficComplete
		case protocol.EventSettled:
			next = protocol.StateSettled
		case protocol.EventCleanupReady:
			next = protocol.StateCleanup
		case protocol.EventAbandoned:
			ev, err = o.abandon(ctx, tx, r, "the suite", "the suite abandoned the run")
			return err
		default:
			return refuse(http.StatusBadRequest, "a suite publishes %s, %s, %s or %s, not %q",
				protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady, protocol.EventAbandoned, eventType)
		}
		if order[next] < order[r.state] {
			return refuse(http.StatusConflict, "%s cannot follow on %s: it is already %s", eventType, runID, r.state)
		}
		if _, err := tx.ExecContext(ctx, `update runs set state = ? where id = ?`, next, runID); err != nil {
			return err
		}
		if next == protocol.StateCleanup {
			if _, err := tx.ExecContext(ctx, `update runs set cleanup_at = coalesce(cleanup_at, ?) where id = ?`, millis(o.now()), runID); err != nil {
				return err
			}
		}
		if next == protocol.StateTrafficComplete {
			if _, err := tx.ExecContext(ctx, `update runs set window_to = ? where id = ? and window_to is null`, millis(o.now()), runID); err != nil {
				return err
			}
		}
		ev, err = o.appendEvent(ctx, tx, runID, r.group, eventType)
		return err
	})
	return ev, err
}

// abandon sends a run to cleanup because its suite went away: whatever was prepared or created
// is still cleaned up.
func (o *Orchestrator) abandon(ctx context.Context, q querier, r *runRow, actor, why string) (*protocol.Event, error) {
	if _, err := q.ExecContext(ctx, `update runs set state = ?, abandoned = 1, cleanup_at = coalesce(cleanup_at, ?) where id = ?`, protocol.StateCleanup, millis(o.now()), r.id); err != nil {
		return nil, err
	}
	if err := o.logRun(ctx, q, r.id, actor, why+"; cleanup opens"); err != nil {
		return nil, err
	}
	return o.appendEvent(ctx, q, r.id, r.group, protocol.EventAbandoned)
}

// fail sends a run whose gate cannot open to cleanup, and says why in the same breath, because
// the why is the whole point. Anything already prepared is cleaned up.
func (o *Orchestrator) fail(ctx context.Context, q querier, r *runRow, reason string) error {
	if _, err := q.ExecContext(ctx, `update runs set state = ?, failed = 1, fail_reason = ?, cleanup_at = coalesce(cleanup_at, ?) where id = ?`, protocol.StateCleanup, reason, millis(o.now()), r.id); err != nil {
		return err
	}
	if err := o.logRun(ctx, q, r.id, "orchestrator", "the run fails: "+reason); err != nil {
		return err
	}
	_, err := o.appendEvent(ctx, q, r.id, r.group, protocol.EventFailed)
	return err
}

// Heartbeat renews the suite's lease on a run.
func (o *Orchestrator) Heartbeat(ctx context.Context, runID string) error {
	return o.tx(ctx, func(tx *sql.Tx) error {
		r, err := o.runRow(ctx, tx, runID)
		if err != nil {
			return err
		}
		switch {
		case r.abandoned:
			return refuse(http.StatusGone, "%s was abandoned: its lease expired or its suite gave it up", runID)
		case r.failed:
			return refuse(http.StatusGone, "%s failed: %s", runID, r.failReason.String)
		case r.state == protocol.StateClosed:
			return refuse(http.StatusGone, "%s is closed", runID)
		}
		_, err = tx.ExecContext(ctx, `update runs set lease_expires_at = ? where id = ?`, millis(o.now().Add(o.cfg.Lease)), runID)
		return err
	})
}

func (o *Orchestrator) addStream(ctx context.Context, q querier, runID, actor string, s protocol.StreamRef) error {
	res, err := q.ExecContext(ctx, `insert into run_streams (run_id, name, seed) values (?, ?, ?) on conflict do nothing`, runID, s.Name, s.Seed)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return o.logRun(ctx, q, runID, actor, "generates the "+s.Name+" stream with seed "+s.Seed)
	}
	return nil
}

// AddStream records a stream the run generates records from.
func (o *Orchestrator) AddStream(ctx context.Context, runID string, s protocol.StreamRef) error {
	if blank(s.Name) {
		return refuse(http.StatusBadRequest, "a stream needs a name")
	}
	return o.tx(ctx, func(tx *sql.Tx) error {
		r, err := o.runRow(ctx, tx, runID)
		if err != nil {
			return err
		}
		return o.addStream(ctx, tx, runID, r.initiator, s)
	})
}

func (o *Orchestrator) requireInstance(ctx context.Context, q querier, runID, name, instance string) (*runDispatcherRow, error) {
	ds, err := o.runDispatchers(ctx, q, runID)
	if err != nil {
		return nil, err
	}
	for i := range ds {
		if ds[i].name == name && ds[i].instance == instance {
			return &ds[i], nil
		}
	}
	return nil, refuse(http.StatusNotFound, "%s/%s is not on %s", name, instance, runID)
}

var statuses = []string{protocol.StatusPrepared, protocol.StatusInProgress, protocol.StatusPartial, protocol.StatusBlocked, protocol.StatusCompleted}

// ReportStatus records a dispatcher instance's status on a run. Prepared and completed move its
// state; the others say how it is getting on.
func (o *Orchestrator) ReportStatus(ctx context.Context, runID, name, instance string, s protocol.Status) error {
	if !slices.Contains(statuses, s.State) {
		return refuse(http.StatusBadRequest, "a status is one of %s, not %q", strings.Join(statuses, ", "), s.State)
	}
	return o.tx(ctx, func(tx *sql.Tx) error {
		r, err := o.runRow(ctx, tx, runID)
		if err != nil {
			return err
		}
		if s.FootprintVersion < 0 || s.FootprintVersion > r.footprintVersion {
			return refuse(http.StatusBadRequest, "the footprint of %s is at version %d; no round began from version %d", runID, r.footprintVersion, s.FootprintVersion)
		}
		d, err := o.requireInstance(ctx, tx, runID, name, instance)
		if err != nil {
			return err
		}
		now := millis(o.now())
		state := d.state
		if s.State == protocol.StatusPrepared || s.State == protocol.StatusCompleted {
			state = s.State
		} else if state == protocol.InstanceNotified {
			state = protocol.InstanceAcknowledged
		}
		// Only a round that settled says which footprint it settled on: one still under way has
		// not seen the footprint yet, whichever version it began from.
		seen := d.footprintVersion
		if s.State == protocol.StatusPrepared || s.State == protocol.StatusCompleted {
			seen = s.FootprintVersion
		}
		if _, err := tx.ExecContext(ctx, `update run_dispatchers set state = ?, last_status = ?, evidence = ?, detail = ?, last_report_at = ?, footprint_version = ?
			where run_id = ? and name = ? and instance = ?`, state, s.State, s.Evidence, s.Detail, now, seen, runID, name, instance); err != nil {
			return err
		}
		// Only changes are moments: instances report again on a cadence, and a journal full of
		// "still prepared" is a journal nobody reads.
		if s.State != d.lastStatus.String {
			text := "reports " + s.State
			if !blank(s.Detail) {
				text += ": " + s.Detail
			}
			if err := o.logRun(ctx, tx, runID, name+"/"+instance, text); err != nil {
				return err
			}
		}
		return o.maybeClose(ctx, tx, runID)
	})
}

var resolutions = []string{protocol.ResolutionExpected, protocol.ResolutionRemovedVerified, protocol.ResolutionCleanBy}

// ReportFootprint records footprint entries from a dispatcher instance, or from the suite.
// When a report declares something its reporter has not said at that place before, the
// footprint grew: its version goes up and every instance in the group is notified, so that
// prepare rounds run again until one adds nothing. A removal is not growth: it answers for
// something, and nobody derives from it. The same statement again is recorded once.
func (o *Orchestrator) ReportFootprint(ctx context.Context, runID, name, instance string, rep protocol.FootprintReport) (*protocol.FootprintResult, error) {
	// Declarations grow the store; removals answer for what is in it, and always go through.
	if slices.ContainsFunc(rep.Entries, func(e protocol.FootprintEntry) bool { return e.Resolution == protocol.ResolutionExpected }) {
		if err := o.mustHaveRoom("declarations"); err != nil {
			return nil, err
		}
	}
	for _, e := range rep.Entries {
		switch {
		case blank(e.Store) || blank(e.Locator):
			return nil, refuse(http.StatusBadRequest, "a footprint entry needs a store and a locator")
		case !slices.Contains(resolutions, e.Resolution):
			return nil, refuse(http.StatusBadRequest, "a footprint entry's resolution is one of %s, not %q", strings.Join(resolutions, ", "), e.Resolution)
		case e.Resolution == protocol.ResolutionCleanBy:
			if _, err := time.Parse(time.DateOnly, e.CleanBy); err != nil {
				return nil, refuse(http.StatusBadRequest, "a clean-by entry needs cleanBy, a date (YYYY-MM-DD), not %q", e.CleanBy)
			}
		}
	}
	result := &protocol.FootprintResult{}
	return result, o.tx(ctx, func(tx *sql.Tx) error {
		r, err := o.runRow(ctx, tx, runID)
		if err != nil {
			return err
		}
		if r.state == protocol.StateClosed {
			return refuse(http.StatusConflict, "%s is closed", runID)
		}
		suite := name == protocol.Suite
		if !suite {
			if _, err := o.requireInstance(ctx, tx, runID, name, instance); err != nil {
				return err
			}
		}
		grew := false
		now := millis(o.now())
		for _, e := range rep.Entries {
			if len(e.Identities) == 0 {
				continue // a report that names nothing leaves nothing behind
			}
			packed := pack(e.Identities)
			d := digest(packed)
			// The latest time this reporter said these identities here, if it ever has.
			var saidAs, saidBy sql.NullString
			err := tx.QueryRowContext(ctx, `select resolution, coalesce(clean_by, '') from footprint_entries
				where run_id = ? and dispatcher = ? and instance = ? and store = ? and locator = ? and digest = ?
				order by id desc limit 1`, runID, name, instance, e.Store, e.Locator, d).Scan(&saidAs, &saidBy)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			isNew := !saidAs.Valid
			if !isNew && saidAs.String == e.Resolution && saidBy.String == e.CleanBy {
				// The very same statement again, as a round that finds nothing new makes it:
				// there is nothing to record.
				continue
			}
			grew = grew || (isNew && e.Resolution == protocol.ResolutionExpected)
			if err := o.logRun(ctx, tx, runID, name+"/"+instance, describeEntry(e)); err != nil {
				return err
			}
			var entryID int64
			if err := tx.QueryRowContext(ctx, `insert into footprint_entries (run_id, dispatcher, instance, store, locator, resolution, clean_by, at, identity_count, digest)
				values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) returning id`,
				runID, name, instance, e.Store, e.Locator, e.Resolution, nullString(e.CleanBy), now, len(e.Identities), d).Scan(&entryID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `insert into footprint_blobs (entry_id, identities) values (?, ?)`, entryID, packed); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `update runs set claims = claims + ? where id = ?`, len(e.Identities), runID); err != nil {
				return err
			}
		}
		result.Grew = grew
		if grew {
			if _, err := tx.ExecContext(ctx, `update runs set footprint_updated_at = ?, footprint_version = footprint_version + 1 where id = ?`, now, runID); err != nil {
				return err
			}
			// Everyone, the reporter included: an instance settles by running again and finding
			// nothing left to add, so the notification it causes is the one it needs.
			if _, err := o.appendEvent(ctx, tx, runID, r.group, protocol.EventFootprintUpdated); err != nil {
				return err
			}
		}
		if !suite {
			// A reporting instance has plainly received the run.
			if _, err := tx.ExecContext(ctx, `update run_dispatchers set state = ? where run_id = ? and name = ? and instance = ? and state = ?`,
				protocol.InstanceAcknowledged, runID, name, instance, protocol.InstanceNotified); err != nil {
				return err
			}
		}
		return o.maybeClose(ctx, tx, runID)
	})
}

func describeEntry(e protocol.FootprintEntry) string {
	n := len(e.Identities)
	noun := "identities"
	if n == 1 {
		noun = "identity"
	}
	if e.Resolution == protocol.ResolutionExpected {
		return fmt.Sprintf("declares %d %s at %s:%s", n, noun, e.Store, e.Locator)
	}
	res := e.Resolution
	if e.Resolution == protocol.ResolutionCleanBy {
		res += " " + e.CleanBy
	}
	return fmt.Sprintf("reports %d %s at %s:%s (%s)", n, noun, e.Store, e.Locator, res)
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// maybeClose closes a run in cleanup once every instance on it has completed (or is
// unreachable) and has reported since the footprint last grew: the same fact the gate waits
// on, so closing needs no timer either.
func (o *Orchestrator) maybeClose(ctx context.Context, q querier, runID string) error {
	r, ds, ok, err := o.closable(ctx, q, runID)
	if err != nil || !ok {
		return err
	}
	var unreachable []string
	for _, d := range ds {
		if d.state == protocol.InstanceUnreachable {
			unreachable = append(unreachable, d.name+"/"+d.instance)
		}
	}
	res, err := q.ExecContext(ctx, `update runs set state = ?, closed_at = ? where id = ? and state = ?`, protocol.StateClosed, millis(o.now()), runID, protocol.StateCleanup)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	claims := fmt.Sprintf("%d %s claimed", r.claims, plural(r.claims, "identity", "identities"))
	switch {
	case len(ds) == 0:
		return o.logRun(ctx, q, runID, "orchestrator", "the run closes with no dispatcher on it: nobody cleaned up the "+claims)
	case len(unreachable) > 0:
		// An unreachable instance holds nothing up, so that a replica that scaled away does not
		// keep a run open; but what it claimed is not answered for, and the journal says so.
		return o.logRun(ctx, q, runID, "orchestrator", fmt.Sprintf("the run closes with %s unreachable: what %s claimed is not answered for (%s in all)",
			strings.Join(unreachable, ", "), plural(int64(len(unreachable)), "it", "they"), claims))
	}
	return o.logRun(ctx, q, runID, "orchestrator", fmt.Sprintf("the run closes: every instance completed and the footprint stopped growing, with %d %s answered for",
		r.claims, plural(r.claims, "identity", "identities")))
}

// closable says whether a run in cleanup may close: every instance on it completed, on the
// footprint as it is, or is unreachable.
func (o *Orchestrator) closable(ctx context.Context, q querier, runID string) (*runRow, []runDispatcherRow, bool, error) {
	r, err := o.runRow(ctx, q, runID)
	if err != nil || r.state != protocol.StateCleanup {
		return nil, nil, false, err
	}
	ds, err := o.runDispatchers(ctx, q, runID)
	if err != nil {
		return nil, nil, false, err
	}
	for _, d := range ds {
		if d.state != protocol.InstanceCompleted && d.state != protocol.InstanceUnreachable {
			return nil, nil, false, nil
		}
		if d.state == protocol.InstanceCompleted && d.footprintVersion < r.footprintVersion {
			return nil, nil, false, nil // it completed before something new was declared
		}
	}
	return r, ds, true, nil
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// --- events ---

// appendEvent records an event and queues its notification for every live instance of every
// dispatcher in the group.
func (o *Orchestrator) appendEvent(ctx context.Context, q querier, runID, group, eventType string) (*protocol.Event, error) {
	now := o.now()
	ev := &protocol.Event{ID: newID("evt-", 6), RunID: runID, Type: eventType, Time: fromMillis(millis(now)), Ref: "/runs/" + runID}
	var seq int64
	if err := q.QueryRowContext(ctx, `insert into events (id, run_id, type, at) values (?, ?, ?, ?) returning seq`,
		ev.ID, runID, eventType, millis(now)).Scan(&seq); err != nil {
		return nil, err
	}
	// Every live instance of the group's dispatchers, in one statement. A notification is a claim
	// check: it says "look at this run", and the instance reads the run as it is when it does. One
	// not yet on its way stands for every later event of the same run, so it is not queued twice
	// (one already sending is not counted on).
	if _, err := q.ExecContext(ctx, `insert into deliveries (event_seq, dispatcher, instance, status, attempts, next_attempt_at)
		select ?, d.name, d.instance, 'pending', 0, ? from group_members m join dispatchers d on d.name = m.dispatcher
		where m.group_name = ? and d.lease_expires_at > ?
		and not exists (select 1 from deliveries p join events e on e.seq = p.event_seq
			where p.dispatcher = d.name and p.instance = d.instance and p.status = 'pending' and e.run_id = ?)`,
		seq, millis(now), group, millis(now), runID); err != nil {
		return nil, err
	}
	return ev, nil
}

// Events is the event feed after the cursor since: the backstop for notifications that never
// arrived. The cursor it returns is the last event it returned, so nothing is skipped.
func (o *Orchestrator) Events(ctx context.Context, since int64, limit int, groups ...string) (*protocol.EventFeed, error) {
	// One read transaction, so the page and the feed's end come from one snapshot. One writer
	// numbers events as they commit, so a snapshot holds every event up to its last.
	tx, err := o.ro.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var end int64
	if err := tx.QueryRowContext(ctx, `select coalesce(max(seq), 0) from events`).Scan(&end); err != nil {
		return nil, err
	}
	q := `select e.seq, e.id, e.run_id, e.type, e.at from events e`
	args := []any{}
	if len(groups) > 0 {
		in := strings.TrimSuffix(strings.Repeat("?, ", len(groups)), ", ")
		for _, g := range groups {
			args = append(args, g)
		}
		if since == 0 {
			// A dispatcher starting out begins at its groups' unfinished runs: what closed before
			// it started is nothing it can act on.
			if err := tx.QueryRowContext(ctx, `select coalesce(min(e.seq) - 1, ?) from events e join runs r on r.id = e.run_id
				where r.state != 'closed' and r.group_name in (`+in+`)`, append([]any{end}, args...)...).Scan(&since); err != nil {
				return nil, err
			}
		}
		q += ` join runs r on r.id = e.run_id where r.group_name in (` + in + `) and e.seq > ?`
	} else {
		q += ` where e.seq > ?`
	}
	args = append(args, since, limit)
	rows, err := tx.QueryContext(ctx, q+` order by e.seq limit ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	feed := &protocol.EventFeed{Events: []protocol.Event{}, Cursor: since}
	for rows.Next() {
		var ev protocol.Event
		var at int64
		if err := rows.Scan(&feed.Cursor, &ev.ID, &ev.RunID, &ev.Type, &at); err != nil {
			return nil, err
		}
		ev.Time = fromMillis(at)
		ev.Ref = "/runs/" + ev.RunID
		feed.Events = append(feed.Events, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// A short page is the whole rest of the snapshot: the cursor moves past the other groups'
	// events too, so the next read does not scan them again.
	if len(feed.Events) < limit && len(groups) > 0 {
		feed.Cursor = max(feed.Cursor, end)
	}
	return feed, nil
}

// --- the footprint ---

// PageIdentities is about how many identities a footprint page holds at most: a page ends after
// the report that reaches it, so a report is never split, and a page always has one.
const PageIdentities = 100_000

// FootprintPage is a page of a run's footprint: at most reports reports made after the cursor
// after and no later than until (0: the run's latest report now), as they were written, and
// about PageIdentities identities at most. A reader that wants the settled set applies
// the rule Claims does: a later report from the same instance at the same place supersedes its
// own earlier one.
func (o *Orchestrator) FootprintPage(ctx context.Context, runID string, after, until int64, reports int, f protocol.FootprintFilter) (*protocol.FootprintPage, error) {
	if err := o.footprintKept(ctx, runID); err != nil {
		return nil, err
	}
	if until <= 0 {
		// Until bounds the whole footprint, not the filtered part: it is where the walk ends.
		if err := o.ro.QueryRowContext(ctx, `select coalesce(max(id), 0) from footprint_entries where run_id = ?`, runID).Scan(&until); err != nil {
			return nil, err
		}
	}
	// A filter is matched on the reports' rows, in the order of the run's reports: a report that
	// does not match is skipped before its identities are read (ADR 0024).
	where, args := "", []any{runID, after, until}
	if len(f.Dispatchers) > 0 {
		where += " and e.dispatcher in (?" + strings.Repeat(", ?", len(f.Dispatchers)-1) + ")"
		for _, d := range f.Dispatchers {
			args = append(args, d)
		}
	}
	if f.Store != "" {
		where += " and e.store = ?"
		args = append(args, f.Store)
	}
	if f.Locator != "" {
		where += " and e.locator = ?"
		args = append(args, f.Locator)
	}
	rows, err := o.ro.QueryContext(ctx, `select e.id, e.dispatcher, e.instance, e.store, e.locator, e.resolution, coalesce(e.clean_by, ''), b.identities
		from footprint_entries e indexed by footprint_run join footprint_blobs b on b.entry_id = e.id
		where e.run_id = ? and e.id > ? and e.id <= ?`+where+` order by e.id limit ?`, append(args, reports)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := &protocol.FootprintPage{Reports: []protocol.Report{}, Until: until}
	var last int64
	held := 0
	for held < PageIdentities && rows.Next() {
		var r protocol.Report
		var packed []byte
		if err := rows.Scan(&last, &r.Dispatcher, &r.Instance, &r.Store, &r.Locator, &r.Resolution, &r.CleanBy, &packed); err != nil {
			return nil, err
		}
		unpack(packed, func(id string) { r.Identities = append(r.Identities, id) })
		held += len(r.Identities)
		page.Reports = append(page.Reports, r)
	}
	if last > 0 {
		page.Next = &last
	}
	return page, rows.Err()
}

// Claims settles a run's footprint in memory: who claims what, after supersession. A later
// report from the same instance at the same place supersedes its own earlier one, so each claim
// carries what its instance last said of it there. Only for runs small enough to hold; large ones
// are read a page at a time.
func (o *Orchestrator) Claims(ctx context.Context, runID string) ([]protocol.Claim, error) {
	if err := o.footprintKept(ctx, runID); err != nil {
		return nil, err
	}
	rows, err := o.ro.QueryContext(ctx, `select e.dispatcher, e.instance, e.store, e.locator, e.resolution, coalesce(e.clean_by, ''), b.identities
		from footprint_entries e join footprint_blobs b on b.entry_id = e.id where e.run_id = ? order by e.id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type key struct{ dispatcher, instance, store, locator, identity string }
	index := map[key]int{}
	out := []protocol.Claim{}
	for rows.Next() {
		var c protocol.Claim
		var packed []byte
		if err := rows.Scan(&c.Dispatcher, &c.Instance, &c.Store, &c.Locator, &c.Resolution, &c.CleanBy, &packed); err != nil {
			return nil, err
		}
		unpack(packed, func(id string) {
			c.Identity = id
			k := key{c.Dispatcher, c.Instance, c.Store, c.Locator, id}
			if i, ok := index[k]; ok {
				out[i].Resolution, out[i].CleanBy = c.Resolution, c.CleanBy
				return
			}
			index[k] = len(out)
			out = append(out, c)
		})
	}
	return out, rows.Err()
}

// --- the log ---

func (o *Orchestrator) logRun(ctx context.Context, q querier, runID, actor, text string) error {
	_, err := q.ExecContext(ctx, `insert into run_log (run_id, at, actor, text) values (?, ?, ?, ?)`, runID, millis(o.now()), actor, text)
	return err
}

func strings0(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
