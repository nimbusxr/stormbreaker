package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLinkTo(t *testing.T) {
	for addr, want := range map[string]string{
		"[::]:18096":     "http://localhost:18096",
		"0.0.0.0:8080":   "http://localhost:8080",
		"127.0.0.1:8095": "http://127.0.0.1:8095",
		"[::1]:8095":     "http://[::1]:8095",
		"10.0.0.7:80":    "http://10.0.0.7:80",
	} {
		tcp, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := linkTo(tcp); got != want {
			t.Errorf("%s: %s, want %s", addr, got, want)
		}
	}
}

// lockedBuffer is written by the service while the test reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// serving runs a serve command until the test ends, and returns what it printed once it matches
// want.
func serving(t *testing.T, want *regexp.Regexp, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var out lockedBuffer
	root := newRoot(&App{Out: &out, Err: io.Discard})
	root.SetArgs(args)
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("stormbreaker %v ended with: %v", args, err)
			}
		case <-time.After(15 * time.Second):
			t.Errorf("stormbreaker %v did not stop", args)
		}
	})
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if s := out.String(); want.MatchString(s) {
			return s
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("stormbreaker %v ended early: %v, having printed %q", args, err, out.String())
		default:
		}
	}
	t.Fatalf("stormbreaker %v printed %q", args, out.String())
	return ""
}

func get(t *testing.T, link string) string {
	t.Helper()
	resp, err := http.Get(link)
	if err != nil {
		t.Fatalf("the link printed does not answer: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s answered %d: %s", link, resp.StatusCode, body)
	}
	return string(body)
}

func TestTheUISaysWhereItIs(t *testing.T) {
	line := regexp.MustCompile(`(?m)^stormbreaker UI: (http://127\.0\.0\.1:\d+)$`)
	out := serving(t, line, "ui", "serve", "--environment", "lab=http://127.0.0.1:1", "--addr", "127.0.0.1:0")
	if body := get(t, line.FindStringSubmatch(out)[1]); !strings.Contains(body, `id="app"`) {
		t.Errorf("the link is not the UI: %s", body)
	}
}

func TestTheUISaysWhereEachEnvironmentIs(t *testing.T) {
	line := regexp.MustCompile(`(?m)^  prod: (http://127\.0\.0\.1:\d+/env/prod)$`)
	out := serving(t, line, "ui", "serve", "--environment", "staging=http://127.0.0.1:1", "--environment", "prod=http://127.0.0.1:2", "--addr", "127.0.0.1:0")
	if !strings.Contains(out, "stormbreaker UI:\n  staging: http://127.0.0.1:") {
		t.Errorf("printed %q", out)
	}
	get(t, line.FindStringSubmatch(out)[1])
}

func TestTheUISaysWhereItIsAsJSON(t *testing.T) {
	out := serving(t, regexp.MustCompile(`(?s)\}\s*$`), "--json", "ui", "serve", "--environment", "lab=http://127.0.0.1:1", "--addr", "127.0.0.1:0")
	var said struct {
		URL          string `json:"url"`
		Environments []struct {
			Environment, URL string
		} `json:"environments"`
	}
	if err := json.Unmarshal([]byte(out), &said); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	if len(said.Environments) != 1 || said.Environments[0].Environment != "lab" {
		t.Errorf("said %+v", said)
	}
	get(t, said.URL)
}

func TestTheOrchestratorSaysWhereItIs(t *testing.T) {
	line := regexp.MustCompile(`(?m)^stormbreaker orchestrator: (http://127\.0\.0\.1:\d+)\n  its API: (\S+)$`)
	out := serving(t, line, "orchestrator", "serve", "--addr", "127.0.0.1:0", "--db", filepath.Join(t.TempDir(), "o.db"))
	m := line.FindStringSubmatch(out)
	if body := get(t, m[2]); !strings.Contains(body, "openapi") {
		t.Errorf("the API's link is not its document: %.80s", body)
	}
	get(t, m[1]+"/healthz")
}
