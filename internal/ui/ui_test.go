package ui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The UI only reads, and only what it shows; the token goes to the orchestrator, never to the
// browser.
func TestTheUIOnlyReadsWhatItShows(t *testing.T) {
	var got []string
	orch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"runs":[]}`)
	}))
	defer orch.Close()
	envs, err := ParseEnvironments([]string{"staging=" + orch.URL}, "read-token")
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(envs, nil)
	call := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	if rec := call(http.MethodGet, "/api/env/staging/runs?limit=50"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "runs") {
		t.Errorf("a read: %d %s", rec.Code, rec.Body)
	}
	if len(got) != 1 || got[0] != "GET /runs?limit=50 Bearer read-token" {
		t.Errorf("the orchestrator saw %q", got)
	}
	for _, path := range []string{"/api/env/staging/runs/run-1/footprint/claims", "/api/env/staging/metrics", "/api/env/staging/openapi.yaml"} {
		if rec := call(http.MethodGet, path); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s: %d, want 403", path, rec.Code)
		}
	}
	if rec := call(http.MethodPost, "/api/env/staging/runs"); rec.Code == http.StatusOK {
		t.Errorf("a POST went through: %d", rec.Code)
	}
	if rec := call(http.MethodGet, "/api/env/nowhere/runs"); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown environment: %d", rec.Code)
	}
	if len(got) != 1 {
		t.Errorf("refused requests reached the orchestrator: %q", got)
	}
	if rec := call(http.MethodGet, "/api/environments"); !strings.Contains(rec.Body.String(), `"staging"`) || strings.Contains(rec.Body.String(), "read-token") {
		t.Errorf("environments: %s", rec.Body)
	}
	// Any page is the app, which routes itself.
	if rec := call(http.MethodGet, "/env/staging/runs/run-1"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="app"`) {
		t.Errorf("a page: %d", rec.Code)
	}
	// With the docs' icon, which the page's security headers let it load.
	if rec := call(http.MethodGet, "/env/staging"); !strings.Contains(rec.Body.String(), `href="/assets/favicon.svg"`) {
		t.Errorf("a page without its icon: %s", rec.Body)
	}
	if rec := call(http.MethodGet, "/assets/favicon.svg"); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("the icon: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := call(http.MethodGet, "/assets/app.js"); rec.Code != http.StatusOK {
		t.Errorf("the app's script: %d", rec.Code)
	}
}

func TestEnvironmentsAreNameEqualsURL(t *testing.T) {
	for _, bad := range [][]string{{}, {"staging"}, {"staging=not a url"}, {"a=http://x,a=http://y"}} {
		if _, err := ParseEnvironments(bad, ""); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
	envs, err := ParseEnvironments([]string{"staging=http://o:8080/, perf=http://p:8080"}, "t")
	if err != nil || len(envs) != 2 || envs[0].URL != "http://o:8080" || envs[1].Name != "perf" {
		t.Errorf("%+v %v", envs, err)
	}
}
