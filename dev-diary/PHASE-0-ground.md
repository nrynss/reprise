# P0: Ground and probes

```yaml
id:       P0
size:     M
requires: []
blocks:   everything
parallel: yes, within the phase
```

**Goal:** Stand up the repository on Keel and Chaaya, and settle every provider behaviour the plan
assumes with a live probe before code depends on it.

**Why probes come first:** The AssemblyAI docs already overturned two assumptions in `product.md`.
The live transcript has no user word timings, and auto chapters are deprecated. Everything else the
plan assumes gets measured before anything is built on it.

---

### T0.1: Repository and checks ★
```yaml
requires:   []
fixture-ok: no
size:       M · mid
owns:       go.mod, go.sum, cmd/reprise/main.go, web/, Dockerfile, .dockerignore, .gitignore,
            .github/workflows/ci.yml, tools/check.sh, dev-diary/tools/audit_docs.py
status:     done:a02280b68dc30d469ef9f1a841270cbdfdd9c673
```
**Build on:**
- `go get github.com/nrynss/keel@v0.3.0`. Keel is public, so no `replace` directive and no private
  module settings.
- `@nrynss/chaaya` at `0.2.0` from npm, with `bits-ui`, `svelte` and `axe-core` as its peers. Take
  Svelte, SvelteKit, Vite and TypeScript versions from Chaaya's `package.json` so the peer ranges
  agree. Reprise uses the static adapter. Pin the exact version: an earlier one carries no duplex
  path.
- Keel's `tools/check.sh` and Chaaya's `tools/check.sh` as references for the checks. Reprise writes
  its own runner, because it gates a Go binary and a web build together.

The module is `github.com/nrynss/reprise`. The Go binary serves the built `web/` directory and
`GET /healthz`. **The repository is private through development, and carries no `LICENSE` file.** No
task adds one and no task names a license. The owner chooses the license when the project ships.

**`.gitignore` keeps planning out of the public repository.** It lists `AGENTS.md`, `product.md`,
`dev-diary/`, `.env` and `.env.*` with `!.env.example`. Same rule as Keel and Chaaya.

`tools/check.sh` runs, in order: `gofmt`, `go vet`, `staticcheck`, `go test -race`, `svelte-check`,
ESLint, Vitest, Playwright, the Svelte 4 leakage guard, a scan for consumer and planning names in
tracked files, and the plan reference scan. CI runs the same file and installs a pinned static
`ffmpeg` and `ffprobe`.

**Done when:** `docker build` produces an image that serves the app shell and `/healthz`. Each check
fails on a planted violation. A clean clone passes the gate three times in a row, exit 0 each time.

---

### T0.2: Voice Agent probe ★
```yaml
requires:   T0.1, T1.5
fixture-ok: no
size:       M · frontier
owns:       internal/assemblyai/live_test.go, dev-diary/probes/voice-agent.md,
status:     done:b3fc58aab1e9e5818d0598e2033cd68bb9b8ef1b
```
**Read first:** AssemblyAI's `voice-agent-api` pages: browser integration, events reference, session
history, session configuration. Download the Markdown versions again. Earlier copies were temporary.

A Go test behind the `live` build tag opens a real session with a token from `internal/assemblyai`.
It streams generated speech as PCM into `input.audio` and records every event. `testdata/speech/`
holds the generated clips, each made by a script that is committed with them.

Measure each row of the facts table in `project.md`, and these as well.

* A token minted with `max_session_duration_seconds=60` ends the session at 60 seconds. Record the
  event that arrives.
* `session.end` followed by `session.ended` bills connected time only. Read `duration_seconds` from
  the Sessions API.
* A greeting built from a sentence about an earlier episode is spoken first, unprompted.
* `transcript.user` carries no word timings. `transcript.agent.delta` carries `start_ms` and
  `end_ms`.
* The session recording is stereo Opus with the user on the left. Run `ffprobe` on it.
* `DELETE /v1/sessions/{id}` makes the session disappear from the list.
* Keyterms change recognition of an invented name. Stream a clip that says it, with and without the
  keyterm.

Spend under $1. Cap every token at 120 seconds.

**Done when:** The record holds every measurement with its raw event or `ffprobe` output. Any fact
that differs from `project.md` updates `project.md` in the same commit.

---

### T0.3: Gemini access probe ★
```yaml
requires:   T0.1, T1.5
fixture-ok: no
size:       S · frontier
owns:       internal/gemini/live_test.go, dev-diary/probes/gemini.md
status:     done:5ab25cad6d12d68bbd9480fe5c3255fb2287bba2
**Build on:** `internal/gemini`, written against the `google.golang.org/genai` documentation. The
credential comes through `keel/config` as a secret reference. The Hetzner box has no metadata
server, so default credential discovery does not apply.

Settle three questions.

* **Credentials on the box.** The route is decided: Vertex AI with a service account key file
  through a `file` source, because the credit sits on a Cloud billing account and the box runs no
  metadata server. Prove the key resolves, that the client reaches Vertex with the project and
  location from settings, and that a wrong or unreadable file fails at boot by name.
* **Audio size.** Vertex has no Files API. Send a 20 minute Opus stem inline and record whether it
  is accepted and what it costs in tokens. Only if inline refuses it, measure a Cloud Storage URI in
  the same project, and record that a bucket became a deployment dependency.
* **Listening, not reading.** Generate a take where one answer is flat and one carries laughter.
  Ask which should open the episode, without the transcript. Record its choice and reason.

**Done when:** The record holds the chosen credential route, the size limit found, token counts,
the usage fields each response reports, and the listening test's answer.

---

### T0.4: AssemblyAI batch probe ★
```yaml
requires:   T0.1, T1.5
fixture-ok: no
size:       S · frontier
owns:       internal/assemblyai/batch_live_test.go, dev-diary/probes/batch.md
status:     done:c87e596a29b542f8428ee61ad4d490aab3218b2e
```
Upload a 48 kHz generated user stem and transcribe it with Universal-3.5 Pro. Record these.

* Word timestamps against the generated clip's known word boundaries. The script that made the clip
  records where each word starts.
* Entity detection, key phrases and summarization on the same request.
* Chapters through LLM Gateway over the transcript. Record the request, the model and the cost.
* `DELETE` on the transcript. Confirm a fetch afterwards fails.
* Whether results arrive by webhook to a public URL or need polling. Test both.

**Done when:** The record holds every response shape, the timestamp error per word, and the chosen
chapter route and result route.

---

### T0.5: Recorded fixtures
```yaml
requires:   T0.2
fixture-ok: no
size:       S · mid
owns:       testdata/sessions/
status:     in-progress:land:t0.5-rem-r1@a150012312dc78bd3b2f24e31e2e662fe37b3db2
```
Run three short live sessions from generated speech and commit their artifacts. Downstream tasks
build on these offline.

* The user stem as generated, the host stem as received, the event log with local clock times, and
  the provider's stereo recording.
* One session with a steady exchange, one with long pauses, and one with a barge-in timed into a
  host reply.
* A README listing each file's `ffprobe` values and SHA-256, plus the command that recreates it.

No real voice goes in, and no guest recording ever becomes a fixture.

**Done when:** P3 tasks run against `testdata/sessions/` with no network.

---

### T0.6: Repair the speech fixtures
```yaml
requires:   T0.2
fixture-ok: yes
size:       S · mid
owns:       testdata/speech/
status:     done:14d5b3cbdc2f2cefb5a1b9699be45b0b9a791822
```
T0.2's review recorded two fixture defects out of scope. Both are real and neither changes a
measurement, so the record stands. They are fixed here.

**The committed wav files carry a corrupt header.** Both declare a RIFF size of 2147479588 and a
data size of 2147479552, against real sizes of 157712 and 175346 bytes. `ffprobe` warns and
estimates the duration from the bitrate. The cause is in `generate.sh`, which pipes `espeak-ng
--stdout` into a file. Standard output does not seek, so the synthesizer never patches the
placeholder sizes it wrote first. Writing with `-w` instead lets it seek and patch.

**Nothing builds the bytes the probe actually streams.** The script writes wav only, while the test
streams `exchange-24k.pcm` and `keyterm-24k.pcm`. Their provenance is by inspection today, because
no committed step converts one to the other. The script does the conversion, so one command makes
every file beside it.

**The image is pinned by tag, not by content.** `ubuntu:24.04` moves, and `apt-get install
espeak-ng` takes whatever version the index offers that day. The record claims byte for byte
reproduction, which holds only while that version holds. Pin the package version, or the image
digest, or both, and name what was pinned.

**Regeneration must not move the measured bytes.** The probe measured these clips, so a regenerated
pcm that differs byte for byte invalidates the record rather than repairing it. Compare before
committing. If the bytes differ, say why in the record and replace both the pcm and the record's
sizes and durations together.

**Done when:** `ffprobe` reads each wav with no warning and a duration matching the record, 3.58 and
3.98 seconds. Running `generate.sh` twice produces identical bytes for all four files. The
regenerated pcm matches what the live probe streamed, or the record says why it moved.

---

### T0.7: Pin the media tools to the build Keel measured with
```yaml
requires:   T0.1
fixture-ok: yes
size:       S · mid
owns:       .github/workflows/ci.yml, Dockerfile, tools/check.sh
status:     done:4d2c372a3c12a0dd702e1be93b2e58729af70a70
```
T6.1's review recorded the dead install at H severity and out of scope, because
`.github/workflows/ci.yml` belongs to T0.1, which is done. Nobody owns it, so nobody fixes it. The
version is wrong as well as the URL.

**The pinned archive is gone.** The install step fetches `ffmpeg-linux-amd64` and
`ffprobe-linux-amd64` from the `mwader/static-ffmpeg` v7.1 release. Both answer 404, confirmed on
2026-09-19. `curl -fsSL` fails the step, so no run reaches the gate. Nothing has broken yet only
because the repository has no remote, so the workflow has never run.

**Reprise renders with Keel's code, so it measures with Keel's ffmpeg.** `keel/edl`, `keel/ffmpeg`
and `keel/waveform` do the rendering, and Keel pins
`mwader/static-ffmpeg:9.0.1@sha256:54e55b0cb8f672870fc38ceb2e6c411855cb3b39c505f5f3b2505ee01ed5f2b7`
in its own CI, by digest, copied out with `docker create`. Its audio fixtures are byte-reproducible
against that build. Reprise carries 7.1 instead, inherited from an older pattern in another project.
T3.4 then reads loudness within 1 LU and peak deltas at every cut boundary with a tool the library
never validated. Move to Keel's digest, and copy the pair the same way Keel does.

**Pin every place the tool runs.** The workflow, the Dockerfile and `tools/check.sh` all name the
pair today, and only the first two name a version. `check.sh` checks presence alone, so a local gate
measures with whatever ffmpeg the machine holds. Make it refuse a version that is not the pinned one,
because a measurement is comparable only when the measuring tool is identical wherever it runs.

**Done when:** `ffmpeg -version` prints 9.0.1 in the workflow, in the image and under `check.sh`, and
all three name the same digest. `check.sh` fails on a machine whose ffmpeg differs, with a message
naming the pin.

---

## Exit criteria

- [ ] The image builds and serves the shell, and a clean clone passes the gate three times.
- [ ] Every provider fact in `project.md` has a live measurement behind it.
- [ ] The Gemini credential route, the chapter route and the result route are decided.
- [ ] `testdata/sessions/` holds three recorded sessions made from generated speech.
- [ ] Every committed fixture comes from its own script, and `ffprobe` reads it with no warning.
- [ ] CI reaches the gate, and `ffmpeg` is the 9.0.1 build Keel measures with, pinned by digest.

---

## Handoff log

### What exists now

The repository stands on Keel `v0.3.0` and Chaaya `0.2.0`, both pinned exactly. The Go binary serves
the built shell and `GET /healthz` from a distroless image. `tools/check.sh` gates gofmt, vet,
staticcheck, race tests, svelte-check, ESLint, Vitest, Playwright, the runes guard and the two
name scans. `dev-diary/tools/audit_docs.py` lints the planning tree. The round 2 review approved
with zero findings.

The auditor checks requires ids, status values, handoff headings, links and task references.

T0.4 landed on Orchestrator-1's watch: Universal-3.5 Pro measured 52 of 54 word stamps within
150 ms, entities 5 of 5, chapters through the gateway on `qwen3.5-4b-32k-fast`, DELETE confirmed
soft, polling chosen over webhooks, spend under two cents. Landed without loop review: the probe
record carries raw responses, and the owns line gains `doc.go` with the package clause.

T0.3 landed after round 2 approval with zero residue: the file credential reaches Vertex, a 19.5
minute Opus stem plays inline with no bucket, and the laughing take wins the transcript-free
listening test. About 30k tokens across three calls. The probe builds its client inline, so T3.2
still needs a shared client owner for `internal/gemini`.

T0.2 landed after round 1 approval with zero in-scope findings: five live sessions measured the
token ranges, the unprompted greeting, the transcript shapes, the stereo recording, the DELETE
proof, and the keyterm shift. The cap does not end the session, so the browser runs its own timer
and `project.md` records that in the same commit. Spend about 14 cents final run, near 1.02
dollars with exploration. The duplicate `doc.go` resolved to main's copy at landing.

T0.6 landed after round 1 approval with zero findings: the generator pins the Ubuntu digest, the
espeak-ng version, and the ffmpeg digest, writes seekable wavs, and scripts the pcm conversion.
Regenerated pcm matches the streamed bytes exactly, so no record update was needed. ffprobe reads
clean at 3.58 and 3.98 seconds.

### What surprised us
The AssemblyAI docs, read while planning, showed the live user transcript has no word timings and
auto chapters are deprecated. `project.md` records both departures.

### Notes for the next developer
T0.5 starts now that T0.2 landed. The other orchestrator's tree held no dirt at this landing.
