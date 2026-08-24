#!/usr/bin/env bash
# Runs what .github/workflows/ci.yml runs, with the versions it pins.
#
# The point is the versions. This machine may carry a newer Go than the
# pipeline, and a newer Go both hides failures (a module graph resolves
# from a warm cache) and invents them (golangci-lint is built against a
# specific Go and refuses to parse files a newer one accepts). Running
# `go test` with whatever is on PATH proves nothing about the pipeline.
set -uo pipefail

export GOTOOLCHAIN=go1.25.13
CFG="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.golangci.yml"
ROOT="$(dirname "$CFG")"
cd "$ROOT" || exit 1

TIDY_MODULES=". providers/anthropic providers/bedrock providers/google providers/openai providerset memory/sqlite memory/pgvector memory/qdrant memory/okf examples"
LINT_MODULES=". cmd/galdor providers/anthropic providers/bedrock providers/google providers/openai providerset memory/sqlite memory/pgvector memory/qdrant memory/okf"

failed=0
step() { printf '\n\033[1m== %s\033[0m\n' "$1"; }
check() { if [ "$1" -ne 0 ]; then echo "FAILED: $2"; failed=1; fi; }

step "go version"
go version

step "tidy (each module)"
# CI compares against HEAD because it runs on a clean checkout. Here the tree
# usually carries the edit being tested, so the comparison is against a
# snapshot taken just before tidy — otherwise every uncommitted change reads
# as "go.mod is out of date" and the real signal is lost in the noise.
SNAP="$(mktemp -d)"
trap 'rm -rf "$SNAP"' EXIT
for mod in $TIDY_MODULES; do
  mkdir -p "$SNAP/$mod"
  cp "$mod/go.mod" "$SNAP/$mod/go.mod"
  [ -f "$mod/go.sum" ] && cp "$mod/go.sum" "$SNAP/$mod/go.sum"
done
for mod in $TIDY_MODULES; do
  ( cd "$mod" && go mod tidy )
  for f in go.mod go.sum; do
    [ -f "$mod/$f" ] || continue
    if ! diff -q "$SNAP/$mod/$f" "$mod/$f" >/dev/null 2>&1; then
      echo "$mod/$f is out of date; run 'go mod tidy'"
      diff -u "$SNAP/$mod/$f" "$mod/$f" || true
      failed=1
    fi
  done
done

step "build (workspace)";  go build ./...; check $? build
step "vet (workspace)";    go vet ./...;   check $? vet
step "test -race (workspace)"; go test -race ./...; check $? test

step "golangci-lint (each module)"
for mod in $LINT_MODULES; do
  printf '%-24s' "$mod"
  ( cd "$mod" && golangci-lint run --config="$CFG" ./... ) || failed=1
done

# gosec and govulncheck need their binaries; skipped rather than faked
# when they are not installed, and said so out loud.
if command -v gosec >/dev/null; then
  step "gosec (each module)"
  for mod in $LINT_MODULES; do ( cd "$mod" && gosec -quiet ./... ) || failed=1; done
else
  echo "gosec not installed — that job is NOT covered by this run"
fi
if command -v govulncheck >/dev/null; then
  step "govulncheck (each module)"
  for mod in $LINT_MODULES; do ( cd "$mod" && govulncheck ./... >/dev/null ) || failed=1; done
else
  echo "govulncheck not installed — that job is NOT covered by this run"
fi

printf '\n'
if [ "$failed" -eq 0 ]; then echo "green"; else echo "RED"; fi
exit "$failed"
