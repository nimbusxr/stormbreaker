package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/nimbusxr/stormbreaker/protocol"
)

// groupsCmd manages an orchestrator's groups: a group is made when a dispatcher joins it, but a
// team that runs without a dispatcher yet makes its own once.
func groupsCmd(app *App) *cobra.Command {
	var url, token string
	cmd := &cobra.Command{
		Use:   "groups",
		Short: "List an orchestrator's groups, or make one",
		Long: "A run belongs to a group. A group is made when a dispatcher joins it; a team running\n" +
			"without a dispatcher makes its own once, with `stormbreaker groups create <name>`.\n\n" +
			"The orchestrator is --url (STORMBREAKER_ORCHESTRATOR_URL), with --token (STORMBREAKER_TOKEN).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := client(url, token)
			if err != nil {
				return err
			}
			groups, err := c.Groups(cmd.Context())
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(app.Out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "GROUP\tDISPATCHERS")
			for _, g := range groups {
				fmt.Fprintf(w, "%s\t%s\n", g.Name, strings.Join(g.Dispatchers, ", "))
			}
			return w.Flush()
		},
	}
	cmd.PersistentFlags().StringVar(&url, "url", env("STORMBREAKER_ORCHESTRATOR_URL", ""), "the orchestrator's URL")
	cmd.PersistentFlags().StringVar(&token, "token", env("STORMBREAKER_TOKEN", ""), "its bearer token, if it asks for one")
	cmd.AddCommand(&cobra.Command{
		Use:   "create <name>",
		Short: "Make a group, for runs in it to open",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := client(url, token)
			if err != nil {
				return err
			}
			if err := c.CreateGroup(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "group %s is ready: tag scenarios @stormbreaker-group/%s\n", args[0], args[0])
			return nil
		},
	})
	return cmd
}

func client(url, token string) (*protocol.Client, error) {
	if url == "" {
		return nil, fmt.Errorf("which orchestrator? give its URL with --url or STORMBREAKER_ORCHESTRATOR_URL")
	}
	return protocol.NewClient(url, token), nil
}
