#!/usr/bin/env bash
# The gate. Every check runs, in order, and the first failure names itself and
# stops the run. Nothing here is skipped silently.
#
# Scan patterns, and why they exist:
#
#   svelte 4 leakage: "export let", a "$:" reactive statement, and store-driven
#   reactivity ("svelte/store", writable, readable, derived, get(...)) compile
#   clean under Svelte 5 and then never react. Runes only, mechanically.
#
#   consumer names: "hackathon" and "lablab" name where the idea came from.
#   They belong in no tracked file of the product.
#
#   plan references: task ids (T2.1), phase ids (P0), planning file names
#   (AGENTS.md, PLAN.md, project.md, product.md, libraries.md, handoff.md,
#   PHASE-*, the review area), section marks and numbered invariants. Code and
#   comments state their own reasons and never cite the plan. The .gitignore is
#   exempt: it must name what it keeps out of the repository. This script and
#   the lockfiles are exempt for the same reason.
#
# Scans run over tracked files only, through git grep, so build output and
# tool caches never trip them.

set -euo pipefail
cd "$(dirname "$0")/.."

fail() {
	echo "FAIL [$1] $2" >&2
	exit 1
}

# Tools the later checks and phases measure with must be present.
for tool in ffmpeg ffprobe go node npm; do
	command -v "$tool" >/dev/null 2>&1 || fail "tools" "$tool is not on PATH"
done

echo "-- gofmt"
unformatted=$(gofmt -l cmd)
if [ -n "$unformatted" ]; then
	fail "gofmt" "not gofmt-clean: $unformatted"
fi

# Go tooling must not walk web/node_modules, which carries third-party Go
# snippets that are not this project's code.
GO_PKGS=$(go list ./... | grep -v '^github.com/nrynss/reprise/web/')

echo "-- go vet"
go vet $GO_PKGS || fail "go vet" "go vet reported problems"

echo "-- staticcheck"
go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 $GO_PKGS || fail "staticcheck" "staticcheck reported problems"

echo "-- go test -race"
go test -race $GO_PKGS || fail "go test -race" "tests failed"

cd web

echo "-- svelte-check"
npm run check >/dev/null || fail "svelte-check" "svelte-check reported problems"

echo "-- eslint"
npm run lint >/dev/null || fail "eslint" "eslint reported problems"

echo "-- vitest"
npm run test >/dev/null || fail "vitest" "unit tests failed"

echo "-- playwright"
npm run test:e2e >/dev/null || fail "playwright" "end-to-end tests failed"

cd ..

echo "-- svelte 4 leakage"
leak=$(git grep -InE 'export let|\$:|svelte/store|\b(writable|readable|derived|get)\s*\(' -- 'web/src' || true)
if [ -n "$leak" ]; then
	fail "svelte 4 leakage" "runes-only violation:
$leak"
fi

echo "-- consumer names"
names=$(git grep -InEi 'hackathon|lablab' -- ':!tools/check.sh' || true)
if [ -n "$names" ]; then
	fail "consumer names" "provider or consumer names in tracked files:
$names"
fi

echo "-- plan references"
refs=$(git grep -InE '\bT[0-9]+\.[0-9]+[a-z]?\b|\bP[0-9]+\b|AGENTS\.md|PLAN\.md|project\.md|product\.md|libraries\.md|handoff\.md|PHASE-|adversarial-review|§' \
	-- ':!.gitignore' ':!tools/check.sh' ':!web/package-lock.json' || true)
if [ -n "$refs" ]; then
	fail "plan references" "tracked files cite the plan:
$refs"
fi

echo "OK all checks passed"
