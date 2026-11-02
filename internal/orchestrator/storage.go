package orchestrator

// The orchestrator's storage (ADR 0016): how much its store takes, how much room its volume has,
// the reserve it keeps so that a full disk never breaks a run halfway, and how closed runs give
// their room back.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// room is the volume's free bytes, looked at on every write that grows the store: one system
// call, and a reserve smaller than a second of writes is still never overrun.
func (o *Orchestrator) room() (uint64, bool) {
	if o.cfg.DB == ":memory:" {
		return 0, false
	}
	return volumeFree(filepath.Dir(o.cfg.DB))
}

// mustHaveRoom refuses what grows the store while the volume is below the reserve. What answers
// for the store (removals, statuses, closing) never asks, so cleanup goes on.
func (o *Orchestrator) mustHaveRoom(what string) error {
	if o.cfg.DiskReserve <= 0 {
		return nil
	}
	free, ok := o.room()
	if !ok || free >= uint64(o.cfg.DiskReserve) {
		return nil
	}
	return refuse(http.StatusInsufficientStorage, "the orchestrator's volume has %s free, below its reserve of %s: it takes no %s until room is made; "+
		"removals, statuses and closing still go through", sizeOf(int64(free)), sizeOf(o.cfg.DiskReserve), what)
}

// storeSize is the database's size and, of that, the pages free for reuse.
func (o *Orchestrator) storeSize(ctx context.Context) (size, reusable int64, err error) {
	var pages, pageSize, free int64
	for q, into := range map[string]*int64{`pragma page_count`: &pages, `pragma page_size`: &pageSize, `pragma freelist_count`: &free} {
		if err := o.ro.QueryRowContext(ctx, q).Scan(into); err != nil {
			return 0, 0, err
		}
	}
	return pages * pageSize, free * pageSize, nil
}

// footprintKept refuses to read a run's identities once the footprint retention dropped them.
func (o *Orchestrator) footprintKept(ctx context.Context, runID string) error {
	if _, err := o.runRow(ctx, o.ro, runID); err != nil {
		return err
	}
	var pruned bool
	if err := o.ro.QueryRowContext(ctx, `select footprint_pruned from runs where id = ?`, runID).Scan(&pruned); err != nil {
		return err
	}
	if pruned {
		return refuse(http.StatusGone, "run %s's identities were dropped %s after it closed (the footprint retention); its journal still counts them by place, "+
			"and its streams can generate them again", runID, o.cfg.FootprintRetention)
	}
	return nil
}

// pruneBatch is how many reports a pruning transaction deletes: a run of a billion identities is
// tens of thousands of reports, and one transaction for all of them would grow the write-ahead
// log by the whole run.
const pruneBatch = 2_000

// deleteInBatches runs stmt, which deletes at most pruneBatch rows of what it names, until it
// deletes nothing.
func (o *Orchestrator) deleteInBatches(ctx context.Context, stmt string, args ...any) error {
	for {
		var n int64
		err := o.tx(ctx, func(tx *sql.Tx) error {
			res, err := tx.ExecContext(ctx, stmt, args...)
			if err != nil {
				return err
			}
			n, err = res.RowsAffected()
			return err
		})
		if err != nil || n == 0 {
			return err
		}
	}
}

// Prune gives closed runs' room back. A run closed longer ago than the footprint retention loses
// its identities (its journal still counts them by place), and one closed longer ago than the
// retention goes with everything recorded about it. Both go a batch at a time, and the freed
// pages go back to the volume.
func (o *Orchestrator) Prune(ctx context.Context) (int, error) {
	closedBefore := func(d time.Duration) int64 { return millis(o.now().Add(-d)) }
	kept, err := strings0(o.db.QueryContext(ctx, `select id from runs where state = ? and footprint_pruned = 0 and coalesce(closed_at, opened_at) < ?`,
		"closed", closedBefore(o.cfg.FootprintRetention)))
	if err != nil {
		return 0, err
	}
	for _, id := range kept {
		if err := o.deleteInBatches(ctx, `delete from footprint_blobs where entry_id in (select e.id from footprint_entries e
			join footprint_blobs b on b.entry_id = e.id where e.run_id = ? limit `+fmt.Sprint(pruneBatch)+`)`, id); err != nil {
			return 0, err
		}
		if err := o.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `update runs set footprint_pruned = 1 where id = ?`, id); err != nil {
				return err
			}
			return o.logRun(ctx, tx, id, "orchestrator", fmt.Sprintf("its identities are dropped, %s after it closed (the footprint retention); "+
				"what it claimed, by place, stays here until it goes after %s", o.cfg.FootprintRetention, o.cfg.Retention))
		}); err != nil {
			return 0, err
		}
	}
	gone, err := strings0(o.db.QueryContext(ctx, `select id from runs where state = ? and coalesce(closed_at, opened_at) < ?`,
		"closed", closedBefore(o.cfg.Retention)))
	if err != nil {
		return 0, err
	}
	for _, id := range gone {
		batch := fmt.Sprint(pruneBatch)
		for _, stmt := range []string{
			`delete from footprint_blobs where entry_id in (select e.id from footprint_entries e join footprint_blobs b on b.entry_id = e.id where e.run_id = ? limit ` + batch + `)`,
			`delete from footprint_entries where id in (select id from footprint_entries where run_id = ? limit ` + batch + `)`,
			`delete from deliveries where rowid in (select d.rowid from deliveries d join events e on e.seq = d.event_seq where e.run_id = ? limit ` + batch + `)`,
		} {
			if err := o.deleteInBatches(ctx, stmt, id); err != nil {
				return 0, err
			}
		}
		if err := o.tx(ctx, func(tx *sql.Tx) error {
			for _, stmt := range []string{
				`delete from events where run_id = ?`,
				`delete from run_dispatchers where run_id = ?`,
				`delete from run_streams where run_id = ?`,
				`delete from run_log where run_id = ?`,
				`delete from runs where id = ?`,
			} {
				if _, err := tx.ExecContext(ctx, stmt, id); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return 0, err
		}
	}
	if len(kept)+len(gone) > 0 {
		// Give the freed pages back to the volume, where the database was made to (auto_vacuum
		// incremental); elsewhere this does nothing, and the pages are reused instead.
		if _, err := o.db.ExecContext(ctx, `pragma incremental_vacuum`); err != nil {
			return 0, err
		}
	}
	return len(gone), nil
}

// sizeOf is a size as a person reads it.
func sizeOf(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Runs lists runs, newest first: those in any of states (all if none), in group (any if empty),
// opened before before (Unix milliseconds; now if zero), at most limit.
func (o *Orchestrator) Runs(ctx context.Context, states []string, group string, before int64, limit int) (*protocol.RunList, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := `select id, group_name, state, initiator, failed, abandoned, released, unfinished, claims, opened_at, cleanup_at, closed_at from runs where 1 = 1`
	var args []any
	if len(states) > 0 {
		q += ` and state in (` + strings.TrimSuffix(strings.Repeat("?, ", len(states)), ", ") + `)`
		for _, s := range states {
			args = append(args, s)
		}
	}
	if group != "" {
		q += ` and group_name = ?`
		args = append(args, group)
	}
	if before > 0 {
		q += ` and opened_at < ?`
		args = append(args, before)
	}
	q += ` order by opened_at desc limit ?`
	args = append(args, limit+1)
	rows, err := o.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := &protocol.RunList{Runs: []protocol.RunSummary{}}
	for rows.Next() {
		var s protocol.RunSummary
		var released bool
		var opened int64
		var cleanup, closed sql.NullInt64
		if err := rows.Scan(&s.ID, &s.Group, &s.State, &s.Initiator, &s.Failed, &s.Abandoned, &released, &s.Unfinished, &s.Claims, &opened, &cleanup, &closed); err != nil {
			return nil, err
		}
		s.OpenedAt, s.CleanupAt, s.ClosedAt = fromMillis(opened), nullMillis(cleanup), nullMillis(closed)
		s.Holds = s.State != protocol.StateClosed && !released
		list.Runs = append(list.Runs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(list.Runs) > limit {
		list.Runs = list.Runs[:limit]
		list.Before = millis(list.Runs[limit-1].OpenedAt)
	}
	return list, nil
}
