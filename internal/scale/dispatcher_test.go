//go:build scale

package scale

import (
	"context"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/labtest"
	"github.com/nimbusxr/stormbreaker/internal/orchestrator"
	"github.com/nimbusxr/stormbreaker/protocol"
)

// TestDispatcherAtScale is the tracking team's dispatcher answering for a run of 60 million
// parcels (STORMBREAKER_SCALE_IDENTITIES): its prepare round derives a tracking record for every
// parcel the suite declared, a round after it settles, and its cleanup removes them all, each
// through the streaming hooks, holding nothing.
//
//	go test -tags scale -run DispatcherAtScale -timeout 60m -v ./internal/scale
func TestDispatcherAtScale(t *testing.T) {
	n := identities()
	// The gate is measured here, not held to the default readiness timeout (two minutes): how
	// long it takes at this size is what the test is for.
	limit := patience(n)
	var dbPath string
	lab := labtest.New(t, func(c *orchestrator.Config) { c.ReadinessTimeout = limit; dbPath = c.DB })
	lab.Client.HTTP = &http.Client{Timeout: 5 * time.Minute}
	heap := watchHeap()
	var declared, removed, rounds, cleanups, lastRound atomic.Int64
	lab.Start("tracking", "shared", []string{"delivery"}, dispatcher.Hooks{
		Prepare: func(ctx context.Context, run *protocol.Run, fp dispatcher.Footprint, declare dispatcher.Report) error {
			declared.Store(0)
			rounds.Add(1)
			began := time.Now()
			defer func() { lastRound.Store(int64(time.Since(began))) }()
			for c, err := range fp(ctx, walkOnly(protocol.FootprintFilter{Dispatchers: []string{protocol.Suite}})) {
				if err != nil {
					return err
				}
				if c.Dispatcher != protocol.Suite {
					continue
				}
				declared.Add(1)
				if err := declare(protocol.FootprintEntry{Store: "tracking-db", Locator: "tracking", Identities: []string{"TRK-" + c.Identity}}); err != nil {
					return err
				}
			}
			return nil
		},
		Cleanup: func(ctx context.Context, run *protocol.Run, fp dispatcher.Footprint, report dispatcher.Report) (bool, error) {
			removed.Store(0)
			cleanups.Add(1)
			for c, err := range fp(ctx, walkOnly(protocol.FootprintFilter{Dispatchers: []string{"tracking"}, Store: "tracking-db"})) {
				if err != nil {
					return false, err
				}
				if c.Dispatcher != "tracking" || c.Store != "tracking-db" {
					continue
				}
				removed.Add(1)
				if err := report(protocol.FootprintEntry{Store: c.Store, Locator: c.Locator, Identities: []string{c.Identity}, Resolution: protocol.ResolutionRemovedVerified}); err != nil {
					return false, err
				}
			}
			return true, nil
		},
	})
	ctx := context.Background()
	run, err := lab.Client.OpenRun(ctx, protocol.OpenRun{Initiator: "scale", Group: "delivery", Requires: []string{"tracking"}})
	if err != nil {
		t.Fatal(err)
	}
	// The suite renews its lease throughout, as stormbreaker-core does: a run whose suite goes
	// quiet is abandoned and cleaned up.
	beat, stopBeat := context.WithCancel(ctx)
	defer stopBeat()
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-beat.Done():
				return
			case <-tick.C:
				if err := lab.Client.Heartbeat(beat, run.ID); err != nil && beat.Err() == nil {
					t.Logf("heartbeat: %v", err)
				}
			}
		}
	}()
	begin := time.Now()
	const batch = 25_000
	ids := make([]string, 0, batch)
	for i := range n {
		ids = append(ids, uuidish(i))
		if len(ids) == batch || i == n-1 {
			if _, err := lab.Client.ReportFootprint(ctx, run.ID, protocol.Suite, "", protocol.FootprintReport{Entries: []protocol.FootprintEntry{
				{Store: "parcels", Locator: "/parcels", Identities: ids, Resolution: protocol.ResolutionExpected},
			}}); err != nil {
				t.Fatal(err)
			}
			ids = ids[:0]
		}
	}
	suite := time.Since(begin)

	wait := func(what string, limit time.Duration, ok func(*protocol.Run) bool) time.Duration {
		start := time.Now()
		for {
			r, err := lab.Client.Run(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if ok(r) {
				return time.Since(start)
			}
			if r.Done() || r.Failed || r.Abandoned {
				j, _ := lab.Client.Journal(ctx, run.ID)
				t.Fatalf("waiting for %s, the run ended (%s, failed %v, abandoned %v):\n%s", what, r.State, r.Failed, r.Abandoned, j)
			}
			if time.Since(start) > limit {
				t.Fatalf("waited %s for %s: %+v", limit, what, r)
			}
			if os.Getenv("STORMBREAKER_SCALE_DEBUG") != "" {
				t.Logf("%s: state %s claims %d gate %+v, %d prepare rounds, %d cleanup rounds, dispatchers %+v", what, r.State, r.Claims, r.Gate, rounds.Load(), cleanups.Load(), r.Dispatchers)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	gate := wait("the gate", limit, func(r *protocol.Run) bool { return r.Gate.Open })
	declaredSeen, prepareRounds, finalRound := declared.Load(), rounds.Load(), time.Duration(lastRound.Load())
	atGate := onDisk(dbPath)
	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		if _, err := lab.Client.Publish(ctx, run.ID, ev); err != nil {
			t.Fatal(err)
		}
	}
	closeTime := wait("the run to close", limit, func(r *protocol.Run) bool { return r.Done() })
	r, _ := lab.Client.Run(ctx, run.ID)
	peak := heap.end()

	t.Logf("identities the suite declared   %d, in %s", n, suite.Round(time.Millisecond))
	t.Logf("prepare: derived and declared   %d, the gate open %s later, in %d rounds", declaredSeen, gate.Round(time.Millisecond), prepareRounds)
	t.Logf("the last prepare round took     %s", finalRound.Round(time.Millisecond))
	t.Logf("gate open after the run opened  %s", (suite + gate).Round(time.Millisecond))
	t.Logf("cleanup: removed and verified   %d, the run closed %s after cleanup opened, in %d rounds", removed.Load(), closeTime.Round(time.Millisecond), cleanups.Load())
	t.Logf("claims answered for             %d", r.Claims)
	t.Logf("database on disk                %s at the gate, %s at close (with its WAL)", mb(uint64(atGate)), mb(uint64(onDisk(dbPath))))
	t.Logf("heap at its peak                %s (orchestrator, dispatcher and suite in one process)", mb(peak))
	j, _ := lab.Client.Journal(ctx, run.ID)
	t.Logf("journal                         %d bytes, %d lines:\n%s", len(j), strings.Count(j, "\n"), j)
	if declaredSeen != int64(n) || removed.Load() != int64(n) {
		t.Errorf("declared %d and removed %d of %d", declaredSeen, removed.Load(), n)
	}
	// What the suite declared, what tracking derived, and its removals: each said once.
	if r.Claims != 3*int64(n) {
		t.Errorf("%d claims, want %d", r.Claims, 3*n)
	}
}
