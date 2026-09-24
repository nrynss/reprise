#!/usr/bin/env bash
# Reruns every prior pin for the editor draft lookup, applies each mutation, and reverts it.
# Usage: bash residue.sh <worktree root>. Needs npm ci already run in web/.
cd "$1/web" || exit 99
P=/Users/narayan/reprise/dev-diary/probes
F=src/lib/editor/draft.ts
cp $P/t7.34-rev-r1/draft.prototype.test.ts $P/t7.34-rev-r2/draft.stored-keys.test.ts $P/t7.34-rev-r2/draft.lookup-type.ts src/lib/editor/
run() {
	npx vitest run src/lib/editor/draft.prototype.test.ts src/lib/editor/draft.stored-keys.test.ts 2>&1 | grep -E '×|Tests '
	npx tsc --noEmit -p tsconfig.json >/dev/null 2>&1; echo "tsc exit=$?"
}
echo "== as handed"; run
echo "== mutation: helper returns {}"
sed -i '' 's/return Object.create(null) as CutProposals;/return {} as CutProposals;/' $F; run; git checkout -q $F
echo "== mutation: Record<string, string>"
sed -i '' 's/type CutProposals = Record<string, string | undefined>;/type CutProposals = Record<string, string>;/' $F; run; git checkout -q $F
echo "== reverted"; run
rm src/lib/editor/draft.prototype.test.ts src/lib/editor/draft.stored-keys.test.ts src/lib/editor/draft.lookup-type.ts
cd ..
for h in 13248a4 ef473d8 HEAD; do python3 $P/t7.34-rev-r2/sentence-length.py "$PWD" $h; echo "sentences $h exit=$?"; done
OWNS=$(grep -A6 '^### T7.34' /Users/narayan/reprise/dev-diary/PHASE-7-production-issues.md | sed -n 's/^owns: *//p' | tr ',' '\n' | sed 's/^ *//')
git diff --name-only 68039d0..HEAD | grep -vxF "$OWNS"; echo "owns pin exit=$?"
git diff --name-only 68039d0..HEAD | grep -vxF "$(echo "$OWNS" | grep -v prototype-keys)"; echo "owns mutation exit=$?"
git status --short
