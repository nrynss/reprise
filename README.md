# Reprise

Reprise is a personal podcast. You talk, a host asks questions, and the conversation becomes an episode. The host remembers earlier episodes, so the show calls back to what you said before.

The moment that sells it is memory across episodes. You mention dreading a conversation in episode one. A few episodes later the host opens by asking whether you ever had it. Nobody needs the feature explained after hearing that.

## How it works

1. Record. One voice session with a host that interrupts and follows up.
2. Edit, optionally. Strike a sentence in the transcript and the audio cut follows it. The model proposes cuts and you approve or revert each one.
3. Mark done. An explicit gate renders the final audio once per episode.
4. Analyse. Batch transcription runs on the rendered file. Chapters, show notes, names, and key phrases come out of that pass.
5. Gallery. Episodes, transcripts, downloads, and share links live in one place.

Episodes stay private by default. Publishing is an explicit action per episode. Every session ends explicitly, because streaming bills on connected time.

## Stack

| Layer | Choice |
|---|---|
| Backend | Go 1.27.1, standard library first, on Keel `v0.3.0` |
| Frontend | Svelte 5 with runes, SvelteKit with the static adapter, TypeScript strict, on Chaaya `0.2.0` |
| Live voice | AssemblyAI voice agent: transcription, turn-taking, interruption, and voice |
| Batch transcription | AssemblyAI batch models on the rendered file |
| Editorial model | Gemini through Vertex AI: cuts, chapters, titles, notes, callbacks, cover prompts |
| Media | Pinned static ffmpeg and ffprobe `9.0.1` |
| State | One SQLite file plus media on disk, served by the Go binary |
| Ship | One distroless container behind Traefik |

Keel and Chaaya are the author's prior open source work. Keel is a Go toolkit for durable server work: settings, budgets, jobs, uploads, media, rendering helpers, and erasure. Chaaya is a browser toolkit for the live path: PCM capture, streaming upload, playback, levels, and the transcript editor. Reprise wires both and builds what is specific to this product.

## Run locally

You need Go `1.27.1`, Node `26`, npm, and ffmpeg plus ffprobe `9.0.1` on `PATH`. A fresh clone on a clean machine builds with no access to any other machine.

Copy the secret template and fill both values:

```sh
cp .env.example .env
```

Place a Vertex AI service account key where the local settings point. `config/reprise.local.toml` names the path. Point that entry at your key file if it lives elsewhere.

Build the web app:

```sh
cd web
npm ci
npm run build
cd ..
```

Run the server from the repository root:

```sh
REPRISE_CONFIG=config/reprise.local.toml go run ./cmd/reprise
```

The server listens on `:8080` and serves the built app. Check it with:

```sh
curl -fsS http://localhost:8080/healthz
```

It answers `ok` with the boot id and the build version. For frontend iteration, run `npm run dev` inside `web`.

The demo needs real provider credentials. Without an AssemblyAI key and a Vertex key, the app boots but no live session can start.

## Build the image

The repository root carries a `Dockerfile`. It builds the Go binary, the web app, and the pinned media tools into one distroless container:

```sh
docker build -t reprise:local .
```

The image bakes in the box settings and serves on `8080`. `deploy/README.md` documents how the operator runs it on the live box.

## Config and secrets

One TOML file holds the settings for one environment. `config/reprise.local.toml` is for local work. `config/reprise.box.toml` is for the live box. `REPRISE_CONFIG` names the file. When it names nothing, the server falls back to the local file.

Secrets are references, never values. The AssemblyAI key and the session signing key resolve from `.env` locally. The Vertex credential is a service account key file. No secret value appears in any tracked file. Never export a secret in an interactive shell, because it lands in shell history.

The boot log prints the resolution plan. Every secret shows as resolved and no value appears. A missing or unreadable secret stops the process before it listens.

## Checks

`./tools/check.sh` is the gate. It checks formatting, vet, staticcheck, Go tests with the race detector, Svelte check, lint, unit tests, the web build, and end to end tests. Run `npm ci` inside `web` first, then run the gate from the repository root:

```sh
cd web
npm ci
cd ..
./tools/check.sh
```

The gate needs the pinned ffmpeg and ffprobe on `PATH`. It scans tracked files only.

## Live instance

`https://reprise.nryn.dev` serves the current build. `/healthz` reports the running version.

## Source layout

`cmd/reprise` holds the binary. `internal/assemblyai` is the only code that talks to AssemblyAI. `internal/gemini` is the only code that talks to Gemini. `web/src` holds the SvelteKit app. `config` holds one settings file per environment. `deploy` holds operator notes and box scripts. `tools` holds the gate.

## License

No license file ships yet. The owner adds the terms when the repository goes public.
