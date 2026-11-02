// Command tracking is the lab's tracking service: another team's service, downstream of parcels.
// It consumes ParcelRegistered events, books a collection with the courier, a partner nobody in
// the environment owns (in the lab, a mock), and writes a tracking record to Postgres for each.
// It keeps records for seven days: its own retention, which is what its dispatcher promises when
// it reports records clean-by.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"
)

type registered struct {
	Type      string `json:"type"`
	Reference string `json:"reference"`
}

// collection asks the courier when it will collect a parcel.
func collection(ctx context.Context, courier, reference string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(courier, "/")+"/courier/collections/"+reference, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the courier answered %s", resp.Status)
	}
	var c struct {
		Slot string `json:"slot"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return "", err
	}
	return c.Slot, nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec(ctx, `create table if not exists tracking (
		tracking_id text primary key, reference text not null, collection_slot text not null, status text not null,
		created_at timestamptz not null default now())`); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(os.Getenv("KAFKA_BROKERS"), ",")...),
		kgo.ConsumerGroup("tracking"), kgo.ConsumeTopics(env("TOPIC", "parcel-events")))
	if err != nil {
		return fmt.Errorf("kafka: %w", err)
	}
	defer cl.Close()

	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /tracking/{id}", func(w http.ResponseWriter, r *http.Request) {
			var status, slot string
			err := db.QueryRow(r.Context(), `select status, collection_slot from tracking where tracking_id = $1`, r.PathValue("id")).Scan(&status, &slot)
			if err != nil {
				http.Error(w, "no such tracking record", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"trackingId": r.PathValue("id"), "status": status, "collectionSlot": slot})
		})
		mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"ok"}`)) })
		srv := &http.Server{Addr: env("ADDR", ":8082"), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "err", err)
		}
	}()
	// Retention: records older than seven days go, whoever made them.
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			if _, err := db.Exec(ctx, `delete from tracking where created_at < now() - interval '7 days'`); err != nil && ctx.Err() == nil {
				log.Error("retention", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	courier := os.Getenv("COURIER_URL")
	log.Info("tracking consuming")
	for ctx.Err() == nil {
		fetches := cl.PollFetches(ctx)
		fetches.EachRecord(func(rec *kgo.Record) {
			var ev registered
			if json.Unmarshal(rec.Value, &ev) != nil || ev.Type != "ParcelRegistered" {
				return
			}
			slot, err := collection(ctx, courier, ev.Reference)
			if err != nil {
				log.Error("collection", "parcel", ev.Reference, "err", err)
				return
			}
			if _, err := db.Exec(ctx, `insert into tracking (tracking_id, reference, collection_slot, status) values ($1, $2, $3, 'AWAITING_COLLECTION')
				on conflict (tracking_id) do nothing`, "TRK-"+ev.Reference, ev.Reference, slot); err != nil {
				log.Error("insert", "parcel", ev.Reference, "err", err)
			}
		})
	}
	return nil
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
