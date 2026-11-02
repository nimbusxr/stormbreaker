package ui

// The UI pays for its own viewers (ADR 0027). Reads of an environment are shared for a second:
// viewers asking the same thing within it get one read of the orchestrator. And the live view
// is one reader per environment, reading only while someone watches, whose every read goes to
// every watcher over Server-Sent Events. A hundred viewers cost the orchestrator what one does.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// reading is a read of the orchestrator, shared by whoever asks for it while it is fresh.
type reading struct {
	done   chan struct{}
	at     time.Time
	status int
	ctype  string
	body   []byte
	err    error
}

// reads are an environment's reads, each shared for ttl.
type reads struct {
	env    Environment
	client *http.Client
	ttl    time.Duration
	mu     sync.Mutex
	recent map[string]*reading
}

func newReads(env Environment, client *http.Client) *reads {
	return &reads{env: env, client: client, ttl: time.Second, recent: map[string]*reading{}}
}

// read reads path (and its query) from the orchestrator, or shares a read of it that is in
// flight or fresh. The read itself does not end with ctx: others may be waiting on it.
func (s *reads) read(ctx context.Context, path string) *reading {
	s.mu.Lock()
	rd, ok := s.recent[path]
	if ok {
		select {
		case <-rd.done:
			ok = time.Since(rd.at) < s.ttl
		default: // in flight: wait for it
		}
	}
	if !ok {
		rd = &reading{done: make(chan struct{})}
		s.recent[path] = rd
		if len(s.recent) > 512 {
			for k, o := range s.recent {
				select {
				case <-o.done:
					if time.Since(o.at) >= s.ttl {
						delete(s.recent, k)
					}
				default:
				}
			}
		}
		go s.fetch(rd, path)
	}
	s.mu.Unlock()
	select {
	case <-rd.done:
		return rd
	case <-ctx.Done():
		return &reading{err: ctx.Err()}
	}
}

func (s *reads) fetch(rd *reading, path string) {
	defer close(rd.done)
	defer func() { rd.at = time.Now() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.env.URL+"/"+path, nil)
	if err != nil {
		rd.err = err
		return
	}
	if s.env.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.env.Token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		rd.err = err
		return
	}
	defer resp.Body.Close()
	rd.status, rd.ctype = resp.StatusCode, resp.Header.Get("Content-Type")
	rd.body, rd.err = io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

// decode reads path into v; a 404 leaves v alone and says so.
func (s *reads) decode(ctx context.Context, path string, v any) (found bool, err error) {
	rd := s.read(ctx, path)
	switch {
	case rd.err != nil:
		return false, rd.err
	case rd.status == http.StatusNotFound:
		return false, nil
	case rd.status != http.StatusOK:
		return false, fmt.Errorf("GET /%s answered %d", path, rd.status)
	}
	return true, json.Unmarshal(rd.body, v)
}

// sample is one read of a live run's load, for the charts: the browser draws rates from the
// differences between samples.
type sample struct {
	At   time.Time    `json:"at"`
	Load []sampleLoad `json:"load"`
}

type sampleLoad struct {
	Stream   string  `json:"stream"`
	Sent     int64   `json:"sent"`
	Failed   int64   `json:"failed"`
	InFlight int64   `json:"inFlight"`
	P50      float64 `json:"p50"`
	P99      float64 `json:"p99"`
}

// liveRun is a run not closed yet, and how its suite says it is going.
type liveRun struct {
	Run      protocol.Run       `json:"run"`
	Progress *protocol.Progress `json:"progress,omitempty"`
}

// snapshot is one read of the environment, as every watcher gets it.
type snapshot struct {
	Environment string                `json:"environment"`
	At          time.Time             `json:"at"`
	Error       string                `json:"error,omitempty"`
	Runs        []protocol.RunSummary `json:"runs"`
	Before      int64                 `json:"before,omitempty"`
	Groups      []protocol.Group      `json:"groups"`
	Dispatchers []protocol.Dispatcher `json:"dispatchers"`
	Live        []liveRun             `json:"live"`
	// Samples are this read's new samples, by run.
	Samples map[string]sample `json:"samples,omitempty"`
}

// history is the samples of a run, as long as it is worth keeping.
type history struct {
	samples []sample
	last    time.Time // the progress the last sample came from
	seen    time.Time
}

const (
	// pageSize is how many runs a page lists.
	pageSize = 20
	// keepSamples is how many samples a run keeps: half an hour of one a second.
	keepSamples = 1800
	// keepHistory is how long a run's samples last after it was last seen open.
	keepHistory = time.Hour
)

// openStates are a run's states before it closes.
var openStates = []string{protocol.StateOpened, protocol.StateTrafficComplete, protocol.StateSettled, protocol.StateCleanup}

type event struct {
	name string
	data []byte
}

// hub is an environment's live view: one reader while anyone watches.
type hub struct {
	name  string
	reads *reads
	every time.Duration
	log   *slog.Logger

	mu        sync.Mutex
	watchers  map[chan event]struct{}
	running   bool
	last      []byte
	histories map[string]*history
}

func newHub(name string, r *reads, log *slog.Logger) *hub {
	return &hub{name: name, reads: r, every: time.Second, log: log, watchers: map[chan event]struct{}{}, histories: map[string]*history{}}
}

// watch adds a watcher: it gets the samples so far and the latest read at once, then each read
// as it comes. stop removes it; the reader stops when nobody watches.
func (h *hub) watch() (events chan event, stop func()) {
	ch := make(chan event, 16)
	h.mu.Lock()
	h.watchers[ch] = struct{}{}
	if !h.running {
		h.running = true
		go h.loop()
	}
	all := map[string][]sample{}
	for id, hs := range h.histories {
		all[id] = hs.samples
	}
	body, _ := json.Marshal(all)
	ch <- event{"history", body}
	if h.last != nil {
		ch <- event{"snapshot", h.last}
	}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.watchers, ch)
		h.mu.Unlock()
	}
}

func (h *hub) loop() {
	t := time.NewTicker(h.every)
	defer t.Stop()
	for {
		h.tick(context.Background())
		<-t.C
		h.mu.Lock()
		if len(h.watchers) == 0 {
			h.running = false
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
	}
}

// tick reads the environment once and sends it to every watcher.
func (h *hub) tick(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	now := time.Now().UTC()
	snap := snapshot{Environment: h.name, At: now, Runs: []protocol.RunSummary{}, Groups: []protocol.Group{}, Dispatchers: []protocol.Dispatcher{}, Live: []liveRun{}}
	var page, open protocol.RunList
	q := url.Values{"state": openStates, "limit": {"100"}}
	err := firstErr(
		func() error { _, err := h.reads.decode(ctx, fmt.Sprintf("runs?limit=%d", pageSize), &page); return err },
		func() error { _, err := h.reads.decode(ctx, "runs?"+q.Encode(), &open); return err },
		func() error { _, err := h.reads.decode(ctx, "groups", &snap.Groups); return err },
		func() error { _, err := h.reads.decode(ctx, "dispatchers", &snap.Dispatchers); return err },
	)
	if err == nil {
		snap.Runs, snap.Before = page.Runs, page.Before
		for _, s := range open.Runs {
			lr := liveRun{}
			if _, err := h.reads.decode(ctx, "runs/"+url.PathEscape(s.ID), &lr.Run); err != nil {
				continue
			}
			var p protocol.Progress
			if found, err := h.reads.decode(ctx, "runs/"+url.PathEscape(s.ID)+"/progress", &p); err == nil && found {
				lr.Progress = &p
			}
			snap.Live = append(snap.Live, lr)
		}
	} else {
		snap.Error = "cannot read " + h.name + "'s orchestrator: " + err.Error()
		h.log.Warn("cannot read the orchestrator", "environment", h.name, "err", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, lr := range snap.Live {
		hs := h.histories[lr.Run.ID]
		if hs == nil {
			hs = &history{}
			h.histories[lr.Run.ID] = hs
		}
		hs.seen = now
		if lr.Progress == nil || len(lr.Progress.Load) == 0 || !lr.Progress.At.After(hs.last) {
			continue
		}
		s := sample{At: lr.Progress.At, Load: make([]sampleLoad, 0, len(lr.Progress.Load))}
		for _, l := range lr.Progress.Load {
			sl := sampleLoad{Stream: l.Stream, Sent: l.Sent, Failed: l.Failed, InFlight: l.InFlight}
			if l.Latency != nil {
				sl.P50, sl.P99 = l.Latency.P50, l.Latency.P99
			}
			s.Load = append(s.Load, sl)
		}
		hs.last = lr.Progress.At
		hs.samples = append(hs.samples, s)
		if len(hs.samples) > keepSamples {
			hs.samples = hs.samples[len(hs.samples)-keepSamples:]
		}
		if snap.Samples == nil {
			snap.Samples = map[string]sample{}
		}
		snap.Samples[lr.Run.ID] = s
	}
	for id, hs := range h.histories {
		if now.Sub(hs.seen) > keepHistory {
			delete(h.histories, id)
		}
	}
	body, _ := json.Marshal(snap)
	h.last = body
	for ch := range h.watchers {
		select {
		case ch <- event{"snapshot", body}:
		default: // a watcher that does not keep up misses a read; the next supersedes it
		}
	}
}

// samples are a run's samples, while the hub keeps them.
func (h *hub) samples(runID string) []sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	if hs := h.histories[runID]; hs != nil {
		return hs.samples
	}
	return []sample{}
}

func firstErr(fns ...func() error) error {
	for _, fn := range fns {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}

// serveLive streams the hub's reads to one watcher until it goes.
func serveLive(w http.ResponseWriter, r *http.Request, h *hub) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported here", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	events, stop := h.watch()
	defer stop()
	_, _ = io.WriteString(w, "retry: 2000\n\n")
	flusher.Flush()
	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-events:
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data); err != nil {
				return
			}
			flusher.Flush()
		case <-keep.C:
			if _, err := io.WriteString(w, ": still here\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
