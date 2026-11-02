// Command tracking-dispatcher is the tracking team's dispatcher, built with the dispatcher-sdk.
//
// Prepare: for every parcel the parcels team declared, the tracking service will write a tracking
// record, TRK-<parcel>: it declares those, deriving them from the footprint and never minting
// anything of its own. Cleanup: it deletes those records from Postgres, counts again to prove
// they are gone, and reports them removed and verified.
//
// The tracking service writes asynchronously, from Kafka, so a record can arrive after cleanup
// begins. Until every record it declared has been removed, cleanup stays open, for a grace period
// after the run's traffic ended (TRACKING_CLEANUP_GRACE, 60s). A record that has not arrived by
// then is reported clean-by: the service's own seven-day retention removes it if it ever arrives.
//
// It keeps nothing in memory between passes: what it still has to answer for is in the footprint
// (Unanswered), so a restart mid-cleanup carries on where it was.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nimbusxr/stormbreaker/dispatcher/sdk"
)

const (
	store   = "tracking-db"
	locator = "public.tracking"
	// retention is how long the tracking service keeps a record, whoever made it.
	retention = 7 * 24 * time.Hour
)

func main() {
	grace := 60 * time.Second
	if g, err := time.ParseDuration(os.Getenv("TRACKING_CLEANUP_GRACE")); err == nil {
		grace = g
	}
	db, err := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "tracking-dispatcher:", err)
		os.Exit(2)
	}
	defer db.Close()
	sdk.Main(sdk.Hooks{
		Prepare: func(ctx context.Context, run *sdk.Run, declare func(sdk.Entry) error) error {
			// Every region of the parcels team declares the parcels it prepared: one record each.
			seen := map[string]bool{}
			for c, err := range run.Footprint(ctx, sdk.From("parcels"), sdk.At("parcels-mem", "")) {
				if err != nil {
					return err
				}
				if seen[c.Identity] {
					continue
				}
				seen[c.Identity] = true
				if err := declare(sdk.Entry{Store: store, Locator: locator, Identities: []string{"TRK-" + c.Identity}}); err != nil {
					return err
				}
			}
			return nil
		},
		Cleanup: func(ctx context.Context, run *sdk.Run, remove func(sdk.Removal) error) (bool, error) {
			// What it declared and has not answered for yet, removed or promised: each once.
			var records []string
			for id, err := range run.Unanswered(ctx, store, locator) {
				if err != nil {
					return false, err
				}
				records = append(records, id)
			}
			if len(records) == 0 {
				return true, nil
			}
			rows, err := db.Query(ctx, `delete from tracking where tracking_id = any($1) returning tracking_id`, records)
			if err != nil {
				return false, err
			}
			deleted := map[string]bool{}
			var now []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return false, err
				}
				deleted[id] = true
				now = append(now, id)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return false, err
			}
			var left int
			if err := db.QueryRow(ctx, `select count(*) from tracking where tracking_id = any($1)`, records).Scan(&left); err != nil {
				return false, err
			}
			if left > 0 {
				return false, fmt.Errorf("%d of the run's tracking records are still there after their delete", left)
			}
			if len(now) > 0 {
				if err := remove(sdk.Removal{Store: store, Locator: locator, Identities: now}); err != nil {
					return false, err
				}
			}
			var missing []string
			for _, id := range records {
				if !deleted[id] {
					missing = append(missing, id)
				}
			}
			if len(missing) == 0 {
				return true, nil
			}
			// Records still to arrive: wait for them, until the grace after the traffic ended.
			ended := run.WindowFrom
			if run.WindowTo != nil {
				ended = *run.WindowTo
			}
			if time.Since(ended) <= grace {
				return false, nil
			}
			// What never arrived may yet: the service's retention removes it by this date.
			cleanBy := time.Now().Add(retention).Format("2006-01-02")
			if err := remove(sdk.Removal{Store: store, Locator: locator, Identities: missing, CleanBy: cleanBy}); err != nil {
				return false, err
			}
			return true, nil
		},
	})
}
