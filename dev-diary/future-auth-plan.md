# Future authentication plan

This note records the identity shape to build after the next dogfood rounds.
It is a design boundary, not an implementation checklist.

## Purpose

Every visitor can remain anonymous or choose Google sign-in. Both choices
represent the same local person and the same private diary. Google sign-in
adds recovery across devices. It does not create a separate product path.

One Google subject holds operator privileges. That privilege is a role, not a
different identity model. The operator receives a seeded season like every
other empty user.

## Stable local identity

The server creates one local user and one server-backed session on a first
visit. The browser cookie holds only the signed session identifier. Episodes,
media, seeded rows, receipts, and spend limits belong to that local user.

Choosing Google sign-in attaches a verified Google subject to the existing
local user. It never creates a replacement user. The local user identifier
does not change, so existing recordings and seeded copies remain reachable.

Each Google subject maps to one local user. A returning person who signs in
on another device resumes that mapped local user and its season.

## Google sign-in

Google sign-in is optional for every visitor. It is not an operator-only
button. A visitor may record anonymously, then sign in later without losing
their diary.

The server uses Google OpenID Connect authorization code flow. It binds the
start and callback with state, nonce, and PKCE. The callback validates the
issuer, audience, expiry, nonce, signature, and the Google subject.

The Google subject is the identity key. Email is display information and never
an authorization key. The server rotates the local session after successful
sign-in and revokes the pre-sign-in session.

When a Google subject is already mapped, the callback resumes its mapped local
user. A callback never silently merges two local diaries. If the current
anonymous user already holds diary content, the product asks the visitor to
choose a safe path instead of moving recordings automatically.

No Google API permission is needed. OpenID Connect identity is the only
requested capability.

## Operator role

All authenticated Google users use the same identity flow. Operator access is
an authorization decision after identity validation. A configured allowlist of
Google subjects grants the operator role.

The role protects admin controls, including pause and spend views. It must not
change media ownership, episode access, or seeded-season eligibility. An
operator signs in through the ordinary visitor flow.

## Seeded season

The operator places one or two catalog episodes in `data/season/`. The catalog
media lives once. Each empty local user receives private copied rows when they
first list a non-empty season.

Seed eligibility does not depend on anonymous, Google-authenticated, or
operator status. A user with no season and no copy receipt receives the
catalog. A user who already has copies or recordings does not receive another
copy.

Promoting an anonymous user to Google sign-in preserves that user identifier,
their seeded copies, and their receipt. Deleting a seeded episode removes only
that user's copied rows. It never removes catalog media or another user's
copy. The receipt remains, so a deleted seed does not return.

## Scope boundaries

This first authentication release excludes passwords, email magic links,
Resend, general account merging, and mandatory sign-in. It also excludes
Google access to mail, calendar, Drive, or any other account data.

The implementation keeps guest-first recording. Authentication improves
continuity and unlocks operator controls. It never becomes a gate before a
visitor can understand the product.

## Dogfood questions

Dogfood determines the user-facing placement and wording of sign-in. It also
tests whether an operator needs a recovery method beyond Google and whether
cross-device continuation feels essential.

Before implementation, confirm the desired behavior when a signed-in Google
identity conflicts with an anonymous diary that already contains recordings.
No automatic diary merge is safe without an explicit, reversible design.
