package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// TestTheStoreIsDurable pins how the database is opened: every commit on the disk before it
// returns (synchronous FULL, in WAL mode), space freed by pruning returnable (incremental auto
// vacuum), one writer, and readers that cannot write.
func TestTheStoreIsDurable(t *testing.T) {
	ctx := context.Background()
	writer, readers, checkpointer, err := openDB(ctx, filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	defer readers.Close()
	defer checkpointer.Close()
	for pragma, want := range map[string]string{"journal_mode": "wal", "synchronous": "2", "auto_vacuum": "2"} {
		var got string
		if err := writer.QueryRowContext(ctx, "pragma "+pragma).Scan(&got); err != nil || got != want {
			t.Errorf("pragma %s is %q (%v), want %q", pragma, got, err, want)
		}
	}
	if n := writer.Stats().MaxOpenConnections; n != 1 {
		t.Errorf("the writer has %d connections, want 1", n)
	}
	// The checkpointer is a connection of its own, that waits for the readers only briefly.
	if n := checkpointer.Stats().MaxOpenConnections; n != 1 || checkpointer == writer {
		t.Errorf("the checkpointer has %d connections (the writer's: %v), want 1 of its own", n, checkpointer == writer)
	}
	for pragma, want := range map[string]string{"busy_timeout": fmt.Sprint(checkpointPatience.Milliseconds()), "synchronous": "2"} {
		var got string
		if err := checkpointer.QueryRowContext(ctx, "pragma "+pragma).Scan(&got); err != nil || got != want {
			t.Errorf("the checkpointer's pragma %s is %q (%v), want %q", pragma, got, err, want)
		}
	}
	if _, err := readers.ExecContext(ctx, `insert into run_groups (name, created_at) values ('x', 0)`); err == nil {
		t.Error("a reader wrote")
	}
	if _, err := writer.ExecContext(ctx, `insert into run_groups (name, created_at) values ('x', 0)`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := readers.QueryRowContext(ctx, `select count(*) from run_groups`).Scan(&n); err != nil || n != 1 {
		t.Errorf("a reader sees %d groups after the writer's commit (%v), want 1", n, err)
	}
}

// TestTheLogStaysBoundedWhileReadersRead: with readers always reading, a checkpoint that never
// waits for them never reaches the log's end. Past its limit, the checkpoint waits for them, and
// the log starts again.
func TestTheLogStaysBoundedWhileReadersRead(t *testing.T) {
	old := walLimit
	walLimit = 256 << 10
	defer func() { walLimit = old }()
	e := newEnv(t)
	e.attach("delivery", "tracking", "shared", &receiver{t: t})
	run := e.open("delivery", "tracking")
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var wg sync.WaitGroup
	for range 4 { // readers, overlapping, never letting up
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				_, _ = e.o.FootprintPage(ctx, run.ID, 0, 0, 64, protocol.FootprintFilter{})
			}
		}()
	}
	largest := int64(0)
	for i := range 60 { // about five times the log's limit
		ids := make([]string, 500)
		for j := range ids {
			ids[j] = fmt.Sprintf("parcel-%05d-%04d-with-the-length-of-a-uuid", i, j)
		}
		e.footprint(run.ID, "suite/", expected("parcels", "/parcels", ids...))
		if i%10 == 0 {
			if err := e.o.checkpoint(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if fi, err := os.Stat(e.o.cfg.DB + "-wal"); err == nil {
			largest = max(largest, fi.Size())
		}
	}
	stop()
	wg.Wait()
	if largest > 8*walLimit {
		t.Errorf("the log reached %d bytes, past %d", largest, 8*walLimit)
	}
}
