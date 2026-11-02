package orchestrator

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// Journal tells a run as a story, in plain text. Everything in it is already recorded: the
// events, the log, the state every instance ended in, the claims by place. The journal invents
// nothing; it puts them on one timeline in sentences, because reading a run should not require
// getting into a pod.
func (o *Orchestrator) Journal(ctx context.Context, runID string) (string, error) {
	run, err := o.Run(ctx, runID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run %s  group %s\n", run.ID, run.Group)
	fmt.Fprintf(&b, "opened  %s  by %s\n", run.OpenedAt.Format(time.RFC3339Nano), run.Initiator)
	fmt.Fprintf(&b, "state   %s", run.State)
	switch {
	case run.Failed:
		fmt.Fprintf(&b, "  (failed: %s)", run.FailReason)
	case run.Abandoned:
		b.WriteString("  (abandoned)")
	}
	b.WriteString("\n")
	if len(run.Streams) > 0 {
		b.WriteString("\nstreams\n")
		for _, s := range run.Streams {
			fmt.Fprintf(&b, "  %-24s seed %s\n", s.Name, s.Seed)
		}
	}
	b.WriteString("\ndispatchers on this run\n")
	for _, d := range run.Dispatchers {
		need := "optional"
		if d.Required {
			need = "required"
		}
		state := d.State
		if d.Detail != "" {
			state += ": " + d.Detail
		}
		fmt.Fprintf(&b, "  %-20s %-12s %-9s %s\n", d.Name, d.Instance, need, state)
	}

	var timeline []line
	rows, err := o.ro.QueryContext(ctx, `select type, at from events where run_id = ? order by seq`, runID)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var typ string
		var at int64
		if err := rows.Scan(&typ, &at); err != nil {
			rows.Close()
			return "", err
		}
		timeline = append(timeline, line{at: at, actor: "orchestrator", text: narrate(typ), grew: typ == protocol.EventFootprintUpdated})
	}
	rows.Close()
	rows, err = o.ro.QueryContext(ctx, `select at, actor, text from run_log where run_id = ? order by seq`, runID)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var at int64
		var actor, text string
		if err := rows.Scan(&at, &actor, &text); err != nil {
			rows.Close()
			return "", err
		}
		working := strings.HasPrefix(text, "reports "+protocol.StatusInProgress)
		timeline = append(timeline, line{at: at, actor: actor, text: text, claim: parseClaim(text), working: working})
	}
	rows.Close()
	sort.SliceStable(timeline, func(i, j int) bool { return timeline[i].at < timeline[j].at })
	b.WriteString("\nwhat happened\n")
	opened := run.OpenedAt.UnixMilli()
	since := func(at int64) string {
		d := time.Duration(at-opened) * time.Millisecond
		return fmt.Sprintf("+%d.%03ds", d/time.Second, (d%time.Second)/time.Millisecond)
	}
	for _, l := range coalesce(timeline, since) {
		fmt.Fprintf(&b, "  %-10s %-28s %s\n", since(l.at), l.actor, l.text)
	}

	// Counted by the database, not built here: the footprint is the one thing that grows with a
	// run, and describing it should not need holding it.
	fmt.Fprintf(&b, "\nwhat the run is answerable for\n  %d %s claimed into the footprint\n", run.Claims, plural(run.Claims, "identity", "identities"))
	rows, err = o.ro.QueryContext(ctx, `select store, locator, sum(identity_count) from footprint_entries where run_id = ?
		group by store, locator order by store, locator`, runID)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var store, locator string
		var n int64
		if err := rows.Scan(&store, &locator, &n); err != nil {
			rows.Close()
			return "", err
		}
		fmt.Fprintf(&b, "  %-40s %d\n", store+":"+locator, n)
	}
	rows.Close()
	if run.State == protocol.StateOpened && !run.Gate.Open {
		fmt.Fprintf(&b, "\nthe readiness gate is waiting on %s\n", strings.Join(run.Gate.Waiting, "; "))
	}
	return b.String(), nil
}

// line is a moment of the journal's timeline.
type line struct {
	at          int64
	actor, text string
	grew        bool   // the footprint grew
	claim       *claim // the actor reported identities
	working     bool   // an instance said a round is under way, which a burst runs on through
}

// claim is a log line that reports identities: "declares 25000 identities at tracking-db:public.tracking".
type claim struct {
	verb, place string
	n           int64
}

var claimLine = regexp.MustCompile(`^(declares|reports) (\d+) identit(?:y|ies) at (.+)$`)

func parseClaim(text string) *claim {
	m := claimLine.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	n, _ := strconv.ParseInt(m[2], 10, 64)
	return &claim{verb: m[1], place: m[3], n: n}
}

// coalesce tells a burst of reports once. A run of sixty million identities is thousands of
// reports of 25,000, each growing the footprint, and a journal that lists them all is a journal
// nobody reads. Between two other moments, the reports of one party at one place become one
// line, and the growth they caused another.
func coalesce(timeline []line, since func(int64) string) []line {
	var out, burst []line
	flush := func() {
		type group struct {
			line
			last    int64
			reports int
		}
		var groups []*group
		byKey := map[string]*group{}
		var grew int
		var grewAt int64
		var told []line
		for _, l := range burst {
			if l.working {
				told = append(told, l)
				continue
			}
			if l.grew {
				if grew == 0 {
					grewAt = l.at
				}
				grew++
				continue
			}
			k := l.actor + "\x00" + l.claim.verb + "\x00" + l.claim.place
			g := byKey[k]
			if g == nil {
				g = &group{line: line{at: l.at, actor: l.actor, claim: &claim{verb: l.claim.verb, place: l.claim.place}}}
				byKey[k] = g
				groups = append(groups, g)
			}
			g.claim.n += l.claim.n
			g.reports++
			g.last = l.at
		}
		for _, g := range groups {
			text := fmt.Sprintf("%s %d %s at %s", g.claim.verb, g.claim.n, plural(g.claim.n, "identity", "identities"), g.claim.place)
			if g.reports > 1 {
				text += fmt.Sprintf(", in %d reports until %s", g.reports, since(g.last))
			}
			told = append(told, line{at: g.at, actor: g.actor, text: text})
		}
		if grew > 0 {
			text := narrate(protocol.EventFootprintUpdated)
			if grew > 1 {
				text = fmt.Sprintf("the footprint grew %d times; each time, another prepare round runs and the readiness gate waits for it", grew)
			}
			told = append(told, line{at: grewAt, actor: "orchestrator", text: text})
		}
		sort.SliceStable(told, func(i, j int) bool { return told[i].at < told[j].at })
		out = append(out, told...)
		burst = burst[:0]
	}
	for _, l := range timeline {
		if l.grew || l.claim != nil || (l.working && len(burst) > 0) {
			burst = append(burst, l)
			continue
		}
		flush()
		out = append(out, l)
	}
	flush()
	return out
}

func narrate(eventType string) string {
	switch eventType {
	case protocol.EventOpened:
		return "the run opens; every instance of every dispatcher in the group is notified"
	case protocol.EventFootprintUpdated:
		return "the footprint grew, so another prepare round runs and the readiness gate waits for it"
	case protocol.EventTrafficComplete:
		return "the suite says its traffic is done; the window closes"
	case protocol.EventSettled:
		return "the suite says downstream has settled"
	case protocol.EventCleanupReady:
		return "cleanup opens; every instance removes what it claimed"
	case protocol.EventFailed:
		return "the run failed; cleanup opens for whatever was prepared"
	case protocol.EventAbandoned:
		return "the run was abandoned; cleanup opens all the same"
	default:
		return eventType
	}
}
