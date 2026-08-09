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
		fmt.Fprintf(w, "  %-*s  ", width, name)

		if !svc.Probeable {
			// A gated service has no readiness of its own to report, but the
			// wait it inherited is the point: dead time does not stay on the
			// service that caused it.
			if blocked, ok := run.Blocked(name); ok {
				fmt.Fprintf(w, "blocked %s on %s\n", short(blocked), waitedFor(run, name))
				continue
			}
			fmt.Fprintln(w, "no healthcheck")
			continue
		}

		boot, _ := svc.Boot()
		gap, ok := svc.Gap()
		if !ok {
			fmt.Fprintf(w, "ready %s, never declared healthy\n", short(boot))
			continue
		}
		fmt.Fprintf(w, "ready %s   declared healthy %s   %s dead\n",
			short(boot), short(svc.DeclaredHealthy.Sub(svc.Start)), short(gap))
	}
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
