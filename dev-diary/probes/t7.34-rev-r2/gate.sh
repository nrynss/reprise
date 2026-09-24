#!/usr/bin/env bash
# Runs every gate step after the media pin check, one by one, and prints each exit code.
cd "$1" || exit 99
r() { local name="$1"; shift; "$@" >"/tmp/gate-$name.log" 2>&1; echo "$name exit=$?"; }
unformatted=$(gofmt -l cmd); echo "gofmt exit=$? out=[$unformatted]"
GO_PKGS=$(go list ./... | grep -v '^github.com/nrynss/reprise/web/'); echo "go list exit=$?"
r govet go vet $GO_PKGS
r staticcheck go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 $GO_PKGS
r gotestrace go test -race $GO_PKGS
cd web
r sync npx svelte-kit sync
r check npm run check
r lint npm run lint
r vitest npm run test
r build npm run build
r e2e npm run test:e2e
cd ..
leak=$(git grep -InE 'export let|\$:|svelte/store|\b(writable|readable|derived|get)\s*\(' -- 'web/src'); echo "leak grep exit=$? out=[$leak]"
names=$(git grep -InEi 'hackathon|lablab' -- ':!tools/check.sh' ':!AGENTS.md' ':!product.md' ':!dev-diary'); echo "names grep exit=$? out=[$names]"
refs=$(git grep -InE '\bT[0-9]+\.[0-9]+[a-z]?\b|\bP[0-9]+\b|AGENTS\.md|PLAN\.md|project\.md|product\.md|libraries\.md|handoff\.md|PHASE-|adversarial-review|§' \
	-- ':!.gitignore' ':!tools/check.sh' ':!web/package-lock.json' \
	':!AGENTS.md' ':!product.md' ':!dev-diary'); echo "refs grep exit=$? out=[$refs]"
r gotestall go test -race ./...
