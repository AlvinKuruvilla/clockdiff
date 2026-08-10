package report

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AlvinKuruvilla/clockdiff/internal/runtime"
)

func TestWriteJSON(t *testing.T) {
	start := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

	run := &runtime.Run{
		Project: "demo",
		T0:      start,
		Services: map[string]*runtime.Service{
			"db": {
				Name:            "db",
				Created:         start.Add(100 * time.Millisecond),
				Start:           start.Add(300 * time.Millisecond),
				PredicateTrue:   start.Add(400 * time.Millisecond),
				DeclaredHealthy: start.Add(5300 * time.Millisecond),
				HasHealthcheck:  true,
			},
			"api": {
				Name:  "api",
				Start: start.Add(6 * time.Second),
			},
		},
		AlreadyRunning: []string{"cache"},
	}
	run.SetDependsOn("api", []runtime.Dependency{
		{Service: "db", Condition: runtime.ConditionHealthy},
	})

	var out strings.Builder
	if err := WriteJSON(&out, run); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	var doc Document
	if err := json.Unmarshal([]byte(out.String()), &doc); err != nil {
		t.Fatalf("the output does not round-trip: %v", err)
	}

	if doc.Version != FormatVersion {
		t.Errorf("version = %d, want %d", doc.Version, FormatVersion)
	}
	// Sorted, so a run file diffs against another run of the same stack.
	if len(doc.Services) != 2 || doc.Services[0].Name != "api" {
		t.Fatalf("services = %+v, want api first", doc.Services)
	}

	api, db := doc.Services[0], doc.Services[1]

	if db.Outcome != "healthy" {
		t.Errorf("db outcome = %q, want healthy", db.Outcome)
	}
	if api.Outcome != "no-readiness" {
		t.Errorf("api outcome = %q, want no-readiness", api.Outcome)
	}
	if len(api.DependsOn) != 1 || api.DependsOn[0].Service != "db" {
		t.Errorf("api dependsOn = %+v", api.DependsOn)
	}
	if !doc.StartedAt.Equal(start) {
		t.Errorf("startedAt = %v, want %v", doc.StartedAt, start)
	}
}

// A moment that was never observed and one at the zero time are different
// facts. Marshalling a bare time.Time would write both as year 1.
func TestUnobservedMomentsAreAbsent(t *testing.T) {
	// StartedAt is not optional — a run with no beginning is not a run — so
	// it is supplied here rather than left to write a year-1 timestamp.
	run := &runtime.Run{
		Project:  "demo",
		T0:       time.Now(),
		Services: map[string]*runtime.Service{"api": {Name: "api", Start: time.Now()}},
	}

	var out strings.Builder
	if err := WriteJSON(&out, run); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	got := out.String()
	for _, absent := range []string{"0001-01-01", "predicateTrue", "declaredHealthy", "exitCode"} {
		if strings.Contains(got, absent) {
			t.Errorf("unobserved field %q was written:\n%s", absent, got)
		}
	}
}

// Precision is the reason the file exists rather than a screenshot of the
// report: the text rounds to 10ms, a viewer that zooms needs what was recorded.
func TestTimestampsAreNotRounded(t *testing.T) {
	start := time.Date(2026, 8, 10, 9, 0, 0, 123456789, time.UTC)
	run := &runtime.Run{
		Project:  "demo",
		T0:       start,
		Services: map[string]*runtime.Service{"api": {Name: "api", Start: start}},
	}

	var out strings.Builder
	if err := WriteJSON(&out, run); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	if !strings.Contains(out.String(), ".123456789Z") {
		t.Errorf("nanoseconds were lost:\n%s", out.String())
	}
}

// Escape sequences in a run file are corruption, not decoration: the output is
// meant to be piped into jq, a file, or a viewer. Colour is granted only to a
// terminal, and withdrawn on request.
func TestColorIsWithheldFromAnythingButATerminal(t *testing.T) {
	if wantsColor(&strings.Builder{}) {
		t.Error("coloured a plain writer")
	}

	pipe, _, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	if wantsColor(pipe) {
		t.Error("coloured a pipe, which is a file but not a terminal")
	}

	t.Setenv("NO_COLOR", "1")
	if wantsColor(os.Stdout) {
		t.Error("coloured despite NO_COLOR")
	}
}
