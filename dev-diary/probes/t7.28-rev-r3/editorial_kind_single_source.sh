#!/usr/bin/env bash
# Review probe. The binary must start the editorial pass under the one
# kind name the episode detail reads back. It exits 1 while the binary
# still spells that kind as its own string literal.
# Run from the worktree root.
set -u
if grep -nE '^\s*kindEditorial\s*=\s*"' cmd/reprise/main.go; then
	echo "cmd/reprise/main.go names the editorial kind as its own literal, not episode.EditorialKind"
	exit 1
fi
grep -qE '^\s*kindEditorial\s*=\s*episode\.EditorialKind' cmd/reprise/main.go || {
	echo "kindEditorial does not read episode.EditorialKind"
	exit 1
}
echo "one source for the editorial kind"
