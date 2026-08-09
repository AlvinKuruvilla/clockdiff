package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/AlvinKuruvilla/clockdiff/internal/compose"
	"github.com/AlvinKuruvilla/clockdiff/internal/report"
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

	err := root.Execute()
	if err == nil {
		return
	}

	var code exitCode
	if errors.As(err, &code) {
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
