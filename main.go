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
	return &cobra.Command{
		Use:   "check <compose-file>",
		Short: "Report static defects in a compose file, without running it",
		Long: "Report static defects in a compose file, without running it.\n\n" +
			"Only one defect is statically decidable: a start_interval that cannot\n" +
			"take effect. Everything else clockdiff reports needs a measured run.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			project, err := compose.LoadProject(path)
			if err != nil {
				return err
			}

			findings := compose.Check(project)
			report.WriteCheck(cmd.OutOrStdout(), path, findings, compose.Summarize(project))

			if len(findings) == 0 {
				return nil
			}
			return exitFindings
		},
	}
}

func newUpCommand() *cobra.Command {
	var (
		timeout time.Duration
		project string
	)

	cmd := &cobra.Command{
		Use:   "up <compose-file>",
		Short: "Start the stack and measure where its startup went",
		Long: "Start the stack and measure where its startup went.\n\n" +
			"Each service's own healthcheck is run out-of-band from the moment its\n" +
			"container starts, so readiness is known independently of when Docker\n" +
			"gets round to noticing it. The difference is dead time.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]

			// Compose names a project after the directory holding the file.
			if project == "" {
				abs, err := filepath.Abs(path)
				if err != nil {
					return err
				}
				project = strings.ToLower(filepath.Base(filepath.Dir(abs)))
			}

			cli, err := runtime.NewClient()
			if err != nil {
				return err
			}
			defer cli.Close()

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			run, err := runtime.Observe(ctx, cli, path, project)
			if err != nil {
				return err
			}

			report.WriteProfile(cmd.OutOrStdout(), run)
			return nil
		},
	}

	cmd.Flags().DurationVar(&timeout, "timeout", 2*time.Minute,
		"give up if the stack has not settled")
	cmd.Flags().StringVarP(&project, "project-name", "p", "",
		"compose project name (default: the compose file's directory)")

	return cmd
}
