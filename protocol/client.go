package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Problem is an RFC 9457 problem detail: how the orchestrator says why it refused a request.
type Problem struct {
	Type   string `json:"type,omitempty"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	// ConflictsWith names the run a refused run would overlap: it holds dispatchers the refused
	// run's group needs (ADR 0015). Only a 409 answering POST /runs carries it.
	ConflictsWith string `json:"conflictsWith,omitempty"`
}

// ConflictOf is the run a refused open would overlap, if err is that refusal.
func ConflictOf(err error) string {
	var p *Problem
	if errors.As(err, &p) && p.Status == http.StatusConflict {
		return p.ConflictsWith
	}
	return ""
}

func (p *Problem) Error() string {
	if p.Detail != "" {
		return fmt.Sprintf("%d %s: %s", p.Status, p.Title, p.Detail)
	}
	return fmt.Sprintf("%d %s", p.Status, p.Title)
}

// StatusOf is the HTTP status of a *Problem in err's chain, or 0.
func StatusOf(err error) int {
	var p *Problem
	if errors.As(err, &p) {
		return p.Status
	}
	return 0
}

// Client calls an orchestrator's API.
type Client struct {
	// BaseURL is the orchestrator's, e.g. http://orchestrator:8080.
	BaseURL string
	// Token is the bearer token the orchestrator asks for, if it asks for one.
	Token string
	// HTTP sends the requests; nil means a client with a 10 second timeout.
	HTTP *http.Client
}

// NewClient returns a client of the orchestrator at baseURL.
func NewClient(baseURL, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: 10 * time.Second, Transport: Pooled(16)}}
}

// Pooled is a transport that keeps up to perHost idle connections to each host, and no limit
// across hosts: a client of the orchestrator talks to one host from many goroutines, and the
// orchestrator notifies every dispatcher instance in the environment. With Go's defaults (two a
// host, a hundred in all), most of their requests would open a connection, TLS and all, and
// leave its port waiting to close.
func Pooled(perHost int) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 0
	t.MaxIdleConnsPerHost = perHost
	return t
}

var defaultHTTP = &http.Client{Timeout: 10 * time.Second, Transport: Pooled(16)}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	u := strings.TrimRight(c.BaseURL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		p := &Problem{Status: resp.StatusCode, Title: http.StatusText(resp.StatusCode)}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(b, p) != nil || p.Title == "" {
			p.Title = http.StatusText(resp.StatusCode)
			p.Detail = strings.TrimSpace(string(b))
		}
		p.Status = resp.StatusCode
		return fmt.Errorf("%s %s: %w", method, path, p)
	}
	switch out := out.(type) {
	case nil:
		return nil
	case *string:
		b, err := io.ReadAll(resp.Body)
		*out = string(b)
		return err
	default:
		return json.NewDecoder(resp.Body).Decode(out)
	}
}

func instanceQuery(instance string) url.Values { return url.Values{"instance": {instance}} }

// Runs lists runs, newest first: in any of states (all if none), in group (any if empty), opened
// before before (Unix milliseconds; zero for the newest).
func (c *Client) Runs(ctx context.Context, states []string, group string, before int64, limit int) (*RunList, error) {
	q := url.Values{}
	for _, s := range states {
		q.Add("state", s)
	}
	if group != "" {
		q.Set("group", group)
	}
	if before > 0 {
		q.Set("before", strconv.FormatInt(before, 10))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out RunList
	return &out, c.do(ctx, http.MethodGet, "/runs", q, nil, &out)
}

// CreateGroup creates a group (groups are also created when a dispatcher joins one).
func (c *Client) CreateGroup(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, "/groups", nil, CreateGroup{Name: name}, nil)
}

// Groups lists the groups and their dispatchers.
func (c *Client) Groups(ctx context.Context) ([]Group, error) {
	var out []Group
	return out, c.do(ctx, http.MethodGet, "/groups", nil, nil, &out)
}

// JoinGroup adds a dispatcher to a group, creating the group if it has to.
func (c *Client) JoinGroup(ctx context.Context, group, dispatcher string) error {
	return c.do(ctx, http.MethodPut, "/groups/"+url.PathEscape(group)+"/dispatchers/"+url.PathEscape(dispatcher), nil, nil, nil)
}

// LeaveGroup takes a dispatcher out of a group.
func (c *Client) LeaveGroup(ctx context.Context, group, dispatcher string) error {
	return c.do(ctx, http.MethodDelete, "/groups/"+url.PathEscape(group)+"/dispatchers/"+url.PathEscape(dispatcher), nil, nil, nil)
}

// Register registers a dispatcher instance, or renews its lease.
func (c *Client) Register(ctx context.Context, r Registration) (*Dispatcher, error) {
	if r.Protocol == 0 {
		r.Protocol = Version
	}
	var out Dispatcher
	return &out, c.do(ctx, http.MethodPost, "/dispatchers", nil, r, &out)
}

// Dispatchers lists the registered dispatcher instances.
func (c *Client) Dispatchers(ctx context.Context) ([]Dispatcher, error) {
	var out []Dispatcher
	return out, c.do(ctx, http.MethodGet, "/dispatchers", nil, nil, &out)
}

// OpenRun opens a run.
func (c *Client) OpenRun(ctx context.Context, o OpenRun) (*Run, error) {
	if o.Protocol == 0 {
		o.Protocol = Version
	}
	var out Run
	return &out, c.do(ctx, http.MethodPost, "/runs", nil, o, &out)
}

// Run reads a run.
func (c *Client) Run(ctx context.Context, id string) (*Run, error) {
	var out Run
	return &out, c.do(ctx, http.MethodGet, "/runs/"+url.PathEscape(id), nil, nil, &out)
}

// Publish publishes a lifecycle event on a run.
func (c *Client) Publish(ctx context.Context, runID, eventType string) (*Event, error) {
	var out Event
	return &out, c.do(ctx, http.MethodPost, "/runs/"+url.PathEscape(runID)+"/events", nil, PublishEvent{Type: eventType}, &out)
}

// Heartbeat renews the suite's lease on a run.
func (c *Client) Heartbeat(ctx context.Context, runID string) error {
	return c.do(ctx, http.MethodPost, "/runs/"+url.PathEscape(runID)+"/heartbeat", nil, nil, nil)
}

// SendProgress says how the run's scenario is going (ADR 0027).
func (c *Client) SendProgress(ctx context.Context, runID string, p Progress) error {
	return c.do(ctx, http.MethodPut, "/runs/"+url.PathEscape(runID)+"/progress", nil, p, nil)
}

// Progress reads how a run's scenario is going, or went: the latest its suite said.
func (c *Client) Progress(ctx context.Context, runID string) (Progress, error) {
	var p Progress
	err := c.do(ctx, http.MethodGet, "/runs/"+url.PathEscape(runID)+"/progress", nil, nil, &p)
	return p, err
}

// AddStream records a stream the run generates records from.
func (c *Client) AddStream(ctx context.Context, runID string, s StreamRef) error {
	return c.do(ctx, http.MethodPost, "/runs/"+url.PathEscape(runID)+"/streams", nil, s, nil)
}

// ReportStatus reports a dispatcher instance's status on a run.
func (c *Client) ReportStatus(ctx context.Context, runID, dispatcher, instance string, s Status) error {
	return c.do(ctx, http.MethodPost, "/runs/"+url.PathEscape(runID)+"/dispatchers/"+url.PathEscape(dispatcher)+"/status", instanceQuery(instance), s, nil)
}

// ReportFootprint reports footprint entries for a dispatcher instance, or for the suite
// (dispatcher Suite), and says whether they grew the footprint.
func (c *Client) ReportFootprint(ctx context.Context, runID, dispatcher, instance string, r FootprintReport) (*FootprintResult, error) {
	var out FootprintResult
	return &out, c.do(ctx, http.MethodPost, "/runs/"+url.PathEscape(runID)+"/footprint/"+url.PathEscape(dispatcher), instanceQuery(instance), r, &out)
}

// FootprintPage reads at most reports reports made after the cursor after, and no later than
// until (0: the run's latest report now, which the page's Until says).
func (c *Client) FootprintPage(ctx context.Context, runID string, after, until int64, reports int, f FootprintFilter) (*FootprintPage, error) {
	var out FootprintPage
	q := url.Values{"after": {strconv.FormatInt(after, 10)}, "reports": {strconv.Itoa(reports)}}
	if until > 0 {
		q.Set("until", strconv.FormatInt(until, 10))
	}
	if len(f.Dispatchers) > 0 {
		q["dispatcher"] = f.Dispatchers
	}
	if f.Store != "" {
		q.Set("store", f.Store)
	}
	if f.Locator != "" {
		q.Set("locator", f.Locator)
	}
	return &out, c.do(ctx, http.MethodGet, "/runs/"+url.PathEscape(runID)+"/footprint", q, nil, &out)
}

// Footprint walks a run's footprint a page at a time, holding one page: every claim the filter
// keeps (the zero filter keeps them all), in the order it was made, as the footprint was when
// the walk began. Each walk starts from the beginning.
func (c *Client) Footprint(ctx context.Context, runID string, f FootprintFilter) iter.Seq2[Claim, error] {
	return func(yield func(Claim, error) bool) {
		var after, until int64
		for {
			page, err := c.FootprintPage(ctx, runID, after, until, 64, f)
			if err != nil {
				yield(Claim{}, err)
				return
			}
			for _, r := range page.Reports {
				cl := Claim{Dispatcher: r.Dispatcher, Instance: r.Instance, Store: r.Store, Locator: r.Locator, Resolution: r.Resolution, CleanBy: r.CleanBy}
				for _, id := range r.Identities {
					cl.Identity = id
					if !yield(cl, nil) {
						return
					}
				}
			}
			if page.Next == nil {
				return
			}
			after, until = *page.Next, page.Until
		}
	}
}

// Journal reads a run's journal: the run told as a story, in plain text.
func (c *Client) Journal(ctx context.Context, runID string) (string, error) {
	var out string
	return out, c.do(ctx, http.MethodGet, "/runs/"+url.PathEscape(runID)+"/journal", nil, nil, &out)
}

// Events reads the event feed after the cursor since: every group's, or only groups'. Read from 0
// with groups, it starts at their unfinished runs.
func (c *Client) Events(ctx context.Context, since int64, limit int, groups ...string) (*EventFeed, error) {
	var out EventFeed
	q := url.Values{"since": {strconv.FormatInt(since, 10)}, "limit": {strconv.Itoa(limit)}, "group": groups}
	return &out, c.do(ctx, http.MethodGet, "/events", q, nil, &out)
}
