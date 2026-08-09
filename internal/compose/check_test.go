package compose

import (
	"os"
	"path/filepath"
	"testing"
)

// corpusDir returns the validation corpus, or skips. The corpus is 254 real
// compose files fetched from GitHub code search; it is gitignored and lives in
// another repository.
func corpusDir(t *testing.T) string {
	t.Helper()

	dir := os.Getenv("CLOCKDIFF_CORPUS")
	if dir == "" {
		t.Skip("CLOCKDIFF_CORPUS unset; the corpus lives outside this repository")
	}
	// The justfile sets the variable unconditionally, so a checkout without
	// the corpus alongside it must skip rather than fail on ReadDir.
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("corpus not present at %s", dir)
	}
	return dir
}

// The corpus has a known answer, fixed by the population scan that preceded
// this tool: 17 services across 254 repos set start_interval, 13 of them
// inertly, and all 13 sit in one project.
//
// Only three corpus files mention start_interval. Two load;
// Rahona-Hosting__secrets.yml has a `labels:` block whose every entry is
// commented out, so it parses as null and fails validation — Compose rejects
// it too. Its one usage is a correct one, so it cannot move this count.
func TestCorpusInertStartIntervals(t *testing.T) {
	dir := corpusDir(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}

	findingsByFile := make(map[string]int)
	var loaded, failed int

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		project, err := LoadProject(filepath.Join(dir, entry.Name()))
		if err != nil {
			failed++
			continue
		}
		loaded++

		if n := len(Check(project)); n > 0 {
			findingsByFile[entry.Name()] = n
		}
	}

	// A loading regression shrinks the population the count is drawn from,
	// which presents as the check going quiet rather than as a failure.
	t.Logf("loaded %d of %d corpus files, %d failed", loaded, loaded+failed, failed)
	if loaded < 185 {
		t.Errorf("loaded %d corpus files, expected at least 185", loaded)
	}

	total := 0
	for _, n := range findingsByFile {
		total += n
	}
	if total != 13 {
		t.Errorf("total inert start_intervals = %d, want 13; by file: %v", total, findingsByFile)
	}
	if n := findingsByFile["Synapsecom__coolblock-panel.yml"]; n != 13 {
		t.Errorf("Synapsecom__coolblock-panel.yml has %d findings, want 13", n)
	}
	if len(findingsByFile) != 1 {
		t.Errorf("findings spread across %d files, want 1: %v", len(findingsByFile), findingsByFile)
	}
}

// qnimbus__bunq.yml is the negative case: three services that set both keys
// correctly. Without it, a check that flagged every start_interval would still
// be caught by the total, but would report the wrong reason.
func TestCorpusCorrectStartIntervalsAreNotFlagged(t *testing.T) {
	dir := corpusDir(t)

	project, err := LoadProject(filepath.Join(dir, "qnimbus__bunq.yml"))
	if err != nil {
		t.Fatalf("load qnimbus__bunq.yml: %v", err)
	}

	var withStartInterval int
	for _, service := range project.Services {
		if service.HealthCheck != nil && service.HealthCheck.StartInterval != nil {
			withStartInterval++
		}
	}
	if withStartInterval != 3 {
		t.Errorf("%d services set start_interval, want 3", withStartInterval)
	}

	if findings := Check(project); len(findings) != 0 {
		t.Errorf("flagged %d correctly-configured services: %v", len(findings), findings)
	}
}
