#!/usr/bin/env bash
# Recreate the six guest clips with the pinned toolchain.
# Ubuntu 24.04 stays fixed by image digest.
# espeak-ng stays fixed at version 1.51.
# Seekable wav output patches every RIFF size.
# Live sessions need a provider key and spend money.
# This script writes only the offline clips. The README
# describes the stream and store step for events.json
# and timeline.json and metadata.json and recording.ogg.
set -euo pipefail
cd "$(dirname "$0")"
OUT="$(pwd)"
TTS_IMG="${TTS_IMAGE:-ubuntu:24.04@sha256:b3cc40b72b93588182b5410f723c7aaf142363311c2aa993d8a453ddcbb3ae15}"
ESPEAK_VERSION="1.51+dfsg-12build1"
docker run --rm -e "ESPEAK_VERSION=$ESPEAK_VERSION" -v "$OUT:/out" "$TTS_IMG" bash -c '
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -qy "espeak-ng=${ESPEAK_VERSION}" > /dev/null
espeak-ng -v en-us -s 160 -w /out/steady/user-a.wav "The garden gate needs paint before winter."
espeak-ng -v en-us -s 160 -w /out/steady/user-b.wav "We should buy brushes on Saturday morning."
espeak-ng -v en-us -s 160 -w /out/pauses/user-a.wav "The lake was calm when we launched the canoe."
espeak-ng -v en-us -s 160 -w /out/pauses/user-b.wav "A heron watched us from the far dock."
espeak-ng -v en-us -s 160 -w /out/bargein/user-trigger.wav "Please tell me a longer story about the lake."
espeak-ng -v en-us -s 160 -w /out/bargein/user-bargein.wav "Sorry to cut in, but the oven timer just rang."
ls -la /out/steady /out/pauses /out/bargein'
echo "Guest clips now sit beside this script."
echo "Stream each clip through a live session to record"
echo "events.json and timeline.json and metadata.json"
echo "and recording.ogg under steady/ and pauses/ and bargein/."
echo "That step needs network access and a provider key."
echo "Each run mints three tokens capped at 120 seconds"
echo "and deletes every session afterwards."
