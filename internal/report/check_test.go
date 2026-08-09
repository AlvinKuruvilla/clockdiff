package report

import (
	"strings"
	"testing"
	"time"

	"github.com/AlvinKuruvilla/clockdiff/internal/compose"
)

// A clean file must still produce output. Printing nothing is the behaviour
// that made `check` look broken on the stack it was written for.
func TestReportIsNeverSilent(t *testing.T) {
	var out strings.Builder
	WriteCheck(&out, "compose.yml", nil, compose.Summary{Services: 4, Healthchecked: 1})

	got := out.String()
	for _, want := range []string{
		"compose.yml",
		"no inert start_intervals",
		"4 services, 1 with a healthcheck",
		"needs a measured run",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("clean report missing %q:\n%s", want, got)
		}
	}
}

func TestUndecidedGrammar(t *testing.T) {
	tests := []struct {
		name    string
		summary compose.Summary
		want    string
	}{
		{"one of each", compose.Summary{Services: 1, Healthchecked: 1}, "1 service, 1 with a healthcheck. Whether that probe interval wastes"},
		{"one healthchecked", compose.Summary{Services: 4, Healthchecked: 1}, "4 services, 1 with a healthcheck. Whether that probe interval wastes"},
		{"many healthchecked", compose.Summary{Services: 4, Healthchecked: 3}, "4 services, 3 with a healthcheck. Whether those probe intervals waste"},
		{"none healthchecked", compose.Summary{Services: 4, Healthchecked: 0}, "4 services, none with a healthcheck"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := undecided(tt.summary); !strings.Contains(got, tt.want) {
				t.Errorf("got %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

// The two causes read differently because they are fixed differently: one key
// is missing, the other is present and useless.
func TestFindingDistinguishesAbsentFromZero(t *testing.T) {
	var out strings.Builder
	writeFindings(&out, []compose.Finding{
		{Service: "cache", StartInterval: 250 * time.Millisecond, StartPeriodSet: false},
		{Service: "postgres", StartInterval: 250 * time.Millisecond, StartPeriodSet: true},
	})

	got := out.String()
	for _, want := range []string{
		"start_period is absent",
		"start_period is 0s",
		"2 inert start_intervals found",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}
