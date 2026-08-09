package runtime

import (
	"reflect"
	"testing"
)

func TestParseDependsOn(t *testing.T) {
	tests := []struct {
		name  string
		label string
		want  []Dependency
	}{
		{"no dependencies", "", nil},
		{
			"one",
			"cache:service_healthy:false",
			[]Dependency{{Service: "cache", Condition: ConditionHealthy}},
		},
		{
			// Compose emits these in an order that follows neither the file
			// nor the alphabet, so nothing may be read into it.
			"fan-in",
			"seed:service_completed_successfully:false,cache:service_healthy:false",
			[]Dependency{
				{Service: "seed", Condition: ConditionCompleted},
				{Service: "cache", Condition: ConditionHealthy},
			},
		},
		{"entry with no condition is skipped", "cache", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseDependsOn(tt.label); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseDependsOn(%q) = %v, want %v", tt.label, got, tt.want)
			}
		})
	}
}

func TestGraphRecordsBothDirections(t *testing.T) {
	g := newGraph()
	g.add("app", []Dependency{{Service: "cache", Condition: ConditionHealthy}})

	if got := g.DependsOn("app"); len(got) != 1 || got[0].Service != "cache" {
		t.Errorf("DependsOn(app) = %v", got)
	}
	if got := g.Dependents("cache"); !reflect.DeepEqual(got, []string{"app"}) {
		t.Errorf("Dependents(cache) = %v, want [app]", got)
	}
}

// depends_on is baked into a container's labels at creation, so a restart
// re-emits start with identical edges. Adding twice must not double the
// reverse edge.
func TestGraphAddIsIdempotent(t *testing.T) {
	g := newGraph()
	deps := []Dependency{{Service: "cache", Condition: ConditionHealthy}}
	g.add("app", deps)
	g.add("app", deps)

	if got := g.Dependents("cache"); len(got) != 1 {
		t.Errorf("Dependents(cache) = %v, want one entry", got)
	}
}
