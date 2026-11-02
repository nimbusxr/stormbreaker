// Package labtest is a stormbreaker lab in one process, for tests: an orchestrator that really
// serves and notifies, and dispatchers that join it.
package labtest

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/orchestrator"
	"github.com/nimbusxr/stormbreaker/protocol"
)

// Token is the lab orchestrator's bearer token.
const Token = "lab-token"

// Quiet is a logger that writes nothing.
var Quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// Lab is an orchestrator in a test.
type Lab struct {
	T      testing.TB
	Ctx    context.Context
	URL    string
	Client *protocol.Client
}

// New starts an orchestrator for the test, stopped when it ends. Each tune adjusts its
// configuration first.
func New(t testing.TB, tune ...func(*orchestrator.Config)) *Lab {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := orchestrator.Config{
		DB: filepath.Join(t.TempDir(), "orchestrator.db"), Token: Token, FanoutInterval: 20 * time.Millisecond,
		RetryBase: 50 * time.Millisecond, RetryMax: 200 * time.Millisecond,
	}
	for _, f := range tune {
		f(&cfg)
	}
	o, err := orchestrator.Open(ctx, cfg, orchestrator.WithLogger(Quiet))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(o.Handler())
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		srv.Close()
		o.Close()
	})
	return &Lab{T: t, Ctx: ctx, URL: srv.URL, Client: protocol.NewClient(srv.URL, Token)}
}

// Start runs a dispatcher instance in the lab, joined to groups, and waits until it has joined.
func (l *Lab) Start(name, instance string, groups []string, hooks dispatcher.Hooks) {
	l.T.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	l.T.Cleanup(srv.Close)
	d, err := dispatcher.New(dispatcher.Config{
		Name: name, Instance: instance, Team: "team-" + name, CallbackURL: srv.URL + "/events",
		Orchestrator: l.URL, Token: Token, Groups: groups, Reassert: 100 * time.Millisecond, Logger: Quiet,
	}, hooks)
	if err != nil {
		l.T.Fatal(err)
	}
	mux.Handle("/", d.Handler())
	ctx, cancel := context.WithCancel(l.Ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.Run(ctx)
	}()
	l.T.Cleanup(func() {
		cancel()
		<-done
	})
	l.Eventually("dispatcher "+name+" to join "+groups0(groups), func() bool {
		gs, err := l.Client.Groups(l.Ctx)
		if err != nil {
			return false
		}
		for _, g := range groups {
			found := false
			for _, have := range gs {
				if have.Name == g && slices.Contains(have.Dispatchers, name) {
					found = true
				}
			}
			if !found {
				return false
			}
		}
		ds, err := l.Client.Dispatchers(l.Ctx)
		if err != nil {
			return false
		}
		return slices.ContainsFunc(ds, func(d protocol.Dispatcher) bool { return d.Name == name && d.Instance == instance })
	})
}

func groups0(gs []string) string {
	if len(gs) == 0 {
		return "no group"
	}
	return gs[0]
}

// Eventually polls ok until it holds, failing the test after ten seconds.
func (l *Lab) Eventually(what string, ok func() bool) {
	l.T.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			l.T.Fatalf("waited for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Await polls a run until ok, failing the test, with the run's journal, after ten seconds.
func (l *Lab) Await(runID, what string, ok func(*protocol.Run) bool) *protocol.Run {
	l.T.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		run, err := l.Client.Run(l.Ctx, runID)
		if err != nil {
			l.T.Fatal(err)
		}
		if ok(run) {
			return run
		}
		if time.Now().After(deadline) {
			j, _ := l.Client.Journal(l.Ctx, runID)
			l.T.Fatalf("waited for %s; the run is %+v\n%s", what, run, j)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
