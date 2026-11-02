package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/nimbusxr/stormbreaker/internal/version"
)

func versionCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the stormbreaker version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			info := version.Get()
			return app.Emit(info, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "stormbreaker %s %s %s\n", info, info.Platform, shortCommit(info.Commit))
				return err
			})
		},
	}
}

func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}
