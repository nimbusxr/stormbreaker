package orchestrator

// The orchestrator's metrics, for an operator's Prometheus: what its runs are doing, what its
// store takes, and what its volume has left (ADR 0016). They are counts, without the names of
// runs or teams.

import (
	"fmt"
	"net/http"
	rtmetrics "runtime/metrics"
	"strings"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

func (o *Orchestrator) metrics(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	var b strings.Builder
	gauge := func(name, help string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	}

	gauge("stormbreaker_runs", "Runs the orchestrator keeps, by state.")
	byState := map[string]int64{}
	for _, s := range []string{protocol.StateOpened, protocol.StateTrafficComplete, protocol.StateSettled, protocol.StateCleanup, protocol.StateClosed} {
		byState[s] = 0
	}
	rows, err := o.ro.QueryContext(ctx, `select state, count(*) from runs group by state`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var s string
		var n int64
		if err := rows.Scan(&s, &n); err != nil {
			rows.Close()
			return err
		}
		byState[s] = n
	}
	rows.Close()
	for _, s := range []string{protocol.StateOpened, protocol.StateTrafficComplete, protocol.StateSettled, protocol.StateCleanup, protocol.StateClosed} {
		fmt.Fprintf(&b, "stormbreaker_runs{state=%q} %d\n", s, byState[s])
	}

	one := func(name, help, query string, args ...any) error {
		var n int64
		if err := o.ro.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			return err
		}
		gauge(name, help)
		fmt.Fprintf(&b, "%s %d\n", name, n)
		return nil
	}
	for _, m := range []struct{ name, help, query string }{
		{"stormbreaker_runs_holding", "Runs holding their groups' dispatchers: no other run opens on them.", `select count(*) from runs where state != 'closed' and released = 0`},
		{"stormbreaker_runs_cleanup_past_hold", "Runs still cleaning up after the cleanup hold: their cleanup has not verified.", `select count(*) from runs where state = 'cleanup' and released = 1`},
		{"stormbreaker_runs_unfinished", "Runs kept that closed at the cleanup deadline, some instances never completing.", `select count(*) from runs where unfinished = 1`},
		{"stormbreaker_runs_failed", "Runs kept that failed at their readiness gate.", `select count(*) from runs where failed = 1`},
		{"stormbreaker_runs_abandoned", "Runs kept that their suites abandoned.", `select count(*) from runs where abandoned = 1`},
		{"stormbreaker_claims_open", "Identities claimed into the footprints of runs not yet closed.", `select coalesce(sum(claims), 0) from runs where state != 'closed'`},
		{"stormbreaker_claims_kept", "Identities claimed into every footprint the store still holds.", `select coalesce(sum(claims), 0) from runs where footprint_pruned = 0`},
	} {
		if err := one(m.name, m.help, m.query); err != nil {
			return err
		}
	}

	gauge("stormbreaker_notifications", "Notifications, by how their delivery stands.")
	for _, s := range []string{"pending", "sending", "exhausted"} {
		var n int64
		if err := o.ro.QueryRowContext(ctx, `select count(*) from deliveries where status = ?`, s).Scan(&n); err != nil {
			return err
		}
		fmt.Fprintf(&b, "stormbreaker_notifications{status=%q} %d\n", s, n)
	}
	gauge("stormbreaker_dispatcher_instances", "Registered dispatcher instances, by whether their lease is live.")
	var live, lapsed int64
	if err := o.ro.QueryRowContext(ctx, `select coalesce(sum(lease_expires_at > ?), 0), coalesce(sum(lease_expires_at <= ?), 0) from dispatchers`,
		millis(o.now()), millis(o.now())).Scan(&live, &lapsed); err != nil {
		return err
	}
	fmt.Fprintf(&b, "stormbreaker_dispatcher_instances{lease=\"live\"} %d\nstormbreaker_dispatcher_instances{lease=\"lapsed\"} %d\n", live, lapsed)

	size, reusable, err := o.storeSize(ctx)
	if err != nil {
		return err
	}
	gauge("stormbreaker_store_bytes", "The database's size on the volume.")
	fmt.Fprintf(&b, "stormbreaker_store_bytes %d\n", size)
	gauge("stormbreaker_store_reusable_bytes", "Of that, pages freed by pruning and free for reuse.")
	fmt.Fprintf(&b, "stormbreaker_store_reusable_bytes %d\n", reusable)
	if free, ok := o.room(); ok {
		gauge("stormbreaker_volume_free_bytes", "Free bytes on the database's volume.")
		fmt.Fprintf(&b, "stormbreaker_volume_free_bytes %d\n", free)
	}
	gauge("stormbreaker_volume_reserve_bytes", "The room the orchestrator keeps: below it, it takes no new runs or declarations.")
	fmt.Fprintf(&b, "stormbreaker_volume_reserve_bytes %d\n", o.cfg.DiskReserve)

	// The writer: every change is a transaction on its one connection. The rate of its busy
	// seconds is how much of the time it is in use; near 1, the orchestrator has reached what one
	// writer on this volume can do, and requests queue for it (wait).
	counter := func(name, help string, v float64) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n%s %g\n", name, help, name, name, v)
	}
	counter("stormbreaker_writes_total", "Write transactions committed or rolled back.", float64(o.writes.n.Load()))
	counter("stormbreaker_writer_busy_seconds_total", "Seconds the writer spent in transactions: its rate is the writer's utilization.", time.Duration(o.writes.busy.Load()).Seconds())
	counter("stormbreaker_writer_wait_seconds_total", "Seconds requests waited for the writer, all of them together.", time.Duration(o.writes.wait.Load()).Seconds())

	// The process's own memory, as the Go runtime has it: what a container's limit must hold,
	// besides the page cache the kernel gives back under pressure.
	mem := []rtmetrics.Sample{{Name: "/memory/classes/total:bytes"}, {Name: "/memory/classes/heap/objects:bytes"}}
	rtmetrics.Read(mem)
	gauge("stormbreaker_process_memory_bytes", "Memory the orchestrator's Go runtime has from the system.")
	fmt.Fprintf(&b, "stormbreaker_process_memory_bytes %d\n", mem[0].Value.Uint64())
	gauge("stormbreaker_heap_bytes", "Of that, live and unswept heap objects.")
	fmt.Fprintf(&b, "stormbreaker_heap_bytes %d\n", mem[1].Value.Uint64())

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, err = w.Write([]byte(b.String()))
	return err
}
