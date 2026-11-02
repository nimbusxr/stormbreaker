package dispatcher

import (
	"container/list"
	"encoding/json"
	"sync"
)

// passes schedules a dispatcher's work. Runs go side by side, since a team's hook takes as long
// as its store takes and one slow run must not hold up every other. Passes over one run go in
// order, since a cleanup pass overtaking a prepare pass would clean up a world still being built.
//
// Notifications also coalesce. They come from the webhook and again from the event feed, several
// a run, and every pass reads the run afresh and runs the same idempotent round. So a run has at
// most one pass running and one waiting behind it: a notification during a pass schedules the
// next one, and any more fold into that.
type passes struct {
	mu   sync.Mutex
	runs map[string]*runPasses
	wg   sync.WaitGroup
}

type runPasses struct {
	order  sync.Mutex // held while a pass runs
	queued bool
}

func newPasses() *passes { return &passes{runs: map[string]*runPasses{}} }

// submit schedules a pass over a run, unless one is already waiting.
func (p *passes) submit(runID string, pass func()) {
	p.mu.Lock()
	rp, ok := p.runs[runID]
	if !ok {
		rp = &runPasses{}
		p.runs[runID] = rp
	}
	if rp.queued {
		p.mu.Unlock()
		return
	}
	rp.queued = true
	p.wg.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.wg.Done()
		rp.order.Lock()
		defer rp.order.Unlock()
		// Cleared inside the lock and before the work: a notification during this pass queues
		// the next, which waits here for this one to finish.
		p.mu.Lock()
		rp.queued = false
		p.mu.Unlock()
		pass()
	}()
}

// forget drops a run that will not be passed over again.
func (p *passes) forget(runID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if rp, ok := p.runs[runID]; ok && !rp.queued {
		delete(p.runs, runID)
	}
}

// wait waits for the passes under way.
func (p *passes) wait() { p.wg.Wait() }

// seen remembers the most recent event ids, to drop repeated notifications.
type seen struct {
	mu    sync.Mutex
	max   int
	order *list.List
	ids   map[string]*list.Element
}

func newSeen(max int) *seen {
	return &seen{max: max, order: list.New(), ids: map[string]*list.Element{}}
}

// add reports whether id is new, remembering it.
func (s *seen) add(id string) bool {
	if id == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ids[id]; ok {
		return false
	}
	s.ids[id] = s.order.PushBack(id)
	if s.order.Len() > s.max {
		oldest := s.order.Front()
		s.order.Remove(oldest)
		delete(s.ids, oldest.Value.(string))
	}
	return true
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
