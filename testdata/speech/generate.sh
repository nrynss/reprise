#!/usr/bin/env bash
# Make the voice probe clips from fixed text with pinned tools.
# Synthesis runs in a pinned Ubuntu image with a pinned espeak-ng package.
# Conversion runs in the pinned media image. One run writes all four files
# beside this script with identical bytes on every run.
#
# Pins live in the two image variables below, with the espeak-ng version in
# the install step. The Ubuntu digest fixes the base system. The package
# version fixes the synthesizer. The media digest fixes the resampler, and
# it matches the digest the image build and the gate pin.
#
# Synthesis writes wav files directly with -w. The flag lets the tool seek
# back and patch the header sizes. Piping standard output into a file leaves
# placeholder sizes in place, which readers flag as corrupt.
#
# Conversion resamples each wav to 24000 Hz mono 16 bit little endian PCM.
# The probe streams that exact shape, so the script builds it beside the wav.
set -euo pipefail
cd "$(dirname "$0")"
OUT="$(pwd)"
TTS_IMG="${TTS_IMAGE:-ubuntu:24.04@sha256:b3cc40b72b93588182b5410f723c7aaf142363311c2aa993d8a453ddcbb3ae15}"
MEDIA_IMG="${MEDIA_IMAGE:-mwader/static-ffmpeg:9.0.1@sha256:54e55b0cb8f672870fc38ceb2e6c411855cb3b39c505f5f3b2505ee01ed5f2b7}"
ESPEAK_VERSION="1.51+dfsg-12build1"
docker run --rm -v "$OUT:/out" -e ESPEAK_VERSION="$ESPEAK_VERSION" "$TTS_IMG" bash -c '
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -qy "espeak-ng=${ESPEAK_VERSION}" > /dev/null
espeak-ng -v en-us -s 160 -w /out/exchange.wav "Hello world, this is a test of the voice agent probe."
espeak-ng -v en-us -s 160 -w /out/keyterm.wav "My friend Zaffranil vistrex malquenar and I went hiking."
'
docker run --rm -v "$OUT:/work" "$MEDIA_IMG" -hide_banner -y -i /work/exchange.wav -ar 24000 -ac 1 -f s16le -acodec pcm_s16le /work/exchange-24k.pcm
docker run --rm -v "$OUT:/work" "$MEDIA_IMG" -hide_banner -y -i /work/keyterm.wav -ar 24000 -ac 1 -f s16le -acodec pcm_s16le /work/keyterm-24k.pcm
ls -la exchange.wav keyterm.wav exchange-24k.pcm keyterm-24k.pcm
