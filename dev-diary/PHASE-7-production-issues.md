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
status:     blocked:owner login, which lands after dogfooding
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
status:     in-progress:remediate-r1:t7.5-rem-r1
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

---

## Exit criteria

- [ ] A fixture episode becomes a ready episode through the wired handlers with no network.
- [ ] No mint stays held after its end plus the sweep margin.
- [ ] The uploader reads its own bytes and anon reads carry the header.
- [ ] Round 2 of the production record re-measures every check on the deployed build.

---

## Handoff log

### What exists now

T7.3 landed after round 1 APPROVE with zero findings. Upload opens
resolve to the session user and media refusals carry private
no-store. Keel issue nrynss/keel#1 tracks the library header. Only
T7.4 (blocked) and T7.5 remain.
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

Owner login lands after dogfooding, so T7.4 waits past the
re-verification. T7.5 records that sequencing and skips the switch
flip until the login exists.
T7.1 and T7.2 own disjoint paths and may start together. T7.3 waits
on T7.2 for the shared `main.go`. T7.4 is blocked on the owner login
decision, not on code. T7.5 appends round 2 and never rewrites round 1.
