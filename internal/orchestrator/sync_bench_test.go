package orchestrator

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
)

// BenchmarkCommit is what one small write costs the writer, by synchronous mode: what a status
// report, a renewal or a heartbeat is.
func BenchmarkCommit(b *testing.B) {
	for _, mode := range []string{"NORMAL", "FULL"} {
		b.Run(mode, func(b *testing.B) {
			q := url.Values{}
			q.Add("_pragma", "journal_mode(WAL)")
			q.Add("_pragma", "synchronous("+mode+")")
			q.Set("_txlock", "immediate")
			db, err := sql.Open("sqlite", "file:"+filepath.Join(b.TempDir(), "b.db")+"?"+q.Encode())
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			ctx := context.Background()
			if _, err := db.ExecContext(ctx, `create table t (k text primary key, v integer)`); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := range b.N {
				tx, _ := db.BeginTx(ctx, nil)
				if _, err := tx.ExecContext(ctx, `insert into t (k, v) values (?, ?) on conflict (k) do update set v = excluded.v`, fmt.Sprint(i%500), i); err != nil {
					b.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
