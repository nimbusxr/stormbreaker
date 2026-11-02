// Package sbrest is the stormbreaker-rest pack: load through a service's front door. A stream's
// records become HTTP requests to a service axx's rest pack declared, sent open-model at a
// declared arrival rate, with what the client saw (its latencies, its failures) recorded as the
// client view. Hand-written requests can join a run too: their payloads' identities are declared
// before they are sent.
package sbrest

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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nimbusxr/axx/core"
	"github.com/nimbusxr/axx/packs/rest"

	"github.com/nimbusxr/stormbreaker/arrival"
	"github.com/nimbusxr/stormbreaker/internal/jsonpath"
	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
	"github.com/nimbusxr/stormbreaker/streams"
)

// Name is the pack's name, and its section of axx.yaml.
const Name = "stormbreaker-rest"

// Config is the pack's section of axx.yaml.
type Config struct {
	// Connections is how requests reach the service: "reuse" (the default) keeps a pool of up to
	// MaxConnections open, which a load balancer spreads across the service's replicas as it
	// spreads its users' connections; "fresh" opens one for each request, which runs out of the
	// machine's ports past a few thousand requests a second.
	Connections string `json:"connections"`
	// MaxConnections is how many connections to the service may be open at once (default 512):
	// past it, requests wait for one, and their latency says so.
	MaxConnections int `json:"maxConnections"`
	// MaxInFlight is how many requests may be in flight before the load stops, failing: the
	// service is not keeping up (default 50,000).
	MaxInFlight int64 `json:"maxInFlight"`
	// Timeout bounds each request (default 30s).
	Timeout string `json:"timeout"`
	// MaxDrift is how late requests may leave their due time, for five seconds in a row, before
	// the load stops, failing: the driver is not keeping up (default 1s; "off" turns it off).
	MaxDrift string `json:"maxDrift"`
}

type settings struct {
	client      *http.Client
	timeout     time.Duration
	maxInFlight int64
	maxDrift    time.Duration
}

// parseDrift reads a maxDrift setting: a duration, or "off" (-1, the guardrail off); "" is the
// default (0).
func parseDrift(pack, v string) (time.Duration, error) {
	switch v {
	case "":
		return 0, nil
	case "off":
		return -1, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("packs.%s.maxDrift is a duration, like 1s, or off, not %q", pack, v)
	}
	return d, nil
}

func load(s *core.Suite) (*settings, error) {
	return core.Cached(s, Name+".settings", func() (*settings, error) {
		var c Config
		if err := s.PackConfig(Name, &c); err != nil {
			return nil, err
		}
		st := &settings{timeout: 30 * time.Second, maxInFlight: c.MaxInFlight}
		if c.Timeout != "" {
			d, err := time.ParseDuration(s.Interpolate(c.Timeout))
			if err != nil {
				return nil, fmt.Errorf("packs.%s.timeout: %w", Name, err)
			}
			st.timeout = d
		}
		drift, err := parseDrift(Name, s.Interpolate(c.MaxDrift))
		if err != nil {
			return nil, err
		}
		st.maxDrift = drift
		conns := c.MaxConnections
		if conns <= 0 {
			conns = 512
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.MaxIdleConns, tr.MaxIdleConnsPerHost, tr.MaxConnsPerHost = 0, conns, conns
		switch s.Interpolate(c.Connections) {
		case "", "reuse":
		case "fresh":
			tr.DisableKeepAlives = true
		default:
			return nil, fmt.Errorf("packs.%s.connections is fresh or reuse, not %q", Name, c.Connections)
		}
		st.client = &http.Client{Transport: tr}
		return st, nil
	})
}

// binding is a stream bound to a service's endpoint, and where its records' addresses come from.
type binding struct {
	name, method, path            string
	service                       *rest.Service
	stream                        *streams.Stream
	pathMeta, queryMeta, headMeta string
	pathSet, querySet, headerSet  map[string]string
}

// observed is which payload paths identify a service's hand-written requests.
type observed struct {
	service string
	paths   []string
}

type scenarioContext struct {
	mu        sync.Mutex
	bindings  map[string]*binding
	observed  []observed
	observing bool
	last      *arrival.Result
}

var stateKey = core.NewStateKey(Name, func(*core.Scenario) *scenarioContext {
	return &scenarioContext{bindings: map[string]*binding{}}
}, nil)

func (c *scenarioContext) binding(name string) (*binding, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.bindings[name]
	if !ok {
		return nil, fmt.Errorf("no %s stream is bound; bind it with \"the %s stream of <METHOD> requests to <path>\"", name, name)
	}
	return b, nil
}

// Step definitions that send traffic, which stormbreaker-core's readiness gate holds back.
const (
	stepExecute = "stormbreaker.rest.stream.execute"
	stepSend    = "stormbreaker.rest.send"
)

// Pack returns the stormbreaker-rest pack.
func Pack() core.Pack {
	sbcore.Traffic(stepExecute, stepSend)
	return pack{}
}

type pack struct{}

func (pack) Manifest() core.Manifest {
	return core.Manifest{
		Name: Name, Namespace: Name, Requires: []string{"rest"}, ConfigSchema: configSchema,
		Doc: "Load through a service's front door: a stream's records become HTTP requests to a service axx's rest pack " +
			"declared, sent open-model at a declared arrival rate, and what the client saw is recorded as the client view. " +
			"Builds on stormbreaker-core (list it in axx-packs.yaml): binding a stream declares the identities it will create " +
			"before anything is sent.",
		Steps: steps(),
	}
}

var configSchema = json.RawMessage(`{
  "type": "object",
  "description": "How stormbreaker-rest sends its load.",
  "properties": {
    "connections": {"enum": ["reuse", "fresh"], "description": "reuse (the default) keeps a pool of connections open, which a load balancer spreads across the service's replicas as it spreads its users'; fresh opens one for each request, and runs out of the machine's ports past a few thousand requests a second."},
    "maxConnections": {"type": "integer", "description": "How many connections to the service may be open at once (default 512): past it, requests wait for one, and their latency says so."},
    "maxInFlight": {"type": "integer", "description": "How many requests may be in flight before the load stops, failing because the service is not keeping up (default 50000)."},
    "timeout": {"type": "string", "description": "How long a request may take (default 30s)."},
    "maxDrift": {"type": "string", "description": "How late requests may leave their due time, for five seconds in a row, before the load stops, failing because the driver is not keeping up (default 1s; off turns it off)."}
  },
  "additionalProperties": false
}`)

func steps() []core.StepDef {
	meta := func(id, what, field string) core.StepDef {
		return core.StepDef{
			ID: "stormbreaker.rest.stream." + id, Keyword: "Given",
			Expr: "the {word} stream " + what + " are the {word} metadata",
			Doc:  "Take each request's " + what + " from a metadata entry of its record: a map, by name.",
			Run: func(sc *core.Scenario, a core.Args) error {
				b, err := stateKey.Of(sc).binding(a.String(0))
				if err != nil {
					return err
				}
				switch field {
				case "path":
					b.pathMeta = a.String(1)
				case "query":
					b.queryMeta = a.String(1)
				default:
					b.headMeta = a.String(1)
				}
				return nil
			},
		}
	}
	set := func(id, what, field string) core.StepDef {
		return core.StepDef{
			ID: "stormbreaker.rest.stream." + id, Keyword: "Given",
			Expr: "the {word} stream " + what + " {word} is {string}",
			Doc: "Give every request of the stream the same " + what + ", over what its record says. It is text: " +
				"one that reads as a number or a boolean goes in double quotes inside the step's own (`'\"42\"'`).",
			// What it sets, by axx's rule for values (its ADR 0014): axx validate and the run read it.
			TextArgs: []int{2},
			Run: func(sc *core.Scenario, a core.Args) error {
				b, err := stateKey.Of(sc).binding(a.String(0))
				if err != nil {
					return err
				}
				v := sc.Suite().Interpolate(a.String(2))
				switch field {
				case "path":
					b.pathSet[a.String(1)] = v
				case "query":
					b.querySet[a.String(1)] = v
				default:
					b.headerSet[a.String(1)] = v
				}
				return nil
			},
		}
	}
	return []core.StepDef{
		{
			ID: "stormbreaker.rest.stream", Keyword: "Given",
			Expr: "the {word} stream of {word} requests to {word}[[ on {service}]]",
			Doc: "Bind a stream (`<name>.stream.yaml`) to an endpoint: each record's data is a request's JSON body. Binding " +
				"declares every identity the stream will create, at the service and path, before anything is sent. The path " +
				"can name variables, `{name}`, filled from the record's metadata.",
			Examples: []string{
				"Given the parcels stream of POST requests to /parcels",
				"Given the collections stream of POST requests to /parcels/{reference}/collection on parcels",
			},
			Run: bindStream,
		},
		meta("path-variables", "path variables", "path"),
		meta("query-parameters", "query parameters", "query"),
		meta("headers", "headers", "header"),
		set("path-variable", "path variable", "path"),
		set("query-parameter", "query parameter", "query"),
		set("header", "header", "header"),
		{
			ID: "stormbreaker.rest.identified", Keyword: "Given",
			Expr: "the requests[[ on {service}]] are identified by {word}",
			Doc: "Declare the identities in the payloads of the requests written by hand (with the rest pack's steps) before " +
				"each is sent: the values at the JSON paths given, comma-separated.",
			Examples: []string{"Given the requests are identified by $.reference", "Given the requests on parcels are identified by $.reference,$.pieces[*].id"},
			Run:      identified,
		},
		{
			ID: stepExecute, Keyword: "When",
			Expr:     "the {word} stream is sent at {int} requests per second",
			Doc:      "Send the stream's requests open-model: request i is due at i/rate seconds, however the ones before it are doing.",
			Examples: []string{"When the parcels stream is sent at 25 requests per second"},
			Run:      sendStream,
		},
		{
			ID: stepSend, Keyword: "When",
			Expr:     "{int} {word} requests are sent to {word} at {int} requests per second[[ on {service}]]",
			Doc:      "Send requests without bodies, open-model, at the rate given.",
			Examples: []string{"When 100 GET requests are sent to /ping at 50 requests per second"},
			Run:      sendCount,
		},
		{
			ID: "stormbreaker.rest.no-failures", Keyword: "Then",
			Expr:     "the load completes with no failed requests",
			Doc:      "Check that no request of the last load failed: none went unanswered, and none was answered 400 or over.",
			Examples: []string{"Then the load completes with no failed requests"},
			Run:      noFailures,
		},
		{
			ID: "stormbreaker.rest.client-quantile", Keyword: "Then",
			Expr: "the client's {word} latency of the load is at most {float} seconds",
			Doc: "Check a latency quantile of the last load as the client saw it (`p50`, `p99`, `p99.9`): the client view, " +
				"which is labelled apart from what the service's metrics say.",
			Examples: []string{"Then the client's p99 latency of the load is at most 0.5 seconds"},
			Run:      clientQuantile,
		},
	}
}

// service is the step's named service, or the scenario's default REST service.
func service(sc *core.Scenario, a core.Args, i int) (*rest.Service, error) {
	if a.Present(i) {
		if s, ok := a.Value(i).(*rest.Service); ok {
			return s, nil
		}
	}
	return rest.Context(sc).Service()
}

func currentRun(sc *core.Scenario) (*sbcore.Run, error) {
	r, ok := sbcore.Current(sc)
	if !ok {
		return nil, fmt.Errorf("a stream needs a stormbreaker run: tag the scenario @%s<group>, with %s in axx-packs.yaml", sbcore.TagGroup, sbcore.Name)
	}
	return r, nil
}

func bindStream(sc *core.Scenario, a core.Args) error {
	r, err := currentRun(sc)
	if err != nil {
		return err
	}
	svc, err := service(sc, a, 3)
	if err != nil {
		return err
	}
	name, path := a.String(0), a.String(2)
	s, err := r.Bind(sc.Context(), name, svc.Name, path)
	if err != nil {
		return err
	}
	c := stateKey.Of(sc)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.bindings[name]; ok {
		return fmt.Errorf("the %s stream is already bound", name)
	}
	c.bindings[name] = &binding{
		name: name, method: strings.ToUpper(a.String(1)), path: path, service: svc, stream: s,
		pathSet: map[string]string{}, querySet: map[string]string{}, headerSet: map[string]string{},
	}
	return nil
}

func identified(sc *core.Scenario, a core.Args) error {
	r, err := currentRun(sc)
	if err != nil {
		return err
	}
	svc, err := service(sc, a, 0)
	if err != nil {
		return err
	}
	paths := jsonpath.Paths(a.String(1))
	for _, p := range paths {
		if _, err := jsonpath.Read([]byte("{}"), p); err != nil {
			return err
		}
	}
	c := stateKey.Of(sc)
	c.mu.Lock()
	c.observed = append(c.observed, observed{service: svc.Name, paths: paths})
	first := !c.observing
	c.observing = true
	c.mu.Unlock()
	if first {
		sbcore.Observe(sc, func(r *sbcore.Run) error { return observe(sc, r) })
	}
	return observe(sc, r)
}

// observe declares the identities in the payloads of the requests staged by hand. The locator is
// the path each request goes to, read off the staged request: where an identity was seen, not
// where a step said to look.
func observe(sc *core.Scenario, r *sbcore.Run) error {
	c := stateKey.Of(sc)
	c.mu.Lock()
	decls := append([]observed(nil), c.observed...)
	c.mu.Unlock()
	for _, svc := range rest.Context(sc).Services() {
		for _, req := range svc.Requests() {
			if req.Payload == nil || strings.TrimSpace(*req.Payload) == "" {
				continue
			}
			for _, d := range decls {
				if d.service != svc.Name {
					continue
				}
				for _, p := range d.paths {
					ids, err := jsonpath.Read([]byte(*req.Payload), p)
					if err != nil {
						return err
					}
					if err := r.DeclareObserved(sc.Context(), svc.Name, req.Path+":"+p, ids); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func ready(sc *core.Scenario) error {
	return sbcore.Ready(sc)
}

func sendStream(sc *core.Scenario, a core.Args) error {
	st, err := load(sc.Suite())
	if err != nil {
		return err
	}
	b, err := stateKey.Of(sc).binding(a.String(0))
	if err != nil {
		return err
	}
	if err := ready(sc); err != nil {
		return err
	}
	rate := float64(a.Int(1))
	// Requests are built as they come due, a few thousand ahead: building them all first would
	// hold the whole stream.
	requests := func(yield func(*http.Request, error) bool) {
		for rec, err := range b.stream.All() {
			if err != nil {
				yield(nil, err)
				return
			}
			req, err := b.request(sc.Context(), rec)
			if !yield(req, err) || err != nil {
				return
			}
		}
	}
	return run(sc, st, b.name, sending{stream: b.name, target: b.service.Name, planned: b.stream.Records()}, rate, requests)
}

func sendCount(sc *core.Scenario, a core.Args) error {
	st, err := load(sc.Suite())
	if err != nil {
		return err
	}
	svc, err := service(sc, a, 4)
	if err != nil {
		return err
	}
	n, method, path, rate := int64(a.Int(0)), strings.ToUpper(a.String(1)), a.String(2), float64(a.Int(3))
	u := strings.TrimRight(svc.URL, "/") + path
	requests := func(yield func(*http.Request, error) bool) {
		for range n {
			req, err := http.NewRequestWithContext(sc.Context(), method, u, nil)
			if !yield(req, err) || err != nil {
				return
			}
		}
	}
	what := fmt.Sprintf("%d %s %s", n, method, path)
	return run(sc, st, what, sending{stream: what, target: svc.Name, planned: n}, rate, requests)
}

// sending is what a run of requests adds to the run's progress: what it sends, where, and how
// much.
type sending struct {
	stream, target string
	planned        int64
}

// report is the client view of a load, attached to the report.
type report struct {
	Load     string           `json:"load"`
	Rate     float64          `json:"ratePerSecond"`
	Sent     int64            `json:"sent"`
	Failed   int64            `json:"failed"`
	Elapsed  string           `json:"elapsed"`
	Latency  arrival.Summary  `json:"latencySeconds"`
	Drift    arrival.Summary  `json:"arrivalDriftSeconds"`
	Error    string           `json:"firstError,omitempty"`
	Statuses map[string]int64 `json:"statuses,omitempty"`
}

func run(sc *core.Scenario, st *settings, what string, l sending, rate float64, requests iter.Seq2[*http.Request, error]) error {
	live := &arrival.Live{}
	if r, ok := sbcore.Current(sc); ok {
		r.Load(l.stream, sbcore.Arrivals(l.stream, "rest", l.target, rate, l.planned, live))
	}
	var mu sync.Mutex
	statuses := map[string]int64{}
	res, err := arrival.Run(sc.Context(), rate, requests, func(ctx context.Context, req *http.Request) error {
		resp, err := st.client.Do(req.WithContext(ctx))
		if err != nil {
			mu.Lock()
			statuses["error"]++
			mu.Unlock()
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		mu.Lock()
		statuses[fmt.Sprint(resp.StatusCode)]++
		mu.Unlock()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("%s %s answered %s", req.Method, req.URL.Path, resp.Status)
		}
		return nil
	}, arrival.Options{Timeout: st.timeout, MaxInFlight: st.maxInFlight, MaxDrift: st.maxDrift, Live: live})
	if res != nil {
		c := stateKey.Of(sc)
		c.mu.Lock()
		c.last = res
		c.mu.Unlock()
		rep := report{
			Load: what, Rate: rate, Sent: res.Sent, Failed: res.Failed, Elapsed: res.Elapsed.Round(time.Millisecond).String(),
			Latency: res.Latency.Summary(), Drift: res.Drift.Summary(), Error: res.FirstError, Statuses: statuses,
		}
		body, _ := json.MarshalIndent(rep, "", "  ")
		sc.Attach("application/json", body, "stormbreaker-rest client view: "+what)
		sc.Log("stormbreaker-rest: %s: %d sent at %g/s, %d failed, client p99 %s", what, res.Sent, rate, res.Failed, res.Latency.Quantile(0.99))
	}
	return err
}

func last(sc *core.Scenario) (*arrival.Result, error) {
	c := stateKey.Of(sc)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		return nil, errors.New("no load was sent in this scenario")
	}
	return c.last, nil
}

func noFailures(sc *core.Scenario, _ core.Args) error {
	res, err := last(sc)
	if err != nil {
		return err
	}
	if res.Failed > 0 {
		return core.Fail(fmt.Sprintf("%d of %d requests of the last load failed; the first: %s", res.Failed, res.Sent, res.FirstError), "0 failed", fmt.Sprintf("%d failed", res.Failed))
	}
	return nil
}

var quantileWord = regexp.MustCompile(`^p(\d{1,2}(?:\.\d+)?)$`)

func clientQuantile(sc *core.Scenario, a core.Args) error {
	m := quantileWord.FindStringSubmatch(a.String(0))
	if m == nil {
		return fmt.Errorf("a quantile is like p50, p99 or p99.9, not %q", a.String(0))
	}
	q, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return err
	}
	limit, err := strconv.ParseFloat(a.Raw(1), 64)
	if err != nil {
		return err
	}
	res, err := last(sc)
	if err != nil {
		return err
	}
	got := res.Latency.Quantile(q / 100).Seconds()
	if got > limit {
		return core.Fail(fmt.Sprintf("the client's %s latency of the last load is %gs, above the %gs limit", a.String(0), got, limit),
			fmt.Sprintf("at most %gs", limit), fmt.Sprintf("%gs", got))
	}
	return nil
}

var variable = regexp.MustCompile(`\{([^{}]+)\}`)

// request builds the request for a record: the service's URL, the path with its variables
// filled, the query, the headers and the record's data as its JSON body.
func (b *binding) request(ctx context.Context, rec *streams.Record) (*http.Request, error) {
	u, err := b.address(rec)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, b.method, u, bytes.NewReader(rec.Data.CompactJSON()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	headers, err := merged(rec, b.headMeta, "headers", b.headerSet)
	if err != nil {
		return nil, err
	}
	for _, k := range sortedKeys(headers) {
		req.Header.Set(k, fmt.Sprint(headers[k]))
	}
	return req, nil
}

func (b *binding) address(rec *streams.Record) (string, error) {
	vars, err := metaMap(rec, b.pathMeta, "path variables")
	if err != nil {
		return "", err
	}
	var missing error
	path := variable.ReplaceAllStringFunc(b.path, func(m string) string {
		name := m[1 : len(m)-1]
		if v, ok := b.pathSet[name]; ok {
			return url.PathEscape(v)
		}
		// A variable can name a nested value: it is a path into the metadata, not a key.
		v := valueAt(vars, name)
		if v == nil {
			if b.pathMeta == "" {
				missing = fmt.Errorf("the path %s names {%s}, but the %s stream takes no path variables from its records; "+
					"say where with \"the %s stream path variables are the <name> metadata\"", b.path, name, b.name, b.name)
			} else {
				missing = fmt.Errorf("the %s metadata of %s has no %s, which the path %s names", b.pathMeta, rec.Ref, name, b.path)
			}
			return m
		}
		return url.PathEscape(fmt.Sprint(v))
	})
	if missing != nil {
		return "", missing
	}
	query, err := merged(rec, b.queryMeta, "query parameters", b.querySet)
	if err != nil {
		return "", err
	}
	u := strings.TrimRight(b.service.URL, "/") + path
	if len(query) > 0 {
		q := url.Values{}
		for _, k := range sortedKeys(query) {
			q.Set(k, fmt.Sprint(query[k]))
		}
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		u += sep + q.Encode()
	}
	return u, nil
}

func metaMap(rec *streams.Record, entry, what string) (map[string]any, error) {
	if entry == "" {
		return nil, nil
	}
	v := rec.Metadata.Get(entry)
	if v == nil {
		return nil, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the %s metadata of %s is not a map of %s", entry, rec.Ref, what)
	}
	return m, nil
}

// merged is a record's metadata map with the step's values over it.
func merged(rec *streams.Record, entry, what string, set map[string]string) (map[string]any, error) {
	m, err := metaMap(rec, entry, what)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(m)+len(set))
	for k, v := range m {
		out[k] = v
	}
	for k, v := range set {
		out[k] = v
	}
	return out, nil
}

// valueAt reads a dotted path in a metadata map.
func valueAt(m map[string]any, path string) any {
	var v any = m
	for _, part := range strings.Split(path, ".") {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[part]
	}
	return v
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
