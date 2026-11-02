package cli

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nimbusxr/stormbreaker/internal/ui"
)

func uiCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "The stormbreaker UI: an environment and its runs, as its orchestrator has them",
	}
	cmd.AddCommand(uiServeCmd(app))
	return cmd
}

func uiServeCmd(app *App) *cobra.Command {
	addr := ":8095"
	var environments []string
	var token string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the UI for one or more environments' orchestrators",
		Long: "Serves the stormbreaker UI (ADR 0026): an environment's dispatchers, groups and runs, and\n" +
			"each run as it happens. It reads each orchestrator through its API, with a token that may\n" +
			"read it, which stays on the server; it only reads.\n\n" +
			"Every flag can be set with its STORMBREAKER_ environment variable instead.",
		Example: "  stormbreaker ui serve --environment staging=http://orchestrator:8080 --token \"$READ_TOKEN\"",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			envs, err := ui.ParseEnvironments(environments, token)
			if err != nil {
				return err
			}
			log := slog.New(slog.NewJSONHandler(app.Err, nil))
			return serveUI(cmd.Context(), app, addr, envs, log)
		},
	}
	f := cmd.Flags()
	f.StringVar(&addr, "addr", env("STORMBREAKER_UI_ADDR", addr), "the address to listen on")
	f.StringArrayVar(&environments, "environment", splitList(env("STORMBREAKER_UI_ENVIRONMENTS", "")), "an environment to show, name=url of its orchestrator; repeat it for several")
	f.StringVar(&token, "token", env("STORMBREAKER_TOKEN", ""), "a token that may read the orchestrators")
	return cmd
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func serveUI(ctx context.Context, app *App, addr string, envs []ui.Environment, log *slog.Logger) error {
	// Requests share the command's context, so that live views end when it does, and shutting
	// down does not wait on them.
	srv := &http.Server{
		Addr: addr, Handler: ui.Handler(envs, log), ReadHeaderTimeout: 10 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	names := make([]string, len(envs))
	for i, e := range envs {
		names[i] = e.Name
	}
	log.Info("ui serving", "addr", ln.Addr().String(), "environments", strings.Join(names, ","))
	announceUI(app, ln.Addr(), envs)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
