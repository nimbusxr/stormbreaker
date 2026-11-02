// Package protocol is the stormbreaker protocol: the orchestrator's wire types, the signing of
// the notifications it sends dispatchers, and a Go client for its API. The orchestrator's
// OpenAPI document (api/orchestrator.openapi.yaml) is the contract; these types are its Go form.
package protocol

import (
	"slices"
	"time"
)

// Suite is the reserved dispatcher name a suite reports its own footprint under: the identities
// a run's streams will create, claimed before any traffic flows.
const Suite = "suite"

// Run states, in the order a run moves through them. A run that fails its readiness gate, or
// is abandoned, goes to cleanup straight away: Failed and Abandoned say why.
const (
	StateOpened          = "opened"
	StateTrafficComplete = "traffic-complete"
	StateSettled         = "settled"
	StateCleanup         = "cleanup"
	StateClosed          = "closed"
)

// Event types. The suite publishes traffic-complete, settled, cleanup-ready and abandoned; the
// orchestrator publishes the rest.
const (
	EventOpened           = "run.opened"
	EventFootprintUpdated = "run.footprint-updated"
	EventTrafficComplete  = "run.traffic-complete"
	EventSettled          = "run.settled"
	EventCleanupReady     = "run.cleanup-ready"
	EventFailed           = "run.failed"
	EventAbandoned        = "run.abandoned"
)

// States of one dispatcher instance on a run.
const (
	InstanceNotified     = "notified"
	InstanceAcknowledged = "acknowledged"
	InstancePrepared     = "prepared"
	InstanceCompleted    = "completed"
	InstanceUnreachable  = "unreachable"
)

// Statuses a dispatcher instance reports. Only prepared and completed move its state; the rest
// say how it is getting on.
const (
	StatusPrepared   = "prepared"
	StatusInProgress = "in-progress"
	StatusPartial    = "partial"
	StatusBlocked    = "blocked"
	StatusCompleted  = "completed"
)

// Resolutions of a footprint entry: declared before traffic, removed and checked gone, or
// promised gone by a date.
const (
	ResolutionExpected        = "expected"
	ResolutionRemovedVerified = "removed-verified"
	ResolutionCleanBy         = "clean-by"
)

// CreateGroup creates a group. Groups are also created when a dispatcher joins one.
type CreateGroup struct {
	Name string `json:"name"`
}

// Group is a named set of dispatchers: every dispatcher in it is notified of every run opened
// for it.
type Group struct {
	Name        string   `json:"name"`
	Dispatchers []string `json:"dispatchers"`
}

// Version is the protocol version: what the orchestrator, the dispatchers and the suites agree on.
// A dispatcher registers, and a suite opens a run, saying the version it speaks; the orchestrator
// refuses one it does not, and says why (ADR 0021). It goes up when a change would make an older
// party misbehave rather than fail.
const Version = 1

// Registration registers a dispatcher instance, or renews its lease. Secret signs the
// notifications sent to CallbackURL: "whsec_" and the key in base64 (Standard Webhooks).
type Registration struct {
	Name        string `json:"name"`
	Instance    string `json:"instance"`
	Team        string `json:"team"`
	CallbackURL string `json:"callbackUrl"`
	Secret      string `json:"secret"`
	// Protocol is the protocol version the dispatcher speaks: Version. The client sets it.
	Protocol int `json:"protocol"`
}

// Dispatcher is a registered dispatcher instance. Its secret is never returned.
type Dispatcher struct {
	Name           string    `json:"name"`
	Instance       string    `json:"instance"`
	Team           string    `json:"team"`
	CallbackURL    string    `json:"callbackUrl"`
	LeaseExpiresAt time.Time `json:"leaseExpiresAt"`
}

// StreamRef names a stream a run generates records from, and the seed it uses, so that the
// run's records can be generated again later.
type StreamRef struct {
	Name string `json:"name"`
	Seed string `json:"seed"`
}

// OpenRun opens a run for a group. Requires names the dispatchers whose every instance must be
// prepared before traffic flows.
type OpenRun struct {
	Initiator string      `json:"initiator"`
	Group     string      `json:"group"`
	Requires  []string    `json:"requires,omitempty"`
	Streams   []StreamRef `json:"streams,omitempty"`
	// Protocol is the protocol version the suite speaks: Version. The client sets it.
	Protocol int `json:"protocol"`
}

// Gate is the run's readiness gate. It is open when every instance of every required dispatcher
// is prepared, in a round that began from the footprint's version now (ADR 0013); Waiting names
// the ones it waits on.
type Gate struct {
	Open    bool     `json:"open"`
	Waiting []string `json:"waiting,omitempty"`
}

// RunDispatcher is one dispatcher instance on a run.
type RunDispatcher struct {
	Name         string     `json:"name"`
	Instance     string     `json:"instance"`
	Required     bool       `json:"required"`
	State        string     `json:"state"`
	LastStatus   string     `json:"lastStatus,omitempty"`
	Evidence     string     `json:"evidence,omitempty"`
	Detail       string     `json:"detail,omitempty"`
	LastReportAt *time.Time `json:"lastReportAt,omitempty"`
	// FootprintVersion is the footprint version the instance's last prepared or completed round
	// began from.
	FootprintVersion int64 `json:"footprintVersion"`
}

// Run is a run as the orchestrator holds it, without its footprint (read that a page at a time).
type Run struct {
	ID          string          `json:"id"`
	Group       string          `json:"group"`
	State       string          `json:"state"`
	Initiator   string          `json:"initiator"`
	Failed      bool            `json:"failed"`
	FailReason  string          `json:"failReason,omitempty"`
	Abandoned   bool            `json:"abandoned"`
	OpenedAt    time.Time       `json:"openedAt"`
	WindowFrom  time.Time       `json:"windowFrom"`
	WindowTo    *time.Time      `json:"windowTo,omitempty"`
	Gate        Gate            `json:"gate"`
	Dispatchers []RunDispatcher `json:"dispatchers"`
	Streams     []StreamRef     `json:"streams"`
	// FootprintUpdatedAt is when the footprint last grew: a dispatcher that has gone over the run
	// since has seen everything there is to derive from.
	FootprintUpdatedAt *time.Time `json:"footprintUpdatedAt,omitempty"`
	// FootprintVersion counts the times the footprint grew. A round reports the version it began
	// from, and the gate counts an instance prepared on the footprint as it is now only when that
	// is this version.
	FootprintVersion int64 `json:"footprintVersion"`
	// Claims counts the identities claimed into the footprint as they were written: an instance
	// that claims the same identities again, differently, counts them again.
	Claims int64 `json:"claims"`
	// CleanupAt is when it went to cleanup, and ClosedAt when it closed.
	CleanupAt *time.Time `json:"cleanupAt,omitempty"`
	ClosedAt  *time.Time `json:"closedAt,omitempty"`
	// Holds is whether the run holds its group's dispatchers: from its opening until it closes,
	// or until its cleanup outlasts the orchestrator's cleanup hold. No other run may open on a
	// dispatcher a run holds (ADR 0015).
	Holds bool `json:"holds"`
	// Unfinished is whether the run closed at the orchestrator's cleanup deadline, with instances
	// that never completed: what they claimed is not answered for (ADR 0023).
	Unfinished bool `json:"unfinished"`
}

// Done reports whether the run has finished: closed, its footprint answered for.
func (r *Run) Done() bool { return r.State == StateClosed }

// PublishEvent publishes a lifecycle event on a run.
type PublishEvent struct {
	Type string `json:"type"`
}

// Event is a notification: thin, a claim check. Its receiver reads the run at Ref for the rest.
type Event struct {
	ID    string    `json:"id"`
	RunID string    `json:"runId"`
	Type  string    `json:"type"`
	Time  time.Time `json:"time"`
	Ref   string    `json:"ref"`
}

// RunSummary is a run as the list of runs shows it: where it is, since when, and what it holds.
type RunSummary struct {
	ID         string     `json:"id"`
	Group      string     `json:"group"`
	State      string     `json:"state"`
	Initiator  string     `json:"initiator"`
	Failed     bool       `json:"failed"`
	Abandoned  bool       `json:"abandoned"`
	Holds      bool       `json:"holds"`
	Unfinished bool       `json:"unfinished"`
	Claims     int64      `json:"claims"`
	OpenedAt   time.Time  `json:"openedAt"`
	CleanupAt  *time.Time `json:"cleanupAt,omitempty"`
	ClosedAt   *time.Time `json:"closedAt,omitempty"`
}

// RunList is a page of the list of runs, newest first. Ask again with Before for older ones.
type RunList struct {
	Runs []RunSummary `json:"runs"`
	// Before is the cursor for the next page: the opening time, in Unix milliseconds, of the
	// oldest run on this one. Zero when there are no more.
	Before int64 `json:"before,omitempty"`
}

// EventFeed is a page of the event feed, the backstop for notifications that never arrived.
// Ask again with Cursor as since.
type EventFeed struct {
	Events []Event `json:"events"`
	Cursor int64   `json:"cursor"`
}

// Status is what a dispatcher instance reports about its work on a run.
type Status struct {
	State    string `json:"state"`
	Evidence string `json:"evidence,omitempty"`
	Detail   string `json:"detail,omitempty"`
	// FootprintVersion is the run's footprint version when the round that led to this status
	// began: read the run, then run the round. Prepared and completed need it; a round reported
	// on a version the footprint has since passed counts for nothing.
	FootprintVersion int64 `json:"footprintVersion,omitempty"`
}

// FootprintEntry claims identities at a place: a store, and where in it (a table, a topic, a
// path). CleanBy (a date, YYYY-MM-DD) goes with the clean-by resolution.
type FootprintEntry struct {
	Store      string   `json:"store"`
	Locator    string   `json:"locator"`
	Identities []string `json:"identities"`
	Resolution string   `json:"resolution"`
	CleanBy    string   `json:"cleanBy,omitempty"`
}

// FootprintReport is one report of footprint entries. Reports are deltas: what is new since the
// reporter last reported.
type FootprintReport struct {
	Entries []FootprintEntry `json:"entries"`
}

// FootprintResult is what the orchestrator says of a footprint report: whether it grew the
// footprint, because it said something its reporter had not said at that place before.
type FootprintResult struct {
	Grew bool `json:"grew"`
}

// Claim is one identity at one place, as one dispatcher instance claims it, and what it said of
// it: expected, removed-verified, or clean-by a date. A dispatcher tells what it has already
// answered for from its own claims, so that a restart remembers nothing it needs.
type Claim struct {
	Dispatcher string `json:"dispatcher"`
	Instance   string `json:"instance"`
	Store      string `json:"store"`
	Locator    string `json:"locator"`
	Identity   string `json:"identity"`
	Resolution string `json:"resolution"`
	CleanBy    string `json:"cleanBy,omitempty"`
}

// Report is one footprint report as it was written: who said what of which identities, where.
type Report struct {
	Dispatcher string   `json:"dispatcher"`
	Instance   string   `json:"instance"`
	Store      string   `json:"store"`
	Locator    string   `json:"locator"`
	Identities []string `json:"identities"`
	Resolution string   `json:"resolution"`
	CleanBy    string   `json:"cleanBy,omitempty"`
}

// FootprintPage is a page of a run's footprint: the reports after a cursor, in the order they
// were made, at most a hundred thousand identities' worth (and at least one report). Next is the
// cursor for the next page, nil when this page is empty. Until is where the walk ends: the run's
// latest report when its first page was read, so that a walk sees the footprint as it was when
// it began, and never what the walker reports while walking. A later report from the same
// instance at the same place supersedes its own earlier one.
type FootprintPage struct {
	Reports []Report `json:"reports"`
	Next    *int64   `json:"next"`
	Until   int64    `json:"until"`
}

// FootprintFilter narrows a walk of the footprint to the reports a hook reads (ADR 0024): those of
// the named dispatchers (Suite for the suite's own), at one store, at one locator. An empty field
// narrows nothing, and the zero filter is the whole footprint. The orchestrator reads and sends
// only the reports that match, so a hook that derives from the suite's parcels reads those, and
// not every claim every team made beside them.
type FootprintFilter struct {
	Dispatchers    []string
	Store, Locator string
}

// Whole says whether the filter narrows nothing.
func (f FootprintFilter) Whole() bool {
	return len(f.Dispatchers) == 0 && f.Store == "" && f.Locator == ""
}

// Matches says whether a report by dispatcher at store and locator is one the filter keeps.
func (f FootprintFilter) Matches(dispatcher, store, locator string) bool {
	return (len(f.Dispatchers) == 0 || slices.Contains(f.Dispatchers, dispatcher)) &&
		(f.Store == "" || f.Store == store) && (f.Locator == "" || f.Locator == locator)
}

// Progress is how a run's suite says its scenario is going (ADR 0027): its steps, the load it
// sends, and what its checks found. The suite sends it about once a second while the run is
// open; the orchestrator keeps only the latest, in memory, and the final one with the run.
// Nothing waits on it.
type Progress struct {
	// Scenario is the scenario's name, and Feature where it is: its file and line.
	Scenario string `json:"scenario"`
	Feature  string `json:"feature,omitempty"`
	// Status is the scenario's, once it has one: passed, failed or skipped.
	Status string         `json:"status,omitempty"`
	Steps  []StepProgress `json:"steps"`
	Load   []LoadProgress `json:"load,omitempty"`
	Checks []CheckResult  `json:"checks,omitempty"`
	// Final marks the suite's last word, sent before it closes the run.
	Final bool `json:"final,omitempty"`
	// At is when the orchestrator received it; it sets it.
	At time.Time `json:"at,omitzero"`
}

// StepProgress is one of the scenario's steps: its status is empty until it runs, running as
// it runs, and its outcome after (passed, failed, skipped, pending, undefined or ambiguous).
type StepProgress struct {
	Keyword string `json:"keyword"`
	Text    string `json:"text"`
	Status  string `json:"status,omitempty"`
	Error   string `json:"error,omitempty"`
}

// LoadProgress is the load one stream sends, as the suite sees it.
type LoadProgress struct {
	// Stream is the stream's name; Protocol how it is sent (rest, kafka); Target where to (a
	// service, or a topic).
	Stream   string `json:"stream"`
	Protocol string `json:"protocol"`
	Target   string `json:"target"`
	// Rate is the declared arrival rate, a second; Planned the records the run sends in all.
	Rate    float64 `json:"rate"`
	Planned int64   `json:"planned"`
	// Sent counts the records sent, InFlight those not answered yet, and Failed those that
	// failed: an error, a timeout, or a status the step does not accept.
	Sent     int64 `json:"sent"`
	InFlight int64 `json:"inFlight"`
	Failed   int64 `json:"failed"`
	// Latency is what the suite saw, in milliseconds, of the records answered so far.
	Latency *Latency `json:"latency,omitempty"`
	// Done says the stream has sent everything, or stopped.
	Done bool `json:"done,omitempty"`
}

// Latency is a distribution's quantiles, in milliseconds.
type Latency struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

// CheckResult is one of the suite's checks: what it checked, where, and what it found. Its
// status is waiting while it judges within its patience, then passed or failed.
type CheckResult struct {
	Check  string `json:"check"`
	Source string `json:"source,omitempty"`
	Status string `json:"status"`
	Found  string `json:"found,omitempty"`
}
