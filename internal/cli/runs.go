package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// runsCmd lists an orchestrator's runs, for an operator: where each is, since when, and whether
// it holds its group.
func runsCmd(app *App) *cobra.Command {
	var url, token, group string
	var states []string
	var limit int
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List an orchestrator's runs, newest first",
		Long: "Lists runs: their group, state, whether they hold their group's dispatchers, their claims,\n" +
			"and when they opened. --state (repeatable) and --group narrow it.\n\n" +
			"The orchestrator is --url (STORMBREAKER_ORCHESTRATOR_URL), with --token (STORMBREAKER_TOKEN).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client(url, token)
			if err != nil {
				return err
			}
			list, err := c.Runs(cmd.Context(), states, group, 0, limit)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(app.Out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "RUN\tGROUP\tSTATE\tHOLDS\tCLAIMS\tOPENED")
			for _, r := range list.Runs {
				state := r.State
				switch {
				case r.Failed:
					state += " (failed)"
				case r.Abandoned:
					state += " (abandoned)"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%d\t%s\n", r.ID, r.Group, state, r.Holds, r.Claims, r.OpenedAt.Local().Format(time.DateTime))
			}
			return w.Flush()
		},
	}
	cmd.Flags().StringVar(&url, "url", env("STORMBREAKER_ORCHESTRATOR_URL", ""), "the orchestrator's URL")
	cmd.Flags().StringVar(&token, "token", env("STORMBREAKER_TOKEN", ""), "its bearer token, if it asks for one")
	cmd.Flags().StringArrayVar(&states, "state", nil, "a state to list (opened, traffic-complete, settled, cleanup, closed); repeat for several")
	cmd.Flags().StringVar(&group, "group", "", "only this group's runs")
	cmd.Flags().IntVar(&limit, "limit", 50, "the most runs to list")
	return cmd
}
