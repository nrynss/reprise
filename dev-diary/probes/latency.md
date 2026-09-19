# Slow provider record (T2.10 audit)

No live call ran for this task. The figures below come from the recorded
probes, and probing spend stays at zero.

## Measurements on record

The provider answers about ten times slower than baseline. The baseline
put greeting `reply.done` near 4 seconds. Recent runs measured 47
seconds, 51.53 seconds, 46.84 seconds, and 51.88 seconds. Transport
stays healthy: `session.ready` lands near 2 seconds. Teardown stays
clean: `session.end` draws `session.ended` at once. The detail lives in
the T0.8 handoff beside the per run table. The sweep measurement lives
in `voice-agent.md` beside the T0.2 findings.

The product assumes a slow provider from here on. One host reply may
take 60 seconds or more. The canonical floors live in
`internal/broker/timeouts.go`, pinned by `timeouts_test.go` with value
assertions only. No check here uses a wall clock threshold.

## Bounds checked

| Bound | Location | Value | Verdict |
|---|---|---|---|
| Mint HTTP client timeout | `internal/assemblyai/token.go`, nil transport default | 30 s | Holds. A control plane GET, not a reply wait. Slow replies never slow it. |
| Token redemption window | `internal/assemblyai/token.go` `TokenExpirySeconds`, echoed by the broker response | 60 s | Holds. It gates first use only. The page dials at once after mint, and the 50 second greeting runs after redemption. |
| Session cap range | `internal/assemblyai/token.go`, `internal/broker/broker.go` | 60 to 10800 s | Holds. A length cap, not a reply bound. |
| Sessions fetch and delete HTTP timeout | `internal/assemblyai/terminate.go`, nil transport default | 30 s | Holds. Control plane reads. The sweep settles before deleting because the duration reads unreadable after. |
| Artifact fetch HTTP timeout | `internal/broker/reconcile.go` `NewHTTPArtifactFetcher` | 30 s | Holds. A CDN byte download, not model latency. A full session stereo recording fits with wide room. |
| Reconcile job attempts and resume | `internal/broker/reconcile.go` `Kind` | 3 attempts, idempotent, `Resume` rebuilds from the progress snapshot | Holds. The job runner sets no attempt deadline (work owns its deadlines), so slow reads never time out at this layer. |
| Sweep job attempts and resume | `internal/broker/sweep.go` `Kind` | 3 attempts, idempotent, `Resume` rebuilds from the record | Holds, same reasoning as reconcile. |
| Over cap margin | `internal/broker/reconcile.go` `DefaultMarginSeconds` | 60 s past the cap | Holds. A grace for clock skew and settle lag, not a reply give-up. |
| Sweep abandon margin | `internal/broker/sweep.go` `SweeperConfig.MarginSeconds` | Sweeper default, no production value wired yet | Holds on latency. See follow-up 4 for the missing wiring. |
| Socket `reply.done` wait | `web/src/lib/voice/socket.ts` `route` | Unbounded, no give-up | Holds. A 50 second reply arrives with no timer to beat it. |
| Socket `end` wait for `session.ended` | `web/src/lib/voice/socket.ts` `end` | Unbounded | Holds on latency, with a hang risk recorded as follow-up 1. |
| Browser session cap timer | `web/src/lib/voice/cap.ts` | Ends at the minted cap, warns 60 s ahead | Holds. It ends first by design and never waits on a reply. |
| Mint POST from the record page | `web/src/lib/voice/record-state.ts` through the Chaaya `api` client | No `timeoutMs`, unbounded | Holds. The call stays open through a slow mint. |
| Elapsed and processing refresh intervals | `record-state.ts`, `processing-state.ts` | 500 ms display polls | Holds. Display only, never a give-up. |
| Server HTTP timeouts | `cmd/reprise/main.go` `http.Server` | None set | Holds. Unbounded server timeouts never cut a slow session path. |
| Shutdown drain | `cmd/reprise/main.go` | Drain budget equals the session cap, 1 s poll, 30 s HTTP close grace | Holds. The drain budget tracks the cap, and the grace covers short control calls only. |
| Batch client timeout and transcript poll | `internal/assemblyai/batch.go` | 90 s per call, 5 s poll gap, context bounded | Holds. Post session work, and the poll gap is a gap, never a give-up. |
| Lease cap | `internal/broker/broker.go` lease manager | Session cap | Holds. A length bound, not a reply bound. |
| Spend gate rate limits | `cmd/reprise/main.go` | Per minute bursts | Holds. Mint rate only. |
| Guest cookie lifetime | `internal/identity/identity.go` | 180 days | Holds. Unrelated to reply speed. |

No bound below 60 seconds survives on the reply path. Nothing failed
the audit, so no failure pin stands. The floors that guard against
regression live in `timeouts_test.go`.

The T0.8 probe runner aborted a session when its greeting passed 25
seconds. That abort belonged to probe tooling chasing a fixture
tolerance. It must never become a product bound.

## Follow-ups (recorded, not built)

UX waiting states belong to a later screen task by plan. Everything
else below needs more than a constant swap, so it stays out of
`timeouts.go` and waits for its owning path.

1. Severity M. Owning path `web/src/lib/voice/socket.ts`. The socket
   never wires its close event: the constructor registers open and
   message only, while the browser handle exposes close. A silent drop
   with a pending `end` waiter hangs forever, and no event reaches the
   page. A slow provider stretches every wait, so a stranded wait hurts
   more. Pin: drive `VoiceSocket` through a fake handle, close the
   handle with an `end` waiter pending and no `session.ended`, and fail
   while the waiter never settles or no close surfaces.
2. Severity M. Owning path: the record screen task (`web/src/routes/record/`
   and `web/src/lib/voice/`). Nothing tells the guest the host is still
   working during a 50 second reply gap. The take looks dead while the
   provider thinks. Pin: drive the record page against a mock socket
   that holds `reply.done` back, and fail while no waiting state shows
   during the pending reply.
3. Severity L. Owning paths `internal/broker/reconcile.go` and
   `internal/broker/sweep.go`. Both kinds still carry inline `MaxAttempts: 3`
   and the margin default instead of the `timeouts.go` constants. Swap
   the literals for `ReconcileMaxAttempts`, `SweepMaxAttempts`, and
   `OverCapMarginSeconds` with no behavior change. Pin: the floors in
   `timeouts_test.go`, plus a source scan that fails while the literals
   stay inline (`git grep -n 'MaxAttempts: 3' internal/broker/`).
4. Severity C. Owning path `cmd/reprise/main.go`. Nothing wires the
   reconciler, the sweeper, or a job runner in production: no
   `NewReconciler`, `NewSweeper`, or kind registration appears in `cmd`
   or `internal` outside `internal/broker` itself. The sweep never runs,
   so an abandoned session bills unattended and every honest session
   leaves its reservation held until ceilings refuse new mints. Pin:
   search `cmd` and `internal` for the kind registration and fail on
   the empty result. After the fix, boot the wired server and fail
   unless both kinds answer as registered.
