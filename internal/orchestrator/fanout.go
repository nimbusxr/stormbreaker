package orchestrator

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// due is a notification due to be sent.
type due struct {
	seq              int64
	dispatcher, inst string
	attempts         int
	event            protocol.Event
	callback, secret string
	known            bool
}

// result is how sending one went. A deferred notification was not attempted, because one
// before it to the same instance failed.
type result struct {
	due
	ok, deferred bool
}

// Deliver sends the notifications that are due, signed, at most FanoutWorkers at a time, and
// records how each went: delivered, to try again later with backoff, or exhausted, which makes
// the instance unreachable for that run unless it is still reporting (through the event feed). Nothing is dropped silently, and the event feed is
// there for whatever a dispatcher missed.
func (o *Orchestrator) Deliver(ctx context.Context) error {
	batch, err := o.dueDeliveries(ctx, 256)
	if err != nil || len(batch) == 0 {
		return err
	}
	// In order for each instance, and the instances side by side. Once one notification to an
	// instance fails, the rest of the batch for it waits for the next attempt, so it never hears
	// of a later event before an earlier one.
	results := make([]result, len(batch))
	byInstance := map[[2]string][]int{}
	var instances [][2]string
	for i, d := range batch {
		results[i] = result{due: d}
		k := [2]string{d.dispatcher, d.inst}
		if _, ok := byInstance[k]; !ok {
			instances = append(instances, k)
		}
		byInstance[k] = append(byInstance[k], i)
	}
	sem := make(chan struct{}, o.cfg.FanoutWorkers)
	var wg sync.WaitGroup
	for _, k := range instances {
		wg.Add(1)
		sem <- struct{}{}
		go func(indexes []int) {
			defer wg.Done()
			defer func() { <-sem }()
			for _, i := range indexes {
				d := batch[i]
				if !d.known {
					results[i].ok = true // no such instance any more: nobody to tell
					continue
				}
				if results[i].ok = o.send(ctx, d); !results[i].ok {
					for _, rest := range indexes[slices.Index(indexes, i)+1:] {
						results[rest].deferred = true
					}
					return
				}
			}
		}(byInstance[k])
	}
	wg.Wait()
	return o.tx(ctx, func(tx *sql.Tx) error {
		for _, r := range results {
			if err := o.record(ctx, tx, r); err != nil {
				return err
			}
		}
		return nil
	})
}

// dueDeliveries claims the notifications due now: they are marked sending in the same
// transaction, so an event appended meanwhile is never folded into one already on its way
// (appendEvent). A crash leaves them sending; Open puts them back.
func (o *Orchestrator) dueDeliveries(ctx context.Context, limit int) ([]due, error) {
	// Most ticks find nothing due: they ask a reader, and take the writer only when there is.
	var any bool
	if err := o.ro.QueryRowContext(ctx, `select exists(select 1 from deliveries where status = 'pending' and next_attempt_at <= ?)`, millis(o.now())).Scan(&any); err != nil || !any {
		return nil, err
	}
	var out []due
	err := o.tx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `update deliveries set status = 'sending' where rowid in (
			select rowid from deliveries where status = 'pending' and next_attempt_at <= ? order by event_seq limit ?)
			returning event_seq, dispatcher, instance, attempts`, millis(o.now()), limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var d due
			if err := rows.Scan(&d.seq, &d.dispatcher, &d.inst, &d.attempts); err != nil {
				rows.Close()
				return err
			}
			out = append(out, d)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range out {
			d := &out[i]
			var at int64
			if err := tx.QueryRowContext(ctx, `select e.id, e.run_id, e.type, e.at, coalesce(x.callback_url, ''), coalesce(x.secret, ''), x.name is not null
				from events e left join dispatchers x on x.name = ? and x.instance = ? where e.seq = ?`, d.dispatcher, d.inst, d.seq).
				Scan(&d.event.ID, &d.event.RunID, &d.event.Type, &at, &d.callback, &d.secret, &d.known); err != nil {
				return err
			}
			d.event.Time = fromMillis(at)
			d.event.Ref = "/runs/" + d.event.RunID
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out, err
}

// send posts one notification and reports whether it was received: any 2xx.
func (o *Orchestrator) send(ctx context.Context, d due) bool {
	body, err := json.Marshal(d.event)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, o.cfg.DeliveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.callback, bytes.NewReader(body))
	if err != nil {
		o.log.Warn("notification has a bad callback URL", "dispatcher", d.dispatcher, "instance", d.inst, "err", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if err := protocol.Sign(req.Header, d.secret, d.event.ID, body, o.now()); err != nil {
		o.log.Warn("cannot sign notification", "dispatcher", d.dispatcher, "instance", d.inst, "err", err)
		return false
	}
	resp, err := o.http.Do(req)
	if err != nil {
		o.log.Warn("notification failed", "event", d.event.ID, "dispatcher", d.dispatcher, "instance", d.inst, "err", err)
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		o.log.Warn("notification refused", "event", d.event.ID, "dispatcher", d.dispatcher, "instance", d.inst, "status", resp.StatusCode)
		return false
	}
	return true
}

func (o *Orchestrator) record(ctx context.Context, tx *sql.Tx, r result) error {
	key := []any{r.seq, r.dispatcher, r.inst}
	if r.deferred {
		// Not tried this pass, behind one that failed: due again, as it was.
		_, err := tx.ExecContext(ctx, `update deliveries set status = 'pending' where event_seq = ? and dispatcher = ? and instance = ? and status = 'sending'`, key...)
		return err
	}
	if r.ok {
		if _, err := tx.ExecContext(ctx, `update deliveries set status = 'delivered' where event_seq = ? and dispatcher = ? and instance = ?`, key...); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `update run_dispatchers set state = ? where run_id = ? and name = ? and instance = ? and state = ?`,
			protocol.InstanceAcknowledged, r.event.RunID, r.dispatcher, r.inst, protocol.InstanceNotified)
		return err
	}
	attempts := r.attempts + 1
	if attempts >= o.cfg.MaxAttempts {
		if _, err := tx.ExecContext(ctx, `update deliveries set attempts = ?, status = 'exhausted' where event_seq = ? and dispatcher = ? and instance = ?`,
			append([]any{attempts}, key...)...); err != nil {
			return err
		}
		// An instance that keeps reporting is reading the run some other way (the event feed), so
		// failed notifications alone do not make it unreachable: only with its silence too.
		quietSince := millis(o.now().Add(-o.cfg.StaleAfter))
		res, err := tx.ExecContext(ctx, `update run_dispatchers set state = ?, detail = ? where run_id = ? and name = ? and instance = ? and state not in (?, ?)
			and (last_report_at is null or last_report_at < ?)`,
			protocol.InstanceUnreachable, "notifications stopped reaching it", r.event.RunID, r.dispatcher, r.inst, protocol.InstanceCompleted, protocol.InstanceUnreachable, quietSince)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			o.log.Warn("notification exhausted; instance unreachable", "run", r.event.RunID, "dispatcher", r.dispatcher, "instance", r.inst)
			return o.logRun(ctx, tx, r.event.RunID, "orchestrator", r.dispatcher+"/"+r.inst+" did not receive "+r.event.Type+" after "+
				strconv.Itoa(attempts)+" attempts; it is unreachable")
		}
		o.log.Warn("notification exhausted; the instance is still reporting", "run", r.event.RunID, "dispatcher", r.dispatcher, "instance", r.inst)
		return o.logRun(ctx, tx, r.event.RunID, "orchestrator", r.dispatcher+"/"+r.inst+" did not receive "+r.event.Type+" after "+
			strconv.Itoa(attempts)+" attempts, but it is still reporting, so it reads the run from the event feed")
	}
	next := o.now().Add(o.backoff(attempts))
	_, err := tx.ExecContext(ctx, `update deliveries set status = 'pending', attempts = ?, next_attempt_at = ? where event_seq = ? and dispatcher = ? and instance = ?`,
		append([]any{attempts, millis(next)}, key...)...)
	return err
}

// backoff is the wait before attempt n+1: RetryBase doubled each time, at most RetryMax, give
// or take a fifth so that many instances do not retry in step.
func (o *Orchestrator) backoff(attempts int) time.Duration {
	d := o.cfg.RetryBase
	for i := 1; i < attempts && d < o.cfg.RetryMax; i++ {
		d *= 2
	}
	d = min(d, o.cfg.RetryMax)
	jitter := time.Duration(rand.Int64N(int64(d)/5+1)) - d/10
	return d + jitter
}
