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
status:     done:ddb47aef4de1a8f5fdf7855b5e311c515a1be60d
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
status:     done:250d8adbf4126468ce9bc8ea10145de4e61551e0
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

### T7.21: BindRunner race in resume tests
```yaml
requires:   T7.14
fixture-ok: yes
size:       XS · light
owns:       internal/retention/, internal/privacy/
status:     done:8d9ff10d67dbd1b534a8535979c8b30a91027d5c
```
CI caught a data race T7.14 missed: `retention.Service.run()` reads
a field at `sweep.go:157` while the test writes it through
`BindRunner` at `retention.go:148`, because the old runner's resumed
job still runs when the test rebinds. A race is a C.

* Break the sharing: the resumed job must not read service fields
  the test writes, or the rebind must wait for the old runner to
  stop. Same pattern in privacy if it shares the shape.
* Reproduce with the CI report first, then fix. No sleeps, no
  widened thresholds.

**Done when:** The exact CI race report no longer reproduces and
both resume tests pass twenty consecutive race runs.

### T7.22: Session end accepts the empty close
```yaml
requires:   T7.1, T7.12
fixture-ok: yes
size:       XS · light
owns:       internal/api/, web/src/lib/voice/
status:     done:c82992755154cbc8e3f3fa7c67765303399fe2c3
```
Measured on two real takes: the browser posts `POST
/api/sessions/{id}/end` with an empty body and the server answers
400, so no take ever records its close. The page names the failure
and retries, and the retry fails the same way.

* Accept the empty close on the server, or send the close record
  from the browser. One side moves, not both. The recorded close
  still carries what the reconciler needs.
* Pin both shapes: empty posts 200 and records, malformed posts
  refuse loudly.

**Done when:** An empty end post records the close and a repeat
reports it, pinned by test.

### T7.23: Chunk 429s on real takes
```yaml
requires:   T7.19
fixture-ok: yes
size:       S · mid
owns:       cmd/reprise/main.go
status:     done:6d606b1de2195e4fc6e2083914553103f654dc32
```
Measured on two real takes: sparse chunk PUTs answer 429 (indices
0, 2, 5, 11 across takes), stalling the draft move. T7.19 budgets
are live on the edge, so either the app gate still trips real
cadence or Cloudflare answers 429 itself. Unattributed: the
response bodies were never captured.

* Capture a 429 body first. JSON `rate_limited` means the app gate
  and the fix lands here. HTML means Cloudflare rate limiting and
  the fix is a dashboard rule, not code.
* Whichever side answers, one honest take passes with zero 429s
  and the proof pins it.

**Done when:** The blamed side is named with its body on record,
and the fixed side pins an honest take with zero 429s.

### T7.24: Record page navigates back
```yaml
requires:   T7.12
fixture-ok: yes
size:       XS · light
owns:       web/src/routes/record/
status:     done:e118730f41c474c9dc1d58256ef73b24b36c7323
```
Measured live: the ending page strands the guest with no way back
to the gallery or a fresh take. A stuck page must always offer an
exit.

* Add a plain link back to the gallery on every record phase,
  including the failure states. No gesture needed.

**Done when:** Playwright reaches gallery from each record phase by
link.

### T7.25: A finished draft move stays on the gallery
```yaml
requires:   T7.22, T7.24
fixture-ok: yes
size:       XS · light
owns:       web/src/lib/voice/
status:     done:feecda1aa83b7223aadd0853bab2a767e63ef8cf
```
T7.24 round 1 recorded one out of scope M. The gallery link does
reach `/` during an in-flight draft move. `endTake` keeps running.
When the move succeeds, `goProcessing` assigns `/processing`.
That page has no way back.

* If the guest has already left the record page, do not assign
  `/processing` afterwards.
* Pin it the way the review probe did. Hold the draft move, click
  the gallery link, then fulfill the move. The URL stays `/`.

**Done when:** That probe stays on `/`, and a move that finishes
while the guest is still on the record page still reaches processing.

### T7.26: Accept a raw PCM stem upload
```yaml
requires:   T7.23
fixture-ok: yes
size:       S · mid
owns:       cmd/reprise/main.go
status:     done:8947687c56eee16dbb7f3e871eecc3adfdf12e34
```
A real Chrome take opened both stems as `audio/pcm`. The upload
allowlist refused that type with `unsupported_type`. No blob was
stored. Retry then answered `stems_not_found`, because those ids
were never saved.

* Add `audio/pcm` to the closed content type set. A type still
  outside the set still answers `unsupported_type`.
* The stored bytes are raw signed 16-bit mono. The sample rate is
  the rate already stored on the stem row. The file `ffmpeg` probes
  must carry a header built from those bytes and that rate. A raw
  file with no header is not a WAV.
* One open of `audio/pcm` stores. The draft move links both stems.
  The duration probe reads the stored rate.

**Done when:** A fixture take opens both stems as `audio/pcm`, the
draft move links them, and the duration probe reports the stored rate.

### T7.27: A WebKit browser can start a take
```yaml
requires:   T7.25
fixture-ok: yes
size:       S · mid
owns:       web/src/lib/voice/, web/tests/, web/playwright.config.ts, web/src/routes/welcome/welcome.playwright.config.ts, .github/workflows/ci.yml
status:     done:a9f12371f3f76ff3ee5a96b65bc0e72ee69261db
```
The record page mints a session before it opens the audio context
or the microphone. WebKit treats that first wait as the end of the
click, so the context stays suspended and the microphone request is
no longer a gesture. Chromium still allows both. The welcome teaser
was specified to play on WebKit after the first gesture, and that
proof has never run on a machine that can launch WebKit.

* Inside the start click, before any wait, open the audio context,
  resume it, and start the microphone. The session mint follows.
  A mock take still opens no microphone.
* Playwright WebKit holds the session mint. The context exists and
  the microphone has been requested before that mint returns. The
  same proof passes on Chromium.
* The gate installs the WebKit browser beside Chromium.
* The welcome teaser stays on its existing proof. Chaaya 0.2.0
  primes playback with a silent clip that WebKit rejects. That
  defect belongs to the library. This task does not edit the
  welcome page to hide it.

**Done when:** The WebKit and Chromium start proofs pass, and a
mock take still reaches the gallery and processing as it does today.

### T7.28: A live draft can be heard
```yaml
requires:   T7.26, T7.1
fixture-ok: yes
size:       M · mid
owns:       web/src/routes/+page.svelte, web/src/routes/episode/, web/src/lib/editor/draft.ts, web/src/routes/threads/threads.ts, internal/api/episodes.go, internal/episode/playback.go
status:     done:02e5823d4b4dd4acb6cf2a6bf62d4b89a604e828
```
A real take reached draft. The transcript pass is done and the
proposals are stored. The gallery opens that card on the episode
detail. The detail leaves the audio address empty for every live
episode, and it has no link to the editor. The editor is where a
draft is heard, and the gallery opens it only for a fixture draft.
Opening the editor by hand reads words and an audio address the
detail endpoint does not send, then shows a scripted sample.

* A live draft card opens the editor. The detail of a draft links
  there too.
* The detail endpoint returns the stored words and a playable
  address for the stored stems. The editor plays that audio and
  shows those words.
* Once a render exists, the detail page plays that rendered file.
  A draft with no render still says playback waits, and it still
  offers the editor.

**Done when:** A fixture draft with stored words and stems plays
in the editor from the gallery, and a detail whose render exists
plays that file.

### T7.29: A rendered episode reaches ready
```yaml
requires:   T7.26, T7.28
fixture-ok: yes
size:       L · frontier
owns:       cmd/reprise/, internal/episode/, internal/analysis/, internal/api/episodes.go, internal/api/respond.go, web/src/routes/threads/threads.ts, web/src/lib/editor/draft.ts
status:     done:a4700b7
```
Mark done starts the render job and nothing after it. No caller
runs `MarkRendered` or `MarkReady`. `analysisFunc`, `coverFunc` and
`memoryFunc` in `cmd/reprise/main.go` build their work, but nothing
starts them. A live episode stays `rendering` forever. It never gets
chapters, notes, a cover or marked commitments. The host therefore has
no stored mention to call back to, which is the beat the demo sells.

* A finished render moves the episode to `analysing` and starts the
  analysis, cover and memory jobs once each.
* The episode moves to `ready` when analysis and memory finish. A
  cover failure leaves a plain cover and does not hold the episode.
* An editorial or analysis failure still ships a playable episode
  with a plain title.
* Each paid kind reserves budget first. A restart marks an
  interrupted paid job `interrupted` and never reruns it.
* The render job carries its episode, and the detail reports the
  render and analysis outcome. A rendering or analysing gallery card
  shows that pass, never "Ready", until the episode is `ready`.
* The stored rendered words name the render they came from. The
  detail endpoint sends them only when they match the newest render,
  so a new render never plays under old words.
* The binary takes the editorial kind name from `episode.EditorialKind`
  instead of its own literal, so the two cannot drift.
* Split the wiring so the next three tasks can run side by side. Add
  `cmd/reprise/privacy.go`, `cmd/reprise/seed.go` and
  `cmd/reprise/export.go`. Each file holds two empty hooks that
  `main.go` already calls.
* The first hook returns the job kinds that feature registers, and
  `main.go` merges them before the runner opens. A kind name that
  appears twice refuses the boot.
* The second hook mounts that feature's routes. It receives one
  wiring struct with the mux, the spend gate, the outer rule, the
  guest middleware, the database, the runner and the media store.
* After this task, a feature wires itself by editing only its own
  file. Nothing else in `cmd/reprise/` needs to change.

**Done when:** A fixture episode goes from mark done to `ready`
through the wired binary with no network. Its chapters, cover and
commitments are stored rows. A second mark done starts no second
chain. The three hook files exist, `main.go` calls every hook, and
a test proves a duplicate kind name refuses the boot.

### T7.30: Publish, erase and share reach the binary
```yaml
requires:   T7.28, T7.29
fixture-ok: yes
size:       L · frontier
owns:       cmd/reprise/privacy.go, internal/api/routes.go, internal/privacy/, internal/retention/, web/src/routes/share/, web/src/routes/threads/threads.ts, web/src/routes/episode/[id]/+page.svelte
status:     done:ed0c802a71ea0c261ba56bcafb5a524b03f79793
```
The binary never imports `internal/privacy` or `internal/retention`.
Publish, revoke and erase still answer 501 from the stub. No route
serves `/api/share/{token}`, so the share page receives the app shell.
The episode page publish control only flips a fixture flag. Nothing
schedules the guest retention sweep, so guest data never expires
after the 90 day window.

* Mount publish, revoke and erase behind the gate and the guest
  middleware, scoped to the owner.
* Serve the share metadata and share cover to a caller without a
  session, only while the episode is published.
* The episode page publish and erase controls call those routes and
  show their real outcome.
* Register the erasure and retention sweep kinds, and run the sweep on
  a schedule.
* `retention.Keep` moves a kept episode to the owner but misses the
  `covers` and `resolutions` rows and the rendered source link, so
  the guest delete that follows fails on a foreign key.
* `internal/api/routes.go` registers stubs on the publish and erase
  patterns. Mounting them again from the privacy hook panics, so drop
  those stubs when the real routes land.

**Done when:** A `curl` with no cookie reads a published episode
through its share token and gets 404 after revoke. An erased episode
leaves no row, no media file and no provider transcript. The sweep
removes a guest past the window in a fixture run.

### T7.31: New guests receive the seeded season
```yaml
requires:   T7.29
fixture-ok: yes
size:       M · mid
owns:       cmd/reprise/seed.go, internal/seed/, web/src/routes/welcome/
status:     done:d785b906c2b73610e625e79d862c364c80d7590f
```
The binary never imports `internal/seed`. Nothing syncs `data/season/`
into catalog rows, and nothing calls `EnsureCopy` for a new guest. The
welcome page reads its teaser from a constant and picks empty or
seeded from the query string.

* Sync the catalog at boot. A validation failure refuses the boot by
  name.
* Copy the catalog to each new guest once, on the first season read.
  The owner gets no copy.
* The welcome page reads the real catalog state and teaser episode.
* A seeded copy that adds a render row also adds its render link row,
  or the detail never sends that episode's rendered words.

**Done when:** A fixture catalog of one episode appears in a new
guest's gallery once, carries the seeded flag, and its mention feeds
the host's opening. An empty catalog still renders the record button.

### T7.32: Export reaches the binary
```yaml
requires:   T7.28, T7.29, T7.30
fixture-ok: yes
size:       M · mid
owns:       cmd/reprise/export.go, internal/export/, web/src/routes/episode/, web/src/routes/threads/threads.ts
status:     done:c4169dd71c914c32ae96b0eabcaa453bb8c37a47
```
The binary never imports `internal/export`. No route serves an export
bundle. The episode page export control says it needs the backend.

* Serve the export bundle for a `ready` episode the owner holds.
* The episode page export control downloads it.

**Done when:** A fixture `ready` episode exports audio, video,
captions, cover and a chapter description. `ffprobe` reads the audio
and video, and a stranger gets 404.

### T7.33: The e2e run covers specs beside their pages
```yaml
requires:   T7.28
fixture-ok: yes
size:       S · light
owns:       web/playwright.config.ts, web/package.json, web/src/routes/record/mock.playwright.config.ts
status:     done:173d70eb14551af94c7a3d443a60b72cdbceef34
```
Several Playwright specs sit beside the page they test, each with its
own config, such as `web/src/routes/gallery.spec.ts`. `npm run
test:e2e` reads only `web/tests/`, so the gate never runs them.

* `npm run test:e2e` runs every spec beside a page, in Chromium and
  WebKit where its own config asks for both.
* The per-page configs stay usable on their own, or fold into the
  main config with no lost setting.
* The record mock config loads only Playwright specs. Today it also
  loads the vitest files beside it and exits 1.

**Done when:** Breaking the gallery page makes `./tools/check.sh`
fail, and a fresh worktree passes the gate three times in a row.

### T7.34: The gate passes the editor draft
```yaml
requires:   T7.28
fixture-ok: yes
size:       XS · light
owns:       web/src/lib/editor/draft.ts, web/src/lib/editor/draft.prototype-keys.test.ts
status:     done:72bee67feb5b4de675de9a616245a27016b7271f
```
The svelte 4 leakage scan in `tools/check.sh` matches `get(`, and
`draft.ts` calls `Map.get` in `proposalIdForCut`. CI fails on
`main` at that scan, so nothing deploys.

* Read the cut proposal without a call the scan reads as a store
  `get`. Behaviour stays the same.

**Done when:** `git grep` with the scan's own pattern finds nothing
under `web/src`, and the editor tests still pass.

### T7.35: The render kind follows its settings
```yaml
requires:   T7.29
fixture-ok: yes
size:       S · light
owns:       internal/render/, internal/settings/, cmd/reprise/finish.go, cmd/reprise/main.go, cmd/reprise/main_test.go
status:     done:c8c2bd6f7978a1e7fb5fe4e8cf6997333828187a
```
Both config files set `RenderConcurrency`, and nothing reads it, so
renders run one at a time whatever the setting says. `render.KindOf`
also sets no `MaxAttempts`, so a render never resumes after a restart
unless the binary patches the kind.

* The render kind limit comes from `RenderConcurrency`. A value below
  one refuses the boot by name.
* `render.KindOf` sets its own attempt cap, and the binary drops its
  workaround.

**Done when:** A test boots with a concurrency of two and runs two
renders at once. An interrupted render resumes through the kind alone.

### T7.36: The host stem keeps the call's clock, and a closed tab reports its end ★
```yaml
requires:   T7.26, T7.27
fixture-ok: yes
size:       L · frontier
owns:       web/src/lib/voice/record-state.ts, web/src/lib/voice/host-stem.ts, web/src/lib/voice/host-stem.test.ts, web/src/lib/voice/session-calls.ts, web/src/lib/voice/socket.ts, web/package.json, web/package-lock.json, README.md
status:     done:a19b82714a08d0b74e322554798be204a6cc7470
```
The host stem appends each block as it arrives, so the silence between
replies never reaches it. A live Chrome take put host speech at 4.66,
12.79, 14.22 and 22.61 seconds on the call. The stored stem carries
it at 0.34, 3.13, 4.80 and 7.58 seconds, with offset zero. The render
delays the whole stem by that one offset, so every later reply plays
early and over the guest. Audio that arrives after an interruption
flush also lands in the stem, though the guest never heard it.

On `pagehide` the page sends `session.end` on the socket, and Chaaya's
`SessionGuard` beacons the server end. That beacon carries no body, and
the server learns the provider session id only from an end body. Two tab close takes on 2026-09-27 left session
rows with an empty provider id and zero connected seconds. Each kept
its 2.25 dollar mint hold, and the sweep skipped both. The ledger
missed about 76 connected seconds. The Chrome call closed at once.
Firefox closed about 30 seconds after the tab did.

* Write the host stem on the context clock the user stem uses. Each
  reply starts where the player started it, with silence before it.
* Cut each interrupted reply at the flush, so the stem holds only
  what played.
* Both stems start at the same context instant. Their stored offsets
  stay truthful when they do not.
* Attribute the 20 to 60 ms offset steps between the user stem and
  the provider recording (one in Chrome, four in Firefox). Fix them if
  the user stem drops or repeats blocks. Record them if they sit on the
  provider side.
* Report the provider session id to the server as soon as the socket
  learns it, so a sweep can always find and settle the session.
* Pin Chaaya `0.2.1` exactly. It changes only `SessionGuard`, which
  gains a `body` option read at send time
  (https://github.com/nrynss/chaaya/issues/6). The `pagehide` close
  carries the provider id as a JSON string. The early report stays,
  because a crashed or suspended page sends no close at all.
* Find why Firefox keeps the call open after the tab closes, and end
  it with the tab.

**Done when:** A synthetic take with marked host replies and one
interruption yields a host stem whose marks sit at their play times
within one block. The live probe aligns each stem against its
provider channel with one offset across the take. A mock take reports
its provider id before any end. A take closed by `pagehide` sends one
close whose body names the provider id. The server settles from that
id, and a test reading the beacon body pins it. A live tab close in
Chrome and in Firefox settles through the reconciler, and the Sessions
API shows the call ended within 5 seconds of the close.

At landing the orchestrator moves the Chaaya pin in `AGENTS.md`,
`dev-diary/libraries.md`, `dev-diary/project.md` and
`dev-diary/README.md`. Round files keep the version they measured.

### T7.37: The render reduces the guest's room noise
```yaml
requires:   T7.35
fixture-ok: yes
size:       S · mid
owns:       internal/render/
status:     done:cff150417ae8284b86439ef2e048f9ee33b941bb
```
Capture keeps noise suppression off by design, because `voice_focus`
cleans what the provider hears. Nothing cleans the stem the render
uses. A live take measured about 26 dB between speech peaks and the
room floor at −51 dBFS. `loudnorm` then lifts the guest about 15 dB,
floor included, so the episode sounds noisier than the call.

* Reduce stationary noise on the user stem before the mix and before
  `loudnorm`. The stored stem stays raw.
* Keep speech intact. Measure the speech band level before and after.
* Read `dev-diary/libraries.md` and Keel's media recipes first. If
  noise reduction belongs in Keel, it goes through Keel's loop.

**Done when:** A fixture stem of generated speech over added noise
renders with its floor at least 15 dB lower, measured by `astats` on
the written file. The speech band level moves by less than 1 dB.

### T7.38: The editorial answer fits a long take
```yaml
requires:   T7.2
fixture-ok: yes
size:       S · frontier
owns:       internal/gemini/
status:     done:25cd697e99d7a9420426e2d28048b66e48b3a379
```
A 142 second live take fell back to "Untitled episode (76 words)". The
box logged `editorial: decode: editorial: model failed: unexpected
EOF`. A 32 second take on the same build succeeded. The answer caps at
`editorialMaxTokens` of 4000, and thinking tokens may count against it.

* Find what truncates the answer, and measure it with a live probe on
  generated speech.
* Size the cap, or the thinking budget, so a 30 minute session
  answers whole. A truncated answer names truncation, never a decode
  error.

**Done when:** The live probe returns a complete answer for a fixture
of at least 10 minutes. A unit test pins a truncated reply to its own
sentinel.

### T7.39: A repeat end stores one recording and keeps the timeline
```yaml
requires:   T7.2
fixture-ok: yes
size:       S · mid
owns:       internal/broker/
status:     done:2f9e55c27cfb21e1650ce394ab8b94eb9f737b39
```
Every live take on 2026-09-27 ran two reconcile jobs for one session.
On the Firefox take each job stored its own copy of the provider
recording, as media `e7136cf1…` and `dd4683d3…`. Money settles once,
but private media doubles and one copy has no reference. The End
control posts the end with the provider id, and `SessionGuard.destroy`
then beacons a second, bodiless end. The server starts a reconcile for
each. The reconciler also keeps only the expiring timeline URL, never
the timeline bytes.

* A second reconcile for a settled session stores nothing new.
* The stored recording keeps one media row per session.
* The timeline bytes persist on receipt beside the recording, since the
  URL expires.

**Done when:** A test posts end twice, and the media table holds one
recording and one timeline for the session. The mutation that drops
the guard fails it.

### T7.40: End confirms, then processing follows the jobs
```yaml
requires:   T7.36
fixture-ok: yes
size:       L · frontier
owns:       web/src/routes/processing/, web/src/lib/voice/processing-state.ts, web/src/lib/voice/record-state.ts, web/src/routes/record/
status:     done:a0570e8
```
The live handoff to `/processing` carries no job ids. So transcription
and the editorial pass read "Waiting" forever, while the draft reads
done before either ran. The screen offers no link on. After End, the
page also sits about 21 seconds on a 142 second take while 17.7 MB
upload, with nothing on screen. The screen itself is a bare list with
none of the styling the gallery and episode pages carry.

* End asks the guest to confirm. Cancel keeps the take recording.
  Confirm ends the session at once, on the same path the end control
  uses today.
* Confirm moves the guest to the processing screen at once. Upload is
  its first step, with progress. Leaving the record page never drops
  an upload in flight.
* The screen follows the real transcript and editorial jobs for the
  episode, and the draft reads done only after both.
* It links to the episode once the draft exists, and to the gallery
  at every point.
* The screen matches the gallery and episode pages in layout, type and
  states, including failure.
* The confirmation never delays `pagehide`. Closing the tab still ends
  the session.
* The record mock spec expects the cut host stem. Stored host bytes
  are the greeting plus the lead before it. The interrupted reply is
  absent. Expecting both full tones fails that spec.

**Done when:** A mock take ends through the confirmation and lands on
the processing screen within one second, before a throttled upload
finishes. Its steps follow the fixture jobs to done, then offer the
episode link. Cancel leaves the take recording. A closed tab still
ends the session. The record mock spec passes on the cut host stem.

### T7.41: The transcript carries the host's replies
```yaml
requires:   T7.36, T7.39
fixture-ok: yes
size:       S · mid
owns:       cmd/reprise/main.go, cmd/reprise/main_test.go
status:     done:b19efd5
```
`startTranscript` passes `nil` host replies and zero offsets to the
transcript job. So a live transcript holds only the guest's words. The
Firefox take stored 76 words, exactly the guest's eleven lines, and the
fallback title counted them.

* Pass each host reply with its text and its start on the episode
  clock. Read them from the persisted provider timeline. The `turns`
  table has no writer today.
* Pass the stem offsets the stems table records.

**Done when:** A fixture episode with two host replies stores a
transcript whose host words sit at their reply times. A test fails
when the host replies revert to `nil`.

### T7.42: A capped model call settles what it spent
```yaml
requires:   T7.38
fixture-ok: yes
size:       S · mid
owns:       internal/editorial/, internal/memory/
status:     done:96dacf0ce3f3ddbc8f3220029778ea8dbc727af5
```
T7.38 round 1 approved the truncation sentinel. The same round
recorded two out of scope H findings. A capped call returns
`ErrTruncated` after Vertex has billed it. `editorial.Run` then
releases the reservation and records price 0. `MarkEpisode` and
`ResolveEpisode` return on that error, and their defer releases
too. A decode or parse failure of text the model did return still
settles the estimate.

* On `ErrTruncated`, settle the same estimate a decode failure
  settles. Do not release that reservation.
* Leave every other model error on the release path it has today.
* Leave `internal/analysis` alone. Chapter generation already
  releases on an unusable body, and this sentinel does not change
  that outcome.

**Done when:** A scripted `ErrTruncated` through `editorial.Run`,
`MarkEpisode`, and `ResolveEpisode` settles the estimate and does
not release. Releasing and recording price 0 fails those tests.

### T7.43: The server ends a connected provider socket
```yaml
requires:   T7.36, T7.39
fixture-ok: yes
size:       M · frontier
owns:       internal/assemblyai/, internal/broker/
status:     done:2a90f30
```
T7.36 round 1 approved the client end. The same round recorded an
out of scope H finding. A tab close whose `session.end` frame never
leaves stays billable. `TerminateSession` deletes the provider
record and leaves a connected socket open. Reconcile reads duration
and settles money. It does not send `session.end`. The sweep records
that the server did not end the socket.

* Send `session.end` on the live socket before the delete.
* A socket that is already gone still deletes, and a second end
  does not open a new socket.
* The sweep and the reconcile both use that end.

**Done when:** A test with a live socket sees `session.end` before
the delete. Making the end only DELETE fails that test.

### T7.44: Host words keep their speaker
```yaml
requires:   T7.41
fixture-ok: yes
size:       M · mid
owns:       internal/transcript/, internal/episode/, internal/store/migrations/, internal/store/store_test.go,
             internal/editorial/validate.go, internal/editorial/run.go, web/src/lib/editor/
status:     done:2c11452
```
T7.41 round 1 recorded an out of scope H finding. Host words share
the edit timeline with the guest. The `words` table has no speaker
column. Insert drops the role. The editor labels a missing speaker
as the guest. The editorial timeline is unlabelled too.

* Store the speaker with each word.
* Playback, the editor, and editorial validation read that speaker.
* A host word does not appear as the guest.

**Done when:** A fixture with one host reply and one guest line
shows the host words as the host. Dropping the role on insert
fails that test.

### T7.45: Erase ends a connected provider socket
```yaml
requires:   T7.43
fixture-ok: yes
size:       S · mid
owns:       internal/privacy/
status:     done:1046053
```
T7.43 round 1 recorded an out of scope H finding. Account erase
deletes the provider record through `TerminateSession`. That call
writes no `session.end`. A connected socket stays billable.

* Erase uses the same end the sweep uses. That end writes
  `session.end` before the delete.
* A socket that is already gone still deletes.

**Done when:** A test with a held socket sees `session.end` before
the delete. Calling `TerminateSession` again fails that test.

### T7.46: The detail endpoint forwards the word speaker
```yaml
requires:   T7.44
fixture-ok: yes
size:       XS · light
owns:       internal/api/episodes.go, internal/api/episodes_speaker_test.go
status:     done:2ed5441
```
T7.44 round 1 recorded an out of scope H finding. `wordJSON` has
no speaker field and `wordsOf` drops `EditWord.Speaker`, so the
detail endpoint never forwards the speaker. The editor keeps
receiving words with no speaker key and shows every one as
unknown. A host word never reads as the guest, but it never reads
as the host either.

* Forward the stored speaker on the word shape the detail
  endpoint sends, so the editor names host words as the host.
* Older rows with no speaker keep the old shape the editor
  already maps to unknown.

**Done when:** A fixture draft with one host reply and one guest
line reads its speaker through the detail endpoint. Dropping the
field on the wire fails that test.

### T7.47: CI green again
```yaml
requires:   T7.40, T7.43
fixture-ok: yes
size:       M · frontier
owns:       internal/assemblyai/socket_test.go, web/src/lib/voice/record-state.ts,
             web/src/lib/voice/processing-state.ts, web/src/routes/processing/+page.svelte
status:     done:6fee833
```
CI is red on every `main` commit. The gate stops at the first
failure, so the red has layers. Fix them in gate order and prove
each layer by watching CI go further, not by gut feel.

* `staticcheck` fails with SA4006 at
  `internal/assemblyai/socket_test.go:248`. The first locked read
  assigns `gotUpgrades` and nothing reads it before the second
  locked block reassigns it. Keep the asserted behavior (one
  upgrade across both ends) and drop the dead read.
* `svelte-check` reports four errors in
  `web/src/lib/voice/record-state.ts`, landed with T7.40. The
  gate never reaches them while `staticcheck` stays red. Fix
  them where the record state declares its types, not by
  loosening the check.
* The 2026-09-27 gate passed everything through the web build
  and then failed at `playwright` with its output swallowed
  (`npm run test:e2e >/dev/null`). Rerun the e2e suite with the
  output visible, name the failing spec, and fix it. If the
  culprit lives outside `owns`, raise a contract change instead
  of reaching out.
* The gate itself stays as it is. No check is loosened, skipped,
  or reordered to reach green.

**Done when:** The gate passes end to end on CI for the landed
commit. A fresh worktree passes every step the workstation can
run, and the CI run for the landing commit is green.

### T7.48: The early provider id report neither settles nor ends the call
```yaml
requires:   T7.36, T7.43
fixture-ok: yes
size:       S · mid
owns:       web/src/lib/voice/session-calls.ts, web/src/lib/voice/session-calls.test.ts,
             internal/api/session_end.go, internal/api/handlers_test.go,
             internal/api/routes.go, internal/api/routes_test.go,
             web/src/lib/api/testdata/routes.json, web/src/lib/api/types.ts,
             cmd/reprise/main.go, cmd/reprise/main_test.go
status:     done:cda7ff8
```
**Defect.** `reportProviderSession` (`web/src/lib/voice/session-calls.ts`) stores the provider id
while the take runs. It does that by calling `closeSession`, which posts to
`POST /api/sessions/{id}/end`. End uses the same route. The server wraps that route in
`settleOnEnd` (`cmd/reprise/main.go`, `func (s *settleOnEnd) ServeHTTP`). So a reconcile job starts
about one second into every take. The reconcile then finds the call open and calls `StopSocket`,
which writes `session.end` on the live call. T6.4b round 2 saw this at 05:38:19, 05:59:43 and
06:00:47 UTC on 2026-09-28.

**Change.**
1. Add a route `POST /api/sessions/{id}/provider`. It takes the same body as `/end`,
   `{"provider_session_id": "..."}`, and calls the same `RecordSessionEnd` store method.
   * Register it in `NewSessionEnd` (`internal/api/session_end.go`) beside the `/end` pattern.
   * Add it to `routeTable` and to `handlerFor` in `internal/api/routes.go`. It maps to
     `d.SessionEnd`, like `/end`.
2. In `settleOnEnd.ServeHTTP`, start the reconcile only when `r.URL.Path` ends in `/end`. A
   `/provider` post records the id and returns. It starts no job.
3. In `reportProviderSession`, post to `/api/sessions/{id}/provider` instead of calling
   `closeSession`. Keep its contract: an empty id posts nothing and returns false, and a refusal
   returns false without throwing.
4. Leave `closeSession`, the `SessionGuard` url in `record-state.ts`, and the `pagehide` and
   `beforeunload` paths on `/end`. A real close must still start the reconcile.

**Tests.**
* `cmd/reprise/main_test.go`: copy the setup of `TestSettleEndSettlesExactlyOnce`. Post to
  `/provider` and assert that no reconcile job starts. Post to `/end` and assert that exactly one
  starts.
* `internal/api/routes_test.go`: the table carries the new route, and it serves `SessionEnd`.
* `internal/api/handlers_test.go`, where the `NewSessionEnd` tests live: a `/provider` post
  records the id and answers 200 with the same JSON as `/end`.
* `web/src/lib/voice/session-calls.test.ts`: `reportProviderSession` fetches a url ending in
  `/provider`, and `closeSession` still fetches one ending in `/end`.

**Done when:** All four tests pass, and the gate passes in a fresh worktree. Pointing
`reportProviderSession` back at `closeSession` fails the web test. Deleting the path check in
`settleOnEnd` fails the Go test.

**Do not** change `internal/broker/`. T7.49 owns the reconcile.

### T7.49: A reconcile after a close waits for the provider duration
```yaml
requires:   T7.43
fixture-ok: yes
size:       S · frontier
owns:       internal/broker/reconcile.go, internal/broker/reconcile_test.go,
             internal/broker/end_test.go
status:     done:cd4febb
```
**Defect.** Every reconcile on the four T6.4b round 2 runs failed with
`provider session is still open`. That covers the reconciles after End and after a tab close.
`readForSettle` (`internal/broker/reconcile.go`) reads the provider session. When the read
returns `ErrSessionOpen`, it calls `StopSocket` and reads again straight away. The provider
writes the duration a moment after the close, so the second read also fails. A paid job never
reruns, so the session waits for the sweep. The sweep takes a session only past the 1800 second
cap plus its margin, which was 31 minutes on these runs.

Meanwhile each lease holds 2.25 dollars and the provider recording is not stored. The timeline is
not stored either. So the transcript pass ran without the host replies, and both round 2
transcripts hold only the user's words.

The existing fake `openThenClosed` (`internal/broker/end_test.go`) closes the moment
`StopSocket` runs. That hides the delay, which is why the tests passed.

**Change.**
1. Add `Wait func(ctx context.Context, d time.Duration) error` to `ReconcilerConfig`, and store it
   on `Reconciler`. A nil `Wait` uses a real timer that returns `ctx.Err()` when the context ends.
   Tests pass a `Wait` that returns at once and records each duration.
2. Add a backoff constant, `settleReadBackoff`, of 500 ms, 1 s, 2 s, 4 s, 8 s and 8 s. That is
   23.5 seconds in total, well inside the 100 second limit.
3. In `readForSettle`, keep the first read and the single `StopSocket`. Then read again after each
   backoff step until the read succeeds or returns an error other than `ErrSessionOpen`.
4. If every step still finds the session open, return the error wrapping `ErrSessionOpen`. The
   sweep settles the session later. Never settle on a guessed duration.

**Tests** in `internal/broker/reconcile_test.go`.
* Write a fake reader that returns `ErrSessionOpen` for the first N reads after `StopSocket`,
  then a duration with recording and timeline URLs. With N = 2, one `Reconcile` settles the
  session and stores both artifacts. `reconcile_state.settled` reads 1, and
  `recording_media_id` and `timeline_media_id` are set. `StopSocket` runs once, and `Wait`
  saw 500 ms and then 1 s.
* A reader that never closes returns an error wrapping `ErrSessionOpen`. It leaves the row
  unsettled, and `Wait` saw all six steps.
* A second `Reconcile` on the settled session calls neither `StopSocket` nor `Wait`.

**Done when:** The tests pass under `go test -race ./internal/broker/`, and the gate passes in a
fresh worktree. Replacing the backoff loop with one read fails the first test.

**Note.** This fix lands before the next deploy and together with T7.48. Alone, the early id
report would start a 23 second wait mid-take.

### T7.50: The processing page reports the real stem bytes after a resume
```yaml
requires:   T7.47
fixture-ok: yes
size:       XS · light
owns:       web/src/lib/voice/processing-state.ts, web/src/lib/voice/processing-state.test.ts
status:     done:e2403d1
```
**Defect.** On the T6.4b round 2 Firefox run, Upload read `2 stems, 0 bytes durable`. The box held
5554988 and 2609836 bytes. The resume path in `ProcessingController`
(`web/src/lib/voice/processing-state.ts`, the loop over `ChunkUploader.resume()`) adds
`resumed.receipt?.sizeBytes ?? 0` to `total`. It counts a stem even when its receipt is missing.
It then prints `${count} stems, ${total} bytes durable.`.

**Change.**
1. Export a pure function `resumedUploadDetail(count: number, totalBytes: number): string`. It
   returns `Both stems durable.` when `totalBytes <= 0`. Otherwise it returns
   `${count} stems, ${totalBytes} bytes durable.`.
2. Use it for the `pair === null` branch that now builds the string inline.

**Tests** in `processing-state.test.ts`, beside the `uploadDetail` block.
* `resumedUploadDetail(2, 0)` returns `Both stems durable.`.
* `resumedUploadDetail(2, 8164824)` returns `2 stems, 8164824 bytes durable.`.

**Done when:** Both tests pass, and the gate passes in a fresh worktree. Returning the old
template for zero bytes fails the first test.

### T7.51: The opener speaks from the owner's real history
```yaml
requires:   T7.48
fixture-ok: yes
size:       S · mid
owns:       internal/host/, cmd/reprise/main.go, cmd/reprise/main_test.go
status:     done:2cea171
```
**Defect 1.** The Firefox owner held four episodes, two of them drafts, and heard "Welcome to your
first episode". `host.Build` (`internal/host/host.go`) falls back to the constant `opener`
whenever no unused callback exists. It never checks whether the owner has episodes. An episode
count is a stored fact, so the greeting must not claim one it never read.

**Defect 2.** Nothing calls `memory.MarkUsed` (`internal/memory/callback.go`). Both of the Chrome
owner's callbacks still read `used = 0`. `unusedCallback` in `internal/host/host.go` returns the
oldest unused row. So the host greets every take with the same callback, from episode 5.

**Change.**
1. In `internal/host/host.go`, add `PriorEpisodes int` to `Input`. `Load` fills it with
   `SELECT COUNT(*) FROM episodes WHERE owner_id = ? AND state != 'recording'`. The broker builds
   the config before it creates the new episode row, so the count holds only earlier episodes.
2. Add a second constant, `returningOpener = "Welcome back, tell me what is on your mind today."`.
   `Build` uses the callback greeting when a callback exists. Otherwise it uses `opener` when
   `PriorEpisodes == 0` and `returningOpener` when it is above 0.
3. Add `CallbackID string` with the tag `json:"-"` to `Config`. `Build` sets it to
   `in.Callback.ID` only when the greeting actually cites that callback.
4. In `hostBuilder.BuildSessionConfig` (`cmd/reprise/main.go`), call
   `memory.MarkUsed(ctx, b.db, ownerID, cfg.CallbackID)` after `host.Load` succeeds, when
   `CallbackID` is not empty. A failed mark returns the error, so the mint refuses rather than
   repeating a callback silently.

**Tests.**
* `internal/host/host_test.go`: with `PriorEpisodes: 2` and no callback, the greeting equals
  `returningOpener`. With `PriorEpisodes: 0`, it equals `opener`. With a callback, it cites the
  quote and `CallbackID` equals the callback id.
* `cmd/reprise/main_test.go`: seed one owner with two unused callbacks on two episodes. Call
  `BuildSessionConfig` twice. The two greetings differ, and both rows read `used = 1`.

**Done when:** The tests pass under `go test -race`, and the gate passes in a fresh worktree.
Removing the `MarkUsed` call fails the second test.

**Note.** The `memory.Select` comment says the mark follows the spoken greeting. Marking at mint
is one step earlier, so a mint that fails after the config is built loses one callback. Record
that in the handoff. Do not edit `internal/broker/` to move the mark.

### T7.52: The editor reads, seeks and leaves like a page
```yaml
requires:   T7.47
fixture-ok: yes
size:       S · mid
owns:       web/src/routes/episode/[id]/edit/+page.svelte, web/src/lib/editor/draft.ts,
             web/src/lib/editor/draft.test.ts, web/src/routes/episode/[id]/edit/edit.spec.ts
status:     done:b62d2e1
```
The owner used the editor on both T6.4b round 2 drafts. Seven defects turned up. Fix each one
separately, and pin each one with its own test.

1. **Words run together.** The transcript reads `Oh,hello.Thisisjustatest.`. In `+page.svelte`,
   the `{#each snap.words ...}` block emits one `<button>` per word with no whitespace between
   them. Add a `{' '}` text node after each word, outside the `<button>` and outside the `<s>`.
2. **No pointer seek.** The waveform `<canvas role="slider">` handles only `onkeydown`. Add
   `onpointerdown` and `onpointermove`. `onpointermove` acts only while `event.buttons === 1`.
   Seek to `(event.offsetX / canvas.clientWidth) * snap.duration` through `controller.seekTo`.
   Call `setPointerCapture` on pointer down.
3. **The shown length is wrong.** `installDraft` sets `duration` to the last word's end, so the
   page read `0:40 of 0:34`. The Chaaya `AudioPlayer` exposes `duration`. Once it reads above 0,
   use it for `snap.duration` and for the waveform regions. The position shown never exceeds the
   length shown.
4. **Play and seek stop after a revert.** After `revertCut`, Play and the waveform stop answering
   until a reload. Nobody has found the cause. Write the failing browser test first, then find
   it. Check the `TranscriptFollower` skip spans after `refreshCuts`, and whether
   `this.player.play()` returns false.
5. **No way back.** Add `<a href={resolve('/')}>Back to the gallery</a>` at the top of the page.
   The episode page uses the same link.
6. **An empty planted block.** An editorial answer with no callback still shows "Planted for next
   time", empty but with a revert control. Render that block only when a callback proposal exists.
7. **A cold open nobody proposed.** A draft with no stored cold open showed `Hello,`. The empty
   default in `draft.ts` (`coldOpen: { start: 0, end: 0, ... }`) names word 0. Add
   `hasColdOpen: boolean` to the snapshot. Set it only when a `cold_open` proposal exists, and
   render the cold open block only then.

**Tests.** Use `web/src/routes/episode/[id]/edit/edit.spec.ts` against the fixture draft.
* The transcript's text content has a space between two adjacent words.
* A pointer down at mid canvas moves the position near half the length.
* Revert a cut, then Play. The position advances. Then seek, and the position moves.
* The position text never shows a first time larger than the second.
* A `Back to the gallery` link exists and points to `/`.
* A fixture with no callback and no cold open shows neither block.

**Done when:** Each test fails on `main` and passes after its fix, and the gate passes in a fresh
worktree.

### T7.53: The draft plays the host with the guest
```yaml
requires:   T7.51, T7.52
fixture-ok: yes
size:       L · frontier
owns:       internal/render/, internal/api/episodes.go, internal/api/playback_test.go,
             cmd/reprise/main.go, cmd/reprise/main_test.go, web/src/lib/editor/draft.ts,
             web/src/lib/editor/draft.test.ts
status:     done:e7ecc58
```
**Defect.** The owner edited both T6.4b round 2 drafts hearing only their own voice.
`episodeDetailJSON.AudioURL` (`internal/api/episodes.go`) names the user stem when one exists,
otherwise the host stem. The host joins only at render. Cuts land on a conversation, so the
editor must play the conversation. Both host stems were stored.

**Approach.** Mix a preview on the server, and keep one audio source in the browser. The Chaaya
`AudioPlayer` plays a single source, and two synced players drift.

1. In `internal/render/`, add a preview that mixes the two stems with the offsets and the mix
   the render uses. The preview applies no cuts and no cold open. Its clock is the user stem
   clock, so the stored word times still land on it. Reuse the render's locate and mix steps.
   Do not copy them.
2. Store the preview as media owned by the episode owner. Record its media id with the episode.
   A new table or column is fine, created the way `internal/render/store.go` creates its own.
3. Run the preview as its own `keel/job` kind. It starts where `stemsComplete`
   (`cmd/reprise/main.go`) schedules the transcript job. It makes no paid call.
4. Add `preview_audio_url` to the detail JSON. It stays empty until the preview exists.
5. In `draft.ts`, load `preview_audio_url` when it is set, otherwise `audio_url`. The
   `TranscriptFollower` skip spans stay as they are, because the clock is unchanged.

**Tests.**
* `internal/render/`: build a preview from a user stem of silence and a host stem with a 1 kHz
  tone at 2.0 s, at a host offset of 500 ms. Run `ffmpeg astats` or a sample scan on the output,
  and find the tone at 2.5 s within 20 ms. Dropping the host input fails it.
* `internal/api/playback_test.go`: the detail carries `preview_audio_url` once a preview row exists.
* `draft.test.ts`: the controller loads the preview url when the detail carries one.

**Done when:** The tests pass, and the gate passes in a fresh worktree three times in a row. The
review file records the three exit codes, because this adds a job kind to the binary.

### T7.54: An unfinished take says what it is
```yaml
requires:   T7.47
fixture-ok: yes
size:       S · mid
owns:       web/src/routes/threads/threads.ts, web/src/routes/threads/threads.test.ts,
             web/src/routes/threads/gallery-finish.test.ts, web/src/routes/episode/[id]/+page.svelte
status:     done:0b9acf2
```
Three defects, each pinned by its own test.

1. **A quoted moment that does not exist.** Every episode page opened without a `?w=` parameter
   reads `Quoted moment at word 0. No stored quote names it.`. In `threads.ts`, the episode
   controller runs `Number(queryValue(search, 'w') ?? '')`, and `Number('')` is `0`. Treat a
   missing or empty `w` as no moment. Only a present, finite, non-negative number sets
   `momentWord`.
2. **Publish on an episode with no audio.** The two tab-closed takes left episodes 8 and 4 in
   `recording`, with no stems. `+page.svelte` shows `Publish…` for every unpublished episode. Show
   it only when `snap.state === 'ready'`. For a `recording` episode, show this line instead: "This
   take ended before it was stored. There is nothing to play or publish." Keep Erase.
3. **The gallery card stays on `rendering`.** After episode 7 rendered, its card kept the
   `rendering` badge and `RENDERED_NOTE`. The server already held the episode as `ready`, and
   the analysis had failed. `RECHECKED_STATES` should make the card read the detail again once
   the render pass stops. Find why the card never took the new state. The card must end on the
   stored state, and it names the failed analysis through `stoppedPassNote`.

**Tests.**
* `threads.test.ts`: a controller mounted with an empty search has `momentWord` null, and no
  notice mentions a quoted moment. With `?w=12` it is 12.
* `threads.test.ts`: a `recording` episode with no audio exposes no Publish control.
* `gallery-finish.test.ts`: a rendering card whose render finishes, and whose next detail reads
  `ready` with a failed analysis, ends showing `ready` and the analysis failure.

**Done when:** Each test fails on `main` and passes after its fix, and the gate passes in a fresh
worktree.

**Record in the handoff** whether a take closed mid-way can resume its stems from the browser
store. If it can, name what would offer that. Build nothing for it here.

### T7.55: A cold open never replays the opening
```yaml
requires:   T7.38
fixture-ok: yes
size:       XS · light
owns:       internal/editorial/validate.go, internal/editorial/validate_test.go,
             internal/editorial/run_test.go
status:     done:e0cfc42
```
**Defect.** The episode 7 render in T6.4b round 2 runs 56.6 seconds from a 40.4 second take. Its
stored cold open spans words 0 to 28, which is the opening itself. The render plays those 16
seconds, then starts the episode at word 0. The listener hears the start twice. The render
repeats cold open audio on purpose, so the editorial validation must refuse this span.

`validate.go` already holds a cold open to 10 to 20 seconds (`ColdOpenMinMs`, `ColdOpenMaxMs`).
It has no rule about where the span starts.

**Change.**
1. Add `ColdOpenEarliestMs = 30_000` with a doc comment. A teaser from the first half minute
   replays audio the listener hears moments later.
2. In the cold open check, drop the span when
   `words[start].StartMs - words[0].StartMs < ColdOpenEarliestMs`. Log it the way the length
   check logs, and leave `out.ColdOpen` nil.

**Tests** in `validate_test.go`, beside the existing cold open cases.
* A 15 second span that starts at word 0 is dropped. Model the cases on `TestShortColdOpenDrops`.
* A 15 second span that starts 35 seconds in is kept.
* A take shorter than 30 seconds keeps no cold open.

**Done when:** The tests pass, and the gate passes in a fresh worktree. Deleting the new check
fails the first test.

### T7.56: Chapters fit their output cap
```yaml
requires:   T7.38
fixture-ok: yes
size:       XS · mid
owns:       internal/gemini/chapters.go, internal/gemini/calls_test.go,
             internal/analysis/run.go, internal/analysis/run_test.go
status:     done:7ba505e
```
**Defect.** Analysis failed on the 40 second episode 7 render in T6.4b round 2, with
`chapters: gemini: truncated answer: answer hit the output cap`. `CompleteChapters`
(`internal/gemini/chapters.go`) sets no `ThinkingConfig`. Reasoning then shares the 800 token
cap (`ChapterMaxTokens` in `internal/analysis/run.go`) with the answer, and spends it. T7.38
fixed the same failure for the editorial call in `internal/gemini/schema.go`.

**Change.**
1. In `chapters.go`, add the exported `ChapterThinkingBudget = 1024` with a doc comment. Pass
   `ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: &budget}` in `CompleteChapters`, as
   `GenerateEditorial` does.
2. In `run.go`, raise `ChapterMaxTokens` to 4096 and rewrite its comment. The cap must hold the
   thinking budget plus the answer. The spend estimate reads this constant, so the reservation
   grows with it. That is intended.

**Tests.**
* `calls_test.go`: add `TestCompleteChaptersBoundsReasoning`, modelled on
  `TestGenerateEditorialBoundsReasoning` in `client_test.go`. The config carries a positive
  thinking budget below `MaxOutputTokens`. Pass `analysis.ChapterMaxTokens` as the cap, or 4096.
* `run_test.go`: `ChapterMaxTokens` is at least `gemini.ChapterThinkingBudget + 1024`, so the
  answer keeps 1024 tokens after reasoning.

**Done when:** The tests pass, and the gate passes in a fresh worktree. Removing the
`ThinkingConfig` fails the first test. A live probe is not required. If the implementer has a
key, running `internal/gemini/live_test.go` under the `live` tag on a short render is welcome.
Record the tokens spent in the handoff.

### T7.57: A session the server cannot tie to a provider record still pays
```yaml
requires:   T7.48, T7.49
fixture-ok: yes
size:       M · frontier
owns:       internal/broker/settle.go, internal/broker/sweep.go, internal/broker/reconcile.go,
             internal/broker/settle_test.go, internal/broker/sweep_test.go,
             internal/broker/reconcile_test.go, internal/episode/sessions.go,
             internal/episode/service_test.go, internal/api/session_end.go,
             internal/api/respond.go, internal/api/handlers_test.go
status:     done:9390680
```
**Defect.** The browser reports the provider session id, and the server never checks it. Two
paths then settle nothing, and the reservation expires with no charge.

* **No id reported.** `ListOpen` (`internal/broker/settle.go`) keeps only rows with
  `s.provider_session_id <> ''`. Its comment says such rows "age out of their holds by
  reservation expiry instead". The two tab-closed takes of 2026-09-27 ended exactly so. Their
  leases read `expired` with 0 settled.
* **A made-up id.** The sweep reads the provider status, gets `ErrProviderGone`, and runs
  `reviewGone` (`internal/broker/sweep.go`). That books 0 seconds and raises an alert.

Any anonymous visitor can therefore hold a call up to the 1800 second cap without touching the
owner budget or the daily ceiling. AssemblyAI still bills the account. `RecordSessionEnd`
(`internal/episode/sessions.go`) also lets a later post replace a stored id with a different one.

**Change.**
1. **Charge the cap when the provider record cannot be read.** In `reviewGone`, settle through
   `s.reconciler.ReconcileAbandoned` with `DurationSeconds: candidate.TokenCapSeconds`, and
   empty artifact URLs. Keep the `AlertNeedsReview` alert, and change its detail to say the
   full cap was charged.
2. **Sweep rows with no id.** Drop the `s.provider_session_id <> ''` filter from `ListOpen`, and
   rewrite its comment. In the sweep, a candidate with an empty id never reads the provider. It
   waits until `OpenSeconds >= TokenCapSeconds + margin`, as open sessions do. Then it settles
   the full cap through `ReconcileAbandoned` and raises `AlertNeedsReview`. Remove the empty id
   refusal from the sweep's candidate check. In `validate` (`internal/broker/reconcile.go`),
   allow an empty provider id for `ReconcileAbandoned` only. `Reconcile` still refuses one.
   `endProvider` already skips an empty id.
3. **Reject a bad report.** In `RecordSessionEnd`, refuse with a new sentinel
   `episode.ErrProviderConflict` when:
   * the id does not match `^sess_[0-9a-f]{32}$`.
   * the row already holds a different non-empty id.
   * another session row already holds this id.

   An empty id and a repeat of the stored id still succeed. In `internal/api/session_end.go`,
   map the sentinel to 409 with a new code `CodeProviderConflict = "provider_conflict"` in
   `respond.go`. Both the `/end` and `/provider` routes get this.

**Tests.**
* `sweep_test.go`: a candidate whose status read returns `ErrProviderGone` settles
  `TokenCapSeconds`. Its reservation is released and the owner budget is charged the cap price.
* `sweep_test.go`: a candidate with an empty id and `OpenSeconds` past the cap plus margin
  settles the cap, and no provider read happens. One under the cap is skipped.
* `settle_test.go`: `ListOpen` returns a row with an empty provider id.
* `service_test.go`: each of the three refusals returns `ErrProviderConflict`. An empty id and a
  repeat still succeed.
* `handlers_test.go`: a conflicting post answers 409 with `provider_conflict`.

**Done when:** The tests pass under `go test -race`, and the gate passes in a fresh worktree.
Restoring the zero settle in `reviewGone` fails the first test. Restoring the filter fails the
second.

**Note.** This blocks the next deploy. The fix charges a real user the full $2.25 when the
browser never reports its id, as a crashed tab does. The review alert is how the owner refunds
it. Say that in the handoff.

### T7.58: The editor answers after a revert in Firefox
```yaml
requires:   T7.53
fixture-ok: yes
size:       S · mid
owns:       web/src/routes/episode/[id]/edit/edit.playwright.config.ts,
             web/src/routes/episode/[id]/edit/edit.spec.ts, web/src/lib/editor/draft.ts,
             web/src/routes/episode/[id]/edit/+page.svelte, .github/workflows/ci.yml
status:     done:3ea4c2e
```
**Defect.** In T6.4b round 2 the owner reverted both cuts on the Firefox draft. Play and the
slider then stopped answering until a reload. T7.52 could not reproduce it, because every editor
spec runs in Chromium only. `edit.playwright.config.ts` lists one project, `chromium`. CI installs
only `chromium webkit` (`.github/workflows/ci.yml`, the `npx playwright install` step).

**Change.**
1. Add a `firefox` project to `edit.playwright.config.ts`, and add `firefox` to the CI install
   step.
2. Reproduce in Firefox. Revert every cut on a fixture draft whose first cut starts at word 0,
   as the Firefox draft's did (words 0 to 3). Then press Play and drag the waveform.
3. If it reproduces, write the failing spec first, then fix it in `draft.ts` or `+page.svelte`.
   If it does not, say so in the handoff, with what you ran. Keep the Firefox project either way.
4. Also check whether the Chrome notice "Playback refused. Press play again after a gesture."
   appears on a first Play press after load. If it does, name the cause in the handoff.

**Done when:** The editor suite passes in both projects, and the gate passes in a fresh worktree
three times in a row. The review file records the three exit codes, because the gate changed.

### T7.60: The record repeat-completion spec flakes under suite load
```yaml
requires:   T7.40
fixture-ok: yes
size:       XS · light
owns:       web/src/routes/record/stems-complete.spec.ts, web/src/routes/record/cap.spec.ts,
             web/src/routes/record/confirm-processing.spec.ts, web/src/routes/record/end-control.spec.ts,
             web/src/routes/record/gallery-link.spec.ts, web/src/routes/record/mock-session.spec.ts
status:     in-progress:review-r2:t7.60-rev-r2@b3feb8d2472c16dfee61780f291b61a83e4600e1
```
T7.58 round 1 recorded an out of scope M finding. `a repeat
completion reports the standing outcome` fails under full-suite
load (27 passed, 1 failed in one 45.6 s run) and passes alone
in 1.7 s and at file scope 4/4, so a suite-load flake or a real
ordering defect, cause unattributed.

* Reproduce under repetition first. Name the losing
  interleaving with a failing pin before changing anything.
* Fix the synchronization, not the timeout. No sleeps added, no
  wall-clock threshold widened. Quarantine with `test.fixme`
  only if the defect sits in a library, with the reason stating
  the defect.

**Done when:** The mock record suite passes twenty consecutive
runs, and the gate passes three consecutive full runs in a fresh
worktree.

### T7.59: The transcript stops waiting for host words after a minute
```yaml
requires:   T7.53
fixture-ok: yes
size:       S · mid
owns:       cmd/reprise/main.go, cmd/reprise/main_test.go
status:     done:f616218
```
**Defect.** `awaitHostReplies` (`cmd/reprise/main.go`) polls every `timelinePoll` (20 ms) until
the provider timeline is stored, or until a reconcile for the episode has ended. It has no
deadline of its own. If the End post never reaches the server, no reconcile starts. The
transcript then waits for the sweep, which is about 31 minutes, and the processing page shows
transcription running all that time.

**Change.**
1. Add `hostRepliesWait = 60 * time.Second`, with a doc comment. The reconcile backoff tops out
   at 23.5 seconds, so a minute covers a normal close with margin. It stays under the 100 second
   limit.
2. Store the bound on `jobs` as a field, so a test can shorten it. In `awaitHostReplies`, count
   polls, and once the bound passes, return no replies and no error. The batch then runs on the
   guest words alone, as it does after an errored reconcile.
3. Log one line naming the episode when the bound passes.

**Tests** in `main_test.go`: with the bound set to a few polls and no reconcile job, the wait
returns empty replies and a nil error. With a timeline stored before the bound, it returns the
host replies. Neither test asserts a wall-clock duration.

**Done when:** The tests pass under `go test -race`, and the gate passes in a fresh worktree.
Removing the bound makes the first test hang, and the test's own context timeout fails it.

### T7.61: Quarantine the Firefox play-state specs behind the Chaaya defect
```yaml
requires:   T7.58
fixture-ok: yes
size:       XS · light
owns:       web/src/routes/episode/[id]/edit/edit.spec.ts
status:     in-progress:implement:t7.61-impl
```
**Defect.** Three Firefox specs that T7.58 added fail on every CI run since it landed:
`revert a cut, then play and seek still answer`, `revert every cut, then play and drag still
answer`, and `revert the live cut, then play and drag still answer`. Each one waits for the
"Pause draft" button and never finds it.

A probe on 2026-09-28 found the cause in the CI image, `mcr.microsoft.com/playwright:v1.63.0-noble`.
That container has no audio output device. About 40 ms after `playing`, Firefox fires an `error`
event with code 3 and the message `OnMediaSinkAudioError`. The element keeps playing. Chaaya
0.2.1's `AudioPlayer` then sets `playing = false` and reports a decode failure. The page reads
that flag, so the button stays on "Play draft". The same happens with no revert at all.

This is a Chaaya defect, filed as https://github.com/nrynss/chaaya/issues/7. It is fixed in Chaaya,
not here. Reprise adds no workaround.

**Change.** Mark those three specs `test.fixme` for the Firefox project only, with
`test.fixme(browserName === 'firefox', '...')`. The reason names the defect and the issue URL. The
Chromium runs of the same specs stay live, and every other Firefox spec stays live.

**Done when:** The editor suite passes in both projects in the CI image. The gate passes in a
fresh worktree. T7.64 owns lifting the quarantine.

**Note.** This does not explain the owner's own Firefox report, made on a machine with speakers.
The next live session repeats that check by hand.

### T7.62: Start answers the first tap
```yaml
requires:   T7.40
fixture-ok: yes
size:       XS · mid
owns:       web/src/routes/record/+page.svelte, web/tests/start-gesture.spec.ts
status:     done:76c4a27
```
**Defect.** The record page is prerendered, so the Start button renders enabled before the page's
script runs. The click handler is attached later, inside `$effect`, through
`document.getElementById('record-start')` and `addEventListener`. A tap in that gap does nothing,
with no message. On a slow phone the first tap on the product's main control is lost.

`tests/start-gesture.spec.ts` catches this gap by accident. It clicks right after `page.goto`. In
the CI image it failed 1 run in 3 in Chromium on 2026-09-28, with the capture marks empty, because
nothing had run. That failure has turned CI red on commits that changed only planning files.

**Change.**
1. Bind the handler with Svelte, `onclick={() => void activeController?.start()}`, and delete the
   `getElementById` and `addEventListener` pair and its cleanup. A delegated Svelte handler still
   runs inside the user gesture, which WebKit requires.
2. Keep the button disabled until the controller exists:
   `disabled={activeController === null || snap.phase !== 'preflight'}`. The prerendered HTML then
   ships a disabled button, and Playwright's click waits for it to enable.
3. Leave the spec's assertions as they are. The order `context`, `resume`, `microphone` must
   still hold.

**Done when:** `start-gesture.spec.ts` passes twenty runs in a row in Chromium and WebKit in the CI
image, and the gate passes in a fresh worktree. Moving the handler back to `addEventListener` in
the effect brings the flake back under repetition.

### T7.63: The gate names the failing spec
```yaml
requires:   T7.47
fixture-ok: yes
size:       XS · light
owns:       tools/check.sh
status:     done:578409f
```
**Defect.** `tools/check.sh` runs `npm run test:e2e >/dev/null`. A failing run therefore prints only
`FAIL [playwright] end-to-end tests failed`. No CI log names the spec. T7.58 landed on red CI, and
finding the cause took a local rerun in the CI image.

**Change.** Send the Playwright output to a temporary file. Print it only when the step fails,
then fail as now. A passing run stays quiet. Do the same for any other step in `check.sh` that
discards output with `>/dev/null`.

**Done when:** A deliberately broken spec in a scratch branch makes the gate print that spec's
name and error. The gate passes in a fresh worktree three times in a row, and the review file
records the three exit codes.

### T7.64: Take the Chaaya AudioPlayer fix and lift the Firefox quarantine
```yaml
requires:   T7.61
fixture-ok: yes
size:       XS · light
owns:       web/package.json, web/package-lock.json, web/src/routes/episode/[id]/edit/edit.spec.ts
status:     not-started
```
Once Chaaya publishes the release that fixes issue 7, pin `@nrynss/chaaya` to that exact version
in `web/package.json` and refresh the lock file. Remove the three `test.fixme` lines T7.61 added.
The orchestrator updates the Chaaya version in `dev-diary/libraries.md` at landing.

**Done when:** The three specs pass in Firefox in the CI image, three runs in a row. The gate passes
in a fresh worktree three times in a row, and the review file records the exit codes.

### T7.65: Run the welcome suite in WebKit again
```yaml
requires:   T7.27
fixture-ok: yes
size:       XS · light
owns:       web/src/routes/welcome/welcome.playwright.config.ts
status:     done:2e7aa9a
```
T7.27 took WebKit out of the welcome suite. The seeded teaser spec had failed there, and the
failure was blamed on Chaaya's silent prime (https://github.com/nrynss/chaaya/issues/5). On
2026-09-28 it did not reproduce in the CI image, `mcr.microsoft.com/playwright:v1.63.0-noble`,
with WebKit 26.6. The teaser spec passed in WebKit three runs of three. The issue is closed as a
flake on one macOS host.

**Change.** Add `{ name: 'webkit', use: { browserName: 'webkit' } }` beside the Chromium project
in `welcome.playwright.config.ts`. CI already installs WebKit. Change no spec.

**Done when:** The welcome suite passes in both projects in the CI image, run with
`docker run --rm --ipc=host -v "$PWD":/w -w /w/web mcr.microsoft.com/playwright:v1.63.0-noble`, ten
runs in a row. The gate passes in a fresh worktree three times in a row, and the review file records
the three exit codes. WebKit does not launch on the workstation, so run it only in that image.

### T7.67: The live probe follows the production model
```yaml
requires:   T7.66
fixture-ok: yes
size:       XS · light
owns:       internal/gemini/live_test.go
status:     in-progress:implement:t7.67-impl
```
T7.66 round 1 recorded an out of scope M finding. The live
probe hardcodes `gemini-2.5-flash` while both shipped configs
carry `gemini-3.8-flash`. The next live run would exercise the
old model and prove nothing about production.

* Read the model from the loaded settings (or the shipped
  local config) instead of the constant, so the probe always
  exercises what production calls. Live-only helper, never CI.

**Done when:** The probe resolves the production model id from
settings. Pinning the constant back to the old id fails that
test.

### T7.66: Run Gemini 3.8 Flash on Vertex global, price it right, and draw real covers
```yaml
requires:   T7.56
fixture-ok: yes
size:       M · frontier
owns:       config/reprise.box.toml, config/reprise.local.toml, internal/settings/settings.go,
             internal/settings/settings_test.go, internal/editorial/run.go,
             internal/editorial/run_test.go, internal/cover/cover.go, internal/cover/cover_test.go,
             cmd/reprise/main.go, cmd/reprise/finish.go, cmd/reprise/main_test.go,
             cmd/reprise/boot_test.go
status:     done:c8e7f74
```
**Findings.** A probe on 2026-09-28 used the project's own service account against Vertex.

* Every Gemini call runs `gemini-2.5-flash` (`editorial_model` in both config files) at
  `vertex_location = "us-central1"`.
* `gemini-3.8-flash` answers only at the `global` location. At `us-central1` it returns 404. At
  `global` it takes text and inline WAV audio, and it honours `ThinkingBudget`, so the T7.38 and
  T7.56 budgets keep working.
* The cover passes `editorial_model` to `GenerateImage`. A text model refuses with
  `Multi-modal output is not supported`. The one cover production ever drew reads
  `"Fallback":true` and `Price: 0`. No episode has had a generated cover.
  `gemini-3.1-flash-image` draws a 1024 by 1024 PNG at `global`.
* The AssemblyAI LLM Gateway is no substitute. Its chat completions accept text parts only, and
  this account gets `Your account does not have access to this LLM Gateway model` for every model.

**Prices,** from https://ai.google.dev/gemini-api/docs/pricing, read 2026-09-28.
* `gemini-3.8-flash`: $0.75 per million input tokens, for every modality, and $3.75 per million
  output tokens, thinking included. Both double on 2027-01-01.
* `gemini-3.1-flash-image`: $0.067 for a 1K image.

**Change.**
1. **Model and location.** In both config files, set `editorial_model = "gemini-3.8-flash"` and
   `vertex_location = "global"`. Add a `cover_model` setting to `internal/settings`. It is
   required and is set to `"gemini-3.1-flash-image"` in both files. Pass it to both
   `cover.Run` call sites, in `cmd/reprise/main.go` and `cmd/reprise/finish.go`, instead of the
   editorial model.
2. **Text rates.** In `cmd/reprise/main.go`, set `geminiPromptPerMillionUSD = 0.75` and
   `geminiCompletionPerMillionUSD = 3.75`. The comment names the model and the 2027 doubling.
3. **Editorial estimate.** `editorial.Estimate` prices audio minutes at `DollarsPerMinute = 0.0025`
   and never counts output. The call carries two stems. Audio bills about 32 tokens a second per
   stem, so 3840 input tokens a minute. The answer may reach `editorialMaxTokens` (8192), thinking
   included. Reserve both sides. Input is `ceil(audioSeconds * 64)` tokens plus the timeline
   tokens at 4 characters a token, all at the input rate. Output is 8192 tokens at the output
   rate. Then **settle on the measured `Usage`**. Price `Prompt` at the input rate and
   `Total - Prompt` at the output rate. Today the call settles the estimate itself. The rates
   reach the editorial run from `cmd/reprise/main.go` through its config, as chapters and marking
   receive theirs.
4. **Cover price.** Set `DollarsPerImage = 0.067`, and fix its comment. It is no longer a
   placeholder.

**Tests.**
* `internal/settings`: both config files load, and each carries `cover_model`.
* `internal/editorial`: `Estimate` for 60 seconds with an empty timeline equals
  `3840 * 0.75e-6 + 8192 * 3.75e-6` dollars, to the nanodollar. A run whose scripted usage is
  1000 prompt and 3000 total settles `1000 * 0.75e-6 + 2000 * 3.75e-6` dollars.
* `cmd/reprise`: the cover call receives `cover_model`, never `editorial_model`.
* A `live` tag probe beside `internal/gemini/live_test.go` runs editorial on a 20 second fixture,
  and a cover, against 3.8 and 3.1 Flash Image at `global`. It records tokens, price and the
  cover size in the handoff. It never runs in CI.

**Done when:** The tests pass under `go test -race`, and the gate passes in a fresh worktree. The
live probe draws a real cover and a non-fallback editorial answer. Pointing the cover back at
`editorial_model` fails the `cmd/reprise` test.

**Note.** The box reads `config/reprise.box.toml` baked into the image, so the change ships with
the next deploy. The service account needs no new role, because the probe ran on it.

---

## Exit criteria

- [ ] A fixture episode becomes a ready episode through the wired handlers with no network.
- [ ] No mint stays held after its end plus the sweep margin.
- [ ] The uploader reads its own bytes and anon reads carry the header.
- [ ] Round 2 of the production record re-measures every check on the deployed build.
- [ ] A live take renders with every host reply where the call played it.

---

## Handoff log

### What exists now

T7.66 landed at c8e7f74 after round 2 APPROVE with zero residue.
Editorial runs on 3.8 Flash at global with two-sided pricing
booked at the measured rate, and covers draw with the image
model at the flat price. The tree matches reviewed commit
74c95f0. Main had moved, so the landing rebased. Race tests on
editorial, settings, and cover passed on the rebased commit.
The live probe still names the old model. T7.67 owns that.
T7.62 landed at 76c4a27 after round 1 APPROVE with zero
findings. The record Start button binds in markup and stays
disabled until the controller exists. The tree matches reviewed
commit 57e545b. Main had moved, so the landing rebased. The
landing check ran under a box load average above 30 with
renderer stalls; the button resolved enabled in every run and
the reviewer proved 20 of 20 plus the mutation on the reviewed
code, so CI judges both the Chromium repeat and the WebKit
twenty. Husk worktree dirs with root-owned Playwright output
need `sudo rm -rf`; future image runs should pass `--user`.
T7.63 landed at 578409f after round 1 APPROVE with zero findings.
The gate holds each silenced step's output and prints it only
on failure, with verdicts unchanged. The tree matches reviewed
commit 00fe239. Main had moved, so the landing rebased. Bash
syntax clean on the rebased commit. CI proves the green run.
T7.65 landed at 2e7aa9a after round 1 APPROVE with zero findings.
The welcome suite runs in Chromium and WebKit, green ten times
in a row in the CI image. The tree matches reviewed commit
85e6b8e. Main had moved, so the landing rebased. The Chromium
welcome run passed on the rebased commit; a teaser failure on
one loaded run passed alone, which is load flake, not
regression. Cleanup note: the docker run wrote root-owned
`test-results` into the worktree, so the husk dirs need
`sudo rm -rf` (see below). Future image runs should pass
`--user` with the owner uid.
T7.58 landed at 3ea4c2e after round 1 APPROVE with zero in-scope
findings. The editor suite runs in Firefox and Chromium with
revert-all regression pins. The Firefox stall does not reproduce
on stubbed drafts: with stubbed audio and timings, revert
leaves Play and the waveform answering in both browsers. The
production stall likely needs real stem audio or live timings.
One out of scope M opened T7.60: the record repeat-completion
spec flakes under suite load. The tree matches reviewed commit
0dd823a. Main had moved, so the landing rebased. The edit suite
passed 26/26 on the rebased commit. CI judges the Firefox
install step.
T7.59 landed at f616218 after round 1 APPROVE with zero findings.
The host reply wait ends after a minute and the guest batch
runs alone. The tree matches reviewed commit 1fca47d. Main had
moved, so the landing rebased. Race tests on cmd/reprise passed
on the rebased commit.
T7.57 landed at 9390680 after round 2 APPROVE with zero residue.
Round 1 had one C and one H finding, both stale money-path
comments. The sweep charges the full cap for untraceable
sessions with a review alert for the refund. The tree matches
reviewed commit 87d3f61. Main had moved, so the landing rebased.
Race tests on broker, episode, and api passed on the rebased
commit. This blocks the next deploy: a crashed tab charges the
full $2.25 and the owner refunds from the alert.
T7.53 landed at e7ecc58 after round 1 APPROVE with zero findings.
The draft mixes and plays a two-voice preview on the word clock
through a free idempotent preview kind. The tree matches
reviewed commit 700dc51. Main had moved, so the landing rebased.
Race tests on render, api, and cmd/reprise passed on the rebased
commit, and the draft pin passed. Owns widened by decision to
the draft preview pin. T7.58 and T7.59 unblock on this landing.
T7.51 landed at 2cea171 after round 2 APPROVE with zero residue.
Round 1 had one H finding. Concurrent same-owner mints cited
the same callback. The landed pass claims the row conditionally
and reloads on a lost claim. The tree matches reviewed commit
029edcf. Main had moved, so the landing rebased. Race tests on
host, memory, and cmd/reprise passed on the rebased commit. The
mint-after-config callback loss stands as recorded: an error
path that refuses the mint can consume one unspoken callback.
T7.56 landed at 7ba505e after round 1 APPROVE with zero findings.
Chapter reasoning runs inside a 4096 cap with its own 1024
thinking budget. The tree matches reviewed commit c122b2e. Main
had moved, so the landing rebased. Race tests on gemini and
analysis passed on the rebased commit.
T7.55 landed at e0cfc42 after round 1 APPROVE with zero findings.
The cold open never replays the first 30 seconds. The tree
matches reviewed commit 7ca36ee. Main had moved, so the landing
rebased. The editorial race suite passed on the rebased commit.
T7.54 landed at 0b9acf2 after round 1 APPROVE with zero findings.
An unfinished take says what it is on its page, its card, and
its moment link. The tree matches reviewed commit 11c3752. Main
had moved, so the landing rebased. The threads pin passed on the
rebased commit. A mid-way closed take cannot resume stems: the
recorder keeps nothing and unuploaded audio dies with the tab.
T7.52 landed at b62d2e1 after round 1 APPROVE with zero findings.
All seven editor defects fixed, each with its own pin. The tree
matches reviewed commit 94445e2. Main had moved, so the landing
rebased. The draft pin passed on the rebased commit. Residual
risk stands: if Play still strands on a live draft, the next
step is the browser and the word timings.
T7.50 landed at e2403d1 after round 1 APPROVE with zero findings.
The processing page reports durable stems when a resume carries
no byte receipts. The tree matches reviewed commit 4b4ba31.
Main had moved, so the landing rebased. The processing-state
pin passed on the rebased commit.
T7.49 landed at cd4febb after round 1 APPROVE with zero findings.
The reconcile waits out the provider close over a 500 ms to 8 s
backoff before settling. It never settles a guessed duration.
The broker diff matches reviewed commit 8d1f572. Main had moved,
so the landing rebased. Race tests on broker passed on the
rebased commit. T7.48 and T7.49 landed together as the spec
requires.
T7.48 landed at cda7ff8 after round 1 APPROVE with zero findings.
The early provider id report posts to a settle-free provider
route instead of the session close. A real close still starts
exactly one reconcile. The tree matches reviewed commit 10d6d36.
Main had moved, so the landing rebased. Race tests on api and
cmd/reprise passed on the rebased commit. Owns widened by
decision to the route golden and the web mirror for the new
entry.
T7.47 landed at 6fee833 after round 1 APPROVE with zero findings.
CI was red on every `main` commit. The gate stopped at
`staticcheck` SA4006 on the T7.43 socket test, which hid four
`svelte-check` errors from T7.40 and two leakage scan collisions
on the T7.40 processing screen. The landed pass drops the dead
read, widens three promise types, and respells the two
collisions. Owns widened by decision to the two respell lines.
The tree matches reviewed commit 3d44caf. Main had moved, so the
landing rebased. Vet, staticcheck, and the race suite pass on the
rebased commit, and the leakage grep is clean. The 2026-09-27
Playwright failure does not reproduce on Chromium. CI is the
judge on WebKit.
T7.46 landed at 2ed5441 after round 2 APPROVE with zero residue.
Round 1 had one M finding. The speaker forward shipped with no
pinning test. The landed pass adds the detail endpoint pin on
both word lists. The api diff matches reviewed commit 01fefac.
Main had moved, so the landing rebased. Race tests on the api
package passed on the rebased commit. The speaker now travels
from the stored column to the editor.
T7.44 landed at 2c11452 after round 2 APPROVE with zero residue.
Round 1 had two H findings. The ledger pin ignored the new
speaker migration and the editorial loader never read the
speaker column. The landed pass expects the third migration and
selects the column into the timeline. The tree matches reviewed
commit 655f95e. Main had moved, so the landing rebased. The full
Go race suite passed on the rebased commit. Host words keep
their speaker from store to timeline. The detail endpoint still
drops the speaker. T7.46 owns that.
T7.45 landed at 1046053 after round 1 APPROVE with zero findings.
Erase ends the provider socket through the shared end before the
delete. A gone socket still deletes. The privacy diff matches
reviewed commit b80efeb. Main had moved, so the landing rebased.
Race tests on privacy and retention passed on the rebased commit.
T7.43 landed at 2a90f30 after round 3 APPROVE with zero residue.
Round 2 had one C finding. A sweep that settled and then failed
to end never tried the end again, so the socket stayed billable.
The landed pass marks a failed end pending and retries it on the
next sweep without settling twice. The broker diff matches
reviewed commit 9daf926. Main had moved, so the landing rebased.
Race tests on both packages passed on the rebased commit. Erase
still deletes without the end. T7.45 owns that.
T7.41 landed at b19efd5 after round 3 APPROVE with zero residue.
Round 2 had three H findings. A partial timeline parse failed the
job while reconcile was still writing, a terminal reconcile in
error blocked the guest batch, and an interrupted wait never
rescheduled. The landed pass waits out the partial document, the
terminal reconcile, and the interrupted wait, and uploads once.
The cmd diff matches reviewed commit 8699b58. Main had moved, so
the landing rebased. Race tests on cmd/reprise passed on the
rebased commit. The transcript carries the host's replies at
their reply times. Speaker labels stay with T7.44.
T7.40 landed at a0570e8 after round 3 APPROVE with zero residue.
Round 2 had two H findings. A reload after the draft was ready
rebuilt the upload as waiting with the episode link gone, and an
unlanded draft move was never reposted. The landed pass rebuilds
the finished upload from the address pair when the store is empty
and reposts it through the handoff path. The web tree matches
reviewed commit 7623334. Main had moved, so the landing rebased.
The processing-state unit pin passed on the rebased commit. End
asks the guest to confirm, the processing screen follows the
upload and both jobs, and links on once the draft is ready.
T7.39 landed at 2f9e55c after round 3 APPROVE with zero residue.
Round 1 had two H findings. A second pass could store another
private recording. Round 2 had one C. A crash after the file was
created and before the media row left the id claimed and the table
empty. The landed pass claims the id first, indexes a finished file
that has no row, and replaces a short file. The broker diff matches
reviewed commit 124ac4c. Main had moved, so the landing rebased.
Broker race tests passed on the rebased commit. One session keeps
one recording and one timeline. Money settles once.
T7.37 landed at cff1504 after round 2 APPROVE with zero residue.
Round 1 had one M finding. The mix trimmed the denoiser delay and
dropped the last 25 ms of the guest. The landed chain pads 1200
silent samples before the reducer and still trims that delay.
The render diff matches reviewed commit bc88251. Main had moved,
so the landing rebased. Render race tests passed on the rebased
commit. The stored stem stays raw. The floor drop on the fixture
is 24.82 dB and the speech band moves 0.09 dB.
T7.42 landed at 96dacf0 after round 1 APPROVE with zero findings.
The editorial and memory diff matches reviewed commit 6d0d648.
Main had moved, so the landing rebased. Race tests on both packages
passed on the rebased commit. A capped call settles the same
estimate as a bad decode in `editorial.Run`, `MarkEpisode`, and
`ResolveEpisode`. Any other model error still releases.
T7.36 landed at a19b827 after round 1 APPROVE with zero in-scope
findings. The web tree matches reviewed commit 19be55d. Main had
moved, so the landing rebased. Voice unit tests passed, 85 tests.
Chaaya is pinned to 0.2.1 in the package and in the planning files.
The record mock spec still expects both full host tones. T7.40 owns
that spec. A server delete still leaves a connected socket billable.
T7.43 owns that end.
T7.38 landed at 25cd697 after round 1 APPROVE with zero in-scope
findings. The gemini diff matches reviewed commit 2804f04. Main had
moved, so the landing rebased. The gemini race tests passed on the
rebased commit. The full gate still stops because ffmpeg is 9.0.2
and the pin is 9.0.1. A capped call returns `ErrTruncated`. T7.42 landed the settlement.
T7.34 landed at 72bee67 after round 3 APPROVE with zero residue. CI is
green on `main` again. Review probes under `dev-diary/probes/` now
live in their own Go module, so `go list ./...` skips them. The editor
draft keeps cut proposals in a record with no prototype.
T7.28 landed at 02e5823 after round 4 APPROVE with zero in-scope
findings and zero residue. A live draft opens the editor once its
editorial pass stores proposals. The editor plays the stored user stem
and posts decisions against stored proposal ids. The episode page
plays the newest render on a clock that follows its cuts and 10 ms
crossfades. A failed episode card names the pass that stopped.
T7.26 landed at 8947687 after round 1 APPROVE with zero findings.
Raw PCM stems now open as `audio/pcm` through the real upload
handler. A type outside the set still refuses with
`unsupported_type` at completion. Stored raw bytes head to WAV
from the stored stem rate through one builder, eager on link,
on locate for render, and on read in edit inputs. The draft move
links both stems and the duration probe reads the stored rate.
T7.25 landed at feecda1 after round 3 APPROVE with zero residue.
A draft move that finishes after the guest leaves the record page
does not open processing. A move that finishes while they are still
there still does.
T7.23 landed at 6d606b1 after round 1 APPROVE with zero findings.
Chunk 429s on the edge were the app gate. Uploads and the outer
rule now refill one token every 200ms. One two-stem take clears
both gates. Mint still stops at 6 per minute.
T7.22 landed at c829927 after round 2 APPROVE with zero residue.
An empty close still records. A connected socket posts the provider
id it learned, and a later empty close leaves that id in place.
T7.24 landed at e118730 after round 1 APPROVE with zero in-scope
findings. Every record phase links back to the gallery. One out of
scope M is now T7.25. A draft move that finishes after that click
can still assign `/processing`.
T7.21 landed after round 1 APPROVE with zero in-scope findings. The
bound runner sits behind a lock on all paths, ruled by an explicit
happens-before chain. The gate stops racing.
T7.20 landed after round 1 APPROVE with zero findings. Take calls
fail loudly by name with no double mint, and the clock freezes with
the take.
T7.19 landed after round 2 APPROVE with zero residue. Take budgets
fit machine traffic with honest hints, and retried completions heal
against linked pairs.
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

A pagehide beacon can carry the provider id while the call stays
open. Delete does not end a connected socket. Reconcile does not
send `session.end`. The client end is not enough when that frame
never leaves.
T7.38's truncation sentinel made a billed call look unspent.
`editorial.Run`, `MarkEpisode`, and `ResolveEpisode` released it.
A decode failure of received text still settled. T7.42 now settles
the capped call the same way.
T7.28 took four rounds. Each round found another state the gallery
card had never met: a pending editorial pass, a restart mid pass, and
a render the detail cannot see. The binary still spells the editorial
kind twice. T7.29 owns both fixes.

CI failed on ten pushes before anyone looked. Committed Go probes broke
`go list`, then a `Map.get` tripped the svelte 4 scan. Agents had run
the scans from `web/`, where the `web/src` pathspec matches nothing.
Run every gate step from the worktree root, and read CI after a push.

On this workstation ffmpeg is 9.0.2 against the 9.0.1 pin, so
`tools/check.sh` stops at its tool check. The owner accepted running
every later step by hand, with each exit code recorded.

The first real-voice takes on 2026-09-27 ran on `f79cb91` with the
owner on mic and speakers. Echo cancellation held in Chrome and
Firefox, and connected seconds matched the ledger both times. The
host stem lost its gaps (T7.36), and a long editorial answer came back
truncated (T7.38). `project.md` says both stems share one context
clock. The user stem does, but the host stem was never written on it.
Time to first host audio reached 8.7 seconds once, against a median
near 1.4 seconds. Nothing yet says whether that delay is ours.

The second round of real takes on 2026-09-28 ran on `63c0cb3`. The
tab close now ends the call within a second in both browsers, and the
server learns the provider id. The host stem keeps its gaps. Yet no
reconcile settled on any of the four runs (T7.49). The early id report
also starts a reconcile one second into each take (T7.48). The host
stem still steps 45 to 117 ms at reply boundaries, and the Firefox user
stem steps about 160 ms. Nobody has attributed either. The user stem
floor sat near -31 to -35 dBFS, against -51 on the first round. That
may be the room, and the rendered floor is not measured yet.
The sweep settled runs 1 and 2 at 06:10 UTC, for 40 and 55 seconds,
and stored both recordings. Their leases had already expired at
1800 seconds, so the lease rows carry no settled amount. The budget
tables do carry it. The opener also misread history twice (T7.51).
Takes closed mid-take leave their episode in `recording`.

### Notes for the next developer

Real-take findings, 2026-09-20, all measured on the live edge with
the owner present. First take died silently on Cloudflare challenge
pages (no audio, no transcript, no close persisted; owner
8a5ed00dc57318ede6b8372ad07e1072 episodes 1 to 3 all `recording`).
Owner deployed Configuration Rules (Browser Integrity Check and
Under Attack off for `/api/*` and `/media/*`). Second takes then
failed loudly: sparse chunk 429s (unattributed, needs a response
body) and session end 400 on the empty post. The container was
recreated mid-take at 10:10 UTC with no crash and no OOM; cause
unknown. Ceiling is open (zero held reservations). T6.4b holds one
partial Chrome run; browser fields beyond the user agent string are
still missing.
A later Chrome take opened both stems as `audio/pcm`. The allowlist
refused the open, so retry found no stored ids. T7.26 owns that fix.
T7.27 landed at a9f1237. A WebKit start opens the audio context and
requests the microphone before the session mint. A WebKit teaser
failure was then blamed on Chaaya's silent prime, as
https://github.com/nrynss/chaaya/issues/5. On 2026-09-28 it did not
reproduce. In the CI image, WebKit 26.6 primes that clip cleanly and
the teaser spec passed three runs of three. The failure was a flake
on one macOS host, and the issue is closed. T7.65 puts WebKit back
in the welcome suite.
T7.4 is deferred, not merely waiting: owner login plus the live
switch exercise ships as its own phase after dogfooding, not as a
leftover row here.
Owner login lands after dogfooding, so T7.4 waits past the
re-verification. T7.5 records that sequencing and skips the switch
flip until the login exists.
T7.1 and T7.2 own disjoint paths and may start together. T7.3 waits
on T7.2 for the shared `main.go`. T7.4 is blocked on the owner login
decision, not on code. T7.5 appends round 2 and never rewrites round 1.
T7.26 ran while a sibling orchestrator opened T7.27. Main moved
twice mid-task and the rebase landed clean. This session exposes
no shared memory tools, so nothing was recorded outside git. The
box carries ffmpeg 9.0.2 against the pinned 9.0.1, so the Go
subset stood in for the full gate on this task.
