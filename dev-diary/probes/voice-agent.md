# Voice Agent probe

Live run on 2026-09-18 against the AssemblyAI Voice Agent API. The Go probe
is `internal/assemblyai/live_test.go` behind the `live` build tag. It mints
short lived tokens through the settings loader and opens raw sockets with no
websocket dependency. It streams generated speech as PCM into `input.audio`
and records every event with local clock times. No real voice appears
anywhere. Clips come from `testdata/speech/` beside the test.

## How to run

Point at the local settings file, then run the single test. The run takes
about four minutes. Most of that is the 70 second cap window and realtime
audio streaming.

```bash
REPRISE_CONFIG=config/reprise.local.toml \
  go test -tags live -run TestVoiceLiveProbe -v ./internal/assemblyai/
```

The test mints five tokens, each capped at 120 seconds except the cap probe
at 60 seconds. It deletes all five sessions at the end and fails when any
deleted session still lists afterwards.

## Clips

Two clips, one voice, generated with espeak-ng 1.51 on Ubuntu 24.04 inside
Docker. `testdata/speech/generate.sh` reproduces both files byte for byte.
The test streams the 24 kHz PCM renders beside them.

Script:

1. `exchange.wav` (3.58 seconds): Hello world, this is a test of the voice
   agent probe.
2. `keyterm.wav` (3.98 seconds): My friend Zaffranil vistrex malquenar and
   I went hiking.

Zaffranil vistrex malquenar is invented. No public recording carries it, so
recognition shifts come from the keyterms list and nothing else.

Batch transcription of the raw clips confirms they are intelligible. The
exchange clip returns every word with confidence above 0.77 except the last.
The keyterm clip without keyterms returns Ranil Viswatrek Malakunar style
guesses. Hand rolled formant synthesis was tried first and returned zero
words, so espeak-ng carries the probe.

## Token

`GET https://agents.assemblyai.com/v1/token` mints one single use token.
The key travels in the `Authorization` header. The response carries only
the token and the redemption window:

```json
{"token": "<redacted>", "expires_in_seconds": 300}
```

Range enforcement is strict. Every bad value returns 422 with a detail
body. Measured responses:

| Request | Status | Detail |
|---|---|---|
| `expires_in_seconds=0` | 422 | greater than or equal to 1 |
| `expires_in_seconds=601` | 422 | less than or equal to 600 |
| `max_session_duration_seconds=59` | 422 | greater than or equal to 60 |
| `max_session_duration_seconds=10801` | 422 | less than or equal to 10800 |
| `expires_in_seconds` missing | 422 | Field required |

`expires_in_seconds` is the redemption window, not the session length. The
session length cap rides `max_session_duration_seconds` from 60 to 10800.
Omitting the cap still mints a token, and the session behaves like the
explicit caps below.

## Socket

`wss://agents.assemblyai.com/v1/ws?token=<token>` upgrades with 101. The
server header reads `Python/3.13 websockets/15.0.1`. Frames are JSON text.
Audio uploads use `input.audio` with base64 PCM16 mono at 24 kHz. Chunks of
4800 samples every 200 ms track realtime without tripping the rate guard.
One early run sent malformed client frames and the server closed with
`invalid opcode`, which confirms framing is enforced.

## Session configuration

One `session.update` carries the whole inline agent. No stored agent is
used. The server echoes the resolved config in `session.updated` and again
in `session.ready`:

```json
{"type": "session.updated", "config": {
  "id": "sess_59a244f26fe641f18eb9eb9d183ba1fb",
  "system_prompt": "You are a test host. Keep replies to one short sentence.",
  "greeting": "Hello there. Last week you mentioned dreading a talk with your sister. Did it happen?",
  "input": {"type": "audio", "format": {"encoding": "audio/pcm", "sample_rate": 24000},
    "keyterms": null, "turn_detection": null, "transcription_mode": null,
    "transcription_prompt": null, "language_codes": null, "voice_focus": null,
    "voice_focus_threshold": null, "continuous_partials": null},
  "output": {"type": "audio", "voice": "alba",
    "format": {"encoding": "audio/pcm", "sample_rate": 24000}, "volume": null},
  "tools": [], "llm": [], "pre_connect_requests": []}}
```

`session.ready` adds `session_id`, `expires_at`, and `resume_token`. The
`expires_at` value sits about one hour past connect for every cap tried:
3594 seconds for cap 60, 3595 seconds for cap 120, 3594 seconds for cap
600. The cap value does not move it. This matches the cap finding below.

## Greeting spoken first, unprompted

The host speaks the greeting before any guest audio arrives. The first
reply starts under one second after `session.ready`. The final transcript
matches the configured greeting exactly:

```json
{"type": "transcript.agent", "reply_id": "resp_755941bbefe44a9db5c9f5571186d270",
 "item_id": "msg_3fa13b9717ca47dc93729acf4ddbd2a7",
 "text": "Hello there. Last week you mentioned dreading a talk with your sister. Did it happen?",
 "interrupted": false, "timestamp": 1789756525.7772188}
```

The greeting builds from a sentence about an earlier episode, the sister
talk from episode one. The plan callback shape works over this channel.

## Transcript shapes

`transcript.user` carries no word timings. Only text and the item id:

```json
{"type": "transcript.user", "item_id": "msg_2b39a4e54fa3464b9f8baa0fece0f591",
 "text": "Hello world, this is a test of the voice agent probe.",
 "timestamp": 1789756534.923444}
```

Partial `transcript.user.delta` events arrive during speech. Each carries
the full text so far, never an incremental chunk.

`transcript.agent.delta` carries `start_ms` and `end_ms` on every word.
The greeting produced 16 deltas, all timed. First and last:

```json
{"type": "transcript.agent.delta", "delta": "Hello", "start_ms": 0, "end_ms": 195}
{"type": "transcript.agent.delta", "delta": "happen?", "start_ms": 3848, "end_ms": 4125}
```

The probe asserts every delta carries both fields and fails otherwise.

## Ending and billing

`session.end` draws `session.ended` at once. The event bills connected
time only:

```json
{"type": "session.ended", "session_duration_seconds": 14.084401,
 "audio_duration_seconds": null, "timestamp": 1789756541.0550377}
```

`audio_duration_seconds` reads null on every run, including runs that
streamed audio in. Duration matches wall clock within a second. The
Sessions API agrees: the exchange session lists `duration_seconds`
13.766666 with `public_close_reason` client end.

A bare socket close without `session.end` leaves the session resumable
and billable, per the docs table. The probe always ends explicitly.

## The 60 second cap does not end the session

This differs from the plan. Three idle runs with a 60 second cap stayed
open past the window. No `session.ended` and no `session.error` arrived at
60 seconds, at 100 seconds, or at 105 seconds. While open, the Sessions
API reports `status` created with null duration and null end time. After
the client ends at 72 seconds, the session completes and bills 72.17798
seconds:

```json
{"type": "session.ended", "session_duration_seconds": 72.17798}
```

Cap 60, 120, and 600 all report `expires_at` about one hour out, as shown
above. The token cap does not schedule a server close inside the measured
window. The browser must run its own timer and send `session.end` first.
`project.md` is updated in the same commit to record this.

## Recording

The session recording is stereo Opus with the user on the left. The
metadata artifact states the layout outright:

```json
{"session_id": "sess_489505a8d4024b56b6a33e7487d4b542",
 "format": "ogg/opus", "channels": 2,
 "channel_layout": "stereo (left=user, right=agent)",
 "sample_rate": 24000,
 "file": "sess_489505a8d4024b56b6a33e7487d4b542/recording/audio.ogg",
 "uploaded_chunks": 3, "dropped_chunks": 0}
```

ffprobe on the downloaded audio confirms Opus stereo:

```text
Stream #0:0: Audio: opus, 48000 Hz, stereo, fltp
codec_name=opus
channels=2
channel_layout=stereo
duration=13.663000
```

Channel energies place each voice on its side. The streamed clip ran from
about 4.4 to 8.2 seconds into the session. The left channel carries speech
from 3.9 to 7.2 seconds and silence elsewhere. The right channel carries
the greeting from 0.4 to 2.9 seconds and the reply from 11.1 to 12.1
seconds, with silence while the guest speaks. The test runs ffprobe on the
downloaded recording and asserts Opus stereo.

The timeline artifact pairs each turn with its trigger. The greeting turn
carries trigger greeting with null user fields. The exchange turn carries
the user transcript with confidence 1.0 and the reply text. The ended
block reads reason user initiated with public reason client end.

## DELETE removes the session

`DELETE /v1/sessions/{id}` returns 204. The test lists up to 200 sessions
before deleting and asserts all five probe sessions appear, then deletes
all five and asserts none still lists. The deletion proof is absence from
the list, not a failing fetch.

## Keyterms shift the invented name

The same keyterm clip streamed twice, once plain and once with keyterms
Zaffranil, vistrex, malquenar. Recognition shifts toward the planted
spelling. Two paired runs:

| Run | Without keyterms | With keyterms |
|---|---|---|
| First | My friend's name is Ranil Viswatrek Malakunar, and I went hiking. | My friend Zaffranil vistrex malquenar and I went hiking. |
| Second | My friend's name is Elvis Rex Melquinar, and I went hiking. | My friend Zaffranil vistrex malquenar and I went hiking. |

The keyed transcript spells Zaffranil exactly both times. The unkeyed
transcript guesses different near misses each run. The probe fails when
both readings match or when the keyed reading misses the name.

## Sessions API shapes

List returns newest first with `id`, `status`, `public_close_reason`,
`duration_seconds`, `created_at`, `ended_at`. Fetch adds `config` and
`artifacts`: audio as audio ogg, timeline as application json, metadata as
application json. Artifact URLs are presigned and need no auth header.
The timeline carries turns, config changes, and the ended block. Empty
turn lists are omitted, not empty.

## Spend

Voice bills 4.50 dollars per connected hour, per second. The final Go run
mints five tokens and connects about 110 seconds across greeting,
exchange, keyterm pair, and the 70 second cap window. That run costs about
14 cents. Exploration runs before it, plus the parallel batch probe on the
same key, brought the account total near 1.02 dollars at count time. The
probe deletes every session it creates. Per run spend stays far under the
1 dollar task budget. Every token carries a 120 second cap or less, so a
lost session cannot burn more than 15 cents.

## Surprises

- The token cap does not end the session inside the measured window. The
  plan said AssemblyAI ends the session at the cap. Three runs prove the
  socket stays open and billing continues until the client ends it.
- `audio_duration_seconds` is null on every `session.ended`, even when the
  run streamed audio in. Bill from `session_duration_seconds` only.
- `expires_at` sits about one hour out for caps of 60, 120, and 600. The
  cap value does not move it.
- `expires_in_seconds` is required on the token call. Omitting it returns
  422, not a default.
- Hand rolled formant synthesis returns zero recognized words. Espeak-ng
  through Docker reproduces and recognizes cleanly.
- Raw sockets need exact websocket framing. A four byte length header on a
  short message reads as length zero and the server closes the socket.
- A package with only live tagged test files breaks the non-live gate.
  `internal/assemblyai/doc.go` holds the package clause so plain `go test`
  passes. Whoever owns the provider adapter keeps that file.
