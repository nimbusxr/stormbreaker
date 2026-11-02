package orchestrator

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the database/sql driver "sqlite", pure Go
)

//go:embed migrations/*.sql
var migrations embed.FS

// querier is what both *sql.DB and *sql.Tx offer.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// openDB opens the SQLite database at path, creating it, and brings its schema up to date. It
// returns three handles on it. The writer is one connection: SQLite allows one writer at a time,
// and every write is a short transaction that commits before anyone hears of it. The readers are
// a pool beside it: in WAL mode a reader sees the last commit and never waits for the writer, so
// a slow walk of the footprint or a burst of polls holds up no report and no lease. The
// checkpointer is one connection of its own, which moves the write-ahead log into the database
// beside the writer rather than on its time.
func openDB(ctx context.Context, path string) (writer, readers, checkpointer *sql.DB, err error) {
	dsn := path
	if path != ":memory:" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, nil, nil, err
		}
		dsn = "file:" + (&url.URL{Path: abs}).EscapedPath()
	}
	q := url.Values{}
	// Incremental: space a pruned run freed goes back to the volume when Prune asks for it. It
	// takes only on a new database, before its first table; on an older one it changes nothing.
	q.Add("_pragma", "auto_vacuum(INCREMENTAL)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(10000)")
	// FULL: a commit is on the disk before it returns, so nothing the orchestrator has answered
	// for, or told anyone of, is lost to a crash of the node. NORMAL would lose the last commits
	// to a power cut, after their notifications had gone out.
	q.Add("_pragma", "synchronous(FULL)")
	q.Set("_txlock", "immediate")
	if path != ":memory:" {
		// Checkpoints are housekeeping's (checkpoint), off the writer's commits.
		q.Add("_pragma", "wal_autocheckpoint(0)")
		// A log that a complete checkpoint lets the writer start again from is cut back to
		// this, so a burst that grew it does not keep it large.
		q.Add("_pragma", fmt.Sprintf("journal_size_limit(%d)", walLimit))
	}
	writer, err = sql.Open("sqlite", dsn+"?"+q.Encode())
	if err != nil {
		return nil, nil, nil, err
	}
	writer.SetMaxOpenConns(1)
	if err := migrate(ctx, writer); err != nil {
		writer.Close()
		return nil, nil, nil, err
	}
	if path == ":memory:" {
		return writer, writer, writer, nil // an in-memory database is one connection's own
	}
	r := url.Values{}
	r.Add("_pragma", "busy_timeout(10000)")
	r.Add("_pragma", "query_only(1)")
	readers, err = sql.Open("sqlite", dsn+"?"+r.Encode())
	if err != nil {
		writer.Close()
		return nil, nil, nil, err
	}
	readers.SetMaxOpenConns(max(4, min(16, runtime.NumCPU())))
	readers.SetMaxIdleConns(max(4, min(16, runtime.NumCPU())))
	c := url.Values{}
	// A checkpoint that must wait for the readers (checkpoint, past walLimit) holds new writes
	// back while it waits: it waits this long at most, and tries again on its next tick.
	c.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", checkpointPatience.Milliseconds()))
	c.Add("_pragma", "synchronous(FULL)")
	checkpointer, err = sql.Open("sqlite", dsn+"?"+c.Encode())
	if err != nil {
		writer.Close()
		readers.Close()
		return nil, nil, nil, err
	}
	checkpointer.SetMaxOpenConns(1)
	return writer, readers, checkpointer, nil
}

// checkpointPatience is how long a checkpoint that waits for the readers holds writes back.
const checkpointPatience = 250 * time.Millisecond

// migrate applies the migrations not applied yet, in order, each in a transaction.
func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `create table if not exists schema_migrations (version text primary key)`); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		version := strings.TrimSuffix(filepath.Base(name), ".sql")
		var n int
		if err := db.QueryRowContext(ctx, `select count(*) from schema_migrations where version = ?`, version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, `insert into schema_migrations (version) values (?)`, version); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func millis(t time.Time) int64 { return t.UnixMilli() }

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func nullMillis(ms sql.NullInt64) *time.Time {
	if !ms.Valid {
		return nil
	}
	t := fromMillis(ms.Int64)
	return &t
}

// digest is a fingerprint of exactly what a report said: FNV-1a 64 over its packed identities.
// Order matters, which is right: two reports are the same report only when they say the same
// thing in the same order.
func digest(packed []byte) int64 {
	h := uint64(0xcbf29ce484222325)
	for _, b := range packed {
		h ^= uint64(b)
		h *= 0x100000001b3
	}
	return int64(h)
}

// pack joins identities one a line, UTF-8: what a report's blob holds.
func pack(identities []string) []byte { return []byte(strings.Join(identities, "\n")) }

// unpack calls each for every identity of a packed report, without building a list of them.
func unpack(packed []byte, each func(string)) {
	if len(packed) == 0 {
		return
	}
	s := string(packed)
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			each(s)
			return
		}
		each(s[:i])
		s = s[i+1:]
	}
}
