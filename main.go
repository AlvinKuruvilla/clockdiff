package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/AlvinKuruvilla/clockdiff/internal/compose"
	"github.com/AlvinKuruvilla/clockdiff/internal/report"
	"github.com/AlvinKuruvilla/clockdiff/internal/runtime"
)

// exitCode carries a process exit status out through cobra's error return, for
// commands whose result is a verdict rather than a failure.
type exitCode int

func (c exitCode) Error() string { return fmt.Sprintf("exit status %d", int(c)) }

// Findings exit non-zero so `check` can gate a pre-commit hook.
const exitFindings exitCode = 1

func main() {
	root := &cobra.Command{
		Use:           "clockdiff",
		Short:         "Profile where `docker compose up` spends its time",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newCheckCommand())
	root.AddCommand(newUpCommand())

	err := root.Execute()
	if err == nil {
		return
	}

	if code, ok := errors.AsType[exitCode](err); ok {
		os.Exit(int(code))
	}
	fmt.Fprintf(os.Stderr, "clockdiff: %v\n", err)
	os.Exit(2)
}

func newCheckCommand() *cobra.Command {
	var files []string

	cmd := &cobra.Command{
		Use:   "check [compose.yml...] [-f compose.yml]...",
		Short: "Report static defects in a compose file, without running it",
		Long: "Report static defects in a compose file, without running it.\n\n" +
			"Only one defect is statically decidable: a start_interval that cannot\n" +
			"take effect. Everything else clockdiff reports needs a measured run.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			named, err := namedFiles(files, args)
			if err != nil {
				return err
			}
			files, err := resolveFiles(named)
			if err != nil {
				return err
			}

			project, err := compose.LoadProject(files...)
			if err != nil {
				return err
			}

			findings := compose.Check(project)
			report.WriteCheck(cmd.OutOrStdout(), strings.Join(files, ", "),
				findings, compose.Summarize(project))

			if len(findings) == 0 {
				return nil
			}
			return exitFindings
		},
	}

	addFileFlag(cmd, &files)
	return cmd
}

// addFileFlag mirrors `docker compose -f`: repeatable, in merge order, and
// absent means let Compose find its own files.
func addFileFlag(cmd *cobra.Command, files *[]string) {
	cmd.Flags().StringArrayVarP(files, "file", "f", nil,
		"compose file, repeatable; later files override earlier "+
			"(default: the files compose would discover)")
}

// namedFiles is the file list the user asked for, by either spelling.
//
// Positional paths and -f are both accepted because naming one file is the
// common case and `clockdiff check compose.yml` is how anyone would first try
// it. Giving both is refused rather than merged: the two would have to be
// ordered against each other, and merge order decides which file wins.
func namedFiles(flagged, positional []string) ([]string, error) {
	switch {
	case len(flagged) > 0 && len(positional) > 0:
		return nil, fmt.Errorf("pass compose files either as arguments or with -f, not both")
	case len(flagged) > 0:
		return flagged, nil
	default:
		return positional, nil
	}
}

// resolveFiles fills in what Compose would have loaded when nothing was named.
// check has to do this itself because it loads the model rather than shelling
// out; up leaves the discovery to compose.
func resolveFiles(files []string) ([]string, error) {
	if len(files) > 0 {
		return files, nil
	}

	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	found, err := compose.Discover(dir)
	if err != nil {
		return nil, err
	}

	// Discovery returns absolute paths. They are printed back in the report
	// header, where the reader already knows where they are standing.
	for i, path := range found {
		if rel, err := filepath.Rel(dir, path); err == nil {
			found[i] = rel
		}
	}
	return found, nil
}

func newUpCommand() *cobra.Command {
	var (
		timeout    time.Duration
		project    string
		files      []string
		asJSON     bool
		exportPath string
	)

	cmd := &cobra.Command{
		Use:   "up [compose.yml...] [-f compose.yml]...",
		Short: "Start the stack and measure where its startup went",
		Long: "Start the stack and measure where its startup went.\n\n" +
			"Each service's own healthcheck is run out-of-band from the moment its\n" +
			"container starts, so readiness is known independently of when Docker\n" +
			"gets round to noticing it. The difference is dead time.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			files, err := namedFiles(files, args)
			if err != nil {
				return err
			}

			// Compose names a project after the directory holding the first
			// file, or the working directory when it found them itself.
			if project == "" {
				dir, err := os.Getwd()
				if err != nil {
					return err
				}
				if len(files) > 0 {
					abs, err := filepath.Abs(files[0])
					if err != nil {
						return err
					}
					dir = filepath.Dir(abs)
				}
				project = strings.ToLower(filepath.Base(dir))
			}

			// Exporting is not `--json > run.json`: the report still prints,
			// and a run costs however long the stack takes to settle, so the
			// two forms cannot be had by running it twice. Open the file
			// before anything starts — a path that cannot be written should
			// fail now, not after the wait.
			var export *os.File
			if exportPath != "" {
				export, err = os.Create(exportPath)
				if err != nil {
					return err
				}
				defer export.Close()
			}

			cli, err := runtime.NewClient()
			if err != nil {
				return err
			}
			defer cli.Close()

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			// A run can fail and still have measured most of the stack, so
			// print whatever it managed before reporting the failure.
			run, observeErr := runtime.Observe(ctx, cli, files, project)
			if run != nil {
				if asJSON {
					if err := report.WriteJSON(cmd.OutOrStdout(), run); err != nil {
						return err
					}
				} else {
					report.WriteProfile(cmd.OutOrStdout(), run)
				}

				if export != nil {
					if err := report.WriteJSON(export, run); err != nil {
						return err
					}
					// Closed explicitly as well as deferred: a write that
					// only fails on flush would otherwise be silent.
					if err := export.Close(); err != nil {
						return err
					}
				}
			}
			return observeErr
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute,
		"give up if the stack has not settled")
	cmd.Flags().StringVarP(&project, "project-name", "p", "",
		"compose project name (default: the compose file's directory)")
	cmd.Flags().BoolVar(&asJSON, "json", false,
		"write the run as JSON instead of a report")
	cmd.Flags().StringVar(&exportPath, "export-json", "",
		"also write the run as JSON to this file, keeping the report on stdout")
	addFileFlag(cmd, &files)

	return cmd
}
