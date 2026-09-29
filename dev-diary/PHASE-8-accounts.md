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
status:     in-progress:implement:t8.1-impl
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
status:     in-progress:implement:t8.2-impl
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
owns:       internal/settings/, config/, deploy/
status:     in-progress:implement:t8.3-impl
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
status:     not-started
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
owns:       internal/identity/sendlimit.go, internal/identity/sendlimit_test.go
status:     not-started
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

### T8.6: Sign-in screens
```yaml
requires:   T8.4, T7.81
fixture-ok: yes
size:       M · mid
owns:       web/src/routes/account/, web/src/lib/components/AccountLink.svelte,
             web/src/lib/components/SeasonNav.svelte
status:     not-started
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
status:     not-started
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
status:     not-started
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
status:     not-started
```
Replace `StubOwnerAuth` with a check that the request's user holds an identity listed in the
`operators` setting. An `email:` entry works as soon as T8.4 lands, and a `google:` entry works once
T8.8 does. A signed-in person not on the list gets `403 operator_required`, and a guest gets `401`.
The role changes nothing about ownership.

**Done when:** Tests cover a guest, a signed-in non-operator, an email operator, and a Google operator
against a stubbed identity. The gate passes in a fresh worktree. T7.4's live check then runs inside
T8.10b, because a task may not require a higher id.

### T8.10b: Live sign-in and operator run ★
```yaml
requires:   T8.7, T8.8, T8.9
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

Nothing yet. The phase opened on 2026-09-29.

### What surprised us

The proposed split renamed `guest_sessions` to `sessions`, which the diary already uses for recording
sessions. It stays `guest_sessions`. The split also put send limits in `internal/limits`, the admin
package, but they belong beside the send in `internal/identity`. The operator gate needed Google
only because the old plan assumed Google first. An `email:` operator entry removes that wait.

### Notes for the next developer

`gate.Limit.Every` in Keel v0.3.0 is the interval per token, not a window for the whole burst. That
cost the owner a locked gallery on 2026-09-28 (T7.79).
