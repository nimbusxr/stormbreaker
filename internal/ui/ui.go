// Package ui is the stormbreaker UI's server (ADR 0026): it serves the web app built from ui/ into
// dist, and proxies the reads the app shows to the orchestrators of one or more environments. The
// orchestrators' tokens stay here; the browser never sees them, and only reads go through.
package ui

//go:generate npm --prefix ../../ui run build

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

//go:embed dist
var dist embed.FS

// Environment is an orchestrator the UI shows: its environment's name, its URL, and a token that
// may read it.
type Environment struct {
	Name, URL, Token string
}

// ParseEnvironments reads environments written name=url, comma-separated or repeated, each with
// the shared token.
func ParseEnvironments(specs []string, token string) ([]Environment, error) {
	var out []Environment
	seen := map[string]bool{}
	for _, spec := range specs {
		for _, part := range strings.Split(spec, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, raw, ok := strings.Cut(part, "=")
			u, err := url.Parse(strings.TrimSpace(raw))
			if !ok || strings.TrimSpace(name) == "" || err != nil || u.Scheme == "" || u.Host == "" {
				return nil, fmt.Errorf("an environment is name=url, like staging=http://orchestrator:8080, not %q", part)
			}
			name = strings.TrimSpace(name)
			if seen[name] {
				return nil, fmt.Errorf("environment %s is named twice", name)
			}
			seen[name] = true
			out = append(out, Environment{Name: name, URL: strings.TrimRight(u.String(), "/"), Token: token})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the UI needs an environment to show: --environment name=url")
	}
	return out, nil
}

// allowed are the orchestrator routes the UI shows; nothing else goes through.
var allowed = regexp.MustCompile(`^(runs|runs/[\w-]+|runs/[\w-]+/journal|runs/[\w-]+/progress|dispatchers|groups|groups/[\w-]+/dispatchers|healthz)$`)

// Handler serves the app, its reads, and each environment as it goes.
func Handler(envs []Environment, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	client := &http.Client{Timeout: 10 * time.Second}
	type environment struct {
		reads *reads
		hub   *hub
	}
	byName := map[string]environment{}
	names := make([]string, 0, len(envs))
	for _, e := range envs {
		r := newReads(e, client)
		byName[e.Name] = environment{r, newHub(e.Name, r, log)}
		names = append(names, e.Name)
	}
	assets, _ := fs.Sub(dist, "dist")
	static := http.FileServer(http.FS(assets))
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/environments", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"environments": names})
	})
	mux.HandleFunc("GET /api/env/{env}/live", func(w http.ResponseWriter, r *http.Request) {
		e, ok := byName[r.PathValue("env")]
		if !ok {
			http.Error(w, "no such environment", http.StatusNotFound)
			return
		}
		serveLive(w, r, e.hub)
	})
	mux.HandleFunc("GET /api/env/{env}/samples/{run}", func(w http.ResponseWriter, r *http.Request) {
		e, ok := byName[r.PathValue("env")]
		if !ok {
			http.Error(w, "no such environment", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(e.hub.samples(r.PathValue("run")))
	})
	mux.HandleFunc("GET /api/env/{env}/{path...}", func(w http.ResponseWriter, r *http.Request) {
		e, ok := byName[r.PathValue("env")]
		path := r.PathValue("path")
		switch {
		case !ok:
			http.Error(w, "no such environment", http.StatusNotFound)
			return
		case !allowed.MatchString(path):
			http.Error(w, "the UI only reads what it shows", http.StatusForbidden)
			return
		}
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		rd := e.reads.read(r.Context(), path)
		if rd.err != nil {
			if r.Context().Err() == nil {
				log.Warn("cannot reach the orchestrator", "environment", e.reads.env.Name, "err", rd.err)
			}
			http.Error(w, fmt.Sprintf("cannot reach %s's orchestrator", e.reads.env.Name), http.StatusBadGateway)
			return
		}
		if rd.ctype != "" {
			w.Header().Set("Content-Type", rd.ctype)
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(rd.status)
		_, _ = w.Write(rd.body)
	})
	mux.Handle("GET /assets/", static)
	// Every other path is a page of the app, which routes itself.
	index, _ := fs.ReadFile(assets, "index.html")
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(index)
	})
	return securityHeaders(mux)
}

// securityHeaders keeps the app to itself: no framing, no scripts but its own.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}
