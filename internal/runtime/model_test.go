package runtime

import (
	"testing"
	"time"
)

func newTestRun(services ...*Service) *Run {
	run := &Run{Services: make(map[string]*Service), Graph: newGraph(), allStarted: true}
	for _, svc := range services {
		run.Services[svc.Name] = svc
	}
	return run
}

// A container that crashes fails every probe, so any notion of "our probe
// succeeded" is false for it. Deciding the run is over on that basis stops it
// waiting for the one service that is failing, and the crash goes unreported.
// Completion must key off what the container declares, not on whether probing
// worked.
func TestRunIsNotSettledWhileAHealthcheckedServiceIsUnresolved(t *testing.T) {
	run := newTestRun(&Service{
		Name:           "backend",
		Start:          time.Now(),
		HasHealthcheck: true,
	})

	if run.settled() {
		t.Fatal("settled with a healthchecked service that never reached an outcome")
	}

	// Dying is one of the three outcomes, and resolves the wait.
	run.Services["backend"].Finished = true
	run.Services["backend"].ExitCode = 1
	if !run.settled() {
		t.Error("not settled after the service exited")
	}
}

func TestSettledOutcomes(t *testing.T) {
	started := time.Now()

	tests := []struct {
		name string
		svc  Service
		want bool
	}{
		{"healthchecked and healthy", Service{Start: started, HasHealthcheck: true, DeclaredHealthy: started}, true},
		{"healthchecked and unhealthy", Service{Start: started, HasHealthcheck: true, DeclaredUnhealthy: started}, true},
		{"healthchecked and exited", Service{Start: started, HasHealthcheck: true, Finished: true}, true},
		{"healthchecked, still probing", Service{Start: started, HasHealthcheck: true}, false},
		{"no healthcheck, started", Service{Start: started}, true},
		{"not yet inspected", Service{Start: started, probePending: true}, false},
		// Created but held behind a dependency: it has not started, so it
		// cannot have finished starting.
		{"created, not started", Service{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := tt.svc
			svc.Name = "svc"
			if got := newTestRun(&svc).settled(); got != tt.want {
				t.Errorf("settled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Until compose returns, a gated service has not started and is absent from
// the map, so any judgement about the run being over is premature.
func TestRunIsNotSettledBeforeComposeReturns(t *testing.T) {
	run := newTestRun(&Service{Name: "db", DeclaredHealthy: time.Now(), HasHealthcheck: true})
	run.allStarted = false

	if run.settled() {
		t.Error("settled before compose returned")
	}
}

// Zero is a legitimate measurement, so an unobserved span must be
// distinguishable from an instant one.
func TestUnobservedSpansAreNotZero(t *testing.T) {
	var svc Service
	if _, ok := svc.Gap(); ok {
		t.Error("Gap reported a value for a service with no timestamps")
	}

	svc.PredicateTrue = time.Now()
	if _, ok := svc.Gap(); ok {
		t.Error("Gap reported a value with only one end observed")
	}
}

func TestDependenciesMetTakesTheSlowest(t *testing.T) {
	base := time.Now()
	run := newTestRun(
		&Service{Name: "fast", DeclaredHealthy: base.Add(1 * time.Second)},
		&Service{Name: "slow", DeclaredHealthy: base.Add(5 * time.Second)},
		&Service{Name: "app"},
	)
	run.Graph.add("app", []Dependency{
		{Service: "fast", Condition: ConditionHealthy},
		{Service: "slow", Condition: ConditionHealthy},
	})

	met, ok := run.DependenciesMet("app")
	if !ok {
		t.Fatal("DependenciesMet reported nothing")
	}
	if !met.Equal(base.Add(5 * time.Second)) {
		t.Errorf("gate opened at %v, want the slower dependency at %v", met, base.Add(5*time.Second))
	}
}

// A dependency whose condition was never met leaves the answer unknown. A
// partial maximum would be indistinguishable from a fast start.
func TestDependenciesMetIsUnknownWhenAConditionWasNeverSeen(t *testing.T) {
	run := newTestRun(
		&Service{Name: "seed"},
		&Service{Name: "app"},
	)
	run.Graph.add("app", []Dependency{{Service: "seed", Condition: ConditionCompleted}})

	if _, ok := run.DependenciesMet("app"); ok {
		t.Error("reported a gate time for a dependency that never completed")
	}
}
