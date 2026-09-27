#!/usr/bin/env bash
# Read-only: find the episode and stems behind one AssemblyAI session on the box,
# and copy the stems into runs/<sess_id>/. The box address comes from .env and is
# never printed.
#   pull_stems.sh <sess_id>
set -euo pipefail
sid="$1"
here="$(cd "$(dirname "$0")" && pwd)"
ip=$(grep '^ORIGIN_IP=' /home/nryn/work/reprise/.env | cut -d= -f2- | tr -d '"')
ssh_=(ssh -i "$HOME/.ssh/id_ed25519_hetzner" -o IdentitiesOnly=yes -o BatchMode=yes "root@$ip")
out="$here/runs/$sid"
mkdir -p "$out"

"${ssh_[@]}" python3 - "$sid" > "$out/box.txt" <<'EOF'
import sqlite3, sys
sid = sys.argv[1]
db = sqlite3.connect("file:/srv/reprise/data/reprise.db?mode=ro", uri=True)
row = db.execute("SELECT id, owner_id, episode_id, token_cap, connected_seconds FROM sessions "
                 "WHERE provider_session_id = ?", (sid,)).fetchone()
if not row:
    sys.exit(f"no session row for {sid}")
print(f"session id={row[0]} episode={row[2]} token_cap={row[3]} connected_seconds={row[4]}")
ep = db.execute("SELECT * FROM episodes WHERE id = ?", (row[2],)).fetchone()
cols = [c[1] for c in db.execute("PRAGMA table_info(episodes)")]
print("episode " + " ".join(f"{c}={v}" for c, v in zip(cols, ep) if c not in ("owner_id",)))
for media_id, role, rate, off in db.execute(
        "SELECT media_id, role, sample_rate, start_offset_ms FROM stems WHERE episode_id = ? "
        "ORDER BY rowid", (row[2],)):
    print(f"stem role={role} media={media_id} rate={rate} offset_ms={off}")
EOF
cat "$out/box.txt"

grep '^stem ' "$out/box.txt" | while read -r _ role media rate _; do
  role=${role#role=}; media=${media#media=}; rate=${rate#rate=}
  scp -q -i "$HOME/.ssh/id_ed25519_hetzner" -o IdentitiesOnly=yes -o BatchMode=yes \
    "root@$ip:/srv/reprise/media/$media" "$out/stem-$role.wav"
  echo "copied stem-$role.wav ($(stat -c %s "$out/stem-$role.wav") bytes)"
done
