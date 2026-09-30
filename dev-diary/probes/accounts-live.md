# Accounts live record

The live run for T8.10b on `https://reprise.nryn.dev`, which also carries T7.4's kill switch
exercise. The owner ran every step in a browser, and the orchestrator measured each one on the box
or from the workstation. Times are UTC.

| Step | Date | Result |
|---|---|---|
| Sending domain and secrets | 2026-09-29 | Passed |
| Sign in by code, Chrome | 2026-09-29 | Passed |
| Second device, "Switch to your account" | 2026-09-29 | Passed |
| Conflict, "Keep this device's diary" | not run | Not exercised live. Covered by the sign-in conflict tests only |
| Per-address send limit | not run | Skipped by the owner's decision. Covered by the send limit tests |
| Google sign-in | not run | Not configured. `GOOGLE_CLIENT_SECRET` is empty on the box |
| Operator kill switch (T7.4) | 2026-09-29 | Passed |
| Delete a throwaway account | 2026-09-30 | Passed for every Reprise row. Keel residue is a known gap |

## Sending domain and secrets

Mail goes out as `Reprise <reprise@send.nryn.dev>`, set by `mail_from` in
`config/reprise.box.toml`. The records resolve from a public resolver.

```
$ dig +short TXT send.send.nryn.dev @1.1.1.1
"v=spf1 include:amazonses.com ~all"
$ dig +short MX send.send.nryn.dev @1.1.1.1
10 feedback-smtp.ap-northeast-1.amazonses.com.
$ dig +short TXT resend._domainkey.send.nryn.dev @1.1.1.1
"p=MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBg...
```

`/etc/reprise/env` holds `RESEND_API_KEY`, copied from the workstation `.env` without being
printed, and `LOGIN_CODE_KEY`, generated on the box with `openssl rand -hex 32`. The file is
`root:root`, mode `0600`. The key is send-only, so `GET https://api.resend.com/domains` answers 401.
Delivery is the proof: every code email in the steps below arrived.

## Sign in and second device

* **Chrome.** The owner signed in by code as `email:rocknarayan@gmail.com`. The identity attached
  to the existing user `8a5ed00dc57318ede6b8372ad07e1072`, which held 12 episodes, and the user's
  kind became `owner`. The stored address was cleared, and `login_codes` held only hashes.
* **Firefox.** Signing in with the same address raised the conflict, and the owner chose "Switch to
  your account". Firefox then showed the same diary. The old Firefox guest session was revoked, and
  its guest diary was left to the retention sweep.
* **Operator.** `/admin` opened for the signed-in owner, because `operators` holds
  `email:rocknarayan@gmail.com`.

## Kill switch (T7.4)

The owner flipped the switch from `/admin`, signed in as the operator. The server log at the time
read as follows. The container has been replaced since, so the lines are no longer in
`docker logs`.

| Time | Event | Answer |
|---|---|---|
| 2026-09-29 17:11:33 | Pause from `/admin` | 200 |
| 2026-09-29 17:11:39 | `POST /api/sessions` from Record | 503 `sessions_paused`, "live sessions are paused" |
| 2026-09-29 17:11:49 | Resume from `/admin` | 200 |

No session was minted while the switch was on, so nothing was reserved. An honest session minted
after the resume. Its lease opened at 17:13:47 and closed reconciled, read from the database.

The row, read with Python's `sqlite3` module opening the file with `mode=ro`:

```
id          45159319ca26b456e1c574c65d4f4451
state       closed
reconciled  1
settled_nd  46250000
owner       e0b8b33e4f4919a98adc1c13f266cad1
```

The owner's own take on 2026-09-30 at 02:30:51 minted normally too. The site was never left paused.

## Account deletion

The first run on 2026-09-29 at 17:16 left `cost_owner_budget`, `lease_entry` and `session_settle`
rows. T8.22 fixed the Reprise rows. The owner then deleted a second throwaway account on
2026-09-30, after one short take and an episode erase.

| Time | Job | Status |
|---|---|---|
| 11:57:48 to 11:58:02 | preview, reconcile, edit_transcript, editorial | done |
| 11:58:13 | erase of episode `35d6325d5e7911e6ad7d93b2caeb4849` | done, 10 of 10 |
| 11:59:42 | account-delete of user `0e01b393c53010c3e8a8c387fdc13284` | done, `rows_done: true` |

A scan of every column of all 43 tables for the user id, run read-only on the box:

```
HIT cost_owner_budget owner 1
HIT lease_entry owner 1
HIT jobs progress 7
tables scanned 43 hits 9
media owned 0
```

* **Reprise tables.** Nothing remains. That covers episodes, sessions, stems, words, mentions,
  identities, guest sessions, login codes, `session_settle`, `reconcile_state` and `sweep_state`.
* **Media.** `find /srv/reprise -path '*<user>*' -o -path '*<episode>*'` finds 0 files, and no
  media row names the user.
* **AssemblyAI.** The newest session the Sessions API lists is from 2026-09-27. Every later
  session, this one included, was deleted once its artifacts were stored.
* **Keel residue, a known gap.** Three Keel tables still name the user, and Reprise has no Keel
  API to remove them. Reprise never writes to Keel's tables directly.
  * `cost_owner_budget`: one row, `limit_nd` 1000000000000, `spent_nd` 15523417.
  * `lease_entry`: one closed, reconciled session lease.
  * `jobs`: seven finished records whose `progress` names the user and the episode.
  * The budget and lease gap is https://github.com/nrynss/keel/issues/3. The job gap is
    https://github.com/nrynss/keel/issues/4. None of these rows holds content.
  * The first throwaway user `e0b8b33e4f4919a98adc1c13f266cad1` keeps the same two Keel rows.
    Both clear once Keel ships the APIs and Reprise calls them.
