package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nimbusxr/stormbreaker/internal/orchestrator"
)

func orchestratorCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orchestrator",
		Short: "The orchestrator: one per shared environment, coordinating runs",
	}
	cmd.AddCommand(orchestratorServeCmd(app))
	return cmd
}

func orchestratorServeCmd(app *App) *cobra.Command {
	cfg := orchestrator.Config{}
	var principals string
	addr := ":8080"
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the orchestrator's API and send its notifications",
		Long: "Serves the orchestrator's API (its OpenAPI document is at /openapi.yaml) and sends the\n" +
			"notifications of what happens to runs to the dispatchers of their groups.\n\n" +
			"Every flag can be set with its STORMBREAKER_ environment variable instead.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := slog.New(slog.NewJSONHandler(app.Err, nil))
			if principals != "" {
				ps, err := orchestrator.LoadPrincipals(principals)
				if err != nil {
					return err
				}
				cfg.Principals = ps
			}
			if cfg.Token == "" && len(cfg.Principals) == 0 {
				log.Warn("no token or principals are configured: anyone who can reach the orchestrator can use its API")
			}
			return serveOrchestrator(cmd.Context(), app, addr, cfg, log)
		},
	}
	f := cmd.Flags()
	f.StringVar(&addr, "addr", env("STORMBREAKER_ADDR", addr), "the address to listen on")
	f.StringVar(&cfg.DB, "db", env("STORMBREAKER_DB", "orchestrator.db"), "the SQLite database's path")
	f.StringVar(&cfg.Token, "token", env("STORMBREAKER_TOKEN", ""), "a bearer token with every grant: the lab's, or a lone team's (none if empty)")
	f.StringVar(&principals, "principals", env("STORMBREAKER_PRINCIPALS", ""), "a file of principals: each a token's SHA-256 and its grants (admin, suites, dispatchers)")
	f.DurationVar(&cfg.Lease, "lease", envDuration("STORMBREAKER_LEASE", 30*time.Second), "how long a run's or a dispatcher's lease lasts unrenewed")
	f.DurationVar(&cfg.ReadinessTimeout, "readiness-timeout", envDuration("STORMBREAKER_READINESS_TIMEOUT", 120*time.Second), "how long required dispatchers have to prepare")
	f.DurationVar(&cfg.StaleAfter, "stale-after", envDuration("STORMBREAKER_STALE_AFTER", 30*time.Second), "how long a required dispatcher may stay silent before preparing")
	f.DurationVar(&cfg.Retention, "retention", envDuration("STORMBREAKER_RETENTION", 14*24*time.Hour), "how long closed runs are kept")
	f.DurationVar(&cfg.CleanupHold, "cleanup-hold", envDuration("STORMBREAKER_CLEANUP_HOLD", 15*time.Minute), "how long a run in cleanup keeps holding its group's dispatchers before other runs may open beside it")
	f.DurationVar(&cfg.CleanupDeadline, "cleanup-deadline", envDuration("STORMBREAKER_CLEANUP_DEADLINE", 24*time.Hour), "how long a run may stay in cleanup before it closes unfinished, naming the instances that never completed")
	f.DurationVar(&cfg.FootprintRetention, "footprint-retention", envDuration("STORMBREAKER_FOOTPRINT_RETENTION", 72*time.Hour), "how long a closed run keeps its identities (at most --retention); its journal stays for --retention")
	reserve := sizeFlag{n: 1 << 30}
	if v := env("STORMBREAKER_DISK_RESERVE", ""); v != "" {
		_ = reserve.Set(v)
	}
	f.Var(&reserve, "disk-reserve", "the room kept on the database's volume, like 5GiB: below it, no new runs or declarations are taken (0 keeps none)")
	cmd.PreRun = func(*cobra.Command, []string) {
		cfg.DiskReserve = reserve.n
		if reserve.n == 0 {
			cfg.DiskReserve = -1
		}
	}
	return cmd
}

// sizeFlag is a size in bytes, written like 512MiB, 5GiB or 1TB.
type sizeFlag struct{ n int64 }

func (s *sizeFlag) String() string {
	for _, u := range []struct {
		suffix string
		n      int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if s.n >= u.n && s.n%u.n == 0 {
			return strconv.FormatInt(s.n/u.n, 10) + u.suffix
		}
	}
	return strconv.FormatInt(s.n, 10)
}

func (s *sizeFlag) Type() string { return "size" }

func (s *sizeFlag) Set(v string) error {
	units := []struct {
		suffix string
		n      int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"KB", 1e3}, {"B", 1}}
	v = strings.TrimSpace(v)
	for _, u := range units {
		if num, ok := strings.CutSuffix(v, u.suffix); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			if err != nil || f < 0 {
				return fmt.Errorf("a size is a number and a unit, like 5GiB, not %q", v)
			}
			s.n = int64(f * float64(u.n))
			return nil
		}
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return fmt.Errorf("a size is a number and a unit, like 5GiB, not %q", v)
	}
	s.n = n
	return nil
}

func serveOrchestrator(ctx context.Context, app *App, addr string, cfg orchestrator.Config, log *slog.Logger) error {
	o, err := orchestrator.Open(ctx, cfg, orchestrator.WithLogger(log))
	if err != nil {
		return err
	}
	defer o.Close()
	srv := &http.Server{Addr: addr, Handler: o.Handler(), ReadHeaderTimeout: 10 * time.Second}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.Serve(ctx)
	}()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("orchestrator serving", "addr", ln.Addr().String(), "db", cfg.DB)
	announceOrchestrator(app, ln.Addr())
	select {
	case err := <-errc:
		cancel()
		<-done
		return err
	case <-ctx.Done():
	}
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	err = srv.Shutdown(shutdown)
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func env(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(name); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
