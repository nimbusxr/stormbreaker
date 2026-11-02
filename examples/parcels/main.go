// Command parcels is the lab's parcels service: a team's service under test. Shops register
// parcels at its front door; it publishes a ParcelRegistered event for each when it has a Kafka
// cluster, and keeps them in memory, one store per region. Its admin door (PUT and DELETE on a
// parcel) is what its team's dispatcher prepares and cleans up through. It serves Prometheus
// metrics at /metrics.
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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Parcel is what the front door takes: a shop's registration.
type Parcel struct {
	Reference    string     `json:"reference"`
	Sender       string     `json:"sender,omitempty"`
	ServiceLevel string     `json:"serviceLevel,omitempty"`
	WeightGrams  int        `json:"weightGrams,omitempty"`
	Recipient    *Recipient `json:"recipient,omitempty"`
}

// Recipient is where a parcel goes.
type Recipient struct {
	Name     string `json:"name,omitempty"`
	City     string `json:"city,omitempty"`
	Postcode string `json:"postcode,omitempty"`
	Country  string `json:"country,omitempty"`
}

type service struct {
	region   string
	topic    string
	producer *kgo.Client
	log      *slog.Logger

	mu      sync.Mutex
	parcels map[string]Parcel
}

var requests = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "http_server_requests_seconds",
	Help:    "How long the parcels service took to answer, by route and status.",
	Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
}, []string{"route", "status"})

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }

func measured(route string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		h(rec, r)
		requests.WithLabelValues(route, strconv.Itoa(rec.status)).Observe(time.Since(start).Seconds())
	}
}

func (s *service) register(w http.ResponseWriter, r *http.Request) {
	var p Parcel
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.Reference == "" {
		http.Error(w, "a parcel is JSON with a reference", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.parcels[p.Reference] = p
	s.mu.Unlock()
	if s.producer != nil {
		value, _ := json.Marshal(map[string]any{"type": "ParcelRegistered", "reference": p.Reference, "sender": p.Sender,
			"serviceLevel": p.ServiceLevel, "weightGrams": p.WeightGrams, "region": s.region})
		rec := &kgo.Record{Topic: s.topic, Key: []byte(p.Reference), Value: value}
		if err := s.producer.ProduceSync(r.Context(), rec).FirstErr(); err != nil {
			s.log.Error("cannot publish ParcelRegistered", "parcel", p.Reference, "err", err)
			http.Error(w, "cannot publish the parcel", http.StatusServiceUnavailable)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{"reference": p.Reference})
}

func (s *service) get(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	p, ok := s.parcels[r.PathValue("reference")]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "no such parcel", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

// put is the admin door's prepare: an idempotent upsert.
func (s *service) put(w http.ResponseWriter, r *http.Request) {
	p := Parcel{}
	_ = json.NewDecoder(r.Body).Decode(&p)
	p.Reference = r.PathValue("reference")
	s.mu.Lock()
	s.parcels[p.Reference] = p
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// remove is the admin door's cleanup.
func (s *service) remove(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	delete(s.parcels, r.PathValue("reference"))
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func main() {
	if err := run(); err != nil {
		slog.Error("exiting", "err", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	s := &service{region: env("REGION", "local"), topic: env("TOPIC", "parcel-events"), log: log, parcels: map[string]Parcel{}}
	if brokers := os.Getenv("KAFKA_BROKERS"); brokers != "" {
		cl, err := kgo.NewClient(kgo.SeedBrokers(strings.Split(brokers, ",")...), kgo.AllowAutoTopicCreation())
		if err != nil {
			return fmt.Errorf("kafka: %w", err)
		}
		defer cl.Close()
		s.producer = cl
	}
	prometheus.MustRegister(requests)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /parcels", measured("/parcels", s.register))
	mux.HandleFunc("GET /parcels/{reference}", measured("/parcels/{reference}", s.get))
	mux.HandleFunc("PUT /parcels/{reference}", s.put)
	mux.HandleFunc("DELETE /parcels/{reference}", s.remove)
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"status":"ok"}`)) })
	mux.Handle("GET /metrics", promhttp.Handler())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: env("ADDR", ":8081"), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("parcels serving", "addr", srv.Addr, "region", s.region, "kafka", s.producer != nil)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
