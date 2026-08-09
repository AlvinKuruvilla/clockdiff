package compose

import (
	"maps"
	"slices"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
)

// Finding is one inert start_interval: a service that asks for tight probing
// during startup and does not get it.
type Finding struct {
	Service       string
	StartInterval time.Duration

	// StartPeriodSet distinguishes `start_period: 0s` from an absent key.
	// Both are inert; they differ only in the fix.
	StartPeriodSet bool
}

// Check reports services whose start_interval cannot take effect.
//
// Docker consults start_interval only while a container is inside its start
// period. With start_period absent or zero there is no such window, so probes
// fire at the full interval and the key changes nothing.
func Check(project *types.Project) []Finding {
	var findings []Finding

	for _, name := range slices.Sorted(maps.Keys(project.Services)) {
		health := project.Services[name].HealthCheck
		if health == nil || health.Disable {
			continue
		}
		if health.StartInterval == nil || *health.StartInterval == 0 {
			continue
		}
		if health.StartPeriod != nil && *health.StartPeriod > 0 {
			continue
		}

		findings = append(findings, Finding{
			Service:        name,
			StartInterval:  time.Duration(*health.StartInterval),
			StartPeriodSet: health.StartPeriod != nil,
		})
	}

	return findings
}

// Summary counts how much of a stack could show a quantization gap at all.
type Summary struct {
	Services      int
	Healthchecked int
}

// Summarize counts services carrying an active healthcheck. A service with no
// declared-healthy moment has no gap to measure.
func Summarize(project *types.Project) Summary {
	summary := Summary{Services: len(project.Services)}
	for _, service := range project.Services {
		if service.HealthCheck != nil && !service.HealthCheck.Disable {
			summary.Healthchecked++
		}
	}
	return summary
}
