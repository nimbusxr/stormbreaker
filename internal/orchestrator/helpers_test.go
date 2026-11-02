package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
	vconfig "github.com/pb33f/libopenapi-validator/config"
	liberrors "github.com/pb33f/libopenapi-validator/errors"

	"github.com/nimbusxr/stormbreaker/api"
	"github.com/nimbusxr/stormbreaker/protocol"
)

const testToken = "test-token"

// clock is a clock tests move by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// env is an orchestrator under test, its clock, and its API, every exchange of which is held to
// the OpenAPI document.
type env struct {
	t     *testing.T
	o     *Orchestrator
	clock *clock
	h     http.Handler
	v     validator.Validator
}

func newEnv(t *testing.T, mutate ...func(*Config)) *env {
	t.Helper()
	cfg := Config{
		DB: filepath.Join(t.TempDir(), "orchestrator.db"), Token: testToken,
		RetryBase: time.Second, RetryMax: 4 * time.Second, MaxAttempts: 3,
	}
	for _, m := range mutate {
		m(&cfg)
	}
	c := &clock{t: time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)}
	o, err := Open(context.Background(), cfg, WithClock(c.now), WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { o.Close() })
	// Started a lease ago, as a running orchestrator has: the restart test starts one now.
	o.started = c.now().Add(-o.cfg.Lease)
	return &env{t: t, o: o, clock: c, h: o.Handler(), v: specValidator(t)}
}

var specDoc = sync.OnceValues(func() (libopenapi.Document, error) { return libopenapi.NewDocument(api.Orchestrator) })

func specValidator(t *testing.T) validator.Validator {
	t.Helper()
	doc, err := specDoc()
	if err != nil {
		t.Fatal(err)
	}
	model, err := doc.BuildV3Model()
	if err != nil {
		t.Fatal(err)
	}
	return validator.NewValidatorFromV3Model(&model.Model, vconfig.WithFormatAssertions())
}

// call sends a request to the API and checks the exchange against the OpenAPI document.
func (e *env) call(method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.callAs(testToken, method, path, body)
}

// callAs calls the API with token as the bearer token (ADR 0019); "" sends none.
func (e *env) callAs(token, method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	// Raw registrations and runs speak this protocol unless a test says otherwise, as the client's do.
	switch b := body.(type) {
	case protocol.Registration:
		if b.Protocol == 0 {
			b.Protocol = protocol.Version
		}
		body = b
	case protocol.OpenRun:
		if b.Protocol == 0 {
			b.Protocol = protocol.Version
		}
		body = b
	}
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			e.t.Fatal(err)
		}
	}
	newReq := func() *http.Request {
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		return r
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, newReq())
	resp := rec.Result()
	resp.Body = io.NopCloser(bytes.NewReader(rec.Body.Bytes()))
	// A refusal of a request the document does not allow is the point of some tests: hold such
	// requests' responses to it, and every other exchange whole.
	var ok bool
	var errs []*liberrors.ValidationError
	if rec.Code >= 400 {
		ok, errs = e.v.ValidateHttpResponse(newReq(), resp)
	} else {
		ok, errs = e.v.ValidateHttpRequestResponse(newReq(), resp)
	}
	if !ok {
		var msgs []string
		for _, err := range errs {
			msgs = append(msgs, err.Error()+": "+err.Reason)
			for _, se := range err.SchemaValidationErrors {
				msgs = append(msgs, "  "+se.FieldPath+": "+se.Reason)
			}
		}
		e.t.Errorf("%s %s -> %d does not follow the OpenAPI document:\n%s\nbody: %s", method, path, rec.Code, strings.Join(msgs, "\n"), rec.Body.String())
	}
	return rec
}

// expect sends a request and fails the test unless it answers with status.
func (e *env) expect(status int, method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	rec := e.call(method, path, body)
	if rec.Code != status {
		e.t.Fatalf("%s %s -> %d, want %d: %s", method, path, rec.Code, status, rec.Body.String())
	}
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return v
}

func (e *env) secret() string {
	s, err := protocol.NewSecret()
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

// register registers each "name/instance" and puts every name in group.
func (e *env) register(group string, instances ...string) {
	e.t.Helper()
	for _, ni := range instances {
		name, inst, _ := strings.Cut(ni, "/")
		e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", protocol.Registration{
			Name: name, Instance: inst, Team: "team-" + name, CallbackURL: "http://" + name + "-" + inst + ".invalid/events", Secret: e.secret(),
		})
		e.expect(http.StatusNoContent, http.MethodPut, "/groups/"+group+"/dispatchers/"+name, nil)
	}
}

func (e *env) open(group string, requires ...string) protocol.Run {
	e.t.Helper()
	return decode[protocol.Run](e.t, e.expect(http.StatusCreated, http.MethodPost, "/runs", protocol.OpenRun{Initiator: "suite-under-test", Group: group, Requires: requires}))
}

func (e *env) run(id string) protocol.Run {
	e.t.Helper()
	return decode[protocol.Run](e.t, e.expect(http.StatusOK, http.MethodGet, "/runs/"+id, nil))
}

// status reports as a dispatcher does: it reads the run, runs a round from the footprint as it
// is, and reports on that version.
func (e *env) status(runID, ni, state string) {
	e.t.Helper()
	e.statusOn(runID, ni, state, e.run(runID).FootprintVersion)
}

// statusOn reports a round that began from footprint version v.
func (e *env) statusOn(runID, ni, state string, v int64) {
	e.t.Helper()
	name, inst, _ := strings.Cut(ni, "/")
	e.expect(http.StatusNoContent, http.MethodPost, "/runs/"+runID+"/dispatchers/"+name+"/status?instance="+inst, protocol.Status{State: state, FootprintVersion: v})
}

func (e *env) footprint(runID, ni string, entries ...protocol.FootprintEntry) bool {
	e.t.Helper()
	name, inst, _ := strings.Cut(ni, "/")
	return decode[protocol.FootprintResult](e.t, e.expect(http.StatusOK, http.MethodPost, "/runs/"+runID+"/footprint/"+name+"?instance="+inst, protocol.FootprintReport{Entries: entries})).Grew
}

func (e *env) publish(runID, eventType string) {
	e.t.Helper()
	e.expect(http.StatusAccepted, http.MethodPost, "/runs/"+runID+"/events", protocol.PublishEvent{Type: eventType})
}

func expected(store, locator string, ids ...string) protocol.FootprintEntry {
	return protocol.FootprintEntry{Store: store, Locator: locator, Identities: append([]string{}, ids...), Resolution: protocol.ResolutionExpected}
}

func removed(store, locator string, ids ...string) protocol.FootprintEntry {
	return protocol.FootprintEntry{Store: store, Locator: locator, Identities: append([]string{}, ids...), Resolution: protocol.ResolutionRemovedVerified}
}

// keepAlive renews the leases of runs.
func (e *env) keepAlive(runIDs ...string) {
	e.t.Helper()
	for _, id := range runIDs {
		e.expect(http.StatusNoContent, http.MethodPost, "/runs/"+id+"/heartbeat", nil)
	}
}

// eventTypes are the types of a run's events, in order.
func (e *env) eventTypes(runID string) []string {
	e.t.Helper()
	feed := decode[protocol.EventFeed](e.t, e.expect(http.StatusOK, http.MethodGet, "/events?limit=1000", nil))
	var out []string
	for _, ev := range feed.Events {
		if ev.RunID == runID {
			out = append(out, ev.Type)
		}
	}
	return out
}

func (e *env) housekeeping() {
	e.t.Helper()
	ctx := context.Background()
	for _, fn := range []func(context.Context) error{e.o.EnforceReadiness, e.o.EnforceLeases, e.o.CloseSettled} {
		if err := fn(ctx); err != nil {
			e.t.Fatal(err)
		}
	}
}
