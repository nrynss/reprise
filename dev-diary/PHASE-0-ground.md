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
status:     in-progress:implement:t0.2-impl
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
status:     not-started
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
status:     in-progress:implement:t0.4-impl
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
status:     not-started
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

## Exit criteria

- [ ] The image builds and serves the shell, and a clean clone passes the gate three times.
- [ ] Every provider fact in `project.md` has a live measurement behind it.
- [ ] The Gemini credential route, the chapter route and the result route are decided.
- [ ] `testdata/sessions/` holds three recorded sessions made from generated speech.

---

## Handoff log

### What exists now

The repository stands on Keel `v0.3.0` and Chaaya `0.2.0`, both pinned exactly. The Go binary serves
the built shell and `GET /healthz` from a distroless image. `tools/check.sh` gates gofmt, vet,
staticcheck, race tests, svelte-check, ESLint, Vitest, Playwright, the runes guard and the two
name scans. `dev-diary/tools/audit_docs.py` lints the planning tree. The round 2 review approved
with zero findings.

Round 1 caught a real gap: the gate needed `svelte-kit sync` before type checks on a clean tree.
The auditor checks requires ids, status values, handoff headings, links and task references.

### What surprised us
The AssemblyAI docs, read while planning, showed the live user transcript has no word timings and
auto chapters are deprecated. `project.md` records both departures.

### Notes for the next developer
T1.5 is done, so settings load. T0.2 and T0.4 start when `ASSEMBLYAI_API_KEY` lands in the local
env file. T0.3 starts when T1.6 lands and the Vertex key file exists at the path it configures.
T0.5 starts when T0.2 lands.
