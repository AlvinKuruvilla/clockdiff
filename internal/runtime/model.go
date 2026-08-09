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

	// Exited is the container's die event, which is what
	// service_completed_successfully waits for.
	Exited time.Time

	// PredicateTrue is when clockdiff's own run of the healthcheck command
	// first succeeded — when the service actually became ready.
	PredicateTrue time.Time

	// DeclaredHealthy is when the daemon said so.
	DeclaredHealthy time.Time

	// Probeable records whether the container declares a healthcheck this
	// tool can run. Services without one have no readiness to measure.
	Probeable bool

	// probePending is set between a container starting and its prober
	// reporting. Without it a service that has only just started looks
	// finished, because Probeable is still false.
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

// Run is one observed `docker compose up`.
type Run struct {
	Project  string
	T0       time.Time
	Services map[string]*Service
	Graph    *Graph

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

// settled reports whether every service has been measured as far as it can be.
func (r *Run) settled() bool {
	if !r.allStarted {
		return false
	}
	for _, svc := range r.Services {
		if svc.probePending {
			return false
		}
		if svc.Probeable && (svc.PredicateTrue.IsZero() || svc.DeclaredHealthy.IsZero()) {
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
