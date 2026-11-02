package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// fakeOrchestrator answers the UI's reads with one open run, whose suite has sent so many
// records; it counts what it is asked.
type fakeOrchestrator struct {
	sent  atomic.Int64
	mu    sync.Mutex
	asked map[string]int
}

func (f *fakeOrchestrator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.asked[r.URL.Path]++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	at := time.Now().UTC()
	switch r.URL.Path {
	case "/runs":
		_ = json.NewEncoder(w).Encode(protocol.RunList{Runs: []protocol.RunSummary{{ID: "run-1", Group: "delivery", State: protocol.StateOpened}}})
	case "/groups":
		_ = json.NewEncoder(w).Encode([]protocol.Group{{Name: "delivery", Dispatchers: []string{"parcels"}}})
	case "/dispatchers":
		_ = json.NewEncoder(w).Encode([]protocol.Dispatcher{{Name: "parcels", Instance: "east", Team: "parcels", LeaseExpiresAt: at.Add(time.Minute)}})
	case "/runs/run-1":
		_ = json.NewEncoder(w).Encode(protocol.Run{ID: "run-1", Group: "delivery", State: protocol.StateOpened, Gate: protocol.Gate{Open: true}})
	case "/runs/run-1/progress":
		_ = json.NewEncoder(w).Encode(protocol.Progress{
			Scenario: "parcels hold their rate", At: at, Steps: []protocol.StepProgress{},
			Load: []protocol.LoadProgress{{Stream: "parcels", Protocol: "rest", Target: "parcels", Rate: 50, Planned: 100, Sent: f.sent.Load(), Latency: &protocol.Latency{P50: 3, P99: 9}}},
		})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeOrchestrator) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[path]
}

func TestTheLiveViewIsOneReaderForEveryWatcher(t *testing.T) {
	orch := &fakeOrchestrator{asked: map[string]int{}}
	srv := httptest.NewServer(orch)
	defer srv.Close()
	h := newHub("lab", newReads(Environment{Name: "lab", URL: srv.URL}, srv.Client()), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.every = 20 * time.Millisecond

	next := func(events chan event, name string) []byte {
		t.Helper()
		for {
			select {
			case ev := <-events:
				if ev.name == name {
					return ev.data
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("no %s event", name)
			}
		}
	}
	var watchers []func()
	var streams []chan event
	for range 5 {
		ev, stop := h.watch()
		streams, watchers = append(streams, ev), append(watchers, stop)
	}
	if got := next(streams[0], "history"); string(got) != "{}" {
		t.Fatalf("the first history: %s", got)
	}
	orch.sent.Store(40)
	var snap snapshot
	for snap.Samples["run-1"].Load == nil || snap.Samples["run-1"].Load[0].Sent != 40 {
		snap = snapshot{}
		if err := json.Unmarshal(next(streams[0], "snapshot"), &snap); err != nil {
			t.Fatal(err)
		}
	}
	if len(snap.Live) != 1 || snap.Live[0].Progress == nil || snap.Live[0].Progress.Scenario != "parcels hold their rate" || len(snap.Dispatchers) != 1 || len(snap.Groups) != 1 {
		t.Fatalf("the snapshot: %+v", snap)
	}
	// Every watcher saw it, from one read of each route a tick.
	for _, s := range streams[1:] {
		next(s, "snapshot")
	}
	ticks := orch.count("/groups")
	if runs := orch.count("/runs/run-1"); runs != ticks {
		t.Errorf("five watchers, %d ticks, %d reads of the run", ticks, runs)
	}
	// A watcher that comes later gets the samples so far, at once.
	late, stopLate := h.watch()
	var history map[string][]sample
	if err := json.Unmarshal(next(late, "history"), &history); err != nil || len(history["run-1"]) == 0 {
		t.Fatalf("a late watcher's history: %v %v", history, err)
	}
	stopLate()
	// Nobody watching, nobody reads.
	for _, stop := range watchers {
		stop()
	}
	time.Sleep(100 * time.Millisecond)
	before := orch.count("/groups")
	time.Sleep(200 * time.Millisecond)
	if after := orch.count("/groups"); after != before {
		t.Errorf("the reader went on with nobody watching: %d reads, then %d", before, after)
	}
	if len(h.samples("run-1")) == 0 {
		t.Error("the run's samples went with its watchers")
	}
}

func TestViewersShareReads(t *testing.T) {
	orch := &fakeOrchestrator{asked: map[string]int{}}
	srv := httptest.NewServer(orch)
	defer srv.Close()
	h := Handler([]Environment{{Name: "lab", URL: srv.URL}}, nil)
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/env/lab/runs/run-1/progress", nil))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "parcels hold their rate") {
				t.Errorf("a viewer's read: %d %s", rec.Code, rec.Body)
			}
		}()
	}
	wg.Wait()
	if n := orch.count("/runs/run-1/progress"); n != 1 {
		t.Errorf("50 viewers at once: %d reads of the orchestrator", n)
	}
}

func TestTheLiveViewStreams(t *testing.T) {
	orch := &fakeOrchestrator{asked: map[string]int{}}
	up := httptest.NewServer(orch)
	defer up.Close()
	ui := httptest.NewServer(Handler([]Environment{{Name: "lab", URL: up.URL}}, nil))
	defer ui.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ui.URL+"/api/env/lab/live", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var names []string
	for sc.Scan() && len(names) < 2 {
		if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			names = append(names, name)
		}
	}
	if strings.Join(names, ",") != "history,snapshot" {
		t.Fatalf("events %v", names)
	}
}
