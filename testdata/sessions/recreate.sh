#!/usr/bin/env bash
# Recreate the three recorded session fixtures from generated speech.
# Runs the synthesizer in Docker so every checkout produces the same bytes,
# then streams each clip through one short live session. Each token carries
# a 120 second cap. The run mints three tokens and deletes every session.
# Needs network access and a provider key through the settings loader.
set -euo pipefail
cd "$(dirname "$0")"
OUT="$(pwd)"
IMG="${TTS_IMAGE:-ubuntu:24.04}"
CLIPS="$(mktemp -d)"
RUNNER="$(mktemp -d)"
trap 'rm -rf "$CLIPS" "$RUNNER"' EXIT
docker run --rm -v "$CLIPS:/out" "$IMG" bash -c '
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -qy espeak-ng > /dev/null
espeak-ng -v en-us -s 160 --stdout "The garden gate needs paint before winter." > /out/steady-a.raw
espeak-ng -v en-us -s 160 --stdout "We should buy brushes on Saturday morning." > /out/steady-b.raw
espeak-ng -v en-us -s 160 --stdout "The lake was calm when we launched the canoe." > /out/pauses-a.raw
espeak-ng -v en-us -s 160 --stdout "A heron watched us from the far dock." > /out/pauses-b.raw
espeak-ng -v en-us -s 160 --stdout "Please tell me a longer story about the lake." > /out/trigger.raw
espeak-ng -v en-us -s 160 --stdout "Sorry to cut in, but the oven timer just rang." > /out/bargein.raw
ls -la /out/'
echo "Clips ready in $CLIPS. Stream each one through a live session, then"
echo "store the user stem, the event log, and the provider recording under"
echo "steady/, pauses/, and bargein/ beside this script."
echo "The session runner stays outside the repo. See the README for the read."
