#!/usr/bin/env bash
# Review probe. Each mutation removes one piece of the boot wiring in
# cmd/reprise/main.go. The suite must fail under every one of them. It
# exits 1 when a mutation survives, and restores main.go after each.
# Run from the root of the reviewed worktree.
set -u
survived=0
mutate() {
	local label=$1 expr=$2
	perl -0pi -e "$expr" cmd/reprise/main.go
	if git diff --quiet cmd/reprise/main.go; then
		echo "SKIP $label: pattern not found"
		return
	fi
	if go test -count=1 ./cmd/reprise/ >/dev/null 2>&1; then
		echo "SURVIVED $label"
		survived=1
	else
		echo "killed $label"
	fi
	git checkout -q cmd/reprise/main.go
}
mutate "boot skips the feature mount hooks" 's/if err := mountFeatures\(ctx, feats, routeWiring\{/if err := func(context.Context, []feature, routeWiring) error { return nil }(ctx, feats, routeWiring{/'
mutate "boot drops the feature kinds" 's/sessionBroker, diary, extra\)/sessionBroker, diary, nil)/; s/extra, err := featureKinds/_, err = featureKinds/'
mutate "boot skips the analysis migration" 's/if err := analysis.Migrate\(ctx, db\); err != nil \{/if err := error(nil); err != nil {/'
mutate "boot never runs the advance loop" 's/\tgo jobs.runAdvanceLoop\(ctx\)\n//'
exit $survived
