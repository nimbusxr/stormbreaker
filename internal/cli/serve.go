package cli

import (
	"fmt"
	"io"
	"net"
	"net/url"

	"github.com/nimbusxr/stormbreaker/internal/ui"
)

// linkTo is where a person on this machine reaches a listener: its real port, and localhost when
// it listens on every address.
func linkTo(addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return "http://" + addr.String()
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// announceUI says where the UI is, once it listens: the one link, or one for each environment.
func announceUI(app *App, addr net.Addr, envs []ui.Environment) {
	link := linkTo(addr)
	pages := make([]map[string]string, len(envs))
	for i, e := range envs {
		pages[i] = map[string]string{"environment": e.Name, "url": link + "/env/" + url.PathEscape(e.Name)}
	}
	// Saying so is a courtesy: a closed stdout does not stop the service.
	_ = app.Emit(map[string]any{"service": "ui", "url": link, "environments": pages}, func(w io.Writer) error {
		if len(envs) == 1 {
			_, err := fmt.Fprintf(w, "stormbreaker UI: %s\n", link)
			return err
		}
		if _, err := fmt.Fprintln(w, "stormbreaker UI:"); err != nil {
			return err
		}
		for _, p := range pages {
			if _, err := fmt.Fprintf(w, "  %s: %s\n", p["environment"], p["url"]); err != nil {
				return err
			}
		}
		return nil
	})
}

// announceOrchestrator says where the orchestrator is, once it listens: the URL to give suites
// and dispatchers, and the page a person can open, since the API has none at its root.
func announceOrchestrator(app *App, addr net.Addr) {
	link := linkTo(addr)
	api := link + "/openapi.yaml"
	_ = app.Emit(map[string]any{"service": "orchestrator", "url": link, "api": api}, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "stormbreaker orchestrator: %s\n  its API: %s\n", link, api)
		return err
	})
}
