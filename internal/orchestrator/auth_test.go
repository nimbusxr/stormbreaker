package orchestrator

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nimbusxr/stormbreaker/protocol"
)

func TestPrincipalsFile(t *testing.T) {
	write := func(body string) string {
		p := filepath.Join(t.TempDir(), "principals.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ps, err := LoadPrincipals(write("principals:\n  - name: platform\n    token-sha256: " + TokenHash("a") + "\n    admin: true\n" +
		"  - name: delivery-ci\n    token-sha256: " + TokenHash("b") + "\n    suites: [delivery]\n"))
	if err != nil || len(ps) != 2 || !ps[0].Admin || ps[1].Suites[0] != "delivery" {
		t.Fatalf("%+v, %v", ps, err)
	}
	if ps, err := LoadPrincipals(write("principals:\n  - name: ui\n    token-sha256: " + TokenHash("c") + "\n    read-only: true\n")); err != nil || !ps[0].ReadOnly {
		t.Errorf("a read-only principal: %+v, %v", ps, err)
	}
	for body, want := range map[string]string{
		"principals:\n  - name: x\n    token-sha256: abc\n    admin: true\n":                                                                                "is not a SHA-256",
		"principals:\n  - name: x\n    token-sha256: " + TokenHash("a") + "\n":                                                                              "may do nothing",
		"principals:\n  - token-sha256: " + TokenHash("a") + "\n    admin: true\n":                                                                          "has no name",
		"principals:\n  - name: x\n    token-sha256: " + TokenHash("a") + "\n    admins: true\n":                                                            "unknown",
		"principals:\n  - {name: x, token-sha256: " + TokenHash("a") + ", admin: true}\n  - {name: y, token-sha256: " + TokenHash("a") + ", admin: true}\n": "share a token",
		"principals:\n  - {name: ui, token-sha256: " + TokenHash("a") + ", read-only: true, suites: [delivery]}\n":                                          "one or the other",
	} {
		if _, err := LoadPrincipals(write(body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", body, err, want)
		}
	}
}

func TestEachPrincipalDoesOnlyWhatItsGrantsAllow(t *testing.T) {
	e := newEnv(t, func(c *Config) {
		c.Principals = []Principal{
			{Name: "delivery-ci", TokenSHA256: TokenHash("ci-token"), Suites: []string{"delivery"}},
			{Name: "parcels-team", TokenSHA256: TokenHash("parcels-token"), Dispatchers: []string{"parcels"}},
			{Name: "ui", TokenSHA256: TokenHash("ui-token"), ReadOnly: true},
		}
	})
	status := func(token, method, path string, body any) int {
		t.Helper()
		return e.callAs(token, method, path, body).Code
	}
	reg := func(name, team string) protocol.Registration {
		return protocol.Registration{Name: name, Instance: "east", Team: team, CallbackURL: "http://" + name + ".invalid/events", Secret: e.secret()}
	}

	if got := status("", http.MethodGet, "/runs/x", nil); got != http.StatusUnauthorized {
		t.Errorf("no token: %d", got)
	}
	if got := status("guess", http.MethodGet, "/groups", nil); got != http.StatusUnauthorized {
		t.Errorf("an unknown token: %d", got)
	}
	// A read-only principal, such as the UI's, reads and does nothing else.
	if got := status("ui-token", http.MethodGet, "/groups", nil); got != http.StatusOK {
		t.Errorf("the UI reading: %d", got)
	}
	if got := status("ui-token", http.MethodPost, "/runs", protocol.OpenRun{Initiator: "ui", Group: "delivery"}); got != http.StatusForbidden {
		t.Errorf("the UI opening a run: %d", got)
	}
	if got := status("ui-token", http.MethodPost, "/groups", protocol.CreateGroup{Name: "ui"}); got != http.StatusForbidden {
		t.Errorf("the UI making a group: %d", got)
	}
	// A team's dispatchers register, join and report as their own dispatcher, and nothing else.
	if got := status("parcels-token", http.MethodPost, "/dispatchers", reg("parcels", "parcels")); got != http.StatusCreated {
		t.Errorf("parcels registering itself: %d", got)
	}
	if got := status("parcels-token", http.MethodPost, "/dispatchers", reg("tracking", "parcels")); got != http.StatusForbidden {
		t.Errorf("parcels registering as tracking: %d", got)
	}
	if got := status("parcels-token", http.MethodPut, "/groups/delivery/dispatchers/parcels", nil); got != http.StatusNoContent {
		t.Errorf("parcels joining delivery: %d", got)
	}
	if got := status("parcels-token", http.MethodPost, "/runs", protocol.OpenRun{Initiator: "x", Group: "delivery"}); got != http.StatusForbidden {
		t.Errorf("a dispatcher opening a run: %d", got)
	}
	// A team's CI opens runs in its groups, and acts as their suite.
	rec := e.callAs("ci-token", http.MethodPost, "/runs", protocol.OpenRun{Initiator: "ci", Group: "delivery"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("CI opening a run in delivery: %d %s", rec.Code, rec.Body.String())
	}
	run := decode[protocol.Run](t, rec)
	if got := status("ci-token", http.MethodPost, "/runs/"+run.ID+"/heartbeat", nil); got != http.StatusNoContent {
		t.Errorf("CI renewing its run: %d", got)
	}
	progress := protocol.Progress{Scenario: "a run", Steps: []protocol.StepProgress{{Keyword: "Given", Text: "a step", Status: "running"}}}
	if got := status("ci-token", http.MethodPut, "/runs/"+run.ID+"/progress", progress); got != http.StatusNoContent {
		t.Errorf("CI saying how its run goes: %d", got)
	}
	if got := status("parcels-token", http.MethodPut, "/runs/"+run.ID+"/progress", progress); got != http.StatusForbidden {
		t.Errorf("a dispatcher saying how a run goes: %d", got)
	}
	if got := status("ui-token", http.MethodPut, "/runs/"+run.ID+"/progress", progress); got != http.StatusForbidden {
		t.Errorf("the UI saying how a run goes: %d", got)
	}
	if got := status("ui-token", http.MethodGet, "/runs/"+run.ID+"/progress", nil); got != http.StatusOK {
		t.Errorf("the UI reading how a run goes: %d", got)
	}
	if got := status("ci-token", http.MethodPost, "/runs/"+run.ID+"/dispatchers/parcels/status?instance=east", protocol.Status{State: protocol.StatusPrepared}); got != http.StatusForbidden {
		t.Errorf("CI reporting for parcels: %d", got)
	}
	if got := status("parcels-token", http.MethodPost, "/runs/"+run.ID+"/events", protocol.PublishEvent{Type: protocol.EventAbandoned}); got != http.StatusForbidden {
		t.Errorf("a dispatcher abandoning a run: %d", got)
	}
	if got := status("ci-token", http.MethodPost, "/groups", protocol.CreateGroup{Name: "returns"}); got != http.StatusForbidden {
		t.Errorf("CI making another team's group: %d", got)
	}
	// Reads are everyone's.
	if got := status("parcels-token", http.MethodGet, "/runs/"+run.ID+"/journal", nil); got != http.StatusOK {
		t.Errorf("a dispatcher reading a journal: %d", got)
	}
	// The shared token still has every grant.
	e.expect(http.StatusCreated, http.MethodPost, "/groups", protocol.CreateGroup{Name: "returns"})
}

func TestADispatchersNameBelongsToOneTeam(t *testing.T) {
	e := newEnv(t)
	e.expect(http.StatusCreated, http.MethodPost, "/dispatchers", protocol.Registration{Name: "parcels", Instance: "east", Team: "parcels", CallbackURL: "http://a.invalid", Secret: e.secret()})
	p := decode[protocol.Problem](t, e.expect(http.StatusConflict, http.MethodPost, "/dispatchers",
		protocol.Registration{Name: "parcels", Instance: "west", Team: "billing", CallbackURL: "http://b.invalid", Secret: e.secret()}))
	if !strings.Contains(p.Detail, "belongs to team parcels") {
		t.Errorf("a takeover: %+v", p)
	}
}
