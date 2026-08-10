package report

import (
	"strings"
	"testing"
	"time"

	"github.com/AlvinKuruvilla/clockdiff/internal/runtime"
)

// A stack that is already up produces no events, so the run comes back empty.
// Printing nothing then reads as a stack with no startup cost at all.
func TestProfileNamesEveryServiceItCouldNotMeasure(t *testing.T) {
	var out strings.Builder
	WriteProfile(&out, &runtime.Run{
		Project:        "precogly",
		Services:       map[string]*runtime.Service{},
		AlreadyRunning: []string{"db", "backend"},
	})

	got := out.String()
	for _, want := range []string{
		"db       already running, not measured",
		"backend  already running, not measured",
		"docker compose -p precogly down",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("empty run missing %q:\n%s", want, got)
		}
	}
}

// A service compose left alone must not be confused with one that started
// instantly, so a partial run says which rows are missing and why.
func TestProfileFlagsPartialRuns(t *testing.T) {
	var out strings.Builder
	WriteProfile(&out, &runtime.Run{
		Project: "precogly",
		Services: map[string]*runtime.Service{
			"backend": {Name: "backend", Start: time.Now()},
		},
		AlreadyRunning: []string{"backend", "db"},
	})

	got := out.String()
	if strings.Contains(got, "backend  already running") {
		t.Errorf("a measured service was reported as unmeasured:\n%s", got)
	}
	if !strings.Contains(got, "db       already running, not measured") {
		t.Errorf("the unmeasured service went unmentioned:\n%s", got)
	}
}
