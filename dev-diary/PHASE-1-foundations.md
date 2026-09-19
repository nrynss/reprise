# P1: Foundations

```yaml
id:       P1
size:     M
requires: [T0.1]
blocks:   P2, P3, P4, P5
parallel: partly
```

**Goal:** Freeze the configuration, the data model, the episode lifecycle, the API surface, and
guest identity. Every later task compiles against these.

---

### T1.5: Settings and secrets ★
```yaml
requires:   T0.1
fixture-ok: yes
size:       S · mid
owns:       internal/settings/, config/reprise.local.toml, config/reprise.box.toml, .env.example
status:     done:e9d2aa693614e1213ce0f03a476444a88eb8ed9b
```
**Build on:** `keel/config` and `keel/config/source`. Keel owns loading, precedence,
validation and the resolution plan. Reprise owns only its settings struct.

One struct, one file per environment, loaded once in `main` and passed down.

* **Settings inline.** Model ids, the session cap, guest quotas, the daily spend ceiling, the render
  concurrency, the data and media paths, the public origin.
* **Secrets as references.** `assemblyai_api_key`, `gemini_credential` and `session_signing_key`.
  Locally each reads `/home/nryn/work/reprise/.env` through an `env_file` source. On the box each
  reads `/etc/reprise/env` through the same source. T1.6 moves the Gemini one to a `file` source
  after the Vertex decision, because a service account key is a file rather than a string.
* **File selection.** `config.Config.PathVar` is `REPRISE_CONFIG`. `Search` falls back to
  `config/reprise.local.toml`.
* **The plan prints at boot.** `Plan.String()` goes to the log on start, so the running process says
  which source each secret came from and never shows a value.
* **`.env.example`** lists the three variable names with empty values.

Keel's loader already refuses a loose secrets file, an inline secret, an unknown key and a quoted
env value. Do not re-implement those checks. Test that they reach Reprise's startup.

**Done when:** Starting with a missing secret stops the process and names the key, its source and its
path. Starting with a group-readable `.env` stops the process. The boot log shows the plan and a
probe finds no secret value anywhere in it. Both TOML files are tracked and hold no value.

---

### T1.6: The Gemini credential becomes a service account file
```yaml
requires:   T1.5
fixture-ok: yes
size:       S · mid
owns:       internal/settings/, config/reprise.local.toml, config/reprise.box.toml, .env.example
status:     done:ce6b8011acc2556d0c0b5438ec54a0b28edd7fa1
```
**Read first:** `dev-diary/project.md`, "Gemini reaches Reprise through Vertex AI", which holds why.

T1.5 landed every secret as an `env_file` reference, which fits a key string. Vertex authenticates a
service account, so the Gemini credential is a JSON key file and its reference has to change with it.

* `[secrets.gemini_credential]` becomes a `file` source naming the key path. Locally that is
  `/home/nryn/.config/reprise/gemini-sa.json`, outside the repository so no ignore rule stands
  between the key and a commit. On the box it is `/etc/reprise/gemini-sa.json`. Both are mode `0600`.
* The key belongs to `reprise-vertex@nryn-personal.iam.gserviceaccount.com`, which holds
  `roles/aiplatform.user` and nothing else.
* `vertex_project` and `vertex_location` join the inline settings, `nryn-personal` and the region
  T0.3 measures against. Neither is a secret, so neither becomes a reference.
* `.env.example` drops `GEMINI_CREDENTIAL`, because nothing reads it any more. The other two names
  stay.
* The boot plan still names the source of every secret and still prints no value, which is the
  property T1.5 pinned and this task must not lose.

**Done when:** **Passes:** a readable key file at the configured path boots, and the plan line for
`gemini_credential` names the `file` source and its path. **Refuses:** a missing file, and a
group-readable file, each stop the process and name the key, the source and the path. A probe over
the whole boot log finds no fragment of the key's contents. `.env.example` and both TOML files agree
on the names the code reads, pinned by a test rather than by eye.

---

### T1.6a: Pin the box config and the credential source
```yaml
requires:   T1.5
fixture-ok: yes
size:       XS · light
owns:       internal/settings/settings_test.go
status:     done:1026cb0e3b1e9bea18fd1b5c8c07e67f9c6f6b8c
```
The review of the landed service account change found two thinned pins. The file that
checks the shipped configs reads only the local one, so box-only drift stays green. The
boot log check wants the string `file`, which every inline row already carries. The runtime
behavior is right. Only the pins are thin.

* `TestShippedFilesAgreeOnSecretNames` opens `config/reprise.box.toml` too, with its
  temp paths rewritten the same way, and fails on a box-only rename.
* `TestBootLogShowsPlanWithoutValues` asserts the credential plan line carries the key
  path, and fails when the credential points at any other source.

**Done when:** Renaming one box variable fails the agreement test, and pointing the
credential at another source fails the boot log test. Both failures name the defect
they pin.

---

### T1.1: Schema ★
```yaml
requires:   T0.1
fixture-ok: yes
size:       M · frontier
owns:       internal/store/, internal/store/migrations/
status:     done:fd9f5319d973dcbe4d982f0ae70aebb44468e860
```
**Build on:** `keel/sqlite`. Reprise migrates under its own namespace with `sqlite.Migrate`. Keel's
job, media, upload and cost stores keep their own namespaces in the same file.

| Table | Holds |
|---|---|
| `users` | Guest and owner rows. Kind, created time, last seen. |
| `episodes` | Owner, number, title, state, visibility, share token, seeded flag. |
| `sessions` | The AssemblyAI session id, token cap, connected seconds reported by the provider. |
| `stems` | Media id, role (user or host), sample rate, start offset on the episode clock. |
| `turns` | The live conversation: role, text, local clock times, provider item id. |
| `words` | Timed words with a source: `edit` (user stem batch plus host deltas) or `rendered`. |
| `proposals` | Model proposals: kind (cut, cold open, title, notes, callback), word range, reason. Append-only. |
| `decisions` | The user's accept or revert on each proposal. Append-only. |
| `renders` | Input hash, media ids for Opus and AAC, loudness measured. |
| `analyses` | Provider transcript id, chapters, summary, entities, key phrases. |
| `mentions` | Person, place, topic or commitment, with episode, word offset, and quote. |
| `callbacks` | The mention chosen to open the next episode, and whether the host used it. |

Two kinds of state are deliberately absent. Owner spend lives in `keel/cost/sqlitestore`, whose
keyed budget holds a ceiling and the headroom left per owner. Runtime switches live in
`keel/flag/sqlitestore`. Both keep their own namespaces in this same SQLite file, so a second table
here would be a second source of truth for state a library already owns.

Every row that holds diary content carries `owner_id` and cascades on episode delete.

**Done when:** Migrations apply twice with no change. A test deletes an episode and a fresh
connection finds no row of its content in any table.

---

### T1.2: Episode lifecycle ★
```yaml
requires:   T1.1
fixture-ok: yes
size:       S · frontier
owns:       internal/episode/lifecycle.go, internal/episode/lifecycle_test.go
status:     done:33ee3270a8c5f86085f41a21b4a6e17d43c22ac1
```
The states are `recording`, `draft`, `rendering`, `analysing`, `ready` and `failed`. Visibility is
separate, and always starts `private`.

| From | To | Trigger |
|---|---|---|
| none | `recording` | A session starts |
| `recording` | `draft` | Both stems finish uploading, confirmed by `keel/upload` completion |
| `draft` | `rendering` | The user marks the episode done. Nothing else moves it. |
| `rendering` | `analysing` | The render job finishes |
| `analysing` | `ready` | The analysis and memory jobs finish |
| any | `failed` | A job ends failed or `interrupted`. The episode keeps every stem. |
| `failed` | the failed step | The user retries |

Transitions are a single SQL update guarded on the current state, so a repeated click is harmless.
An `interrupted` job from a restart moves its episode to `failed`, never to a silent rerun.

**Done when:** A table test tries every pair of states and only the rows above succeed. Two
concurrent "mark done" requests start one render.

---

### T1.3: API surface
```yaml
requires:   T1.1
fixture-ok: yes
size:       S · mid
owns:       internal/api/routes.go, internal/api/routes_test.go, web/src/lib/api/types.ts,
            web/src/lib/api/types.test.ts, web/src/lib/api/testdata/
status:     done:fffc7756cd57177d1c16e7b32a7d96ff43d858df
```
**Build on:** `keel/wire` for every error and event. Chaaya's `api` client and `wire` parsers on the
browser side read the same shapes, so Reprise writes no envelope code of its own.

List every route before handlers exist. Human pages are singular. API routes are plural and live
under `/api`.

| Route | Purpose |
|---|---|
| `POST /api/sessions` | Start a session. Returns a token and session config. |
| `POST /api/sessions/{id}/end` | Record the end. The browser has already sent `session.end`. |
| `/api/uploads/...` | `keel/upload` mounted with `BasePath` `/api/uploads` |
| `GET /api/episodes`, `GET /api/episodes/{id}` | Gallery and detail |
| `POST /api/episodes/{id}/decisions` | Accept or revert a proposal |
| `POST /api/episodes/{id}/done` | Mark done |
| `GET /api/jobs/{id}/events` | `stream.Broker.ServeTopic` on `job.Topic(id)` |
| `POST /api/episodes/{id}/publish`, `DELETE .../publish` | Visibility |
| `DELETE /api/episodes/{id}` | Erase |
| `GET /api/threads` | The thread across episodes |
| `GET /media/{id}` | `keel/mediastore` with Reprise's authorizer |

TypeScript types for Reprise's own payloads mirror the Go handlers by hand, checked by a test that
decodes Go-written golden responses.

**Done when:** The route table is registered with stub handlers that answer `not_implemented`
through `wire.WriteError`. The golden decode test passes, and Chaaya's `parseErrorEnvelope` reads a
stub refusal.

---

### T1.4: Guest identity ★
```yaml
requires:   T1.1
fixture-ok: yes
size:       M · frontier
owns:       internal/identity/
status:     done:889add00f9e484214688164423b076aaa8576c66
```
Keel lists users as a non-goal, and that boundary holds, so identity is Reprise's.

A first visit creates a guest user row and a server-side session in SQLite. The browser holds a
signed, `HttpOnly`, `SameSite=Lax`, `Secure` cookie carrying only the session id. The signing key is
the `session_signing_key` secret from T1.5.

* Every handler that reads diary content checks the session's user owns it.
* `mediastore.Config.Authorize` is the same check, so a private blob never serves to anyone else.
  A refused request gets 404, which Keel already does.
* Revoking a session takes effect on the next request.
* The owner's own login waits on the open decision. Guest mode does not.

**Done when:** Both sides are pinned, because a handler that 404s everyone satisfies the refusals
alone. **Passes:** a first visit creates a guest and its session, and that cookie reads its own
episode and plays its own media, including a Range request. **Refuses:** `curl` without a cookie
gets 404 on another user's episode and its media, and a revoked session fails its next request.

---

### T1.7: Route mounting and main wiring ★
```yaml
requires:   T1.3, T2.1, T1.4, T5.3
fixture-ok: yes
size:       S · mid
owns:       internal/api/routes.go, web/src/lib/api/types.ts, cmd/reprise/main.go
status:     done:e4ff7aefcc68ec7fc8b77576fdd9681b2f14a3c2
```
Opened from T5.3 round 1's out-of-scope row. Handlers exist unmounted: the broker's
`POST /api/sessions` (T2.1) and the three admin patterns (T5.3) have no route table entry and
no browser mirror entry, so the served app 404s them. T1.3's stubs hold the table shape.

* Mount every implemented handler behind the middleware chain the handoffs describe: gate
  outermost, then guest session middleware, then the handler. `OwnerDefaultLimit` must equal
  the broker `OwnerSessionLimit` where the two meet.
* Extend the TypeScript mirror for every newly mounted pattern, with the golden decode test
  covering the additions.
* `main.go` stays the T6.1 shape (settings load, drain, health). This task adds route
  registration only, touching nothing about boot, drain, or the image.

**Done when:** The stub refusal golden still decodes for unmounted routes, every mounted route
answers its handler through the middleware chain, and an unmounted admin pattern no longer
exists for implemented handlers.

---

## Exit criteria

- [ ] Settings load per environment, and no secret value sits in a tracked file.
- [ ] The schema migrates and erases cleanly.
- [ ] Only the listed lifecycle transitions succeed.
- [ ] Every route exists as a stub with typed errors.
- [ ] Guests get isolated sessions, verified with `curl`.

---

## Handoff log

### What exists now

Settings load once per environment through Keel. `internal/settings` holds the struct and the plan
exposure. The diary schema migrates under the `reprise` namespace with twelve tables, and episode
delete cascades across every content table with the owner surviving. The round 2 review approved
T1.1 with zero findings. The missing `main` wiring is a contract change held for the task that
next owns that file. T1.6a strengthened both thinned pins, and its round 1 review
approved with zero findings, so the box config and the credential source stay pinned. T1.2 landed
(Orchestrator-2) after a round 1 APPROVE with zero findings. The rebase onto current main kept the
gate green. T1.3 landed (Orchestrator-2) after a round 1 APPROVE with zero findings. Its owns
 line now covers the golden companions the done criteria demand. The rebase onto current main kept
 the gate green. T1.4 landed (Orchestrator-2) after a round 1 APPROVE with zero findings. The
 diff touches only its owned directory, so no owns change was needed. The rebase onto current main
 kept the gate green. T1.7 landed (Orchestrator-2) after round 1 APPROVE with zero in-scope
 findings. Every implemented handler now mounts behind gate, guest session, handler, with the
 owner-limit equality runtime-guarded and the TS mirror extended. Two out-of-scope rows stand:
 the nil drain registry (T6.1's) and the stub end route (T2.3 candidate). P1 is fully done.

### What surprised us

`keel/sqlite` pulls `modernc.org/sqlite` transitively, so the first import of it needs `go mod
tidy` even when the direct version stays pinned. Keel's migration ledger names itself after the
namespace, and each migration file runs inside one transaction with no `BEGIN` or `COMMIT` of its
own.

### Notes for the next developer
T1.5 sits first in this file because the probes in P0 need it. Its number stays 1.5 so earlier
references keep their meaning.
