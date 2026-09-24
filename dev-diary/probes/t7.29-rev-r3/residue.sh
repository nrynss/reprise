#!/usr/bin/env bash
# Review probe. Applies each round 1 and round 2 mutation in turn, runs
# its pin, and restores the tree. Every pin must fail under its mutation.
# Needs the round 1 and round 2 probe files copied into cmd/reprise/.
# Run from the root of the reviewed worktree. Exits 1 when a pin survives.
set -u
survived=0
check() {
	local label=$1 pkg=$2 run=$3
	if git diff --quiet -- cmd internal; then
		echo "SKIP $label: pattern not found"
		survived=1
		return
	fi
	if go test -count=1 -run "$run" "$pkg" >/dev/null 2>&1; then
		echo "SURVIVED $label"
		survived=1
	else
		echo "killed $label"
	fi
	git checkout -q -- cmd internal
}
perl -0pi -e 's/\tj\.startWaitingRenders\(ctx\)\n//' cmd/reprise/finish.go
check "r1.1 advance never starts waiting renders" ./cmd/reprise/ '^(TestEveryStuckRenderShipsOneAtATime|TestProbeRecoverRendersEveryStuckEpisode)$'
perl -0pi -e 's/episode_id TEXT NOT NULL PRIMARY KEY/owner_id TEXT NOT NULL REFERENCES users (id),\n    episode_id TEXT NOT NULL PRIMARY KEY/' internal/analysis/migrations/0001_rendered_source.sql
perl -0pi -e 's/INSERT INTO rendered_sources \(episode_id, render_id\) VALUES \(\?, \?\)/INSERT INTO rendered_sources (owner_id, episode_id, render_id) VALUES (?, ?, ?)/; s/\t\tepisodeID, renderID\); err != nil \{/\t\townerID, episodeID, renderID); err != nil {/' internal/analysis/store.go
check "r1.2 link row names the guest again" ./cmd/reprise/ '^TestKeptEpisodeLeavesTheGuestDeletable$'
perl -0pi -e 's/(func \(j \*jobs\) forgetSettled\(episodeID string\) \{\n)/$1\treturn\n/' cmd/reprise/finish.go
check "r1.4 settled map never drains" ./cmd/reprise/ '^(TestSettledOutcomesDrainOnShip|TestProbeSettledMapDrains)$'
perl -0pi -e 's/\tif err != nil && s\.waitWhenBusy && errors\.Is\(err, job\.ErrLimit\) \{\n\t\treturn "", nil\n\t\}\n//' internal/episode/done.go
check "r2.1 busy slot fails the episode" ./cmd/reprise/ '^(TestMarkDoneWhileRenderBusyWaitsAndShips|TestProbeMarkDoneWhileRenderBusyStillShips)$'
exit $survived
