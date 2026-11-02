// Package cli is the stormbreaker command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/nimbusxr/stormbreaker/internal/version"
)

// App holds what every command shares: where output goes, and whether it is JSON.
type App struct {
	Out  io.Writer
	Err  io.Writer
	JSON bool
}

// Emit writes v as JSON with --json, and as text through human otherwise.
func (a *App) Emit(v any, human func(io.Writer) error) error {
	if a.JSON {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	return human(a.Out)
}

// Main runs the command line with args and returns the process's exit code.
func Main(args []string) int {
	app := &App{Out: os.Stdout, Err: os.Stderr}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root := newRoot(app)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintln(app.Err, "stormbreaker:", err)
		}
		return 1
	}
	return 0
}

func newRoot(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:   "stormbreaker",
		Short: "Operational acceptance testing in shared environments",
		Long: "stormbreaker runs smoke and load tests in shared environments, where every team is\n" +
			"answerable for the data its systems create. Its packs run inside axx; this command\n" +
			"serves the orchestrator and dispatchers and works with streams.\n\n" +
			"Writing tests: a project lists stormbreaker's packs in axx-packs.yaml, and axx answers for\n" +
			"their steps, offline and for its version:\n" +
			"  axx steps                  every step of the project's packs, one line each: read it once\n" +
			"  axx steps show <id>...     the documentation of steps whose line leaves you unsure\n" +
			"  stormbreaker streams lint  check a stream's definition\n" +
			"  axx validate && axx lint && axx run --compact\n" +
			"                             check the features and the test data, then run, in one command\n\n" +
			"Docs for people: https://stormbreaker.nimbusxr.us",
		Version:       version.Get().String(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	root.SetVersionTemplate("stormbreaker {{.Version}}\n")
	root.PersistentFlags().BoolVar(&app.JSON, "json", false, "write machine-readable JSON")
	root.AddCommand(versionCmd(app), orchestratorCmd(app), dispatcherCmd(app), uiCmd(app), streamsCmd(app), groupsCmd(app), runsCmd(app))
	return root
}
