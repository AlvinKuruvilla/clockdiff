# clockdiff development recipes

# The validation corpus: 254 real compose files, gitignored and living in
# another repository. Tests skip when it is absent rather than failing.
# Override with: just --set corpus /path/to/corpus test
corpus := env("CLOCKDIFF_CORPUS", home_directory() / "Dev/oss/precogly/tmp/clockdiff/corpus")

_default:
    @just --list --unsorted

build:
    go build -o tmp/clockdiff .

# Run the tests, including the corpus acceptance check
test *args:
    CLOCKDIFF_CORPUS={{ corpus }} go test ./... {{ args }}

# Same, showing the load rate and per-file counts
test-verbose *args:
    #!/usr/bin/env bash
    set -euo pipefail
    # compose-go warns `version is obsolete` for most corpus files, which
    # drowns the two log lines worth reading.
    CLOCKDIFF_CORPUS={{ corpus }} go test -v ./... {{ args }} 2>&1 | grep -v 'level=warning'

# Run the static check against one compose file
check file: build
    #!/usr/bin/env bash
    set -euo pipefail
    # Findings exit 1 by design, which is not a recipe failure. A load error
    # or bad usage exits 2 and still propagates.
    status=0
    ./tmp/clockdiff check -f {{ file }} || status=$?
    [[ $status -le 1 ]] || exit "$status"

fmt:
    gofmt -w .

# Install the tools lint needs
tools:
    go install honnef.co/go/tools/cmd/staticcheck@latest

# Verify formatting, vet, and check for deprecated APIs
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    # tmp/ is gitignored throwaway probes; it is part of the module but must
    # not gate anything.
    targets=(. ./internal/...)
    # gofmt -l exits 0 whether or not it lists anything, so the emptiness of
    # its output is the assertion.
    unformatted=$(gofmt -l main.go internal)
    if [[ -n "$unformatted" ]]; then
        echo "not gofmt'd:" >&2
        echo "$unformatted" >&2
        exit 1
    fi
    go vet "${targets[@]}"
    # go vet does not report deprecated identifiers; staticcheck's SA1019 does.
    if ! command -v staticcheck >/dev/null 2>&1; then
        echo "staticcheck missing — run: just tools" >&2
        exit 1
    fi
    staticcheck "${targets[@]}"

# What CI would run, once there is any
ci: lint test

clean:
    rm -rf tmp
