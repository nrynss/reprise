#!/usr/bin/env bash
# Review probe. settings.RenderConcurrency says it caps how many renders run
# at once, and both config files set it. Nothing outside the settings
# package reads it, so the render kind keeps the render package's fixed
# limit of one. Exits 1 while the setting is unread. Run from the root of
# the reviewed worktree.
set -u
readers=$(git grep -n 'RenderConcurrency' -- '*.go' ':!internal/settings/' ':!*_test.go')
if [ -z "$readers" ]; then
	echo "UNREAD: no code outside internal/settings reads RenderConcurrency"
	exit 1
fi
echo "read at: $readers"
