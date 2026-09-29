# P8: Accounts

```yaml
id:       P8
size:     L
requires: [P7 dogfood rounds]
blocks:   T7.4
parallel: partly
```

**Goal:** A visitor who records as a guest can sign in with an emailed code and keep their diary on
any device. The owner signs in the same way and gains the operator role, which unlocks the admin
controls. Google sign-in follows as a second provider on the same model.

**Why now:** Owner login was deferred until after dogfooding. Dogfooding then proved why it matters.
With no login, the owner is a guest, so the guest session cap refused their own takes on
2026-09-29. The cap had to be lifted to 1000, which also removed it as a guard against strangers.
The kill switch still answers `owner_required` to everyone, so T7.4 cannot run.

**Decision, 2026-09-29.** The first provider is a one-time code sent by email through Resend. It
replaces the long recovery code, which would leave a permanent bearer credential in a mailbox. The
code is short, lasts 10 minutes, works once, is stored hashed, and allows few attempts. The person
types it on the device that asked for it. It is never a clicked link, because mail scanners prefetch
links and burn single-use tokens. Google OIDC follows as the second provider.
`dev-diary/future-auth-plan.md` records the model that still holds: one stable local user, and an
identity that attaches to it. Its exclusion of email and Resend is superseded by this decision.

**What stays true from that plan.**
* Guest-first recording. Sign-in is never a gate before a visitor understands the product.
* Signing in attaches an identity to the existing local user. The user id never changes, so
  recordings, seeded copies and receipts stay reachable.
* One identity maps to one local user. A returning person on a new device resumes that user.
* No automatic diary merge. When the device already holds a guest diary with content and the
  identity maps to a different user, the person chooses. They can keep this device's diary and
  sign in later, or switch to the account's diary and leave this one as a guest.
* The operator role is an authorization decision after identity, never a separate identity. It
  changes no ownership and no seed eligibility.

**Sending address.** `mail_from` is a setting, not code. A later domain change is one line of config.
Suggested value: `Reprise <reprise@mail.nryn.dev>`. A subdomain keeps sending reputation and DNS
records apart from anything else on `nryn.dev`. The address need not be a real mailbox. Resend
needs only a domain verified with its SPF and DKIM records. Check that against Resend's current
docs before T8.10b. `mail_reply_to` is optional. Unset, replies to the from address vanish, so set
it to an address the owner reads, or route that address through the DNS provider.

**Invariant.** `internal/mail` is the only code that talks to Resend. The owner approved it into
AGENTS.md on 2026-09-29.

**Spend.** Resend's free tier carries no per-message charge, so no send reserves budget. The send
limits in T8.5 are the ceiling. If the account moves to a paid plan, a send becomes a paid call and
must reserve first.

---

## Wave 1: independent foundations

### T8.1: Mail sender
```yaml
requires:   none
fixture-ok: yes
size:       S · mid
owns:       internal/mail/
status:     done:0dc9ea2
```
A Resend client over plain `net/http`. `internal/mail` is the only code that talks to Resend.

* `mail.Client` built by `mail.New(Config{APIKey, From, ReplyTo, BaseURL, HTTP})`. `BaseURL`
  defaults to Resend's API origin, and tests point it at a fake server.
* `Send(ctx, Message{To, Subject, Text, HTML}) (SendResult, error)`. It returns the provider
  message id. One sentinel per condition: `ErrInvalid`, `ErrRejected` for a 4xx, `ErrUnavailable`
  for a 5xx or a transport failure. Never match Resend's error text.
* `ReplyTo` is sent only when set.
* A consumer declares `type Sender interface { Send(...) }`, and `mail.Fake` records messages for
  tests.
* A `live` probe sends one message to an address named by an environment variable, and records the
  provider id in the handoff. It never runs in CI.

**Done when:** `go test -race ./internal/mail/` passes against the fake server. It covers the request
shape (from, to, subject, text, reply_to present only when set, the bearer header) and each
sentinel. The gate passes in a fresh worktree.

### T8.2: Identity schema
```yaml
requires:   none
fixture-ok: yes
size:       M · frontier
owns:       internal/identity/migrations/
status:     done:26af203
```
New migrations under the identity namespace. Keep `guest_sessions` as it is. The diary already has a
`sessions` table for recording sessions, so a rename would collide.

* `identities`: `id`, `user_id` referencing `users`, `provider` (`email` or `google`), `subject`
  (the normalized address for email, the OIDC `sub` for Google), `created_at`, and
  `UNIQUE (provider, subject)`. One user may hold several identities. One identity maps to one user.
* `login_codes`: `id`, `address_hash` (SHA-256 of the normalized address with the server key as
  HMAC), `code_hash` (HMAC as well), `requesting_session` (the guest session that asked),
  `expires_at`, `attempts`, `used_at`, `created_at`. It never stores a plain code. It stores the
  address in plain text only where the send needs it, and only until the code expires.
* An index for "sends to this address hash in the last day", which T8.5 reads.
* Signing in sets `users.kind` to `owner` (`KindOwner`). The retention sweep selects only
  `kind = 'guest'`, so a signed-in user is never swept, with no retention change.

**Done when:** The migrations apply on a copy of the production schema and on an empty one. A test
checks both unique constraints and the index. The gate passes in a fresh worktree.

### T8.3: Settings and secrets
```yaml
requires:   none
fixture-ok: yes
size:       XS · light
owns:       internal/settings/, config/, deploy/, cmd/reprise/boot_test.go
status:     done:0ab2f2c
```
Every setting this phase needs, added once so later tasks share no path.

* Secrets as references: `resend_api_key` (`env_file`, var `RESEND_API_KEY`),
  `google_client_secret` (`env_file`, var `GOOGLE_CLIENT_SECRET`), and `login_code_key`
  (`env_file`, var `LOGIN_CODE_KEY`) for the code and address HMAC.
* Inline: `mail_from`, `mail_reply_to` (optional), `google_client_id` (empty until T8.8 is set up),
  and `operators` (a list of `email:<address>` or `google:<sub>` entries).
* `deploy/README.md` names the new variables for `/etc/reprise/env`. No value appears in a tracked
  file.
* Settings load required values with the `config:"required"` tag, except the Google pair and
  `mail_reply_to`, which may stay empty.

**Done when:** Both config files load in `settings_test.go` with the new fields. A missing
`resend_api_key` fails boot by name. The gate passes in a fresh worktree.

**Decision, 2026-09-29.** Owns widened to `cmd/reprise/boot_test.go`, the test helper
only. The new required settings break five tests sharing its `bootSettings`, so the
helper must carry the new keys for the gate to pass. T8.4 owns `main.go` and
`main_test.go` in that directory, never this helper, so no task collides.

---

## Wave 2: the email code flow

### T8.4: Email code sign-in
```yaml
requires:   T8.1, T8.2, T8.3, T7.80
fixture-ok: yes
size:       L · frontier
owns:       internal/identity/, internal/api/routes.go, internal/api/routes_test.go,
             web/src/lib/api/types.ts, web/src/lib/api/testdata/routes.json,
             cmd/reprise/main.go, cmd/reprise/main_test.go
status:     done:2ea61d2
```
Two routes, both under the identity middleware.

* `POST /api/login/code` takes `{"email": "..."}`. It normalizes the address (trim, lower-case)
  and stores a fresh code for the requesting guest session. It sends the code through the declared
  `Sender`. It answers **the same 202 body** whether or not the address has an account, so the
  route never reveals who is registered. The code is 6 digits from `crypto/rand`, and lasts 10
  minutes. A new request invalidates earlier unused codes for that address and session.
* `POST /api/login/verify` takes `{"email": "...", "code": "..."}`. It accepts only a code
  requested by this same session, unexpired, unused, and within 5 attempts. On success it marks
  the code used and resolves the identity:
  * **No identity yet.** It attaches `email:<address>` to the current user and sets `kind = owner`.
  * **Identity on this user.** Nothing changes.
  * **Identity on another user, and the current guest holds no episodes.** It switches the device
    to that user.
  * **Identity on another user, and the current guest holds episodes.** It answers `409` with
    `code: "diary_conflict"` and changes nothing. The UI offers the two choices. A second call with
    `{"choice": "switch"}` performs the switch and leaves the guest diary to retention.
* On any successful sign-in or switch, it rotates the session. A new `guest_sessions` row and a new
  cookie are issued, and the old row is revoked.
* The mail says: "Your Reprise sign-in code is 123456. It expires in 10 minutes. If you did not ask
  for it, ignore this mail." It carries no link.
* Wire the routes in the route table, the browser mirror, and `cmd/reprise/main.go`.

**Done when:** Tests cover the same body for known and unknown addresses. A code from another session
is refused. The code dies after the sixth wrong try and after expiry, and works once. Each of the four
resolution cases is tested, and the session id changes on success. The store holds no plain code.
The gate passes in a fresh worktree.

### T8.5: Send limits
```yaml
requires:   T8.4
fixture-ok: yes
size:       M · frontier
owns:       internal/identity/sendlimit.go, internal/identity/sendlimit_test.go,
             internal/identity/login.go, internal/identity/login_test.go
status:     done:028169c
```
Refuse before the send, never after it. Every limit answers `429` with `Retry-After`.

* Per address: at most 5 codes in 24 hours, read from `login_codes` through the T8.2 index.
* Per client: at most 10 requests an hour, through `keel/gate`, keyed as the rest of the app keys
  clients. Remember that `gate.Limit.Every` is one token per interval.
* Global: at most 200 sends a day. Keep it under the Resend free tier.
* The identical-response rule still holds. A refused known address and a refused unknown address
  answer alike.

**Done when:** A fake clock drives each limit to refusal and past its window. The `Sender` fake shows
no send on a refused request. The gate passes in a fresh worktree.

**Decision, 2026-09-29.** Owns widened to `internal/identity/login.go` and its test,
the request path the checker protects. A checker no route calls refuses nothing,
so this task wires the check into the code request (identical 429 body, `Retry-After`
header) rather than leaving the call site to a later task.

### T8.6: Sign-in screens
```yaml
requires:   T8.4, T7.81
fixture-ok: yes
size:       M · mid
owns:       web/src/routes/account/, web/src/lib/components/AccountLink.svelte,
             web/src/lib/components/SeasonNav.svelte
status:     done:85394fb
```
* `SeasonNav` gains a quiet "Sign in" link. It reads "Account" once signed in.
* `/account`: an email field, then a 6-digit code field with a resend link that honours
  `Retry-After`. Signed in, it shows the address, "Sign out" and "Delete account". Copy stays
  plain, per the style T7.81 set.
* The conflict screen names both choices in plain words. "Keep this device's diary" leaves the device
  as it is. "Switch to your account" switches, and says the guest diary on this device will expire
  like any guest diary.
* Sign out revokes the session and starts a fresh guest.
* Svelte 5 runes only. Bits UI for the input primitives where they fit.

**Done when:** A Playwright spec against stubbed routes walks request, verify, conflict (both
choices) and sign out, in Chromium and Firefox. The gate passes in a fresh worktree.

### T8.7: Account deletion
```yaml
requires:   T8.4, T8.6
fixture-ok: yes
size:       M · frontier
owns:       internal/privacy/account.go, internal/privacy/account_test.go,
             web/src/routes/account/delete/
status:     done:008110d
```
* Deleting needs a fresh code, typed within the last 10 minutes, as re-authentication.
* It erases every episode through the existing erase fan-out. It then deletes the identity rows,
  the login code rows, every session row of the user, and the user row. No stored address survives.
* It runs as a `keel/job` with the erase's own reporting, because it can outlast a request.

**Done when:** A test deletes a user with two episodes and two identities, then queries every table
and the media directory for that user and finds nothing. The erased provider transcripts answer
gone. The gate passes in a fresh worktree.

---

## Wave 3: Google and the operator

### T8.8: Google sign-in
```yaml
requires:   T8.5, T8.6
fixture-ok: yes
size:       L · frontier
owns:       internal/identity/google.go, internal/identity/google_test.go,
             web/src/routes/account/google/
status:     done:c026836
```
The authorization code flow in `future-auth-plan.md`: state, nonce and PKCE. The callback validates
issuer, audience, expiry, nonce, signature and `sub`. The identity key is `google:<sub>`, never the
email. Resolution and the conflict rule are T8.4's, reused rather than copied. OpenID scope only.
A test Google issuer served by the test itself signs the tokens.

**Done when:** Tests cover a bad state, a bad nonce, a wrong audience, an expired token, and each
resolution case. The gate passes in a fresh worktree.

### T8.9: Operator role
```yaml
requires:   T8.4
fixture-ok: yes
size:       S · mid
owns:       internal/limits/
status:     done:9094573
```
Replace `StubOwnerAuth` with a check that the request's user holds an identity listed in the
`operators` setting. An `email:` entry works as soon as T8.4 lands, and a `google:` entry works once
T8.8 does. A signed-in person not on the list gets `403 operator_required`, and a guest gets `401`.
The role changes nothing about ownership.

**Done when:** Tests cover a guest, a signed-in non-operator, an email operator, and a Google operator
against a stubbed identity. The gate passes in a fresh worktree. T7.4's live check then runs inside
T8.10b, because a task may not require a higher id.

### T8.14: Account-deletion wiring
```yaml
requires:   T8.7, T8.13
fixture-ok: yes
size:       S · mid
owns:       cmd/reprise/privacy.go, internal/api/routes.go, internal/api/routes_test.go,
             cmd/reprise/main.go, cmd/reprise/main_test.go,
             web/src/routes/account/+page.svelte, web/src/routes/account/account.spec.ts,
             web/src/lib/api/testdata/routes.json, web/src/lib/api/types.ts,
             web/src/routes/account/account.ts, web/src/routes/account/account.test.ts
status:     done:c262e79
```
T8.7 ships deletion behind an unmounted handler and an unregistered job
kind: the route 404s and a restart drops a running deletion. The account
page still holds its entry inert.

* Register the account-delete kind beside the erasure kind in
  `cmd/reprise/privacy.go`, so restarts resume a deletion.
* Mount `POST /api/account/delete` behind the guest middleware in the
  route table and the boot, with golden and mirror updates if those
  files list routes individually.
* Flip the inert delete entry on the account page to link
  `/account/delete`, and update its spec.

**Done when:** A booted binary deletes an account end to end through the
route, survives a restart mid-deletion, and the account page links the
delete screen. The gate passes in a fresh worktree.

**Decision, 2026-09-29.** Owns widened to the route golden and the
browser mirror, which list routes individually and fail on the added
entry until they carry it (same pattern as T8.11). Round 1 also left a
dead `DELETE_SOON` export in the account client, so owns widened
further to `account.ts` and `account.test.ts` for its removal.

### T8.15: Google wiring
```yaml
requires:   T8.8, T8.14
fixture-ok: yes
size:       S · mid
owns:       internal/api/routes.go, internal/api/routes_test.go,
             cmd/reprise/main.go, cmd/reprise/main_test.go,
             web/src/lib/api/testdata/routes.json, web/src/lib/api/types.ts,
             web/package.json
status:     done:9514655
```
T8.8 ships the Google handler unmounted: both API paths 404 and the
google spec runs outside the e2e chain.

* Mount `GET /api/login/google/start` and `GET /api/login/google/callback`
  in the route table and the boot, configured from the Google settings
  (empty client id leaves the start refusing plainly, never crashing).
  Update the golden and the browser mirror if they list routes
  individually.
* Append the google Playwright config to the `test:e2e` chain.

**Done when:** A booted binary answers the start with a provider redirect
and the callback refuses a bad state. The google spec passes through the
chain on Chromium. The gate passes in a fresh worktree.

### T8.13: Operator wiring
```yaml
requires:   T8.9, T8.11
fixture-ok: yes
size:       S · mid
owns:       internal/identity/identity.go, cmd/reprise/main.go, cmd/reprise/main_test.go,
             web/src/routes/admin/
status:     done:a5e5d3b
```
T8.9 ships the `OperatorAuth` seam against a stubbed source, and production
still wires `StubOwnerAuth`, so every admin endpoint keeps answering
`owner_required` to everyone. The web admin client branches on
`owner_required` and would misread the new `operator_required` refusals.

* Add a read-only `identities` row reader on the identity service
  (`provider, subject` for one `user_id`), and wire
  `limits.NewOperatorAuth` with the `operators` setting in the boot.
* Teach the admin client the `operator_required` code and the 401
  against 403 split. Copy stays plain.

**Done when:** A booted binary answers 401 for a guest, 403 for a signed-in
non-operator, and serves an email operator on an admin route. The admin
page names the operator refusal in plain words. The gate passes in a fresh
worktree.

### T8.10b: Live sign-in and operator run ★
```yaml
requires:   T8.7, T8.8, T8.9, T8.14, T8.15
fixture-ok: no
size:       S · frontier
owns:       dev-diary/probes/accounts-live.md
status:     not-started
```
The one task that needs a person and production.

* Verify the sending domain in Resend, add its SPF and DKIM records, and check them against Resend's
  current docs. Put the key and the code key in `/etc/reprise/env`.
* Sign in by code on Chrome, then on Firefox as the same address. Both devices show the same diary.
* Exercise the conflict with a guest that already holds a take, and try both choices.
* Hit the per-address send limit, and see the `Retry-After`.
* Sign in with Google, if T8.8 is configured.
* As the operator, run T7.4: flip the kill switch, watch a mint refuse, flip it back.
* Delete a throwaway account, and confirm by query that nothing remains.

**Done when:** Every step above is recorded with commands and output in `accounts-live.md`.

### T8.11: Sign-out route
```yaml
requires:   T8.4
fixture-ok: yes
size:       S · frontier
owns:       internal/identity/signout.go, internal/identity/signout_test.go,
             internal/api/routes.go, internal/api/routes_test.go,
             cmd/reprise/main.go, cmd/reprise/main_test.go, web/package.json,
             web/src/lib/api/testdata/routes.json, web/src/lib/api/types.ts
status:     done:c1659b4
```
T8.6 round 1 proved no sign-out route exists: the account page calls
`POST /api/login/signout` against an unrouted path, so the session cookie
stays valid and the task's "revokes the session and starts a fresh guest"
sentence is unmet.

* `POST /api/login/signout` under the identity middleware. It revokes the
  current `guest_sessions` row, creates a fresh guest user with a fresh
  session following the T1.4 guest conventions, and sets the cookie.
  It answers the same 200 body for guests and signed-in users alike.
* Wire it in the route table, `cmd/reprise/main.go`, and the browser mirror
  only if the mirror lists routes individually (read it first).
* Append `playwright test -c src/routes/account/account.playwright.config.ts`
  to the `test:e2e` chain in `web/package.json`, so the T8.6 account spec
  runs in CI and the gate.

**Done when:** A test signs in, signs out, then shows the old cookie resolves
as a stranger and the device holds a fresh guest. The account spec passes
through the `test:e2e` chain. The gate passes in a fresh worktree.

**Decision, 2026-09-29.** Owns widened to the route golden
(`web/src/lib/api/testdata/routes.json`) and the hand mirror
(`web/src/lib/api/types.ts`). Both list routes individually, so the
golden test fails on the added entry until they carry it.

### T8.12: Session-status route
```yaml
requires:   T8.4, T8.11, T8.13, T8.14, T8.15
fixture-ok: yes
size:       S · frontier
owns:       internal/identity/status.go, internal/identity/status_test.go
status:     done:0260e01
```
T8.6 round 1 proved the account page trusts `localStorage` alone: a seeded
store renders a signed-in screen with zero API calls, desynced from the
session cookie.

* `GET /api/login/status` under the identity middleware. It answers whether
  the session's user holds an email identity and, when so, its address.
  Guests answer signed out. It never reveals anything but the caller's own
  address.
* The account page adopts it as the source of truth in a later task. This
  task ships the route only, with its wiring in the route table and boot
  through the existing identity service seams (read them first and widen
  owns by decision if the seam forces it).

**Done when:** Tests cover a guest, a signed-in user, and a signed-in user
whose session was revoked elsewhere (answers signed out). The gate passes
in a fresh worktree.

### T8.16: Status-route wiring
```yaml
requires:   T8.12
fixture-ok: yes
size:       XS · light
owns:       internal/api/routes.go, internal/api/routes_test.go,
             cmd/reprise/main.go, cmd/reprise/main_test.go,
             web/src/lib/api/testdata/routes.json, web/src/lib/api/types.ts
status:     done:373abd0
```
T8.12 ships the status handler unmounted: the path 404s and the golden
does not list it.

* Mount `GET /api/login/status` in the route table (beside the signout
  entry) and the boot (beside the login mount), through the existing
  identity service. Update the golden and the browser mirror.

**Done when:** A booted binary answers the status for a guest and a
signed-in user. The golden and mirror tests pass. The gate passes in a
fresh worktree.

### T8.17: The gate scan trips on the Google query parsing
```yaml
requires:   T8.8
fixture-ok: yes
size:       XS · light
owns:       web/src/routes/account/google/google.ts,
             web/src/routes/account/google/google.test.ts
status:     in-progress:review-r1:t8.17-rev-r1@e78c93ace89c865077e3b17926b389aafa99f7c0
```
The gate's Svelte 4 leakage scan (`\b(get)\s*\(`, meant for Svelte
stores) matches `params.get(` in `google.ts`, so the gate is red on
`main`. T8.8's review ran the scan by hand and missed it.

* Read the query without a `.get(` call shape (for example through
  `Object.fromEntries`), keeping behavior identical. Do not weaken
  the gate pattern instead: the check is load-bearing elsewhere.

**Done when:** The exact gate grep from `tools/check.sh:131` returns
empty on `main`, and the google unit tests still pass. The gate
passes in a fresh worktree past the leakage step.

---

## Exit criteria

- [ ] A guest signs in by code and keeps their diary across two devices.
- [ ] No code, and no address past its code's life, sits in plain text in the database.
- [ ] Known and unknown addresses get the same response, and every send limit refuses before sending.
- [ ] The operator reaches the admin controls, and T7.4 has run.
- [ ] An account deletion leaves nothing behind.

---

## Handoff log

### What exists now

T8.16 landed at 373abd0 after round 1 APPROVE with zero in-scope
findings. The status route mounts beside sign-out with golden and
mirror carrying it. One out of scope H stays open under T8.17: the
gate leakage scan trips on the Google query parsing. Reviewed
commit 7958f5d rebased clean. Race tests pass on the landed
commit.
T8.12 landed at 0260e01 after round 1 APPROVE with zero findings.
The status handler answers the caller's own address or signed
out, read-only, with mounting scoped into T8.16. Reviewed commit
bffd61c rebased clean. Race tests pass on the landed commit.
T8.16 unblocks on this landing.
T8.15 landed at 9514655 after round 1 APPROVE with zero findings.
Both Google routes mount from settings with a loud misconfiguration
path, the golden and mirror carry them, and the google spec runs in
the e2e chain. Reviewed commit 511dc56 rebased clean. Race tests
pass on the landed commit. T8.8's Done is now met end to end except
the live provider hit, and T8.12 unblocks on this landing.
T8.14 landed at c262e79 after round 2 APPROVE with zero residue.
Account deletion runs end to end: mounted route, registered kind
with restart resume behind a bind gate, linked delete screen.
Round 1 had one H (golden and mirror missing the entry) plus a
dead copy export pulled into scope; both closed with load-bearing
pins. Reviewed commits 736ad3d and 6670ac5 rebased clean. Race
tests pass on the landed commit. T8.7's Done is now met end to
end, and T8.15 unblocks on this landing.
T8.8 landed at c026836 after round 1 APPROVE with zero findings.
Google OIDC code flow with strict token checks, subject-keyed
identity, and start plus callback pages, probed against algorithm
confusion, replay, and open redirects. Done holds at unit level:
the handler mounts in T8.15, so both API paths 404 for now.
Reviewed commit 38ba0c3 rebased clean. Race tests pass on the
landed commit.
T8.13 landed at a5e5d3b after round 1 APPROVE with zero findings.
The operator check runs in production behind the operators
setting, with a read-only identity adapter and plain admin
refusals. Reviewed commit f56ee9b rebased clean. Race tests pass
on the landed commit. T8.14 unblocks on this landing.
T8.7 landed at 008110d after round 1 APPROVE with zero in-scope
findings. The deletion core is pinned: fresh-code re-auth through
the sign-in verifier, full fan-out erasure, identity, code,
session, and user row removal, no stored address, resumable job.
Done holds at unit level only: the route, the kind registration,
and the account page link land in T8.14, so no account deletes
end to end yet. Reviewed commit bcb16c9 rebased clean. Race
tests pass on the landed commit.
T8.11 landed at c1659b4 after round 2 APPROVE with zero residue.
Sign-out revokes the session and mints a fresh guest behind one
shared body, with the golden and mirror carrying the route and the
account spec running in the e2e chain. Round 1 had one H (golden
and mirror missing the entry). Reviewed commits aabac5b and
e608d32 rebased clean. Race tests pass on the landed commit.
T8.6's sign-out sentence is met, and T8.13 unblocks on this
landing.
T8.9 landed at 9094573 after round 1 APPROVE with zero findings.
`OperatorAuth` gates admin endpoints behind the operators setting,
with 401 for guests and 403 `operator_required` for signed-in
non-operators, proved wireable for T8.13. Reviewed commit af5066e
rebased clean. Race tests pass on the landed commit. T8.13
unblocks on T8.11 now.
T8.6 landed at 85394fb after round 2 APPROVE with zero residue.
The account page walks the mailed code flow with conflict choice
and sign-out against stubbed routes, pinned on Chromium and
Firefox. Round 1 had one L (dead delete link); the remediation
holds the entry inert until T8.7 ships its screen. Three out of
scope items stay open under T8.11 and T8.12: no sign-out route
yet, so "revokes the session and starts a fresh guest" is unmet,
localStorage-only auth state, and the account spec outside the
e2e chain. Reviewed commits 26bd49a and 4d6ea89 rebased clean.
Unit pins pass on the landed commit.
T8.4 landed at 2ea61d2 after round 2 APPROVE with zero residue.
Email code sign-in runs two routes behind the guest middleware,
with identical 202s, hashes-only storage, four resolution cases,
and session rotation in one transaction. Round 1 had one H (plain
address outlived expiry) and one M (concurrent first attach lost
500). The remediation stores no plain address at all and heals
racing attaches inside the transaction, both pins proved
load-bearing by mutation. Reviewed commits 83c5495 and aab6caf
rebased clean. Race tests on identity, api, and the binary pass
on the landed commit. T8.5 unblocks on this landing.
T8.5 landed at 028169c after round 2 APPROVE with zero residue.
Round 1 had one H (checker wired to no route) and one M (gate
probe spent client budget before the durable global check). Owns
widened to the request path by decision, and the remediation wires
identical 429s with Retry-After and orders durable checks first,
both pins proved load-bearing by mutation. Reviewed commits
64fa4a1 and e0fc2e9 rebased clean. Race tests pass on the landed
commit. T8.6 unblocks on this landing.
T8.1 landed at 0dc9ea2 after round 1 APPROVE with zero findings.
`internal/mail` carries a Resend client over plain `net/http`, with
`Sender` and `Fake` for consumers and a `live`-tagged probe. Reviewed
commit e443c20 rebased clean onto main. Race tests pass on the landed
commit.
T8.2 landed at 26af203 after round 2 APPROVE with zero residue.
Round 1 had one H finding, no standing test for the new guards.
The remediation added a test beside the migrations that pins the
provider subject uniqueness and the send lookup index, with both
pins proved load-bearing by mutation. Reviewed commits d0e0e72 and
39c385f rebased clean. Race tests pass on the landed commit.
T8.3 landed at 0ab2f2c after round 2 APPROVE with zero residue.
Round 1 had one H finding: five `cmd/reprise` tests sharing the
boot helper failed on the new required keys. Owns widened to the
helper by decision, and the remediation carries the keys there.
Reviewed commits 3b838c3 and 0bf3ac6 rebased clean. Race tests on
settings and the binary pass on the landed commit.
Wave 1 is done. T8.4 unblocks on this landing.

### What surprised us

The proposed split renamed `guest_sessions` to `sessions`, which the diary already uses for recording
sessions. It stays `guest_sessions`. The split also put send limits in `internal/limits`, the admin
package, but they belong beside the send in `internal/identity`. The operator gate needed Google
only because the old plan assumed Google first. An `email:` operator entry removes that wait.

### Notes for the next developer

`gate.Limit.Every` in Keel v0.3.0 is the interval per token, not a window for the whole burst. That
cost the owner a locked gallery on 2026-09-28 (T7.79).
