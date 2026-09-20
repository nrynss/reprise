# P7: Production issues

```yaml
id:       P7
size:     M
requires: [T6.1b]
blocks:   P6 close
parallel: partly
```

**Goal:** Close every gap the first production verification measured.
`dev-diary/probes/production.md` round 1 records what the edge does
today: session mint and the live call pass, everything after them is a
stub, a 403, or unwired. Each task below wires one seam and pins it
where the probe can re-measure it.

**Why a new phase:** P1 through P5 built the pieces against fixtures.
The probe proved the binary never assembled them. That is one backlog
with one re-verification at its end, so it gets one phase rather than
five reopened ones.

**Spend warning, carried from the probe.** Each mint reserves the full
session cap (about 2.25 dollars) against the 20 dollar daily ceiling,
and nothing releases it until T7.2 lands. Dogfood sparingly until
then. T7.2 is the first task that matters.

---

### T7.1: Missing API handlers ★
```yaml
requires:   T1.7
fixture-ok: yes
size:       M · mid
owns:       internal/api/, internal/episode/
status:     done:2b8ae36d94b78346aced30b4b00d0ee762c5a751
```
**Build on:** the route table from T1.3 and the mounting shape from
T1.7. The probe measured 501 `not_implemented` on `GET /api/episodes`,
`GET /api/threads`, and by the same stub on decisions, done, and the
session end. `handlerFor` in `internal/api/routes.go` returns nil for
every unwired pattern.

* Implement one handler per stubbed route the table names: episode
  list and detail, decisions accept and revert, mark done, session
  end, and threads. Each one checks the session's user owns the rows
  it reads, the way T1.4 pins identity.
* Mark done starts the render job from T3.4 and nothing else moves an
  episode. Session end records the provider close the browser already
  sent, ready for T7.2 to settle.
* If a handler needs a constructor argument the route registration
  cannot supply, raise a contract change instead of reaching into
  `cmd/reprise/main.go`, which is T7.2's.

**Done when:** The stub refusal golden still decodes for routes with
no handler. Every newly wired route answers its handler through the
middleware chain with passing and refusing sides pinned, the way T1.7
pins mounting. A fixture episode lists, decides, marks done, and
reads back through these handlers with no network.

---

### T7.2: Binary wiring and settle ★
```yaml
requires:   T6.1, T2.3, T3.2
fixture-ok: yes
size:       M · frontier
owns:       cmd/reprise/main.go, internal/broker/, internal/gemini/
status:     done:3e3f219990eafaf2675f170e1779e2dfc12b0f45
```
**Build on:** the job kinds P2 and P3 landed, the reconciler from
T2.3, and the broker from T2.1. The probe measured a binary that
imports no Gemini package, calls no reconciler, and schedules no
pipeline job, so mints hold budget forever and no render can start.

* Construct the shared Gemini client once from the file credential
  T1.6 pinned, and pass it to the editorial, analysis, and cover
  jobs. The model id comes from settings, never from code.
* Schedule the pipeline kinds the phases defined: edit transcript,
  editorial, render, analysis, cover, and memory. Paid kinds stay
  non-idempotent, so a restart marks them `interrupted`, never reruns
  them.
* Call the reconciler after every session end and run the abandoned
  sweep from T2.6 on its schedule, so each mint settles or releases
  and the daily ceiling stops leaking.
* Touch `internal/broker/` and `internal/gemini/` only where the
  binary needs a seam (a caller, a constructor). Library-grade logic
  stays where its phase put it. Anything else is a contract change.

**Done when:** A test boots the wired binary shape and proves every
paid kind is constructed once with the settings model id, a recorded
Sessions API close settles its reservation exactly once, and a
restart mid-pipeline marks the paid kind `interrupted` with the
episode `failed`. No mint stays held after its end plus the sweep
margin.

---

### T7.3: Upload owner and media refusals
```yaml
requires:   T7.2
fixture-ok: yes
size:       S · mid
owns:       cmd/reprise/main.go
status:     done:5bce6110f2d0b0e7d5f95441dcee8e8afa4c7879
```
The probe measured the uploader reading 404 on its own blob: the
browser opens uploads with the literal owner `guest` while the media
authorizer compares the resolved user id. It also measured anon media
refusals with no `Cache-Control: private, no-store`, because Keel
answers refusals with a bare `http.NotFound`.

* Resolve the upload owner from the guest session on open, so the
  authorizer sees the same id it compares. A `curl` without a cookie
  still gets 404 on another user's episode and media.
* Refuse private media with `Cache-Control: private, no-store` on the
  path Reprise controls. If the only seam is inside Keel, write the
  issue up in the handoff (version, reproduction against Keel's API,
  expectation) and take the closest Reprise-side header the review
  accepts. Never patch the library from here.

**Done when:** The T2.4 reload path completes over exactly the
persisted bytes against a server authorizer, pinned by a test that
opens as one user and reads as nobody. An anon media read answers
404 with the header present, pinned the way T1.4 pins refusals.

---

### T7.4: Kill switch live exercise
```yaml
requires:   T5.3, T1.4
fixture-ok: yes
size:       S · mid
owns:       internal/limits/, web/src/routes/admin/
status:     blocked:deferred, owner login ships as its own phase after dogfooding
```
The probe measured pause and limits answering 403 `owner_required`
behind `StubOwnerAuth`, so the kill switch path is covered by broker
unit tests only. The stub stands until the owner login decision in
`dev-diary/project.md` lands. This task waits on it.

* Behind the real owner login, flip the switch and watch the next
  session refuse with `sessions_paused`, then flip it back and watch
  an honest session mint. Record both with commands and output.
* Nothing in this task invents the login. T1.4 owns the seam and the
  decision names its shape.

**Done when:** A live run flips the switch, measures the refusal,
restores minting, and records all three. The site is never left
paused.

---

### T7.5: Production re-verification
```yaml
requires:   T6.1b, T7.1, T7.2, T7.3
fixture-ok: no
size:       S · frontier
owns:       dev-diary/probes/production.md
status:     done:76fd95ed6a52a6a3f0098a6225adc9006fc79ff7
```
Automated, from a workstation, never from the box. No person needed.
Runs once, after P7 lands and the new image deploys.

* Append a round 2 section to `production.md`. Round 1 stays
  untouched. Every check from round 1 re-runs against the new build:
  guest episode, render through gallery, anon media headers, kill
  switch, outbound paths, ledger match.
* Each blocked or mixed item closes with its passing evidence or
  carries forward with its reason in words. A carry opens or names
  its task.
* T7.4 stays out of this round unless the login decision lands first.
  Say so in the record either way.

**Done when:** Round 2 records every check with its command and
output against the deployed P7 build. No check still cites round 1
evidence.

### T7.6: Schedule the edit pipeline on stem completion
```yaml
requires:   T7.1, T7.3, T3.1
fixture-ok: yes
size:       S · frontier
owns:       cmd/reprise/main.go, internal/episode/, internal/store/migrations/0002_stems_pair_unique.sql,
            internal/store/store_test.go
status:     done:0b4ff25af862409f63a3cafbb964f258e695a4bc
```
Round 2 carried one item: the binary registers the transcript and
editorial kinds but schedules neither, and nothing calls the
stems-uploaded transition, so episodes stall in `recording` with zero
proposals. This task closes the pipe from upload to draft.

* When both stems finish uploading, move the episode through the
  guarded `recording` to `draft` transition and schedule exactly one
  `edit_transcript` job. A repeated completion signal schedules
  nothing twice.
* When the transcript job lands the word timeline, schedule one
  `editorial` job. Paid kinds stay non-idempotent through the
  existing registration.
* Touch `internal/episode/` only for the completion entry point.
  Lifecycle and render seams stay as T1.2 and T7.1 left them.

**Done when:** Fixture stems completing twice move the episode to
`draft` once with a word timeline and schedule one transcript job,
and the transcript landing schedules one editorial job. A restart
between completion and schedule recovers to the same single pair.

### T7.7: Check-2 live re-run
```yaml
requires:   T7.6
fixture-ok: no
size:       XS · light
owns:       dev-diary/probes/production.md
status:     done:1485268c5297477ffc0fc87d9d19c58804cb2c2e
```
Runs once, after T7.6 deploys. A scripted guest records from
generated speech, posts stem completion, and follows the episode to
draft with proposals. Appends a round 3 note to `production.md`
without touching rounds 1 or 2. Closes the round 2 carry or carries
it with a reason.

**Done when:** Round 3 records the live draft flow with command and
output, or names the defect that still blocks it.

### T7.8: Bound the deploy drain
```yaml
requires:   T7.2, T6.1
fixture-ok: yes
size:       S · frontier
owns:       cmd/reprise/main.go, deploy/
status:     done:ad93cfb312ea6ce6714528dfdcb8c110c6363d34
```
The `c4c3676` deploy held the workflow 30 minutes: the old container
never exited on SIGTERM, so `docker stop` waited out the full 1830
second timeout before the new build could start. Every future deploy
pays that in pipeline minutes and dead edge time.

* Measure first. Stop the previous build locally with one open
  session, one leaked hold, and one idle server, and record where
  the drain hangs. The drain waits on open sessions through the
  T2.1 registry. Name the waiter with a test, not a guess.
* Bound it. A drain that cannot finish in well under the stop
  timeout must log what it waits on and let go: leaked holds are
  not open sessions and must never hold the process. Keep the
  honest-session finish the T6.1 design promises.
* Make the workflow fail fast. If the gate cannot pass within a few
  minutes of the new container starting, `redeploy.sh` rolls back
  and exits instead of holding the runner. A stuck deploy reads as
  red in minutes, never in half hours.

**Done when:** A local stop with a leaked hold exits in seconds with
the waiter named in the log. A stop with one honest open session
lets it finish. The workflow bounds the gate wait and rolls back on
expiry, pinned by a run that proves the rollback path.

### T7.9: Visible transcript job outcome
```yaml
requires:   T7.1, T7.6, T7.7
fixture-ok: yes
size:       S · frontier
owns:       cmd/reprise/main.go, internal/episode/, internal/api/
status:     done:db0bdb6e476f6b10e8d70e5d8bf0e3f5dbae1723
```
Round 3 carried one defect: transcript passes vanish without landing
words and without a surfaced error, and a repeat completion schedules
again instead of reporting the last job outcome. The pipe runs
silent. This task gives it a voice.

* The completion answer reports the last job outcome for the episode:
  state, job id, and error text when it failed. A repeat completion
  reports the standing outcome instead of scheduling beside a silent
  first, reconciled against the T7.6 no-double-schedule pin in the
  record, not by gut feel.
* Episode detail surfaces the same outcome beside its proposals, so
  the gallery card and the detail agree on what the pipe did.
* Paid kinds keep their registration. No new spend path opens.

**Done when:** A transcript pass that lands nothing moves the episode
to `failed` with its error readable on both the completion answer
and the detail, pinned by fixture. A repeat completion reports the
standing outcome and starts nothing new.

### T7.10: Silent-chain live re-run
```yaml
requires:   T7.9
fixture-ok: no
size:       XS · light
owns:       dev-diary/probes/production.md
status:     done:80b480212498fedc75dcbaebc6bd45f19fb24e0c
```
Runs once, after T7.9 deploys. Repeats the round 3 flow live and
appends a round 4 note without touching earlier rounds. Closes the
round 3 carry or carries it with a reason.

**Done when:** Round 4 records the live outcome with command and
output.

### T7.11: Record page posts stem completion
```yaml
requires:   T7.6, T2.4
fixture-ok: yes
size:       S · mid
owns:       web/src/routes/record/
status:     done:ed6078fa611dbe42312f49acd4461f20d4b4933f
```
Round 1 carried a disclosed gap: no browser caller posts to
`POST /api/episodes/{id}/stems/complete`, so only fixture and curl
flows reach the T7.6 schedule. This task wires the caller.

* When both stem uploads complete, the record page posts the media
  pair with sample rates to the new route and surfaces the answer:
  moved and scheduled, or the standing job outcome on a repeat.
* Failures refuse loudly on the page with a retry. No silent stall.
* Chaaya's uploader stays as T2.4 wired it. This task adds the one
  call after completion, nothing general.

**Done when:** Playwright completes both uploads against the mock
and sees the draft move with one transcript job scheduled. A repeat
post reports the standing outcome. No wall-clock threshold.

### T7.12: Controller posts stem completion
```yaml
requires:   T7.11, T2.4
fixture-ok: yes
size:       XS · light
owns:       web/src/lib/voice/, web/src/routes/record/cap.spec.ts,
            web/src/routes/record/mock-session.spec.ts, web/src/routes/record/+page.svelte
status:     done:7f7361c51f427885f1ad8c67593b9f96d2b2defa
```
Round 1 scoped the gap: nothing in a real take calls the completion
driver. The controller finishes both uploads in `endTake` and
navigates away without posting.

* Wire the controller to post the stored pair after both uploads
  finish and before the processing navigation, reusing the driver
  and its loud retry from T7.11.
* Extend the mock proof so the automatic path fires without a hand
  driven handle.

**Done when:** Playwright ends a mock take and the draft move fires
with no manual handle. A failed post refuses loudly with a working
retry.

### T7.13: Batch transcript model id
```yaml
requires:   T3.1, T7.10
fixture-ok: yes
size:       XS · light
owns:       internal/assemblyai/, internal/settings/
status:     done:50aeb490c568dec87d4549d9ca97762d27776a08
```
Round 4 carried one defect: the batch transcript request sends a
`speech_models` value the provider rejects with 400, naming only
`universal-3-pro`, `universal-2`, and `universal-3-5-pro`. No episode
reaches proposals while that value stands.

* Send a model id the provider accepts on the edit and analysis
  batch paths, with the setting as the single source of the value.
  The T0.4 probe measured Universal-3.5 Pro within 150 ms per word,
  so that id is the default unless a live remeasure says otherwise.
* Pin the accepted set against a recorded provider error shape, so
  the next rename fails in tests instead of on the box.

**Done when:** A fixture transcript request carries the accepted id
from settings, and a test rejects a renamed id against the recorded
400 shape.

### T7.14: Deterministic restart resume tests
```yaml
requires:   T4.4, T5.4
fixture-ok: yes
size:       S · mid
owns:       internal/privacy/, internal/retention/
status:     done:49ff37d3725c61c4928fd2ed47366a3c297d1cf8
```
CI failed twice on restart resume tests that pass in isolation:
`TestEraseRestartResumesAndFinishes` (privacy) and
`TestSweepRestartResumesAndFinishes` (retention). Both race the
runner recovery against job progress writes, and load decides the
winner. A flaky gate blocks every ship, so this is a defect, not a
quarantine candidate.

* Reproduce under repetition first (`-race -count=20` or tighter
  timing). Name the losing interleaving with a failing pin before
  changing anything.
* Fix the synchronization, not the timeout. No sleeps added, no
  wall-clock threshold widened. Quarantine with `test.fixme` only if
  the defect sits in a library, with the reason stating the defect.
* Both packages keep their done criteria: a restart mid-erase and
  mid-sweep resumes and finishes exactly once.

**Done when:** Both tests pass twenty consecutive race runs on CI
shapes, and the gate passes three consecutive full runs in a fresh
worktree.

### T7.15: Full-pipe live proof
```yaml
requires:   T7.13
fixture-ok: no
size:       S · frontier
owns:       dev-diary/probes/production.md
status:     done:562d62b3ae69e786a9320134da00cdb9ba73a225
```
Runs once, against the build carrying the batch model fix. A
scripted guest records from generated speech, posts stem completion,
and follows the episode through draft, transcript words, and
proposals. Appends a round 5 note without touching earlier rounds.

**Done when:** Round 5 records an episode reaching proposals with
command and output, or names the defect that still blocks it with
owning paths.

### T7.16: Gallery reads the live season
```yaml
requires:   T7.1, T4.3
fixture-ok: yes
size:       M · mid
owns:       web/src/routes/+page.svelte, web/src/routes/episode/[id]/+page.svelte,
            web/src/routes/episode/[id]/+page.ts, web/src/routes/threads/
status:     done:e757b4d71a3f20e1dee5db0b7a9bd6cdb30ae719
```
The gallery, episode, and threads views still render the scripted
season T4.3 built against. The live handlers exist since T7.1, but
no view calls them, so the landing page shows stubs on production.

* Gallery lists the owner's real episodes newest first with cover,
  title, state, and duration, falling back to the empty season copy
  when the owner has none. Running jobs still draw live progress on
  their cards through `JobStream`.
* Episode view plays the render with chapters, notes, and a
  following transcript from the wired detail route. Threads link to
  the spoken moment.
* No sentiment gauges or charts. Quotes and links only, as T4.3
  pinned.

**Done when:** Playwright against a stubbed season API shows real
rows newest first with progress on a running card, and opening a
thread item starts playback at its quote. The scripted fixture set
stays for offline runs but no longer renders by default.

### T7.17: Browser reads the live host audio key
```yaml
requires:   T2.4, T7.15
fixture-ok: yes
size:       XS · light
owns:       web/src/lib/voice/
status:     done:8da37dba3ff2560659627d54911d5a515b5c45ed
```
Round 5 proved the pipe with a script reading `data` directly,
because the live socket carries host audio under a `data` key on
`reply.audio` while the browser reads `audio`. The mock emits the
`audio` shape, so every test passes and every real take stores an
empty host stem.

* Read the key the provider sends, with the mock emitting the live
  shape. Both shapes may be accepted during transition, but the
  live shape must be pinned by a test that fails today.
* The host stem lands byte exact against the provider channel, or
  the fix is not proven.

**Done when:** A mock emitting the live shape fills the host stem,
and the recorded bytes match the provider channel on the next live
run.

### T7.18: Loud API failure on the record path
```yaml
requires:   T7.12, T2.4
fixture-ok: yes
size:       XS · light
owns:       web/src/routes/record/
status:     done:9ed933b0a5728eda58cb0ebce0c43cb7d84c118d
```
A real take died silently when Cloudflare answered API calls with a
challenge page: uploads, transcript rows, and session end all failed
while the voice socket worked, and the page showed nothing until the
draft retry. A machine client that cannot parse its answer must say
so at once.

* Every API call on the take path validates its content type before
  trusting the body. A non-JSON answer surfaces a loud banner naming
  the failed call, with retry where retry is safe.
* The take clock stops when the take cannot proceed. No silent retry
  loop burns minutes against a challenge page.

**Done when:** Playwright serves HTML on an API route and the page
shows the named failure instead of stalling. The normal path is
unchanged.

### T7.19: Take-path rate limits fit machine traffic
```yaml
requires:   T2.1, T7.6
fixture-ok: yes
size:       S · mid
owns:       cmd/reprise/main.go
status:     in-progress:review-r2:t7.19-rev-r2@b81bc02894952eba9abcbc6956b2f7169e41f1cd
```
A real take fires chunk uploads, polls, and the completion post
faster than the route limits allow. The edge answers 429 on the
draft move and the take stalls with stored stems. The limits were
tuned for clicks, not takes.

* Shape per-route budgets for the take path (session mint, upload
  open and chunks and complete, job events, session end, stem
  completion) so one honest take with two stems never trips them,
  while a burst abuser still does. Retry hints stay honest.
* If a new settings key is needed, raise a contract change instead
  of editing the TOML files. Prefer the existing limits with
  per-route shaping.

**Done when:** A fixture take at real cadence (chunk per block,
polls running, completion at end) passes the wired gate with zero
429s, and a burst test twice as fast still trips. Both pinned.

### T7.20: Guard the voice-layer calls and stop the clock
```yaml
requires:   T7.18, T7.12
fixture-ok: yes
size:       XS · light
owns:       web/src/lib/voice/
status:     in-progress:implement:t7.20-impl
```
Round 1 scoped two gaps outside T7.18: the mint, chunk upload, and
session end calls run through the unchecked client, and the elapsed
display keeps ticking after a failed completion.

* Route mint, chunks, and session end through content type checked
  calls that name the failed call, with retry where retry is safe.
* Stop the take clock when the take cannot proceed. No silent
  ticking against a dead take.

**Done when:** Playwright serves HTML on mint, chunk, and end routes
and the page names each failure instead of stalling. The clock stops
with the take.

---

## Exit criteria

- [ ] A fixture episode becomes a ready episode through the wired handlers with no network.
- [ ] No mint stays held after its end plus the sweep margin.
- [ ] The uploader reads its own bytes and anon reads carry the header.
- [ ] Round 2 of the production record re-measures every check on the deployed build.

---

## Handoff log

### What exists now

T7.18 landed after round 2 APPROVE with zero residue. Challenge
answers surface by name with a working retry. The voice-layer guard
and clock carry to T7.20.
T7.17 landed after round 2 APPROVE with zero residue. The browser
reads the live host audio key and the float path round trips every
int16 code exactly.
T7.15 landed after round 2 APPROVE with zero residue. Round 5 proves
the full pipe live with proposals on detail. Every verification
carry is closed.
T7.16 landed after round 1 APPROVE with zero in-scope findings.
Views read the live season with fixtures behind `?fixture=1`. The
prerender opt-out widened owns by decision. Detail enrichment
(audio, chapters, words, cover, seeking) carries to a follow-up.
T7.14 landed after round 1 APPROVE with the lone L stripped under
the orchestrator exemption. Resume tests cancel the superseded
attempt before release and judge the latest attempt. The gate stops
flaking.
T7.13 landed after round 2 APPROVE with zero residue. Both batch
paths send the accepted dashed id from the dotted setting, pinned
against the recorded 400 shape.
T7.12 landed after round 2 APPROVE with zero residue. Takes post
stem completion automatically with a visible retry on failure, and
the sibling specs pin the new navigation contract.
T7.11 landed after round 1 APPROVE with zero in-scope findings. The
driver, alert, retry, and proofs are correct. The production trigger
carries to T7.12 on the controller path.
T7.9 landed after round 1 APPROVE with zero findings. Completion and
detail report the last transcript outcome, empty passes fail, repeats
report standing outcome. The pipe has a voice.
T7.8 landed after round 1 APPROVE with zero in-scope findings. The
drain reclaims expired holds each second and names its waiter.
Rollback reads red in about a minute. Deploys stop costing half
hours.
T7.7 landed after round 2 APPROVE with zero residue. Round 3 stands:
draft passes live, the silent transcript chain carries to T7.9 with
its defect named.
T7.6 landed after round 2 APPROVE with zero residue. Stem completion
moves recording to draft and chains one transcript job to one
editorial job, with per-episode cover, 429 on a full queue, and one
stem pair under concurrency. Round 2 widened owns by decision to the
new store migration plus its ledger assertion, since the landed
schema cannot change in place. The browser still posts nothing to
the new route. Only T7.4 (blocked) remains.
T7.5 landed after round 2 APPROVE with zero residue.
Keel issue nrynss/keel#1 tracks the library header.
T7.3 landed after round 1 APPROVE with zero findings.
T7.2 landed after round 1 APPROVE with zero findings. Shared
Gemini client, eight kinds, reconcile per close, sweep on schedule.
The ceiling stops leaking.
T7.1 landed after round 1 APPROVE with zero findings. Episode list,
detail, decisions, done, session end, and threads answer behind owner
checks with method plus pattern routing. `main.go` construction of the
new handlers waits on T7.2, which runs now.
Round 1 of `dev-diary/probes/production.md` measured the first
deploy: mint and live call pass, everything after them is unwired.
T7.1 wires the handlers, T7.2 wires the binary and the settle, T7.3
fixes upload ownership and refusal headers, T7.4 waits on the owner
login, T7.5 re-verifies. Each mint holds about 2.25 dollars until
T7.2 lands, so dogfood sparingly.

### What surprised us

Nothing yet.

### Notes for the next developer

T7.4 is deferred, not merely waiting: owner login plus the live
switch exercise ships as its own phase after dogfooding, not as a
leftover row here.
Owner login lands after dogfooding, so T7.4 waits past the
re-verification. T7.5 records that sequencing and skips the switch
flip until the login exists.
T7.1 and T7.2 own disjoint paths and may start together. T7.3 waits
on T7.2 for the shared `main.go`. T7.4 is blocked on the owner login
decision, not on code. T7.5 appends round 2 and never rewrites round 1.
