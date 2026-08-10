package runtime

import "time"

// Service is one compose service's startup.
//
// Every timestamp is the host clock at the moment the observation arrived.
// Daemon timestamps are deliberately unused: on Docker Desktop the VM clock
// drifts from the host's, so mixing the two poisons every subtraction.
type Service struct {
	Name string

	// Created is the container's create event. A gated service is created
	// early and held, so the span from here to Start is what it spent
	// waiting on its dependencies.
	Created time.Time

	// Start is the container's start event.
	Start time.Time

	// ContainerID is needed after the run to fetch logs for anything that
	// crashed.
	ContainerID string

	// Exited is the host clock when the die event arrived, and is set only if
	// the run was still watching when it did.
	Exited time.Time

	// Finished records that the container was not running when the run ended,
	// whether or not its die event was seen.
	Finished bool

	// CrashLog is the tail of a crashed container's output. Empty unless the
	// service exited non-zero.
	CrashLog []string

	// ExitCode is meaningful only once Exited is set.
	ExitCode int

	// PredicateTrue is when clockdiff's own run of the healthcheck command
	// first succeeded — when the service actually became ready.
	PredicateTrue time.Time

	// DeclaredHealthy is when the daemon said so.
	DeclaredHealthy time.Time

	// DeclaredUnhealthy is when the daemon gave up, after `retries` failures.
	DeclaredUnhealthy time.Time

	// Accepting is the time when a port the container declares first appeared in its
	// own /proc/net/tcp as LISTEN.
	Accepting time.Time

	// HasHealthcheck and ExpectsPort are what the container declares, known
	// as soon as it is inspected. Neither is "we observed it": a container
	// that crashes declares both and delivers neither, and only the
	// declaration says whether an outcome is still owed.
	HasHealthcheck bool
	ExpectsPort    bool

	// probePending is set between a container starting and its prober
	// reporting. Without it a service that has only just started looks
	// finished, because HasHealthcheck is not known yet and so reads false.
	probePending bool
}

// measured reports b.Sub(a) only when both ends were observed. A service that
// never went healthy and one that went healthy instantly must not both report
// a zero duration.
func measured(a, b time.Time) (time.Duration, bool) {
	if a.IsZero() || b.IsZero() {
		return 0, false
	}
	return b.Sub(a), true
}

// Gap is the dead time between the service becoming ready and Docker noticing.
func (s *Service) Gap() (time.Duration, bool) {
	return measured(s.PredicateTrue, s.DeclaredHealthy)
}

// Boot is how long the service took to actually become ready.
func (s *Service) Boot() (time.Duration, bool) {
	return measured(s.Start, s.PredicateTrue)
}

// Ran is how long the container was up before it exited.
func (s *Service) Ran() (time.Duration, bool) {
	return measured(s.Start, s.Exited)
}

// Outcome is how far a service got.
//
// Every derived question — did it crash, is it still going, is there anything
// to measure — resolves to this one value, so the answers cannot contradict
// each other the way a handful of independent booleans can.
type Outcome int

const (
	// OutcomePending is a service that could still reach another outcome.
	OutcomePending Outcome = iota
	OutcomeHealthy
	OutcomeUnhealthy
	OutcomeCrashed
	OutcomeCompleted

	// OutcomeAccepting is a service with no healthcheck that was nonetheless
	// seen to start listening on a port it declares. Weaker than healthy —
	// a bound socket is not a served request — but far more than nothing.
	OutcomeAccepting

	// OutcomeNoReadiness is a started service that declares neither a
	// healthcheck nor a port. Compose treats service_started as satisfied the
	// instant it starts, so starting is all the readiness there is to have.
	OutcomeNoReadiness
)

// Outcome classifies a service. A container declaring a healthcheck has three
// terminal states — healthy, unhealthy, dead — and stays pending until one of
// them, which is what lets a run finish on events rather than on a timer.
func (s *Service) Outcome() Outcome {
	switch {
	// A non-zero exit outranks anything the service managed first. A zero
	// exit is a one-shot doing its job, possibly one that something waits on
	// with service_completed_successfully.
	case s.Finished && s.ExitCode != 0:
		return OutcomeCrashed
	case s.Finished:
		return OutcomeCompleted
	case !s.DeclaredUnhealthy.IsZero():
		return OutcomeUnhealthy
	case !s.DeclaredHealthy.IsZero():
		return OutcomeHealthy
	case s.Start.IsZero() || s.probePending || s.HasHealthcheck:
		return OutcomePending

	// A container that exposes a port is asserting it will listen on one, so
	// that assertion is owed an answer the same way a healthcheck is.
	case s.ExpectsPort && s.Accepting.IsZero():
		return OutcomePending

	case !s.Accepting.IsZero():
		return OutcomeAccepting

	default:
		return OutcomeNoReadiness
	}
}

// Serving is how long the service took to start accepting connections.
func (s *Service) Serving() (time.Duration, bool) {
	return measured(s.Start, s.Accepting)
}

// Run is one observed `docker compose up`.
type Run struct {
	Project  string
	T0       time.Time
	Services map[string]*Service
	Graph    *Graph

	// AlreadyRunning are the services that were up before this run began.
	//
	// Compose leaves a running container alone, and Docker emits no events
	// for a container nobody touched, so these produce no measurements at
	// all. Recording them separately is what distinguishes a service with no
	// startup cost from one that simply was not observed.
	AlreadyRunning []string

	// allStarted is set when `compose up -d` returns. Until then a service
	// gated on another has not started and is absent from Services entirely,
	// so the run cannot be judged finished.
	allStarted bool
}

// DependenciesMet is when the slowest of a service's dependencies satisfied
// its condition — the earliest moment compose could have started it.
//
// False when the service has no dependencies, or when any one of them was
// never observed to satisfy its condition. A partial answer here would be
// indistinguishable from a fast one.
func (r *Run) DependenciesMet(name string) (time.Time, bool) {
	deps := r.Graph.DependsOn(name)
	if len(deps) == 0 {
		return time.Time{}, false
	}

	// All conditions must hold at once, so the service waits for whichever is
	// satisfied last.
	var latest time.Time
	for _, dep := range deps {
		met, ok := r.conditionMet(dep)
		if !ok {
			return time.Time{}, false
		}
		if met.After(latest) {
			latest = met
		}
	}
	return latest, true
}

// conditionMet is when one dependency satisfied the condition placed on it.
func (r *Run) conditionMet(dep Dependency) (time.Time, bool) {
	svc, ok := r.Services[dep.Service]
	if !ok {
		return time.Time{}, false
	}

	var met time.Time
	switch dep.Condition {
	case ConditionHealthy:
		met = svc.DeclaredHealthy
	case ConditionStarted:
		met = svc.Start
	case ConditionCompleted:
		met = svc.Exited
	}
	if met.IsZero() {
		return time.Time{}, false
	}
	return met, true
}

// Blocked is how long a gated service sat created but not started, waiting for
// its dependencies.
func (r *Run) Blocked(name string) (time.Duration, bool) {
	svc, ok := r.Services[name]
	if !ok {
		return 0, false
	}
	if _, gated := r.DependenciesMet(name); !gated {
		return 0, false
	}
	return measured(svc.Created, svc.Start)
}

// settled reports whether every service has reached an outcome.
func (r *Run) settled() bool {
	if !r.allStarted {
		return false
	}
	for _, svc := range r.Services {
		if svc.Outcome() == OutcomePending {
			return false
		}
	}
	return true
}

// service returns the named service's record, creating it on first mention.
// Nothing guarantees which event for a service arrives first, so no caller can
// assume the record already exists.
func (r *Run) service(name string) *Service {
	svc, ok := r.Services[name]
	if !ok {
		svc = &Service{Name: name}
		r.Services[name] = svc
	}
	return svc
}
