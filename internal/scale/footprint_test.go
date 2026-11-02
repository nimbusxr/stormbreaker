//go:build scale

package scale

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/internal/orchestrator"
	"github.com/nimbusxr/stormbreaker/protocol"
)

func identities() int {
	if s := os.Getenv("STORMBREAKER_SCALE_IDENTITIES"); s != "" {
		n, err := strconv.Atoi(s)
		if err == nil {
			return n
		}
	}
	return 60_000_000
}

// patience is how long the scale tests wait for the gate and for close: 30 minutes for every 60
// million identities, at least 30 minutes, or STORMBREAKER_SCALE_PATIENCE.
func patience(n int) time.Duration {
	if s := os.Getenv("STORMBREAKER_SCALE_PATIENCE"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
	}
	return max(30*time.Minute, time.Duration(float64(30*time.Minute)*float64(n)/60e6))
}

// walkOnly is f, or the whole footprint with STORMBREAKER_SCALE_WHOLE set: hooks then walk it all,
// as they did before walks were narrowed (ADR 0024), to measure what narrowing saves.
func walkOnly(f protocol.FootprintFilter) protocol.FootprintFilter {
	if os.Getenv("STORMBREAKER_SCALE_WHOLE") != "" {
		return protocol.FootprintFilter{}
	}
	return f
}

// onDisk is the size of a SQLite database and its write-ahead log.
func onDisk(db string) int64 {
	var total int64
	for _, f := range []string{db, db + "-wal"} {
		if fi, err := os.Stat(f); err == nil {
			total += fi.Size()
		}
	}
	return total
}

// uuidish is the i-th identity: UUID-shaped, as the streams' derived ones are.
func uuidish(i int) string {
	sum := sha256.Sum256([]byte(strconv.Itoa(i)))
	h := hex.EncodeToString(sum[:16])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// peak samples the process's heap while it runs.
type peak struct {
	stop chan struct{}
	done chan struct{}
	max  atomic.Uint64
}

func watchHeap() *peak {
	p := &peak{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.HeapInuse > p.max.Load() {
				p.max.Store(m.HeapInuse)
			}
			select {
			case <-p.stop:
				return
			case <-t.C:
			}
		}
	}()
	return p
}

func (p *peak) end() uint64 { close(p.stop); <-p.done; return p.max.Load() }

func mb(b uint64) string {
	if b >= 10e9 {
		return fmt.Sprintf("%.1f GB", float64(b)/1e9)
	}
	return fmt.Sprintf("%.0f MB", float64(b)/1e6)
}

// TestFootprintAtScale is the orchestrator holding a run's footprint of 60 million identities:
// the suite declaring them in batches, over HTTP, as stormbreaker-core does; the run's view and
// journal answering while they arrive; and a dispatcher walking all of them.
func TestFootprintAtScale(t *testing.T) {
	n := identities()
	dir := t.TempDir()
	ctx := context.Background()
	o, err := orchestrator.Open(ctx, orchestrator.Config{DB: filepath.Join(dir, "orchestrator.db")}, orchestrator.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	srv := httptest.NewServer(o.Handler())
	defer srv.Close()
	c := protocol.NewClient(srv.URL, "")
	c.HTTP = &http.Client{Timeout: 5 * time.Minute}
	secret, _ := protocol.NewSecret()
	if _, err := c.Register(ctx, protocol.Registration{Name: "tracking", Instance: "a", Team: "t", CallbackURL: "http://unused.invalid", Secret: secret}); err != nil {
		t.Fatal(err)
	}
	if err := c.JoinGroup(ctx, "delivery", "tracking"); err != nil {
		t.Fatal(err)
	}
	run, err := c.OpenRun(ctx, protocol.OpenRun{Initiator: "scale", Group: "delivery"})
	if err != nil {
		t.Fatal(err)
	}

	// Declaring, while something polls the run's view as the gate does.
	heap := watchHeap()
	var viewMax atomic.Int64
	stopPoll := make(chan struct{})
	var polls sync.WaitGroup
	polls.Add(1)
	go func() {
		defer polls.Done()
		for {
			select {
			case <-stopPoll:
				return
			default:
			}
			begin := time.Now()
			if _, err := c.Run(ctx, run.ID); err != nil {
				t.Error(err)
				return
			}
			if d := time.Since(begin).Microseconds(); d > viewMax.Load() {
				viewMax.Store(d)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
	const batch = 25_000
	ids := make([]string, 0, batch)
	begin := time.Now()
	for i := range n {
		ids = append(ids, uuidish(i))
		if len(ids) == batch || i == n-1 {
			if _, err := c.ReportFootprint(ctx, run.ID, protocol.Suite, "", protocol.FootprintReport{Entries: []protocol.FootprintEntry{
				{Store: "parcels", Locator: "/parcels", Identities: ids, Resolution: protocol.ResolutionExpected},
			}}); err != nil {
				t.Fatal(err)
			}
			ids = ids[:0]
		}
	}
	declare := time.Since(begin)
	close(stopPoll)
	polls.Wait()

	r, err := c.Run(ctx, run.ID)
	if err != nil || r.Claims != int64(n) {
		t.Fatalf("claims %d, %v", r.Claims, err)
	}
	begin = time.Now()
	j, err := c.Journal(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	journal := time.Since(begin)
	dbBytes := onDisk(filepath.Join(dir, "orchestrator.db"))

	// A dispatcher walks every claim, a page at a time.
	begin = time.Now()
	walked := 0
	for _, err := range c.Footprint(ctx, run.ID, protocol.FootprintFilter{}) {
		if err != nil {
			t.Fatal(err)
		}
		walked++
	}
	walk := time.Since(begin)
	if walked != n {
		t.Errorf("walked %d claims of %d", walked, n)
	}
	heapPeak := heap.end()

	t.Logf("identities                 %d", n)
	t.Logf("declared in                %s (%.0f identities/s, batches of %d over HTTP)", declare.Round(time.Millisecond), float64(n)/declare.Seconds(), batch)
	t.Logf("run view while declaring   at most %s", time.Duration(viewMax.Load())*time.Microsecond)
	t.Logf("journal at the end         %s (%d bytes)", journal.Round(time.Millisecond), len(j))
	t.Logf("database on disk           %s", mb(uint64(dbBytes)))
	t.Logf("walked the footprint in    %s (%.0f claims/s)", walk.Round(time.Millisecond), float64(walked)/walk.Seconds())
	t.Logf("heap at its peak           %s (the orchestrator and this client together)", mb(heapPeak))
}
