package cli

import (
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/nimbusxr/stormbreaker/dispatcher/sdk"
)

func dispatcherCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dispatcher",
		Short: "A dispatcher: one per team app per environment, preparing before runs and cleaning up after",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Serve a dispatcher with no hooks",
		Long: "Serves a dispatcher that takes part in runs with nothing to prepare and nothing to clean up:\n" +
			"it reports prepared, then completed. A team's own dispatcher is built with the dispatcher-sdk\n" +
			"(github.com/nimbusxr/stormbreaker/dispatcher/sdk) and its hooks.\n\n" +
			"Configured from the environment: STORMBREAKER_DISPATCHER, STORMBREAKER_INSTANCE (a stable\n" +
			"name: its region or partition, never its pod's), STORMBREAKER_TEAM, STORMBREAKER_CALLBACK_URL, STORMBREAKER_SECRET,\n" +
			"STORMBREAKER_ORCHESTRATOR, STORMBREAKER_TOKEN, STORMBREAKER_GROUPS, STORMBREAKER_ADDR\n" +
			"(default :8090) and STORMBREAKER_REASSERT (default 10s).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := sdk.ConfigFromEnv()
			if err != nil {
				return err
			}
			cfg.Logger = slog.New(slog.NewJSONHandler(app.Err, nil))
			return sdk.Serve(cmd.Context(), cfg, sdk.Hooks{})
		},
	})
	return cmd
}
