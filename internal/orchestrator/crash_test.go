//go:build scale

package orchestrator_test

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	_ "github.com/nimbusxr/stormbreaker/internal/orchestrator" // the sqlite driver, to read the database after each crash
	"github.com/nimbusxr/stormbreaker/protocol"
)

// TestKillNineLosesNothingAcknowledged runs a real orchestrator under load, kills it with SIGKILL at
// a random moment, and checks what it left: the database whole, every acknowledged write in it,
// every run's counters agreeing with its footprint, and the orchestrator serving again on it.
// Rounds of it, on one database.
//
//	STORMBREAKER_CRASH_ROUNDS=20 go test -tags scale -run KillNine -timeout 30m -v ./internal/orchestrator
func TestKillNineLosesNothingAcknowledged(t *testing.T) {
	rounds := 12
	if s := os.Getenv("STORMBREAKER_CRASH_ROUNDS"); s != "" {
		rounds, _ = strconv.Atoi(s)
	}
	work := t.TempDir()
	bin := filepath.Join(work, "stormbreaker")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/stormbreaker")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	db := filepath.Join(work, "o.db")
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer receiver.Close()
	secret, _ := protocol.NewSecret()

	// What the orchestrator acknowledged, by run, across every round.
	var mu sync.Mutex
	acked := map[string]int64{}     // identities in acknowledged footprint reports
	attempted := map[string]int64{} // in every report sent, acknowledged or not
	ackedState := map[string]string{}
	order := map[string]int{protocol.StateOpened: 0, protocol.StateTrafficComplete: 1, protocol.StateSettled: 2, protocol.StateCleanup: 3}
	var runs []string
	owner := map[string]string{} // each run's dispatcher: one run at a time holds a dispatcher
	var total atomic.Int64

	for round := range rounds {
		addr := freeAddr(t)
		cmd := exec.Command(bin, "orchestrator", "serve", "--db", db, "--addr", addr, "--token", "tok")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		c := protocol.NewClient("http://"+addr, "tok")
		c.HTTP = &http.Client{Timeout: 5 * time.Second}
		waitUp(t, c)
		ctx, stop := context.WithCancel(context.Background())
		// Runs from earlier rounds go on where they were; new ones join them, each with a
		// dispatcher and a group of its own.
		mu.Lock()
		names := map[string]bool{}
		for _, d := range owner {
			names[d] = true
		}
		mu.Unlock()
		for k := range 2 {
			names[fmt.Sprintf("tracking%d-%d", round, k)] = true
		}
		for name := range names {
			if _, err := c.Register(ctx, protocol.Registration{Name: name, Instance: "shared", Team: "t", CallbackURL: receiver.URL, Secret: secret}); err != nil {
				t.Fatal(err)
			}
		}
		for k := range 2 {
			name := fmt.Sprintf("tracking%d-%d", round, k)
			if err := c.JoinGroup(ctx, name, name); err != nil {
				t.Fatal(err)
			}
			r, err := c.OpenRun(ctx, protocol.OpenRun{Initiator: "crash", Group: name, Requires: []string{name}})
			if err != nil {
				t.Fatalf("round %d: open a run: %v", round, err)
			}
			mu.Lock()
			runs = append(runs, r.ID)
			owner[r.ID] = name
			ackedState[r.ID] = protocol.StateOpened
			mu.Unlock()
		}
		var wg sync.WaitGroup
		for w := range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rng := rand.New(rand.NewPCG(uint64(round), uint64(w)))
				for n := 0; ctx.Err() == nil; n++ {
					mu.Lock()
					id := runs[rng.IntN(len(runs))]
					name := owner[id]
					mu.Unlock()
					switch k := rng.IntN(20); {
					case k < 14: // a footprint report of fresh identities
						size := 1 + rng.IntN(2000)
						ids := make([]string, size)
						for i := range ids {
							ids[i] = fmt.Sprintf("%s-r%d-w%d-%d-%d", id, round, w, n, i)
						}
						mu.Lock()
						attempted[id] += int64(size)
						mu.Unlock()
						if _, err := c.ReportFootprint(ctx, id, protocol.Suite, "", protocol.FootprintReport{Entries: []protocol.FootprintEntry{
							{Store: "parcels", Locator: "/parcels", Identities: ids, Resolution: protocol.ResolutionExpected},
						}}); err == nil {
							mu.Lock()
							acked[id] += int64(size)
							mu.Unlock()
							total.Add(1)
						}
					case k < 17: // the dispatcher reports, on the version it read
						if run, err := c.Run(ctx, id); err == nil {
							_ = c.ReportStatus(ctx, id, name, "shared", protocol.Status{State: protocol.StatusPrepared, FootprintVersion: run.FootprintVersion})
						}
					case k < 19:
						_ = c.Heartbeat(ctx, id)
					default: // the run moves on
						mu.Lock()
						cur := ackedState[id]
						mu.Unlock()
						next := map[string]string{protocol.StateOpened: protocol.EventTrafficComplete, protocol.StateTrafficComplete: protocol.EventSettled, protocol.StateSettled: protocol.EventCleanupReady}[cur]
						if next == "" {
							continue
						}
						if _, err := c.Publish(ctx, id, next); err == nil {
							mu.Lock()
							if s := map[string]string{protocol.EventTrafficComplete: protocol.StateTrafficComplete, protocol.EventSettled: protocol.StateSettled, protocol.EventCleanupReady: protocol.StateCleanup}[next]; order[s] > order[ackedState[id]] {
								ackedState[id] = s
							}
							mu.Unlock()
						}
					}
				}
			}()
		}
		time.Sleep(time.Duration(300+rand.IntN(1500)) * time.Millisecond)
		_ = cmd.Process.Signal(syscall.SIGKILL)
		_ = cmd.Wait()
		stop()
		wg.Wait()
		check(t, round, db, &mu, acked, attempted, ackedState, order)
	}
	t.Logf("%d rounds, %d acknowledged footprint reports in %d runs: nothing acknowledged was lost", rounds, total.Load(), len(runs))
}

func check(t *testing.T, round int, path string, mu *sync.Mutex, acked, attempted map[string]int64, ackedState map[string]string, order map[string]int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var ok string
	if err := db.QueryRow(`pragma integrity_check`).Scan(&ok); err != nil || ok != "ok" {
		t.Fatalf("round %d: integrity_check: %q, %v", round, ok, err)
	}
	var orphans int
	if err := db.QueryRow(`select (select count(*) from footprint_entries e left join footprint_blobs b on b.entry_id = e.id where b.entry_id is null)
		+ (select count(*) from footprint_blobs b left join footprint_entries e on e.id = b.entry_id where e.id is null)
		+ (select count(*) from deliveries d left join events e on e.seq = d.event_seq where e.seq is null)`).Scan(&orphans); err != nil || orphans != 0 {
		t.Fatalf("round %d: %d rows that belong to nothing, %v", round, orphans, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for id, n := range acked {
		var claims, summed int64
		var state string
		if err := db.QueryRow(`select claims, state, (select coalesce(sum(identity_count), 0) from footprint_entries where run_id = ?) from runs where id = ?`, id, id).Scan(&claims, &state, &summed); err != nil {
			t.Fatalf("round %d: run %s: %v", round, id, err)
		}
		switch {
		case claims != summed:
			t.Errorf("round %d: run %s counts %d claims, and its footprint holds %d", round, id, claims, summed)
		case claims < n:
			t.Errorf("round %d: run %s holds %d claims; %d were acknowledged", round, id, claims, n)
		case claims > attempted[id]:
			t.Errorf("round %d: run %s holds %d claims; only %d were ever sent", round, id, claims, attempted[id])
		}
		if order[state] < order[ackedState[id]] && state != protocol.StateClosed {
			t.Errorf("round %d: run %s is %s; %s was acknowledged", round, id, state, ackedState[id])
		}
		// What was acknowledged and what was sent since may differ only by what was in flight.
		acked[id] = claims
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func waitUp(t *testing.T, c *protocol.Client) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.Groups(context.Background()); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the orchestrator did not come up")
}
