package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// Serve runs the orchestrator's clocks until ctx is done: notifications, readiness, leases,
// closing and retention.
func (o *Orchestrator) Serve(ctx context.Context) {
	tick := func(every time.Duration, what string, fn func(context.Context) error) {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := fn(ctx); err != nil && ctx.Err() == nil {
					o.log.Error(what, "err", err)
				}
			}
		}
	}
	// The write-ahead log goes back into the database here, once a second, rather than after
	// every commit that finds it long: with readers always reading, a checkpoint after a commit
	// rarely finishes, and every commit after it would try again, on the writer's time.
	go tick(time.Second, "checkpoint", o.checkpoint)
	go tick(2*time.Second, "readiness", o.EnforceReadiness)
	go tick(5*time.Second, "leases", o.EnforceLeases)
	// Dispatcher instances' renewals are written together, once a second (ADR 0025).
	go tick(time.Second, "lease renewals", o.flushLeases)
	// Progress nobody renewed goes (ADR 0027).
	go tick(30*time.Second, "progress", o.sweepProgress)
	// Closing is the last thing a run waits on, so look often; it is one indexed read.
	go tick(250*time.Millisecond, "close", o.CloseSettled)
	go tick(time.Hour, "retention", func(ctx context.Context) error {
		n, err := o.Prune(ctx)
		if n > 0 {
			o.log.Info("pruned closed runs past retention", "runs", n)
		}
		return err
	})
	tick(o.cfg.FanoutInterval, "fanout", o.Deliver)
}

func (o *Orchestrator) runsIn(ctx context.Context, q querier, states ...string) ([]string, error) {
	args := make([]any, len(states))
	marks := ""
	for i, s := range states {
		args[i] = s
		if i > 0 {
			marks += ", "
		}
		marks += "?"
	}
	return strings0(q.QueryContext(ctx, `select id from runs where state in (`+marks+`) order by opened_at`, args...))
}

// EnforceReadiness fails the runs still waiting at their gate whose required instances are
// blocked, unreachable, stale, or out of time. Instances whose leases lapsed are unreachable.
func (o *Orchestrator) EnforceReadiness(ctx context.Context) error {
	if err := o.markLapsed(ctx); err != nil {
		return err
	}
	ids, err := o.runsIn(ctx, o.ro, protocol.StateOpened)
	if err != nil {
		return err
	}
	for _, id := range ids {
		err := o.decideThenWrite(ctx,
			func(q querier) (bool, error) { _, why, err := o.unready(ctx, q, id); return why != "", err },
			func(tx *sql.Tx) error {
				r, why, err := o.unready(ctx, tx, id)
				if err != nil || why == "" {
					return err
				}
				return o.fail(ctx, tx, r, why)
			})
		if err != nil {
			return err
		}
	}
	return nil
}

// unready is why an opened run fails its readiness gate now, or "" while it may still pass.
func (o *Orchestrator) unready(ctx context.Context, q querier, id string) (*runRow, string, error) {
	r, err := o.runRow(ctx, q, id)
	if err != nil || r.state != protocol.StateOpened || r.failed || r.abandoned {
		return nil, "", err
	}
	ds, err := o.runDispatchers(ctx, q, id)
	if err != nil {
		return nil, "", err
	}
	now := millis(o.now())
	deadline := r.openedAt + o.cfg.ReadinessTimeout.Milliseconds()
	for _, d := range ds {
		if !d.required || d.state == protocol.InstancePrepared || d.state == protocol.InstanceCompleted {
			continue
		}
		who := d.name + "/" + d.instance
		lastSign := r.openedAt
		if d.lastReportAt.Valid {
			lastSign = d.lastReportAt.Int64
		}
		// Silence counts from when this process started at the earliest: while it was down,
		// nobody could report.
		lastSign = max(lastSign, millis(o.started))
		switch {
		case d.lastStatus.String == protocol.StatusBlocked:
			return r, "required dispatcher blocked: " + who + detailOf(d), nil
		case d.state == protocol.InstanceUnreachable:
			return r, "required dispatcher unreachable: " + who + detailOf(d), nil
		case d.state == protocol.InstanceAcknowledged && lastSign+o.cfg.StaleAfter.Milliseconds() < now:
			return r, "required dispatcher stale: " + who, nil
		case now > deadline:
			return r, "required dispatcher not prepared by the readiness timeout: " + who, nil
		}
	}
	return r, "", nil
}

// decideThenWrite runs a housekeeping step that usually finds nothing to do: needed decides, on
// a reader, whether there is anything; only then does write take the writer, deciding again in
// its transaction, so a stale read changes nothing. The writer is one connection, and a pass
// that took it for every run waited behind every team's reports to change nothing.
func (o *Orchestrator) decideThenWrite(ctx context.Context, needed func(q querier) (bool, error), write func(tx *sql.Tx) error) error {
	var yes bool
	if err := o.read(ctx, func(q querier) error {
		var err error
		yes, err = needed(q)
		return err
	}); err != nil || !yes {
		return err
	}
	return o.tx(ctx, write)
}

func detailOf(d runDispatcherRow) string {
	if d.detail.String == "" {
		return ""
	}
	return " (" + d.detail.String + ")"
}

// markLapsed makes unreachable the instances on unfinished runs whose leases have lapsed: they
// scaled away or died, so no notification reaches them, and nothing should wait on them. A lease
// that lapsed before this process started was not renewable while it was down; it counts only
// once the instance has had a lease since then to renew it.
func (o *Orchestrator) markLapsed(ctx context.Context) error {
	if err := o.flushLeases(ctx); err != nil {
		return err
	}
	since, missing := int64(-1), true
	if o.settling() {
		since, missing = millis(o.started), false
	}
	type lapsed struct{ run, name, instance string }
	find := func(q querier) ([]lapsed, error) {
		rows, err := q.QueryContext(ctx, `select rd.run_id, rd.name, rd.instance from run_dispatchers rd
			join runs r on r.id = rd.run_id
			left join dispatchers d on d.name = rd.name and d.instance = rd.instance
			where r.state != ? and rd.state not in (?, ?)
			and ((d.lease_expires_at is null and ?) or (d.lease_expires_at <= ? and d.lease_expires_at > ?))`,
			protocol.StateClosed, protocol.InstanceCompleted, protocol.InstanceUnreachable, missing, millis(o.now()), since)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var all []lapsed
		for rows.Next() {
			var l lapsed
			if err := rows.Scan(&l.run, &l.name, &l.instance); err != nil {
				return nil, err
			}
			all = append(all, l)
		}
		return all, rows.Err()
	}
	return o.decideThenWrite(ctx, func(q querier) (bool, error) { all, err := find(q); return len(all) > 0, err }, func(tx *sql.Tx) error {
		all, err := find(tx)
		if err != nil {
			return err
		}
		for _, l := range all {
			if _, err := tx.ExecContext(ctx, `update run_dispatchers set state = ?, detail = ? where run_id = ? and name = ? and instance = ?`,
				protocol.InstanceUnreachable, "its lease lapsed", l.run, l.name, l.instance); err != nil {
				return err
			}
			if err := o.logRun(ctx, tx, l.run, "orchestrator", l.name+"/"+l.instance+" stopped renewing its lease; it is unreachable"); err != nil {
				return err
			}
		}
		return nil
	})
}

// releaseHolds lets go of the group of every run whose cleanup has outlasted the cleanup hold:
// it goes on cleaning up, but other runs may open beside it (ADR 0015).
func (o *Orchestrator) releaseHolds(ctx context.Context) error {
	find := func(q querier) ([]string, error) {
		return strings0(q.QueryContext(ctx, `select id from runs where state = ? and released = 0 and cleanup_at is not null and cleanup_at <= ?`,
			protocol.StateCleanup, millis(o.now().Add(-o.cfg.CleanupHold))))
	}
	return o.decideThenWrite(ctx, func(q querier) (bool, error) { ids, err := find(q); return len(ids) > 0, err }, func(tx *sql.Tx) error {
		ids, err := find(tx)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `update runs set released = 1 where id = ?`, id); err != nil {
				return err
			}
			if err := o.logRun(ctx, tx, id, "orchestrator", fmt.Sprintf("cleanup has run %s, the cleanup hold: the run stops holding its group's dispatchers, so other runs may open, and it goes on cleaning up", o.cfg.CleanupHold)); err != nil {
				return err
			}
		}
		return nil
	})
}

// closeUnfinished closes the runs whose cleanup has outlasted the cleanup deadline: their
// journal names the instances that never completed, whose claims nobody answered for, and the
// run is marked unfinished, for an operator to follow up (ADR 0023).
func (o *Orchestrator) closeUnfinished(ctx context.Context) error {
	ids, err := strings0(o.ro.QueryContext(ctx, `select id from runs where state = ? and cleanup_at is not null and cleanup_at <= ?`,
		protocol.StateCleanup, millis(o.now().Add(-o.cfg.CleanupDeadline))))
	if err != nil {
		return err
	}
	for _, id := range ids {
		err := o.tx(ctx, func(tx *sql.Tx) error {
			ds, err := o.runDispatchers(ctx, tx, id)
			if err != nil {
				return err
			}
			var open []string
			for _, d := range ds {
				if d.state != protocol.InstanceCompleted {
					open = append(open, d.name+"/"+d.instance)
				}
			}
			res, err := tx.ExecContext(ctx, `update runs set state = ?, closed_at = ?, unfinished = 1, released = 1 where id = ? and state = ?`,
				protocol.StateClosed, millis(o.now()), id, protocol.StateCleanup)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil
			}
			return o.logRun(ctx, tx, id, "orchestrator", fmt.Sprintf("cleanup has run %s, the cleanup deadline: the run closes unfinished. Not completed: %s; "+
				"what they claimed is not answered for, and stays in the footprint for %s", o.cfg.CleanupDeadline, strings.Join(open, ", "), o.cfg.FootprintRetention))
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// EnforceLeases abandons the runs whose suites stopped renewing their leases. Silence is a
// signal: cleanup still happens. It also lets go of the groups of cleanups past their hold.
func (o *Orchestrator) EnforceLeases(ctx context.Context) error {
	if err := o.releaseHolds(ctx); err != nil {
		return err
	}
	if err := o.closeUnfinished(ctx); err != nil {
		return err
	}
	ids, err := o.runsIn(ctx, o.ro, protocol.StateOpened, protocol.StateTrafficComplete, protocol.StateSettled)
	if err != nil {
		return err
	}
	// lapsed is the run if its suite's lease has lapsed.
	lapsed := func(q querier, id string) (*runRow, error) {
		r, err := o.runRow(ctx, q, id)
		if err != nil || r.failed || r.abandoned || order[r.state] >= order[protocol.StateCleanup] {
			return nil, err
		}
		// A lease that lapsed before this process started gets one lease to be renewed.
		if r.leaseExpiresAt >= millis(o.now()) || (o.settling() && r.leaseExpiresAt <= millis(o.started)) {
			return nil, nil
		}
		return r, nil
	}
	for _, id := range ids {
		err := o.decideThenWrite(ctx, func(q querier) (bool, error) { r, err := lapsed(q, id); return r != nil, err }, func(tx *sql.Tx) error {
			r, err := lapsed(tx, id)
			if err != nil || r == nil {
				return err
			}
			o.log.Warn("lease expired; abandoning the run", "run", id)
			_, err = o.abandon(ctx, tx, r, "orchestrator", "the suite's lease expired, so the orchestrator abandons the run")
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// CloseSettled closes the runs in cleanup that have finished.
func (o *Orchestrator) CloseSettled(ctx context.Context) error {
	if err := o.markLapsed(ctx); err != nil {
		return err
	}
	ids, err := o.runsIn(ctx, o.ro, protocol.StateCleanup)
	if err != nil {
		return err
	}
	for _, id := range ids {
		err := o.decideThenWrite(ctx, func(q querier) (bool, error) { _, _, ok, err := o.closable(ctx, q, id); return ok, err },
			func(tx *sql.Tx) error { return o.maybeClose(ctx, tx, id) })
		if err != nil {
			return err
		}
	}
	return nil
}

// walLimit is how long the write-ahead log may grow before a checkpoint waits for the readers.
var walLimit int64 = 64 << 20

// checkpoint moves what the write-ahead log holds into the database, on the checkpointer's own
// connection. PASSIVE never waits: it runs beside the writer, which goes on committing. A
// checkpoint on the writer's connection held every write back while it copied, seconds at a time
// at a hundred runs at once.
//
// With readers always reading, PASSIVE may never reach the log's end, and the log would grow
// without bound, slowing every read. Once it is past walLimit, TRUNCATE waits for the readers
// still on older snapshots, which take milliseconds, and starts the log again from nothing. It
// holds new writes back while it waits, for checkpointPatience at most; a checkpoint that could
// not finish in that time tries again on the next tick.
func (o *Orchestrator) checkpoint(ctx context.Context) error {
	if o.ro == o.db {
		return nil // one connection: SQLite checkpoints on its own after commits
	}
	mode := "PASSIVE"
	if fi, err := os.Stat(o.cfg.DB + "-wal"); err == nil && fi.Size() > walLimit {
		mode = "TRUNCATE"
	}
	_, err := o.cp.ExecContext(ctx, `pragma wal_checkpoint(`+mode+`)`)
	if mode == "TRUNCATE" && isBusy(err) {
		return nil // the readers did not clear in time: the next tick tries again
	}
	return err
}

// isBusy is SQLite saying it could not get a lock in time.
func isBusy(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked"))
}
