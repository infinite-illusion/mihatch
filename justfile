# MiHatch task runner. Run `just` (no args) to list recipes.
# Requires Go 1.26+; targets produce a darwin/arm64 binary.

# default: list available recipes
default:
    @just --list

# gofmt the whole tree in place
fmt:
    gofmt -w .

# fail if any file is not gofmt-clean (read-only, for CI)
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    out="$(gofmt -l .)"
    if [ -n "$out" ]; then
        echo "$out"
        echo "files above are not gofmt-clean"
        exit 1
    fi

# go vet
vet:
    go vet ./...

# unit + integration tests
test:
    go test ./...

# tests with the race detector
test-race:
    go test -race ./...

# build the darwin/arm64 binary (override output: just build /path/mihatch)
build out="mihatch":
    GOOS=darwin GOARCH=arm64 go build -o {{out}} ./cmd/mihatch

# trimmed/stripped release build
build-release out="mihatch":
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o {{out}} ./cmd/mihatch

# everything CI runs: fmt + vet + test + race + build
check: fmt vet test test-race
    #!/usr/bin/env bash
    set -euo pipefail
    GOOS=darwin GOARCH=arm64 go build -o /tmp/mihatch-check ./cmd/mihatch
    echo "all checks passed"

# remove build artifacts and local .mihatch/ state
clean:
    rm -f mihatch
    rm -rf .mihatch
