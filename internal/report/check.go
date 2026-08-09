// Package report renders clockdiff's results for a terminal.
package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/AlvinKuruvilla/clockdiff/internal/compose"
)

// WriteCheck renders a static pass.
func WriteCheck(w io.Writer, path string, findings []compose.Finding, summary compose.Summary) {
	fmt.Fprintf(w, "%s\n\n", path)

	if len(findings) == 0 {
		fmt.Fprint(w, "no inert start_intervals\n\n")
	} else {
		writeFindings(w, findings)
	}

	fmt.Fprintln(w, undecided(summary))
}

func writeFindings(w io.Writer, findings []compose.Finding) {
	// Service names set the column, and the explanation hangs under the
	// message rather than under the name, so the two lines read as one entry.
	width := 0
	for _, f := range findings {
		width = max(width, len(f.Service))
	}
	hang := strings.Repeat(" ", 2+width+3)

	for _, f := range findings {
		cause := "start_period is absent"
		if f.StartPeriodSet {
			cause = "start_period is 0s"
		}
		fmt.Fprintf(w, "  %-*s   start_interval: %s has no effect\n", width, f.Service, f.StartInterval)
		fmt.Fprintf(w, "%s%s, so probes never enter the tight interval.\n\n", hang, cause)
	}

	fmt.Fprintf(w, "%s found\n\n", count(len(findings), "inert start_interval"))
}

// undecided names what a static pass could not settle. An interval that reads
// as tuned is invisible here and can still be wasting seconds.
func undecided(summary compose.Summary) string {
	if summary.Healthchecked == 0 {
		return fmt.Sprintf("%s, none with a healthcheck. Nothing here can show a quantization gap.",
			count(summary.Services, "service"))
	}

	subject := "those probe intervals waste"
	if summary.Healthchecked == 1 {
		subject = "that probe interval wastes"
	}
	return fmt.Sprintf(
		"%s, %d with a healthcheck. Whether %s time is not\n"+
			"statically decidable — it needs a measured run.",
		count(summary.Services, "service"), summary.Healthchecked, subject)
}

func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
