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
    ./tmp/clockdiff check {{ file }} || status=$?
    [[ $status -le 1 ]] || exit "$status"

fmt:
    gofmt -w .

# Verify formatting and run go vet
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    # gofmt -l exits 0 whether or not it lists anything, so the emptiness of
    # its output is the assertion.
    unformatted=$(gofmt -l .)
    if [[ -n "$unformatted" ]]; then
        echo "not gofmt'd:" >&2
        echo "$unformatted" >&2
        exit 1
    fi
    go vet ./...

# What CI would run, once there is any
ci: lint test

clean:
    rm -rf tmp
