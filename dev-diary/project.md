# Reprise: project

[`../product.md`](../product.md) says what Reprise is and why. This file says how it gets built,
and records every place the build departs from `product.md`. Where the two conflict, this file
wins. Each departure below carries its evidence.

## Architecture

```text
 Browser (SvelteKit static app on Chaaya)
   │  1 POST /api/sessions  ──────────────▶  Go binary on Keel v0.3.0
   │  ◀── single-use token + session config     │ gate, guest quota, kill switch
   │                                            │ budget reservation, token mint
   │  2 wss://agents.assemblyai.com/v1/ws ─▶ AssemblyAI Voice Agent
   │     mic PCM at 24 kHz up, host PCM down
   │  3 user stem (device rate) and host stem (24 kHz)
   │    ── Chaaya ChunkUploader ──────────▶  keel/upload → keel/mediastore
   │                                            │
   │  4 draft: edit transcript job ─────────────┤ AssemblyAI batch on the user stem
   │           editorial job ───────────────────┤ Gemini hears the stems, proposes
   │  5 mark done: render job ──────────────────┤ keel/ffmpeg from stems and decisions
   │           analysis job ────────────────────┤ AssemblyAI batch on the render
   │           memory job ──────────────────────┤ SQL over mentions
   │  6 gallery, thread panel, export  ◀────────┘ keel/sqlite on the box
   │    Chaaya JobStream follows every job over keel/stream
```

One Go binary on the Hetzner box serves the app, the API, and the jobs. One SQLite file holds all
state. Media sits on a volume behind `keel/mediastore`. `keel/config` loads settings from one TOML
file per environment and resolves every secret from where it lives.

## Departures from product.md

### Supabase is gone

`product.md` names Supabase for auth, Postgres and storage. The free tier is exhausted. Reprise runs
on `keel/sqlite`, `keel/mediastore` and its own guest sessions. The rule behind the original choice
survives: a guest is a real user row, and one code path serves guests and owners. Authorization
moves from row-level security into the Go handlers and the `mediastore` authorizer.

### The live transcript has no word timings for the user

`product.md` says the editor cuts against the realtime transcript's word timestamps. The Voice Agent
API does not provide them. `transcript.user` carries only `text` and `item_id`. Only the host's words
carry `start_ms` and `end_ms`, relative to each reply's audio.

So Reprise batch-transcribes the **user stem** after recording, for word timings. That transcript
serves editing only. Analysis still runs on the rendered file, so chapters and timestamps describe
what people hear.

### Auto chapters are deprecated

AssemblyAI deprecated the `auto_chapters` parameter and points to LLM Gateway over the transcript.
Summarization, entity detection and key phrases remain. Reprise produces chapters through LLM
Gateway, so the YouTube chapter markers still rest on AssemblyAI.

### The session cap lives in the token

`GET /v1/token` accepts `max_session_duration_seconds`, from 60 to 10800. The cap does not end the
session by itself. Three idle runs stayed open past a 60 second cap with no server close, and the
Sessions API billed until the client sent `session.end`. The browser runs its own timer and ends first.
The global kill switch stops minting tokens. A bare socket close still bills a 30 second resume window,
so the browser sends `session.end` first.

### The callback belongs in the greeting

The greeting is immutable after the first `session.update`, and the host speaks it first. The server
builds it from the planted callback, so the episode opens on the thing you were dreading.

### AssemblyAI keeps its own copy

Every Voice Agent session is stored with a stereo Opus recording (user left, agent right) and a
timeline. `DELETE /v1/sessions/{id}` soft-deletes it. Reprise deletes the session and every batch
transcript when a user erases an episode, and the privacy page says the provider's deletion is soft.
The stereo recording also measures how well the local stems align.

### Browser audio rates

Forcing a 24 kHz `AudioContext` works only on Chromium. Firefox bypasses its echo canceller at a
non-default rate, and Safari ignores the setting. So the page creates the context at the device rate,
hands it to Chaaya's recorder, and plays the host stream on that same context, which is what puts
both stems on one clock. A copy of each captured block goes to 24 kHz for the API, through Chaaya's
resampler, which filters before it decimates because halving folds everything above 12 kHz back into
the band. The Voice Agent accepts PCM at 24 kHz only. Noise suppression and gain control stay off,
because the server's `voice_focus` does both jobs better and gain riding makes a stem breathe with
the room.

### Secrets are references in a file, not an env file on its own

Configuration is one TOML file per environment, loaded by `keel/config`. Settings sit inline. Each
secret is a reference that names where it lives. Locally that is an `env_file` source reading
`/home/nryn/work/reprise/.env`. On the box it is an `env_file` source reading `/etc/reprise/env`,
mode `0600`. No secret value appears in any TOML file, so the files are safe to track.

## Facts the plan relies on

Verified against AssemblyAI's documentation on 2026-09-15. T0.2 measures each one live.

| Fact | Value |
|---|---|
| Token | `GET https://agents.assemblyai.com/v1/token`, single use, `expires_in_seconds` 1 to 600 |
| Socket | `wss://agents.assemblyai.com/v1/ws?token=<token>` |
| Inline config | `system_prompt`, `greeting`, `input`, `output`, `tools` in `session.update` |
| Keyterms | `input.keyterms`, up to 100, mutable mid-session. `transcription_prompt` up to 1750 characters. |
| Audio | `audio/pcm` at 24 kHz both ways by default |
| Ending | Send `session.end`, wait for `session.ended`. A bare close bills a 30 second window. |
| Sessions API | `GET /v1/sessions`, `GET /v1/sessions/{id}` with expiring artifact URLs, `DELETE` soft-deletes |
| Price | $4.50 per connected hour. Batch Universal-3.5 Pro $0.21 per hour. |

## What Reprise takes from the libraries

Measured against the shipped code, not the plans. [`libraries.md`](libraries.md) holds the detail
and what Reprise builds itself.

| Need | Keel `v0.3.0` | Chaaya |
|---|---|---|
| Settings and secrets | `config`, `config/source` | none |
| Spend protection | `gate`, `cost` with per-owner ceilings and the call seam, `flag`, `lease` | none |
| Long work | `job` with kinds, `stream`, `wire` | `job` `JobStream` |
| Recording | `upload`, `mediastore` | `audio` `AudioRecorder` in PCM mode on a page-owned context, draining through `onChunk`, `ChunkUploader`, `LiveLevel` |
| Playback | `mediastore` Range serving and authorizer | `audio` `AudioPlayer`, `PcmStreamPlayer`, `computePeaksInWorker` |
| Rendering | `ffmpeg`, `edl`, `waveform`, `caption` | none |
| Errors | `wire` | `api`, `wire` |
| Erasure | `erase`, `job` | none |
| Look | none | `tokens`, `theme`, `testing` |
| Editing | none | `transcript` `TranscriptEditor`, `TranscriptFollower`, `regionsFromCuts` |

Reprise builds these itself: the provider adapter, the edit model behind a render, guest identity,
which flags exist, which targets an erasure covers, and the deploy and check tooling. Each is
specific to this product, this provider or this deployment. The mechanisms under them are the
libraries': the live duplex path and the transcript editor from Chaaya `0.2.0`, and leases, budgets
per owner, flags, erasure, rendering and captions from Keel `v0.3.0`.

### Gemini reaches Reprise through Vertex AI

The credit for this work sits on a Google Cloud billing account. AI Studio's paid tier wants its own
prepayment and its SKUs are commonly outside a credit grant, so Reprise calls Gemini through Vertex
AI, where the spend lands on that billing account like any other Cloud service.

That decides the credential's shape. Vertex authenticates a service account, so the secret is a JSON
key file rather than a string. The Hetzner box runs no metadata server, so default credential
discovery finds nothing and the file is explicit. `keel/config` reads it through a `file` source,
which already refuses a group-readable secret. The project id and the location are ordinary settings
and sit inline in the TOML.

One consequence worth carrying: Vertex has no Files API. Audio too large for an inline request goes
through a Cloud Storage URI, and the object must live in the same project. A twenty minute mono Opus
stem is roughly five to ten megabytes, so inline should hold and no bucket should be needed. T0.3
measures that rather than assuming it.

### Planning files are tracked

T0.1 said `.gitignore` keeps `AGENTS.md`, `product.md` and `dev-diary/` out of the repository. They
are tracked. The plan travels with the code. The gate skips those paths. Source still must not name
a task.

### Seeded season is a per-user copy of a small catalog

`product.md` describes four finished episodes and a visitor recording episode five. The shipped
catalog starts empty. Copy, receipt, and drop do not wait on episodes. The operator places one
or two finished episodes in `data/season/` after first deploy, as dogfood. Each new guest and
each new empty account then receives a copy of those rows. The owner does not. A returning user
does not get a second copy. User delete drops that user's copy only. Catalog audio stays.

### Guest retention is 90 days

T5.4 landed with a 90 day window from settings. The sweep does not wait on a catalog.

### Deploy is from GitHub Actions

`thutapi` pulls a public GHCR image on the box. Reprise is private, so the runner pulls and
loads the image over SSH. The box never logs into the registry. The owner overrode the loop
on 2026-09-19 to land this before leaving the workstation. Commits `8eb8e76` and `7a1a7d0`
were not reviewed. The public edge still needs the DNS record.

## Open decisions

Each belongs to the owner. The task that needs it stays `blocked` until it is made.

| Decision | Needed by |
|---|---|
| Which owner login, if any, beyond guest mode | T1.4 |
