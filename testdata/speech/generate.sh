#!/usr/bin/env bash
# Generate the voice probe clips with espeak-ng 1.51 on Ubuntu 24.04.
# Runs the synthesizer in Docker so every checkout produces the same bytes.
# Writes exchange.wav and keyterm.wav next to this script.
set -euo pipefail
cd "$(dirname "$0")"
OUT="$(pwd)"
IMG="${TTS_IMAGE:-ubuntu:24.04}"
docker run --rm -v "$OUT:/out" "$IMG" bash -c '
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -qy espeak-ng > /dev/null
espeak-ng -v en-us -s 160 --stdout "Hello world, this is a test of the voice agent probe." > /out/exchange.raw
espeak-ng -v en-us -s 160 --stdout "My friend Zaffranil vistrex malquenar and I went hiking." > /out/keyterm.raw
mv /out/exchange.raw /out/exchange.wav
mv /out/keyterm.raw /out/keyterm.wav
'
ls -la exchange.wav keyterm.wav
