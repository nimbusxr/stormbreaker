package protocol_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/nimbusxr/stormbreaker/internal/orchestrator"
	"github.com/nimbusxr/stormbreaker/protocol"
)

// TestClient drives a real orchestrator through the client: a run from open to closed.
func TestClient(t *testing.T) {
	ctx := context.Background()
	o, err := orchestrator.Open(ctx, orchestrator.Config{DB: filepath.Join(t.TempDir(), "o.db"), Token: "tok"},
		orchestrator.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	srv := httptest.NewServer(o.Handler())
	defer srv.Close()

	if _, err := protocol.NewClient(srv.URL, "wrong").Groups(ctx); protocol.StatusOf(err) != http.StatusUnauthorized {
		t.Fatalf("a wrong token: %v", err)
	}
	c := protocol.NewClient(srv.URL, "tok")
	secret, _ := protocol.NewSecret()
	if _, err := c.Register(ctx, protocol.Registration{Name: "tracking", Instance: "a", Team: "t", CallbackURL: "http://tracking.invalid", Secret: secret}); err != nil {
		t.Fatal(err)
	}
	if err := c.JoinGroup(ctx, "delivery", "tracking"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenRun(ctx, protocol.OpenRun{Initiator: "test", Group: "nowhere"}); protocol.StatusOf(err) != http.StatusUnprocessableEntity {
		t.Fatalf("a run in no group: %v", err)
	}
	run, err := c.OpenRun(ctx, protocol.OpenRun{Initiator: "test", Group: "delivery", Requires: []string{"tracking"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddStream(ctx, run.ID, protocol.StreamRef{Name: "parcels", Seed: run.ID}); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 300)
	for i := range ids {
		ids[i] = "id-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	for i := 0; i < len(ids); i += 3 {
		if _, err := c.ReportFootprint(ctx, run.ID, protocol.Suite, "", protocol.FootprintReport{Entries: []protocol.FootprintEntry{
			{Store: "svc", Locator: "/parcels", Identities: ids[i : i+3], Resolution: protocol.ResolutionExpected},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for cl, err := range c.Footprint(ctx, run.ID, protocol.FootprintFilter{}) {
		if err != nil {
			t.Fatal(err)
		}
		if cl.Identity != ids[n] {
			t.Fatalf("claim %d is %q, want %q", n, cl.Identity, ids[n])
		}
		n++
	}
	if n != len(ids) {
		t.Fatalf("walked %d claims over pages, want %d", n, len(ids))
	}
	if run, err = c.Run(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.ReportStatus(ctx, run.ID, "tracking", "a", protocol.Status{State: protocol.StatusPrepared, FootprintVersion: run.FootprintVersion}); err != nil {
		t.Fatal(err)
	}
	if run, err = c.Run(ctx, run.ID); err != nil || !run.Gate.Open {
		t.Fatalf("gate %+v, %v", run.Gate, err)
	}
	if err := c.Heartbeat(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{protocol.EventTrafficComplete, protocol.EventSettled, protocol.EventCleanupReady} {
		if _, err := c.Publish(ctx, run.ID, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.ReportStatus(ctx, run.ID, "tracking", "a", protocol.Status{State: protocol.StatusCompleted, Evidence: "nothing to remove", FootprintVersion: run.FootprintVersion}); err != nil {
		t.Fatal(err)
	}
	if run, err = c.Run(ctx, run.ID); err != nil || !run.Done() {
		t.Fatalf("after cleanup the run is %s, %v", run.State, err)
	}
	j, err := c.Journal(ctx, run.ID)
	if err != nil || len(j) == 0 {
		t.Fatalf("journal %q, %v", j, err)
	}
	feed, err := c.Events(ctx, 0, 1000)
	if err != nil || len(feed.Events) == 0 {
		t.Fatalf("events %+v, %v", feed, err)
	}
	if err := c.Heartbeat(ctx, run.ID); protocol.StatusOf(err) != http.StatusGone {
		t.Fatalf("a heartbeat on a closed run: %v", err)
	}
}
