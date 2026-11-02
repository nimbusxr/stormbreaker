package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"

	"github.com/nimbusxr/stormbreaker/api"
	"github.com/nimbusxr/stormbreaker/protocol"
)

// route is one operation of the API. Its pattern is the path as the OpenAPI document writes it;
// every route is in the document, and the document has no path without a route.
type route struct {
	method, path string
	// open routes need no token: health, metrics (counts, never names) and the contract itself.
	open bool
	// may says whether the request's principal may make it, from its path; nil lets every
	// principal (reads). Grants that depend on the body are checked in the handler (ADR 0019).
	may     func(o *Orchestrator, r *http.Request, p *Principal) error
	handler func(o *Orchestrator, w http.ResponseWriter, r *http.Request) error
}

// Grants that depend on the request's path.
var (
	asDispatcher = func(_ *Orchestrator, r *http.Request, p *Principal) error { return mayDispatch(p, r.PathValue("name")) }
	asSuite      = func(o *Orchestrator, r *http.Request, p *Principal) error {
		return o.mayRunAsSuite(r.Context(), p, r.PathValue("id"))
	}
	asReporter = func(o *Orchestrator, r *http.Request, p *Principal) error {
		return o.mayReportAs(r.Context(), p, r.PathValue("id"), r.PathValue("name"))
	}
)

var routes = []route{
	{method: http.MethodGet, path: "/healthz", open: true, handler: (*Orchestrator).health},
	{method: http.MethodGet, path: "/metrics", open: true, handler: (*Orchestrator).metrics},
	{method: http.MethodGet, path: "/openapi.yaml", open: true, handler: (*Orchestrator).specYAML},
	{method: http.MethodGet, path: "/openapi.json", open: true, handler: (*Orchestrator).specJSON},

	{method: http.MethodPost, path: "/groups", handler: (*Orchestrator).postGroup},
	{method: http.MethodGet, path: "/groups", handler: (*Orchestrator).getGroups},
	{method: http.MethodGet, path: "/groups/{group}/dispatchers", handler: (*Orchestrator).getMembers},
	{method: http.MethodPut, path: "/groups/{group}/dispatchers/{name}", may: asDispatcher, handler: (*Orchestrator).putMember},
	{method: http.MethodDelete, path: "/groups/{group}/dispatchers/{name}", may: asDispatcher, handler: (*Orchestrator).deleteMember},

	{method: http.MethodPost, path: "/dispatchers", handler: (*Orchestrator).postDispatcher},
	{method: http.MethodGet, path: "/dispatchers", handler: (*Orchestrator).getDispatchers},

	{method: http.MethodPost, path: "/runs", handler: (*Orchestrator).postRun},
	{method: http.MethodGet, path: "/runs", handler: (*Orchestrator).getRuns},
	{method: http.MethodGet, path: "/runs/{id}", handler: (*Orchestrator).getRun},
	{method: http.MethodPost, path: "/runs/{id}/events", may: asSuite, handler: (*Orchestrator).postEvent},
	{method: http.MethodPost, path: "/runs/{id}/heartbeat", may: asSuite, handler: (*Orchestrator).postHeartbeat},
	{method: http.MethodPost, path: "/runs/{id}/streams", may: asSuite, handler: (*Orchestrator).postStream},
	{method: http.MethodPut, path: "/runs/{id}/progress", may: asSuite, handler: (*Orchestrator).putProgress},
	{method: http.MethodGet, path: "/runs/{id}/progress", handler: (*Orchestrator).getProgress},
	{method: http.MethodPost, path: "/runs/{id}/dispatchers/{name}/status", may: asDispatcher, handler: (*Orchestrator).postStatus},
	{method: http.MethodPost, path: "/runs/{id}/footprint/{name}", may: asReporter, handler: (*Orchestrator).postFootprint},
	{method: http.MethodGet, path: "/runs/{id}/footprint", handler: (*Orchestrator).getFootprint},
	{method: http.MethodGet, path: "/runs/{id}/footprint/claims", handler: (*Orchestrator).getClaims},
	{method: http.MethodGet, path: "/runs/{id}/journal", handler: (*Orchestrator).getJournal},

	{method: http.MethodGet, path: "/events", handler: (*Orchestrator).getEvents},
}

// Handler is the orchestrator's HTTP API.
func (o *Orchestrator) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.HandleFunc(rt.method+" "+rt.path, func(w http.ResponseWriter, r *http.Request) {
			err := func() error {
				if rt.open {
					return nil
				}
				p, ok := o.principal(r)
				if !ok {
					w.Header().Set("WWW-Authenticate", `Bearer realm="stormbreaker"`)
					return refuse(http.StatusUnauthorized, "a bearer token the orchestrator knows is required")
				}
				*r = *r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
				if rt.may != nil {
					return rt.may(o, r, p)
				}
				return nil
			}()
			if err == nil {
				err = rt.handler(o, w, r)
			}
			if err != nil {
				var ae *apiError
				if errors.As(err, &ae) {
					writeProblemFor(w, ae)
					return
				}
				if diskFull(err) {
					// The write rolled back and the store is as it was: say why, as the reserve does.
					o.log.Error("the volume is full", "method", r.Method, "path", r.URL.Path, "err", err)
					writeProblem(w, http.StatusInsufficientStorage, "the orchestrator's volume is full: the write was rolled back, and nothing was lost; make room on its volume")
					return
				}
				if r.Context().Err() == nil {
					o.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
				}
				writeProblem(w, http.StatusInternalServerError, "the orchestrator could not answer: see its log")
			}
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, http.StatusNotFound, "no operation at "+r.Method+" "+r.URL.Path+"; see /openapi.yaml")
	})
	return mux
}

// diskFull is whether err is SQLite's "database or disk is full" (SQLITE_FULL, 13).
func diskFull(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && coded.Code()&0xff == 13
}

// principalOf is the principal the request was let in as.
func principalOf(r *http.Request) *Principal {
	if p, ok := r.Context().Value(principalKey{}).(*Principal); ok {
		return p
	}
	return &Principal{Name: "nobody"}
}

func writeProblemFor(w http.ResponseWriter, ae *apiError) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(ae.status)
	_ = json.NewEncoder(w).Encode(protocol.Problem{Title: http.StatusText(ae.status), Status: ae.status, Detail: ae.detail, ConflictsWith: ae.conflictsWith})
}

func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(protocol.Problem{Title: http.StatusText(status), Status: status, Detail: detail})
}

func writeJSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		return refuse(http.StatusBadRequest, "cannot read the request body: %v", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return refuse(http.StatusBadRequest, "the request body is not the JSON this operation takes: %v", err)
	}
	return nil
}

func queryInt(r *http.Request, name string, def, lo, hi int64) (int64, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, refuse(http.StatusBadRequest, "%s is a number, not %q", name, s)
	}
	return max(lo, min(hi, n)), nil
}

func instanceOf(r *http.Request, name string) (string, error) {
	inst := r.URL.Query().Get("instance")
	if inst == "" && name == protocol.Suite {
		return protocol.Suite, nil
	}
	if inst == "" {
		return "", refuse(http.StatusBadRequest, "instance is required: which instance of %s is reporting", name)
	}
	return inst, nil
}

func (o *Orchestrator) health(w http.ResponseWriter, r *http.Request) error {
	if err := o.ro.PingContext(r.Context()); err != nil {
		return refuse(http.StatusServiceUnavailable, "the database does not answer: %v", err)
	}
	return writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "protocol": protocol.Version})
}

func (o *Orchestrator) specYAML(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "application/yaml")
	_, err := w.Write(api.Orchestrator)
	return err
}

var specJSON = sync.OnceValues(func() ([]byte, error) { return yaml.YAMLToJSON(api.Orchestrator) })

func (o *Orchestrator) specJSON(w http.ResponseWriter, _ *http.Request) error {
	b, err := specJSON()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(b)
	return err
}

func (o *Orchestrator) postGroup(w http.ResponseWriter, r *http.Request) error {
	var req protocol.CreateGroup
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := mayCreateGroup(principalOf(r), req.Name); err != nil {
		return err
	}
	g, err := o.CreateGroup(r.Context(), req.Name)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, g)
}

func (o *Orchestrator) getGroups(w http.ResponseWriter, r *http.Request) error {
	gs, err := o.Groups(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, gs)
}

func (o *Orchestrator) getMembers(w http.ResponseWriter, r *http.Request) error {
	ms, err := o.Members(r.Context(), r.PathValue("group"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, ms)
}

func (o *Orchestrator) putMember(w http.ResponseWriter, r *http.Request) error {
	if err := o.Join(r.Context(), r.PathValue("group"), r.PathValue("name")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (o *Orchestrator) deleteMember(w http.ResponseWriter, r *http.Request) error {
	if err := o.Leave(r.Context(), r.PathValue("group"), r.PathValue("name")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (o *Orchestrator) postDispatcher(w http.ResponseWriter, r *http.Request) error {
	var req protocol.Registration
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := mayDispatch(principalOf(r), req.Name); err != nil {
		return err
	}
	d, err := o.Register(r.Context(), req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, d)
}

func (o *Orchestrator) getDispatchers(w http.ResponseWriter, r *http.Request) error {
	ds, err := o.Dispatchers(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, ds)
}

func (o *Orchestrator) postRun(w http.ResponseWriter, r *http.Request) error {
	var req protocol.OpenRun
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := mayOpen(principalOf(r), req.Group); err != nil {
		return err
	}
	run, err := o.OpenRun(r.Context(), req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, run)
}

func (o *Orchestrator) getRuns(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	var before int64
	limit := 100
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return refuse(http.StatusBadRequest, "before is a time in Unix milliseconds, not %q", v)
		}
		before = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			return refuse(http.StatusBadRequest, "limit is from 1 to 1000, not %q", v)
		}
		limit = n
	}
	list, err := o.Runs(r.Context(), q["state"], q.Get("group"), before, limit)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, list)
}

func (o *Orchestrator) getRun(w http.ResponseWriter, r *http.Request) error {
	run, err := o.Run(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, run)
}

func (o *Orchestrator) postEvent(w http.ResponseWriter, r *http.Request) error {
	var req protocol.PublishEvent
	if err := readJSON(r, &req); err != nil {
		return err
	}
	ev, err := o.Publish(r.Context(), r.PathValue("id"), req.Type)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, ev)
}

func (o *Orchestrator) postHeartbeat(w http.ResponseWriter, r *http.Request) error {
	if err := o.Heartbeat(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (o *Orchestrator) putProgress(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, progressLimit)
	var req protocol.Progress
	if err := readJSON(r, &req); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			return refuse(http.StatusRequestEntityTooLarge, "a progress weighs at most %d KiB", progressLimit>>10)
		}
		return err
	}
	if err := o.SetProgress(r.Context(), r.PathValue("id"), req); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (o *Orchestrator) getProgress(w http.ResponseWriter, r *http.Request) error {
	p, err := o.Progress(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, p)
}

func (o *Orchestrator) postStream(w http.ResponseWriter, r *http.Request) error {
	var req protocol.StreamRef
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := o.AddStream(r.Context(), r.PathValue("id"), req); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (o *Orchestrator) postStatus(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	inst, err := instanceOf(r, name)
	if err != nil {
		return err
	}
	var req protocol.Status
	if err := readJSON(r, &req); err != nil {
		return err
	}
	if err := o.ReportStatus(r.Context(), r.PathValue("id"), name, inst, req); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (o *Orchestrator) postFootprint(w http.ResponseWriter, r *http.Request) error {
	name := r.PathValue("name")
	inst, err := instanceOf(r, name)
	if err != nil {
		return err
	}
	var req protocol.FootprintReport
	if err := readJSON(r, &req); err != nil {
		return err
	}
	res, err := o.ReportFootprint(r.Context(), r.PathValue("id"), name, inst, req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, res)
}

func (o *Orchestrator) getFootprint(w http.ResponseWriter, r *http.Request) error {
	after, err := queryInt(r, "after", 0, 0, 1<<62)
	if err != nil {
		return err
	}
	reports, err := queryInt(r, "reports", 64, 1, 1024)
	if err != nil {
		return err
	}
	until, err := queryInt(r, "until", 0, 0, 1<<62)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	f := protocol.FootprintFilter{Dispatchers: q["dispatcher"], Store: q.Get("store"), Locator: q.Get("locator")}
	page, err := o.FootprintPage(r.Context(), r.PathValue("id"), after, until, int(reports), f)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, page)
}

func (o *Orchestrator) getClaims(w http.ResponseWriter, r *http.Request) error {
	cs, err := o.Claims(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, cs)
}

func (o *Orchestrator) getJournal(w http.ResponseWriter, r *http.Request) error {
	j, err := o.Journal(r.Context(), r.PathValue("id"))
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, err = io.WriteString(w, j)
	return err
}

func (o *Orchestrator) getEvents(w http.ResponseWriter, r *http.Request) error {
	since, err := queryInt(r, "since", 0, 0, 1<<62)
	if err != nil {
		return err
	}
	limit, err := queryInt(r, "limit", 100, 1, 1000)
	if err != nil {
		return err
	}
	feed, err := o.Events(r.Context(), since, int(limit), r.URL.Query()["group"]...)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, feed)
}
