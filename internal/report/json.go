package report

import (
	"encoding/json"
	"io"
	"os"
	"slices"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/nwidger/jsoncolor"

	"github.com/AlvinKuruvilla/clockdiff/internal/runtime"
)

// FormatVersion is the run file's schema version.
//
// The point of writing runs to disk is that whatever reads them can be
// replaced without touching what produces them, which only holds if a reader
// can tell what it is looking at. Bump this when a field changes meaning or
// disappears; adding one does not need it.
const FormatVersion = 1

// Document is a run as it appears on disk.
//
// This type is separate from the internal Run.
// Timestamps are absolute and unrounded: the text report rounds to 10ms because that is
// the honest precision to *read*, but a viewer that lets you zoom needs what
// was actually recorded.
type Document struct {
	Version   int       `json:"version"`
	Project   string    `json:"project"`
	StartedAt time.Time `json:"startedAt"`

	Services []Service `json:"services"`

	// AlreadyRunning were up before the run and so went unmeasured. Without
	// them a reader cannot tell a service that was skipped from one that does
	// not exist.
	AlreadyRunning []string `json:"alreadyRunning,omitempty"`
}

// Service is one service's timeline. Every timestamp is a pointer because an
// unobserved moment and the zero time are different things, and JSON's null
// says so where "0001-01-01T00:00:00Z" would not.
//
// Only recorded moments are written. Durations a reader wants — dead time,
// boot time, how long a dependent was held — are differences between these,
// and deriving them beats storing a second copy that can disagree.
type Service struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"`

	Created           *time.Time `json:"created,omitempty"`
	Started           *time.Time `json:"started,omitempty"`
	PredicateTrue     *time.Time `json:"predicateTrue,omitempty"`
	DeclaredHealthy   *time.Time `json:"declaredHealthy,omitempty"`
	DeclaredUnhealthy *time.Time `json:"declaredUnhealthy,omitempty"`
	Accepting         *time.Time `json:"accepting,omitempty"`
	Exited            *time.Time `json:"exited,omitempty"`

	HasHealthcheck bool `json:"hasHealthcheck"`
	ExpectsPort    bool `json:"expectsPort"`

	ExitCode *int     `json:"exitCode,omitempty"`
	CrashLog []string `json:"crashLog,omitempty"`

	DependsOn []Dependency `json:"dependsOn,omitempty"`
}

// Dependency is one depends_on edge, kept so a reader can redraw the graph
// without parsing the compose file the run came from.
type Dependency struct {
	Service   string `json:"service"`
	Condition string `json:"condition"`
}

// WriteJSON writes a run in the on-disk format.
func WriteJSON(w io.Writer, run *runtime.Run) error {
	doc := Document{
		Version:        FormatVersion,
		Project:        run.Project,
		StartedAt:      run.T0,
		AlreadyRunning: run.AlreadyRunning,
	}

	names := make([]string, 0, len(run.Services))
	for name := range run.Services {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, name := range names {
		doc.Services = append(doc.Services, describeJSON(run, run.Services[name]))
	}

	return encodeJSON(w, doc)
}

// encodeJSON writes doc, colouring it only when a person is reading.
//
// A run file exists to be piped — into jq, into a file, into a viewer — and
// escape sequences in that stream are corruption rather than decoration. So
// the plain encoder is the default and colour is the exception, granted only
// to a terminal that has not asked to go without.
func encodeJSON(w io.Writer, doc Document) error {
	if !wantsColor(w) {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(doc)
	}

	encoder := jsoncolor.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(doc)
}

// wantsColor reports whether w is a terminal a human is reading.
func wantsColor(w io.Writer) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}

	file, ok := w.(*os.File)
	return ok && isatty.IsTerminal(file.Fd())
}

func describeJSON(run *runtime.Run, svc *runtime.Service) Service {
	out := Service{
		Name:              svc.Name,
		Outcome:           svc.Outcome().String(),
		Created:           moment(svc.Created),
		Started:           moment(svc.Start),
		PredicateTrue:     moment(svc.PredicateTrue),
		DeclaredHealthy:   moment(svc.DeclaredHealthy),
		DeclaredUnhealthy: moment(svc.DeclaredUnhealthy),
		Accepting:         moment(svc.Accepting),
		Exited:            moment(svc.Exited),
		HasHealthcheck:    svc.HasHealthcheck,
		ExpectsPort:       svc.ExpectsPort,
		CrashLog:          svc.CrashLog,
	}

	// The exit code only means anything once the container is known to have
	// stopped; a running service would otherwise report a confident zero.
	if svc.Finished {
		code := svc.ExitCode
		out.ExitCode = &code
	}

	for _, dep := range run.Graph.DependsOn(svc.Name) {
		out.DependsOn = append(out.DependsOn, Dependency{
			Service:   dep.Service,
			Condition: dep.Condition,
		})
	}
	return out
}

// moment drops timestamps that were never observed, so absent and epoch stay
// distinguishable in the file.
func moment(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
