# P2: Live session

```yaml
id:       P2
size:     M
requires: [P1, T0.2]
blocks:   P4
parallel: partly
runs-parallel-with: P3
```

**Goal:** A guest talks to the host, the host opens on a callback, both sides land on the server as
clean stems, and no path leaves money running.

**Why this is the money path:** A session costs $4.50 per connected hour, and a public URL can start
them. Every task here is judged first on what it can spend.

**What the libraries already carry here:** `keel/gate`, `keel/cost` with per-owner ceilings and the
paid call seam, `keel/lease`, `keel/flag`, `keel/job`, `keel/upload` and `keel/mediastore` on the
server. In the browser, Chaaya's `AudioRecorder` capturing on a context the page owns and draining
through `onChunk`, its filtered resampler, `encodeWav`, `PcmStreamPlayer` with a flush,
`SessionGuard`, `ChunkUploader` and `LiveLevel`. Reprise wires the voice path. It builds none of
those pieces.

---

### T2.1: Session broker ★
```yaml
requires:   T1.1, T1.4, T1.5
fixture-ok: yes
size:       S · frontier
owns:       internal/broker/, internal/assemblyai/token.go
status:     done:34303aa637b351c901215e1883c6c66654364419
```
**Build on:** Keel `v0.3.0`. `keel/gate` for the route, `keel/cost/sqlitestore` for the global daily
ceiling and the per-owner ceiling beneath it, `keel/flag` for the kill switch, `keel/lease` for the
session lease, and `keel/cost`'s paid call seam so no refusal path leaks a reservation. This task
holds the order of the checks and the provider call. It builds none of those mechanisms.

`POST /api/sessions` runs these in order and stops at the first refusal.

1. `gate.Protect` on the route, per client and global.
2. The kill switch, read at request time through `keel/flag`.
3. The guest's session quota.
4. The owner's ceiling and the global ceiling, reserved for the full session cap at $4.50 per hour.
5. A lease opened for the session cap, counted against the guest's quota.
6. `GET /v1/token` with `expires_in_seconds=60` and `max_session_duration_seconds` set to the cap.
7. A `sessions` row, then the response: token and session config from T2.2.

A refusal after step 4 releases the reservation it holds. A failed token call releases too. A
reservation settles when T2.3 reads the real connected seconds.

**Done when:** Both sides are pinned, because a broker that refuses everything satisfies the
refusals alone. **Passes:** an honest request with the switch off, the guest under quota and headroom
on both ceilings mints exactly one token, holds exactly one reservation and one lease, and writes one
`sessions` row. **Refuses:** each of the seven steps, in its own test, returns its code through
`wire.WriteError` with no token minted and no reservation left held. The API key appears in no
response body.

---

### T2.2: Host prompt and greeting ★
```yaml
requires:   T1.1
fixture-ok: yes
size:       M · frontier
owns:       internal/host/
status:     done:d202492c3a528a429e5421cc9a01c1ad869f40a3
```
Build the session config from stored rows only. It reads the `callbacks` and `mentions` tables from
T1.1. T4.2 fills `callbacks` later, so this task tests against fixture rows.

* **Greeting.** One sentence built from the planted callback row, with the episode it came from.
  With no callback, a warm first-episode opener.
* **System prompt.** The host's voice, the interview style, and up to three recent threads with
  quotes. The host interrupts to follow up and asks one question at a time.
* **Keyterms.** Up to 100 names from `mentions`, most recent and most frequent first.

A number or name the host says must exist in a row. The prompt tells the model to use only what it
is given.

**Done when:** A golden test builds config from the fixture season. Every name and count in the
greeting appears in a fixture row.

---

### T2.3: Session reconciler ★
```yaml
requires:   T2.1
fixture-ok: yes
size:       S · frontier
owns:       internal/broker/reconcile.go
status:     done:3788df18031fe871e4269680f37ed6b9f7f86d5e
```
**Build on:** `keel/job` with a kind named `reconcile`, and `keel/lease` for the settle and the
reconciliation, which records both the measured duration and the provider's. Reading the Sessions API
is safe to repeat,
so the kind sets `Idempotent: true`, `MaxAttempts: 3`, and a `Resume` that rebuilds the read.

* Read `duration_seconds` for each open session from `GET /v1/sessions/{id}`.
* Settle the reservation to the real cost on both ceilings, the owner's and the global one.
* Flag any session connected past its cap plus a margin. That should never happen, so it alerts.
* Store the artifact metadata. Fetch the stereo recording on receipt and persist it privately in
  `keel/mediastore`, because artifact URLs expire.

**Done when:** A test against recorded Sessions API responses settles every reservation and leaves
none held. A restart mid-reconcile resumes and settles exactly once.

---

### T2.4: Voice path and recorder ★
```yaml
requires:   T1.3, T2.1
fixture-ok: yes
size:       M · frontier
owns:       web/src/routes/record/, web/src/routes/processing/, web/src/lib/voice/
status:     done:bb25f89e3cf5d17fb637c0f69672bf2b72e068ae
```
**Mockup:** the `preflight`, `live` and `processing` views ([`mockup`](mockup), `#/preflight`,
`#/live`, `#/processing`).

**The processing screen belongs to the session that just ended.** When a take stops, the page stays
with it and draws upload, transcription and the editorial pass from `JobStream` until the draft is
ready. A guest who walks away instead sees the same work as progress on the gallery card, which is
T4.3's. The screen and the card are the same job read from two places, never two mechanisms.

**Build on:** Chaaya `0.2.0`, which carries the whole duplex path. This task wires it and owns
nothing general. `AudioRecorder` in `pcm` mode for the user stem, `PcmStreamPlayer` for the host, the
filtered resampler for the API copy, `encodeWav` for a file built from drained blocks, `ChunkUploader`
against `/api/uploads` for both stems, `LiveLevel` for the input meter, and `SessionGuard` for the
close. The guard ships under its own `guard` subpath; everything else here is `audio`. The uploader reports a recording complete only once every byte is durable, including across a
reload.

**The page owns the `AudioContext`.** It creates one at the device rate, passes it to the recorder,
and plays the host stream on it. That is what puts both stems on one clock, and it is why the page,
not the library, decides when the context closes.

**Set the recorder deliberately.** `autoStopSeconds: 0`, because the default stops a take after 12
seconds. `echoCancellation: true`. `noiseSuppression: false` and `autoGainControl: false`, because the
server's `voice_focus` does both jobs better. Chaaya documents the three together.

**Drain, never accumulate.** The recorder runs with `retain: false` and an `onChunk` that does three
things and returns: hand the block to `ChunkUploader.append()`, resample a copy to 24 kHz PCM16 for
`input.audio`, and keep nothing. A twenty minute session must hold no more memory than a one minute
session, and a reload nineteen minutes in must lose only the block that had not persisted yet.

What this task writes in `web/src/lib/voice/`, and nothing more:

* **The socket.** `session.update` with the system prompt, greeting, keyterms and tools, then the
  event loop for `reply.audio`, `reply.done` and `session.ended`.
* **The wiring.** Blocks to the uploader and to the socket, `reply.audio` to `PcmStreamPlayer`,
  `reply.done` with `interrupted` to the player's flush, and the flush time recorded as the mark in
  the host stem.
* **The ending.** The end control sends `session.end`, waits for `session.ended`, then closes. The
  guard sends it once on `pagehide` and on destroy.

**Done when:** Playwright drives a session against a mock socket that replays `testdata/sessions/`,
with generated input. Both uploaded stems pass `ffprobe`, and every marker is present in the user
stem. Memory is measured, not asserted: read the heap through the browser's own sampling, once after a
one minute synthetic take and once after a twenty minute one, and pin the ratio rather than a
megabyte figure, because an absolute bar measures the host. A
reload mid-session completes over exactly the persisted bytes. Closing the tab sends `session.end`
exactly once, confirmed in the mock's log.

---

### T2.5: Stem alignment ★
```yaml
requires:   T2.3, T2.4
fixture-ok: yes
size:       S · frontier
owns:       internal/align/
status:     in-progress:review-r3:t2.5-rev-r3@601469ffbf4277bf98ee8d789946514c6cd619d2
```
Place both stems on one episode clock and prove it. The clock is real rather than inferred, because
T2.4's page records and plays on one context: `CaptureChunk` carries `contextTime` per block, and the
stream player reports the scheduled start time of every block it played.

* Store each stem's start offset from that shared clock.
* Cross-correlate each local stem against its channel of the provider's stereo recording.
* Store the measured drift. Over 40 ms fails the episode's alignment check and shows a warning.

**Done when:** On all three fixture sessions, measured offsets agree with the recorder's offsets
within 40 ms.

---

### T2.6: Abandoned session sweep ★
```yaml
requires:   T2.3
fixture-ok: no
size:       M · frontier
owns:       internal/broker/sweep.go, internal/broker/reconcile.go,
            internal/assemblyai/terminate.go, internal/assemblyai/terminate_live_test.go
status:     done:424199f0ef8ba687ecd1d1aa068b8f5dd454ec0e
```
**Build on:** T0.2's record, which overturned the plan. The token cap does not end the session. Three
idle runs held a 60 second cap open past 100 seconds with no close and no error, and billing ran
until the client sent `session.end`. A bare socket close leaves the session resumable and billable,
and `expires_at` sits about an hour out whatever the cap says.

T2.3 alerts on a session connected past its cap and calls that impossible. T0.2 proved it is the
normal failure. A laptop that sleeps, a tab that crashes, or a dropped network leaves a paid socket
open with nothing on the server able to close it.

**Measure first, then build.** Nobody knows what ends a live session from the server side. A live
tagged test answers it on a real session, under the same spend rules T0.2 kept, with every token
capped at 120 seconds.

* Does `DELETE /v1/sessions/{id}` end a connected session, and does billing stop at the delete?
* Does the session still list, and what does `duration_seconds` read afterwards?
* Is there any other server side end, and does the socket see `session.ended`?

Record the answer in `dev-diary/probes/voice-agent.md` beside T0.2's findings.

**Then the sweep.** A `sweep` job kind reads every session row still open past its cap plus a margin.
It ends each one through whatever the measurement found, settles the lease and the reservation, and
records the outcome on the row. The kind is idempotent, because ending an ended session must be safe.

**If nothing ends a session from the server,** the record says so and the sweep settles and alerts
instead. Then the browser timer in T2.7 is the only stop, and the cap the broker mints becomes the
loss ceiling for one abandoned session. Write that down rather than leaving it implied.

**Settlement is wrong today either way.** T2.1 reserves for the full cap, and an abandoned session
bills past it, so the settle must handle a reservation that falls short instead of assuming headroom.
Bill from `session_duration_seconds` alone. `audio_duration_seconds` reads null on every run,
including runs that streamed audio.

**Done when:** The record answers the three questions with raw responses. A fixture session past its
cap sweeps, ends and settles exactly once, and sweeping it again changes nothing. A settle whose real
cost exceeds its reservation lands on both ceilings and leaves nothing held.

---

### T2.7: The browser's own session cap ★
```yaml
requires:   T2.4
fixture-ok: yes
size:       S · mid
owns:       web/src/lib/voice/cap.ts
status:     done:190ef02fbd51597ef391107264073f81c595553d
```
**Build on:** T2.4's socket and session guard. T0.2 proved the provider does not stop at the cap, so
`project.md` now says the browser runs its own timer and ends first. No task owned that timer. This
one does.

* The page starts a timer at `session_max_seconds`, the same cap the broker minted the token with.
* The timer ends the session the way the end control does. It sends `session.end`, waits for
  `session.ended`, then closes.
* The screen warns before the end arrives, so a guest is never cut off without notice.
* A clock that jumps, from a sleeping laptop or a suspended tab, ends the session on the next wake
  rather than waiting out the drift.

**Done when:** A Playwright run against the mock socket holds a session past its cap and sees exactly
one `session.end`, sent by the timer and not by a person. A second run wakes a suspended page past
the cap and sees the same. Neither run needs a wall clock threshold, because the mock drives time.

---

### T2.8: Wire the cap into the record page ★
```yaml
requires:   T2.4, T2.7
fixture-ok: yes
size:       S · mid
owns:       web/src/routes/record/, web/src/routes/record/cap.spec.ts
status:     done:2d86f2cad88158d3b206e94dd0f5eaf5c931f87f
```
Opened from T2.7 round 1's out-of-scope rows. The cap unit landed unwired: the record page never
constructs `SessionCap`, so no timer runs in the app and the Playwright remainder of T2.7's done
criterion had no page to drive.

* Build `SessionCap` beside the socket when the take goes live, with the end control path as
  `finish` and the broker `max_session_duration_seconds`. Call `stop` on every take end path,
  `wake` on show and visibility change, and render `warnText` in a live region with keyboard
  reachable controls while `warning` holds.
* The socket needs nothing new. Its latched `end` already gives the once-only close the timer
  calls into. If it cannot do what the page needs, raise a contract change instead of reaching
  into T2.4's files beyond this task's owns.

**Done when:** A Playwright run against the mock socket holds a session past its cap and sees
exactly one `session.end`, sent by the timer and not by a person. A second run wakes a suspended
page past the cap and sees the same. Neither run uses a wall clock threshold, because the mock
drives time.

---

## Exit criteria

- [ ] No path mints a token without passing gate, kill switch, quota, global budget and owner spend.
- [ ] Every session's connected seconds come from the provider and settle every reservation.
- [ ] No abandoned session bills unattended. Either the server ends it, or the record says why not.
- [ ] The voice path is tested on generated input, with no microphone and no timer thresholds.
- [ ] Stems align within 40 ms, measured against the provider's recording.

---

## Handoff log

### What exists now (Orchestrator-2)

T2.2 landed after a round 1 APPROVE with zero findings. `internal/host` builds the session config
from stored rows only. The rebase onto current main kept the gate green. T2.1 landed
(Orchestrator-2) after a round 1 APPROVE with zero findings. The ConfigBuilder stub stands as a
valid seam for the landed host shape, and the lease-to-session link waits on the store owner. The
rebase onto current main kept the gate green. That landing opens T2.3 and T2.4, both claimed.
T2.3 landed (Orchestrator-2) after round 2 APPROVE with zero residue against round 1. The expired
lease now completes the tail after settling, and the over-cap alert fires once per session on a
durable flag. Landing needed two rebases past the other orchestrator's T0.3 commits with a green
gate each time.
T2.4 landed (Orchestrator-2) after round 2 APPROVE with zero residue against round 1. The duplex
voice path drains without accumulating, ends exactly once on every path including pre-open, and
replays offline behind a mock socket. The first implementer never committed, so a takeover verified
the draft, fixed four defects, and committed. Round 1 found the pre-open end race; the fix
short-circuits open on the end latch. That landing opens T2.5 and T2.7, both claimed.
T2.7 landed (Orchestrator-2) after round 1 APPROVE with zero findings. The timer unit is proved
through the real socket on a fake clock, but the record page never constructs it, so T2.8 opens to
wire the cap into the page with the two Playwright runs.
T2.8 landed (Orchestrator-2) after round 2 APPROVE with zero residue against round 1. The cap
runs on the record page with the two mock-driven Playwright proofs. Round 1 found the dead
standing end control; the fix reaches the live button through delegation. P2 tasks T2.1 through
T2.4 and T2.6 through T2.8 are done; T2.5 is in its second remediation measuring the real
fixtures.
T2.6 landed (Orchestrator-2) after round 2 APPROVE with zero residue against round 1. The live
measurement stands: no server call ends a session, so the sweep settles first and deletes after,
with shortfall settle and idempotent delete. Round 1 fixed the audit row on alert failure. Total
live spend stayed near ten cents.

### What surprised us
Nothing yet.

### Notes for the next developer
Real voices across browsers moved to T6.4b.
