# What the libraries carry

Reprise builds on Keel and Chaaya. This file records what they carry and which Reprise task uses it.

**Reprise does not file asks.** There is no request list and no `proposed` status. A capability that
more than one consumer could use is written in the library that owns the seam, as a task in that
library's own plan, through that library's own loop. Reprise then wires it. An agent working here
never has to read a row and work out whether the thing exists yet.

**Why it works this way.** An ask register says a capability is wanted without saying where it lives,
and an agent reading one has to decide. Writing it in the library removes the decision and gives the
library a real consumer's feedback instead of a paragraph describing one.

**Versions.** Keel `v0.3.0`, public, fetched from the module proxy with no `replace` directive.
Chaaya `0.2.0` from npm, pinned exactly in `web/package.json`. Pin both exactly. An earlier release
of either carries less than this plan builds on.

---

## Keel v0.3.0

| Need | Package | Reprise uses it in |
|---|---|---|
| Settings with secret references, never values | `keel/config`, `keel/config/source` | T1.5 |
| Spend gate per client and global | `keel/gate` | T2.1 |
| A budget that refuses before a paid call, durable across restarts | `keel/cost`, `keel/cost/sqlitestore` | T2.1, T2.3, T5.3 |
| Durable jobs with per-kind limits and opt-in resumption | `keel/job`, `keel/job/sqlitestore` | T2.3, T3.1, T3.4, T3.5, T4.4 |
| Job progress over server-sent events | `keel/stream`, `keel/wire` | T1.3, T3.3, T4.3 |
| Private media with Range serving and an authorizer | `keel/mediastore`, `keel/mediastore/sqlitestore` | T1.4, T4.3, T4.4 |
| Resumable chunked upload with per-owner caps | `keel/upload`, `keel/mediastore` | T2.4 |
| One SQLite file with namespaced migrations and backup | `keel/sqlite` | T1.1, T6.1 |
| ffmpeg bound to a context | `keel/ffmpeg` | T3.4, T5.1 |
| Unguessable ids | `keel/id` | T1.4, T4.4 |
| A ceiling per owner beneath the global one | `keel/cost`, `keel/cost/sqlitestore` | T2.1, T5.3 |
| Reserve, call, settle, release on every failure path | `keel/cost` | T2.1, T3.2, T3.5 |
| A time-capped lease on a metered session, reconciled afterwards | `keel/lease`, `keel/lease/sqlitestore` | T2.1, T2.3 |
| Switches an operator flips without a restart | `keel/flag`, `keel/flag/sqlitestore` | T2.1, T5.3 |
| A delete that retries every target until it confirms | `keel/erase` | T4.4, T5.4 |
| A cut list rendered with crossfades and two-pass loudness | `keel/edl` | T3.4 |
| Captions as SRT and WebVTT from word timings | `keel/caption` | T5.1 |
| Waveform video from a still and audio | `keel/waveform` | T5.1 |

**A durable package takes its store as a field.** `cost`, `job`, `flag`, `lease` and `mediastore`
each declare the seam they need — an `Account`, a `Store`, a `BlobIndex` — and ship a `sqlitestore`
that implements it against the one SQLite file T1.1 opens. Naming the parent package alone leaves a
process that forgets across a restart. `keel/upload` is the exception: it stages chunks on disk and
hands the finished upload to a `mediastore`, so T2.4 wires both.

## Chaaya 0.2.0

| Need | Export | Reprise uses it in |
|---|---|---|
| Token contract, theme, API client, wire parsing | `tokens`, `theme`, `api`, `wire` | T0.1, T1.3, every screen |
| Job stream client with catch-up | `job` `JobStream`, `JobFollower` | T3.3, T4.3 |
| PCM capture on a context the page owns, with the render rate reported | `audio` `AudioRecorder` | T2.4 |
| A take that streams instead of accumulating | `audio` `AudioRecorder`, `onChunk` with `retain: false` | T2.4 |
| Rate conversion that filters before it decimates | `audio` `resampleChunks`, `resampleLinear` | T2.4 |
| A WAV built from blocks the caller holds | `audio` `encodeWav` | T2.4 |
| Gapless scheduled PCM playback with a flush that reports its cut | `audio` `PcmStreamPlayer` | T2.4 |
| Chunked upload that survives a dropped network and a reload | `audio` `ChunkUploader` | T2.4 |
| The upload protocol's own bodies, paths and parsers | `audio` `parseUploadSnapshot` and the rest | T2.4 |
| One retry policy, ceiling included | `audio` `retryDelayMs`, `isRetryableStatus` | T2.4, T1.3 |
| A close message sent once on `pagehide` and on destroy | `guard` `SessionGuard` | T2.4 |
| One playback element unlocked by the first gesture, with seeking | `audio` `AudioPlayer` | T3.3, T4.3, T5.5 |
| Live level and waveform peaks in a worker | `audio` `LiveLevel`, `computePeaksInWorker` | T2.4, T3.3 |
| Timed words, ranges, cuts with reasons, revert | `transcript` `TranscriptEditor` | T3.3 |
| A transcript that follows playback, and click to seek | `transcript` `TranscriptFollower` | T3.3, T4.3 |
| A waveform carrying the regions a cut made | `transcript` `regionsFromCuts` | T3.3 |
| Contrast and accessibility gates | `testing` | every screen |

**Set the recorder deliberately.** `autoStopSeconds: 0`, because the default ends a take after 12
seconds. `echoCancellation: true`. `noiseSuppression: false` and `autoGainControl: false`, because the
Voice Agent's own front end does both jobs better, and gain riding makes a stem breathe with the room.
Chaaya documents the three together.

---

## What Reprise builds, and why it is not library work

Each of these is specific to this product, this provider, or this deployment. None would serve a
second consumer unchanged.

| Piece | Task | Why it lives here |
|---|---|---|
| The provider adapter: token, socket, session config, Sessions API | T2.1, T2.2, T2.3 | One provider's wire protocol, prices and reconciliation |
| The edit model: proposed cuts, reasons, chapters, cold open | T3.2, T3.3 | The editorial decisions are this product's. `keel/edl` renders what they produce |
| Which flags exist, and what the admin page does | T5.3 | `keel/flag` carries the switches. The set is Reprise's |
| Which targets an erasure covers, and when a guest expires | T4.4, T5.4 | `keel/erase` drives the fan-out. The list of places a recording lands, and the retention window, are this product's |
| Guest identity and server sessions | T1.4 | Keel lists users as a non-goal, and that boundary holds. This is the one row justified by a library's boundary rather than by product specificity |
| Provider webhook receiver | T3.1, T3.5 | Only if T0.4 picks webhooks over polling |
| Deploy kit and check tooling | T0.1, T6.1 | One box, one binary |
| Artifact fixtures for the seeded season | T5.2 | This product's content |

**If one of these turns out to be general**, it moves to the library it belongs to, as a task there.
It does not become a row here.
