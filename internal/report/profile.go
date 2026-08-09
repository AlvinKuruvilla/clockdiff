package report

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/AlvinKuruvilla/clockdiff/internal/runtime"
)

// WriteProfile renders a measured run: when each service was actually ready,
// when Docker noticed, and the dead time between the two.
//
// There is no stack-wide total. Services start in parallel, so summing their
// gaps would describe a startup nobody experienced.
func WriteProfile(w io.Writer, run *runtime.Run) {
	names := make([]string, 0, len(run.Services))
	width := 0
	for name := range run.Services {
		names = append(names, name)
		width = max(width, len(name))
	}
	slices.Sort(names)

	for _, name := range names {
		svc := run.Services[name]
		fmt.Fprintf(w, "  %-*s  %s\n", width, name, describe(run, svc))

		// A crash makes the rest of the row unreliable, so say why underneath
		// it rather than leaving the reader to go and run `compose logs`.
		for _, line := range svc.CrashLog {
			fmt.Fprintf(w, "  %-*s    %s\n", width, "", line)
		}
	}
}

// describe is one service's row: what it waited for, then what became of it.
func describe(run *runtime.Run, svc *runtime.Service) string {
	var parts []string

	// The wait a gated service inherited is the point of recording the graph:
	// dead time does not stay on the service that caused it.
	if blocked, ok := run.Blocked(svc.Name); ok {
		parts = append(parts, fmt.Sprintf("blocked %s on %s", short(blocked), waitedFor(run, svc.Name)))
	}

	ran, ranOK := svc.Ran()

	switch svc.Outcome() {
	case runtime.OutcomeCrashed:
		parts = append(parts, fmt.Sprintf("exited code %d%s", svc.ExitCode, since(ran, ranOK)))

	case runtime.OutcomeCompleted:
		parts = append(parts, "completed"+since(ran, ranOK))

	case runtime.OutcomeHealthy:
		parts = append(parts, healthy(svc))

	case runtime.OutcomeUnhealthy:
		parts = append(parts, fmt.Sprintf("declared unhealthy after %s",
			short(svc.DeclaredUnhealthy.Sub(svc.Start))))

	case runtime.OutcomePending:
		parts = append(parts, "still starting when the run ended")

	case runtime.OutcomeNoReadiness:
		// A blocked row already says everything known about a service with
		// nothing of its own to report.
		if len(parts) == 0 {
			parts = append(parts, "no healthcheck")
		}
	}

	return strings.Join(parts, ", ")
}

// healthy describes a service the daemon declared ready. The dead time needs
// both ends; a probe that never succeeded leaves only the daemon's own moment.
func healthy(svc *runtime.Service) string {
	declared := short(svc.DeclaredHealthy.Sub(svc.Start))

	gap, ok := svc.Gap()
	if !ok {
		return "declared healthy " + declared
	}
	boot, _ := svc.Boot()
	return fmt.Sprintf("ready %s, declared healthy %s, %s dead", short(boot), declared, short(gap))
}

// short trims durations to something a reader can compare at a glance. The
// measurement is worth tens of milliseconds at best — the probe interval plus
// exec overhead — so printing nanoseconds would imply precision that is not
// there.
func short(d time.Duration) string {
	return d.Round(10 * time.Millisecond).String()
}

// waitedFor names a service's dependencies, so a blocked row says what it was
// blocked on rather than only how long.
func waitedFor(run *runtime.Run, name string) string {
	deps := run.Graph.DependsOn(name)
	names := make([]string, 0, len(deps))
	for _, dep := range deps {
		names = append(names, dep.Service)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// since renders the run-time of a container that has exited. The die event can
// arrive after the run stops watching, in which case the crash is known but
// its timing is not.
func since(d time.Duration, ok bool) string {
	if !ok {
		return ""
	}
	return " after " + short(d)
}
