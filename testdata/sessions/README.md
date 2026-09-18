# Recorded sessions

Three short live sessions made from generated speech. Every clip comes
from espeak-ng 1.51 on Ubuntu 24.04 inside Docker. No real voice appears
in any file. Each session cost one token capped at 120 seconds.

## Layout

Each directory holds one session with the same file set.

| File | Meaning |
|---|---|
| `user-*.wav` | Guest audio as generated, streamed into the session |
| `recording.ogg` | Provider stereo recording, guest left and host right |
| `events.json` | Every socket event with local clock times |
| `timeline.json` | Provider turn record with speech offsets |
| `metadata.json` | Provider recording layout and chunk counts |

`steady/` streams two lines back to back with no gaps. `pauses/`
waits four seconds before the first line, six seconds between lines,
and three seconds before ending. `bargein/` asks for a longer story,
then cuts into the reply 2.5 seconds after the story audio starts.

## Steady

Guest lines: "The garden gate needs paint before winter." and "We
should buy brushes on Saturday morning." Both transcripts read back
exactly. Session duration 18.11 seconds.

| File | ffprobe | SHA-256 |
|---|---|---|
| `steady/user-a.wav` | pcm_s16le, 22050 Hz, mono, 2.79 s | `30ba621da195a70d69a6d11ba783839c182b43ad0b1be8d3019ec4b5d7c4cdca` |
| `steady/user-b.wav` | pcm_s16le, 22050 Hz, mono, 2.66 s | `f8b5e5dbc2ddd5a9cf3b9e4efa7b8c44e771635d454f70adb2d654e7d7470326` |
| `steady/recording.ogg` | opus, 48000 Hz, stereo, 18.41 s | `9b8f6aa2d1011d89e5e8b4016a5d0a5d6bd31befa43644d5ba9110038ef11fbb` |
| `steady/events.json` | 1168 events, JSON | `52b5bd4b58fc501163c8b0f365c2dca6f29e16abb64298a4c98d8393a3285d2b` |
| `steady/timeline.json` | 3 turns, JSON | `a0a0b18bf60b68b1b8ca98fc184238c8ad1b060a383d485d4468d67237fef8fd` |
| `steady/metadata.json` | 399 bytes, JSON | `964202385c92c301c537627f659e5be971d86c3d002b34ba53384f7ed63a275f` |

## Pauses

Guest lines: "The lake was calm when we launched the canoe." and "A
heron watched us from the far dock." Both transcripts read back
exactly. Session duration 30.38 seconds.

| File | ffprobe | SHA-256 |
|---|---|---|
| `pauses/user-a.wav` | pcm_s16le, 22050 Hz, mono, 2.74 s | `03d38337490697da0a9e8b06dbd745bd86ed661a6b4d1a7293c524928b73d56f` |
| `pauses/user-b.wav` | pcm_s16le, 22050 Hz, mono, 2.39 s | `a32fc9ac8f0271c4d7fd97cef89bd561ecedae0d01f2718aad78856cd1cebf8a` |
| `pauses/recording.ogg` | opus, 48000 Hz, stereo, 30.66 s | `b2b940a420accc71c8279146785f776d1c99942312e39c335537961402fd5d43` |
| `pauses/events.json` | 1083 events, JSON | `938bb04fc33eb244fdbe217d522d7cb7dffecaba61f8f5634887ce208b5d9e67` |
| `pauses/timeline.json` | 4 turns, JSON | `9dbc0ad37fe93953ec395f054f6a05697e38b9da2b7019c28054311917015997` |
| `pauses/metadata.json` | 399 bytes, JSON | `eb2430afcfa24f24f5f85b86b9fba28eca5fe541a8e3db285a0a566fb2c990da` |

## Barge-in

Trigger line: "Please tell me a longer story about the lake." Barge-in
line: "Sorry to cut in, but the oven timer just rang." The story turn
reads interrupted in the timeline and the barge-in turn completes.
Session duration 25.24 seconds.

| File | ffprobe | SHA-256 |
|---|---|---|
| `bargein/user-trigger.wav` | pcm_s16le, 22050 Hz, mono, 2.87 s | `bfb1c5638505bd833bfdd6b9df8b576dcdc5c9415091693bd68cdcd5657baf6e` |
| `bargein/user-bargein.wav` | pcm_s16le, 22050 Hz, mono, 3.41 s | `6d8f5f4a3908454ee6bafc19eda08dc26ec9989bf9ea777b1a86a3c192222a45` |
| `bargein/recording.ogg` | opus, 48000 Hz, stereo, 25.56 s | `8f50435f1e0752dc532465a41ba0e98228caddb805aa3566fd7650962fe5d9f4` |
| `bargein/events.json` | 1936 events, JSON | `1da18bc4197636175469b9b02e3c53b58d9532725f601e2e0496d33a44924b20` |
| `bargein/timeline.json` | 3 turns, JSON | `08ad90aa4399a6c223b228b4eb6cf692cb33b3b59200f65a84f7534f1ff9b3cd` |
| `bargein/metadata.json` | 399 bytes, JSON | `f744163cf7ae0d23a63757994d676f8135161c3803920d0b5ad533edaf11bf86` |

Every hash above was recomputed with `sha256sum` after staging. Every
ffprobe value comes from `ffprobe -hide_banner` on the committed file.

## Recreate

`recreate.sh` regenerates the six guest clips with the pinned Docker
image. Streaming them through live sessions needs network access and a
provider key through the settings loader. Each run mints three tokens
capped at 120 seconds and deletes every session afterwards.

The offline read needs no network: decode one event log and its stems
with `ffprobe` and a small script, as shown in the handoff record.
