package cli

import (
	"bufio"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/nimbusxr/stormbreaker/streams"
)

type streamFlags struct {
	root, seed, run, window string
	from, to                int64
}

func (f *streamFlags) add(cmd *cobra.Command, run bool) {
	cmd.Flags().StringVar(&f.root, "root", ".", "the resource root: where the fixture factories and the streams are")
	if run {
		cmd.Flags().StringVar(&f.run, "run", "", "the run's id (required): the records are the run's")
		cmd.Flags().StringVar(&f.seed, "seed", "", "the seed (default: the run's id, as the packs seed a run's streams)")
		cmd.Flags().StringVar(&f.window, "window", "", "when the run's window opened (RFC 3339), which relative times are anchored to")
		_ = cmd.MarkFlagRequired("run")
	}
}

func (f *streamFlags) open(name string) (*streams.Stream, error) {
	path, err := streams.Find(f.root, name)
	if err != nil {
		return nil, err
	}
	def, err := streams.Load(path)
	if err != nil {
		return nil, err
	}
	opts := streams.Options{Seed: f.seed, Run: f.run}
	if opts.Seed == "" {
		opts.Seed = opts.Run
	}
	if f.window != "" {
		if opts.Window, err = time.Parse(time.RFC3339Nano, f.window); err != nil {
			return nil, fmt.Errorf("--window: %w", err)
		}
	}
	return streams.Open(def, f.root, opts)
}

func streamsCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "streams",
		Short: "Look at streams: a run's volume test data, generated from fixture factories",
		Long: "A stream (<name>.stream.yaml) is a count of keys and the weighted mix of fixture-factory fixtures they\n" +
			"take. Its records are a pure function of the definition, the seed and the run, so they can be\n" +
			"generated again at any time: before a run to see them, or days after one to clean up what it created.\n\n" +
			"Expression functions that teams' packs register are not available here.",
	}
	cmd.AddCommand(streamsLintCmd(app), streamsGenerateCmd(app), streamsIDsCmd(app))
	return cmd
}

func streamsLintCmd(app *App) *cobra.Command {
	f := &streamFlags{}
	cmd := &cobra.Command{
		Use:   "lint <stream>",
		Short: "Check that a stream generates and that no two keys claim the same identity",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if f.run == "" {
				f.run = "lint"
			}
			s, err := f.open(args[0])
			if err != nil {
				return err
			}
			if err := s.Lint(); err != nil {
				return err
			}
			def := s.Definition()
			return app.Emit(map[string]any{"stream": def.Name, "keys": def.Count, "ok": true}, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "%s: %d keys, no identity claimed twice\n", def.Name, def.Count)
				return err
			})
		},
	}
	f.add(cmd, false)
	cmd.Flags().StringVar(&f.run, "run", "", "a run to generate it for (default: one named lint)")
	cmd.Flags().StringVar(&f.seed, "seed", "", "the seed (default: the run's)")
	return cmd
}

func streamsGenerateCmd(app *App) *cobra.Command {
	f := &streamFlags{}
	cmd := &cobra.Command{
		Use:   "generate <stream>",
		Short: "Write a run's records, one JSON object a line",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := f.open(args[0])
			if err != nil {
				return err
			}
			to := f.to
			if to <= 0 {
				to = s.Definition().Count
			}
			w := bufio.NewWriter(app.Out)
			defer w.Flush()
			for r, err := range s.Range(f.from, to) {
				if err != nil {
					return err
				}
				if _, err := w.Write(append(streams.CanonicalBytes(r), '\n')); err != nil {
					return err
				}
			}
			return nil
		},
	}
	f.add(cmd, true)
	cmd.Flags().Int64Var(&f.from, "from", 0, "the first key (inclusive)")
	cmd.Flags().Int64Var(&f.to, "to", 0, "the last key (exclusive; default: the stream's count)")
	return cmd
}

func streamsIDsCmd(app *App) *cobra.Command {
	f := &streamFlags{}
	cmd := &cobra.Command{
		Use:   "ids <stream>",
		Short: "Write the identities a run's stream created, one a line: what its cleanup answers for",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := f.open(args[0])
			if err != nil {
				return err
			}
			w := bufio.NewWriter(app.Out)
			defer w.Flush()
			_, err = s.Identities(10_000, func(ids []string) error {
				for _, id := range ids {
					if _, err := fmt.Fprintln(w, id); err != nil {
						return err
					}
				}
				return nil
			})
			return err
		},
	}
	f.add(cmd, true)
	return cmd
}
