//go:build scale

package orchestrator_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// TestADiskThatFillsLosesNothing fills the orchestrator's volume with footprint reports, and
// checks what it answers, that it keeps serving reads, that a SIGKILL while full loses nothing it
// acknowledged, and that it takes writes again once there is room. It needs a small volume of
// its own (macOS: hdiutil create -size 50m, then attach it):
//
//	STORMBREAKER_DISKFULL_DIR=/Volumes/small go test -tags scale -run DiskThatFills -v ./internal/orchestrator
//
// With STORMBREAKER_DISKFULL_RESERVE (like 12MiB) the orchestrator's reserve refuses growth first.
func TestADiskThatFillsLosesNothing(t *testing.T) {
	dir := os.Getenv("STORMBREAKER_DISKFULL_DIR")
	if dir == "" {
		t.Skip("set STORMBREAKER_DISKFULL_DIR to a small volume of its own")
	}
	bin := filepath.Join(t.TempDir(), "stormbreaker")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/stormbreaker")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	db := filepath.Join(dir, "o.db")
	ballast := filepath.Join(dir, "ballast")
	for _, f := range []string{db, db + "-wal", db + "-shm", ballast} {
		_ = os.Remove(f)
	}
	// Room to give back once the volume is full.
	if err := os.WriteFile(ballast, make([]byte, 8<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer receiver.Close()
	secret, _ := protocol.NewSecret()

	start := func() (*exec.Cmd, *protocol.Client) {
		addr := freeAddr(t)
		reserve := os.Getenv("STORMBREAKER_DISKFULL_RESERVE") // 0, the default here: no guard, the disk itself fills
		if reserve == "" {
			reserve = "0"
		}
		cmd := exec.Command(bin, "orchestrator", "serve", "--db", db, "--addr", addr, "--disk-reserve", reserve)
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		c := protocol.NewClient("http://"+addr, "")
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
		waitUp(t, c)
		return cmd, c
	}
	cmd, c := start()
	ctx := context.Background()
	if _, err := c.Register(ctx, protocol.Registration{Name: "tracking", Instance: "shared", Team: "t", CallbackURL: receiver.URL, Secret: secret}); err != nil {
		t.Fatal(err)
	}
	if err := c.JoinGroup(ctx, "delivery", "tracking"); err != nil {
		t.Fatal(err)
	}
	run, err := c.OpenRun(ctx, protocol.OpenRun{Initiator: "full", Group: "delivery", Requires: []string{"tracking"}})
	if err != nil {
		t.Fatal(err)
	}
	report := func(n int) error {
		ids := make([]string, 5000)
		for i := range ids {
			ids[i] = fmt.Sprintf("%s-%06d-%04d-padding-to-look-like-a-uuid", run.ID, n, i)
		}
		_, err := c.ReportFootprint(ctx, run.ID, protocol.Suite, "", protocol.FootprintReport{Entries: []protocol.FootprintEntry{
			{Store: "parcels", Locator: "/parcels", Identities: ids, Resolution: protocol.ResolutionExpected},
		}})
		return err
	}
	var acked int64
	var full error
	for n := 0; n < 100_000; n++ {
		if err := report(n); err != nil {
			full = err
			break
		}
		acked += 5000
	}
	if full == nil {
		t.Fatal("the volume never filled")
	}
	t.Logf("full after %d acknowledged identities: %v", acked, full)
	if _, err := c.Run(ctx, run.ID); err != nil {
		t.Errorf("a read while full: %v", err)
	}
	if err := c.Heartbeat(ctx, run.ID); err != nil {
		t.Logf("a heartbeat while full: %v", err)
	}

	// Killed while full, and started again on the same, still full, volume.
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_ = cmd.Wait()
	cmd, c = start()
	defer func() { _ = cmd.Process.Signal(syscall.SIGKILL); _ = cmd.Wait() }()
	r, err := c.Run(ctx, run.ID)
	if err != nil {
		t.Fatalf("after a SIGKILL on a full volume: %v", err)
	}
	if r.Claims < acked {
		t.Errorf("%d claims after the restart; %d were acknowledged", r.Claims, acked)
	}
	check := func(when string) {
		conn, err := sql.Open("sqlite", "file:"+db+"?_pragma=query_only(1)")
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var ok string
		if err := conn.QueryRow(`pragma integrity_check`).Scan(&ok); err != nil || ok != "ok" {
			t.Errorf("%s: integrity_check %q, %v", when, ok, err)
		}
	}
	check("full, after a SIGKILL")

	// Room again: writes go on, on the same process.
	if err := os.Remove(ballast); err != nil {
		t.Fatal(err)
	}
	var again error
	for range 3 {
		if again = report(1_000_000); again == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if again != nil && !errors.Is(again, context.Canceled) {
		t.Errorf("with room again, a report: %v", again)
	}
	if r, err = c.Run(ctx, run.ID); err != nil || r.Claims < acked+5000 {
		t.Errorf("with room again: %d claims, %v", r.Claims, err)
	}
	check("with room again")
}
