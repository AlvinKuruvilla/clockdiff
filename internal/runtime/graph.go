package runtime

import "strings"

// Compose's depends_on conditions. A service may wait on several dependencies
// at once, each under its own condition.
const (
	ConditionHealthy   = "service_healthy"
	ConditionStarted   = "service_started"
	ConditionCompleted = "service_completed_successfully"
)

// Dependency is one depends_on edge.
type Dependency struct {
	Service   string
	Condition string
}

// Graph is a run's depends_on topology.
//
// Both directions are kept. The forward edges answer "what was this service
// waiting for"; the reverse ones answer "whose start did this service's delay
// push out", which is the question a per-service table cannot show and the
// reason dead time is worth more than the row it appears on.
type Graph struct {
	out map[string][]Dependency
	in  map[string][]string
}

func newGraph() *Graph {
	return &Graph{
		out: make(map[string][]Dependency),
		in:  make(map[string][]string),
	}
}

// add records a service's dependencies and the reverse edges they imply.
//
// Adding the same service twice is a no-op. depends_on is baked into a
// container's labels when it is created, so a restart re-emits start with
// identical edges and there is nothing to reconcile.
func (g *Graph) add(service string, deps []Dependency) {
	if _, seen := g.out[service]; seen {
		return
	}
	g.out[service] = deps
	for _, dep := range deps {
		g.in[dep.Service] = append(g.in[dep.Service], service)
	}
}

// DependsOn returns what the service waited for. A nil graph has no edges,
// so a Run assembled without one renders as a stack with no dependencies
// rather than crashing the report.
func (g *Graph) DependsOn(service string) []Dependency {
	if g == nil {
		return nil
	}
	return g.out[service]
}

// Dependents returns the services that waited for this one.
func (g *Graph) Dependents(service string) []string {
	if g == nil {
		return nil
	}
	return g.in[service]
}

// parseDependsOn reads compose's depends_on label, which looks like
//
//	seed:service_completed_successfully:false,cache:service_healthy:false
//
// The third field is restart-on-change and is not used here. The order of
// entries does not follow the compose file and carries no meaning.
func parseDependsOn(label string) []Dependency {
	if label == "" {
		return nil
	}

	var deps []Dependency
	for _, entry := range strings.Split(label, ",") {
		parts := strings.Split(entry, ":")
		if len(parts) < 2 {
			continue
		}
		deps = append(deps, Dependency{Service: parts[0], Condition: parts[1]})
	}
	return deps
}
