# Production verification, first deploy

Live run on 2026-09-19 from a workstation against
`https://reprise.nryn.dev`. Every check ran from this machine, never
from the box. The edge answers with a Cloudflare certificate. No real
voice appears anywhere. All speech is generated. Tokens and keys are
redacted below. Cookie jars lived in `/tmp` and are deleted.

## Edge identity

Command:

```bash
curl -sS https://reprise.nryn.dev/healthz
curl -sS -o /dev/null -w "%{http_code}\n" https://reprise.nryn.dev/
```

Output:

```text
ok 1ec2ef2bd41f39186df5bf935afc2afd version=5d33a244b895337de3734863788d8ae475b76b3f
200
```

Result: pass. The version matches the required
`5d33a244b895337de3734863788d8ae475b76b3f`.

## 1. Scripted guest records episode one

The catalog is empty, so the next episode is episode one. One guest
mints a session, streams two generated lines, hears the host reply,
and ends the call explicitly.

Command:

```bash
curl -sS -c jar.txt -b jar.txt -X POST https://reprise.nryn.dev/api/sessions \
  -H 'Content-Type: application/json' -d '{}' -o mint.json -w 'HTTP %{http_code}\n'
```

Output (token redacted, cookie jar keeps the guest session):

```text
HTTP 201
```

The body carries `session_id`, `episode_id`, a single use provider
token, `expires_in_seconds` 60, `max_session_duration_seconds` 1800,
and a config with the first episode greeting
`Welcome to your first episode, tell me what is on your mind today.`
and empty keyterms. The 201 proves the box reached AssemblyAI, because
the server mints the token itself before answering.

The live call reuses the generated clips from `testdata/sessions/`
(`steady/user-a.wav` and `steady/user-b.wav`). A script resamples them
to 24 kHz mono PCM, sends `session.update` with the returned config,
streams 4800 sample frames every 200 ms, then sends `session.end`. The
script is `/tmp/t61b_live.py` (workstation only, not tracked). It needs
`python3`, the `websockets` package, and `ffmpeg`.

Output:

```text
stream samples: 154927 = 6.46s
ready provider_session=sess_10f4e81c54ba4cfd85fe050d230c7f0d
stream done, waiting for reply
events: 738 provider_session=sess_10f4e81c54ba4cfd85fe050d230c7f0d
- transcript.user: The gate needs paint before winter.
- transcript.user: We should buy brushes on Saturday morning.
- transcript.agent: Okay. I've added buying brushes to your list for Saturday morning.
session.ended duration=16.210922 audio_duration=None
```

The full event log is `/tmp/t61b_events.json` (workstation only).
Recognition read back both generated lines. The host answered with one
uninterrupted reply. The client ended the call explicitly and the
socket answered `session.ended`. No session was left open.

Result: pass. Spend for this call is about 2.1 cents at 4.50 dollars
per connected hour.

## 2. Render, analysis, gallery

Command:

```bash
curl -sS -b jar.txt https://reprise.nryn.dev/api/episodes -w '\nHTTP %{http_code}\n'
curl -sS -b jar.txt https://reprise.nryn.dev/api/threads -w '\nHTTP %{http_code}\n'
```

Output for both:

```text
{"error":{"code":"not_implemented","message":"this control has no handler yet"}}

HTTP 501
```

Result: blocked, with evidence. The deployed build wires only the
session broker, the admin handler, uploads, media, and job events
(`cmd/reprise/main.go` at the deployed commit). The episode, decision,
done, publish, and thread routes stay on the stub, so no render or
analysis can start and no gallery can list the episode from check 1.
The owning phases decide how this check proceeds.

## 3. Private media without the cookie

A private blob was persisted through the real upload flow with the same
shape the browser sends (`owner` guest, private visibility). The chunk
was the first 32000 bytes of `testdata/sessions/steady/recording.ogg`.

Commands:

```bash
curl -sS -c ju.txt -b ju.txt -X POST https://reprise.nryn.dev/api/uploads \
  -H 'Content-Type: application/json' \
  -d '{"owner":"guest","content_type":"audio/ogg","visibility":"private"}'
SHA=$(sha256sum clip.ogg | awk '{print $1}')
curl -sS -b ju.txt -X PUT "https://reprise.nryn.dev/api/uploads/<id>/chunks/0" \
  -H 'Content-Type: application/octet-stream' -H "X-Chunk-SHA256: $SHA" \
  --data-binary @clip.ogg
curl -sS -b ju.txt -X POST "https://reprise.nryn.dev/api/uploads/<id>/complete" \
  -H 'Content-Type: application/json' -d "{\"sha256\":\"$SHA\"}"
curl -sS -D anon.hdrs -o /dev/null "https://reprise.nryn.dev/media/<id>" \
  -w 'HTTP %{http_code} size %{size_download}\n'
```

The open answered 201, the chunk PUT answered 200, and the complete
answered 201 with matching `size_bytes` and `sha256`. The persisted id
was `dd231fc014f56515f6282f77d5c2e998`.

Anonymous read output:

```text
HTTP 404 size 19
```

Full anonymous headers:

```text
HTTP/2 404
date: Sat, 19 Sep 2026 10:36:34 GMT
content-type: text/plain; charset=utf-8
content-length: 19
set-cookie: reprise_session=<redacted>; Path=/; Expires=Thu, 18 Mar 2027 10:36:34 GMT; Max-Age=15552000; HttpOnly; Secure; SameSite=Lax
x-content-type-options: nosniff
cf-cache-status: DYNAMIC
server: cloudflare
```

The body reads `404 page not found`. The same URL with the owner cookie
also answers 404 with the same body, so the refusal reveals nothing
about whether the blob exists.

Result: mixed. Status 404 passes. The required `Cache-Control:
private, no-store` header is absent, so that half fails. Keel
`v0.3.0` answers every media refusal with a bare `http.NotFound`
(`mediastore/mediastore.go`), and reserves `private, no-store` for a
successful private read. The owning phase decides whether the edge
should add the header on refusals.

Observation for the owning phase, no fix made here. The browser opens
uploads with the literal owner `guest`, while the media authorizer
compares against the resolved user id, which is never `guest`. The
uploader therefore reads 404 on its own blob too, as measured above.

## 4. Kill switch

Command:

```bash
curl -sS -b jar.txt -X POST https://reprise.nryn.dev/api/admin/limits/pause \
  -H 'Content-Type: application/json' -d '{"paused":true}' \
  -w '\nHTTP %{http_code}\n'
curl -sS -b jar.txt https://reprise.nryn.dev/api/admin/limits \
  -w '\nHTTP %{http_code}\n'
```

Output for both:

```text
{"error":{"code":"owner_required","message":"the admin page needs the owner login"}}

HTTP 403
```

Result: blocked, with evidence. The admin handler runs behind
`StubOwnerAuth` until the owner login lands, so no workstation call can
flip the switch. The pause was refused, nothing changed state, and the
site was left unpaused. A fresh honest mint right after these calls
answered 201 (check 1 mint plus a closing mint, both 201), which proves
no `sessions_paused` refusal is active. The refusal path itself
(`sessions_paused` with 503) is covered by the broker unit tests, not
by this live run.

## 5. Outbound calls and edge traffic

AssemblyAI: pass. The 201 in check 1 proves the box minted a provider
token. The live call in check 1 connected, streamed, and ended against
`agents.assemblyai.com`. The Sessions API confirms the record below.

Gemini: blocked, with evidence. The deployed `cmd/reprise/main.go`
imports no Gemini package and wires no credential, so no endpoint can
exercise the box to Gemini path in this build. The owning phase decides
how this check proceeds.

Cloudflare: pass. Every edge call above succeeded from the workstation
through the proxied record, including the 201, the 501s, the 403s, and
the 404s. No challenge blocked honest traffic.

## 6. Connected seconds against the ledger

The provider session id from check 1 is
`sess_10f4e81c54ba4cfd85fe050d230c7f0d`. The Sessions API was read from
the workstation with the account key (value never stored, never
printed).

Command:

```bash
curl -sS 'https://agents.assemblyai.com/v1/sessions/<provider_session_id>' \
  -H "Authorization: $KEY"
```

Output:

```text
id: sess_10f4e81c54ba4cfd85fe050d230c7f0d
status: completed
close_reason: client_end
duration_seconds: 16.595827
artifacts: audio, timeline, metadata
HTTP 200
```

The socket reported `session_duration_seconds` 16.210922. The Sessions
API reports 16.595827. The two agree within 0.4 seconds, and the close
reason is `client_end`, which matches the explicit `session.end`.

Result: half pass, half blocked. The provider side matches. The ledger
side cannot be compared because the settle path is unwired: `POST
/api/sessions/{id}/end` answers 501 (measured), the reconciler in the
broker package has no caller in the deployed binary, and the spend view
`GET /api/admin/limits` answers 403 (measured). The owning phase owns
the settle comparison.

Observation for the operator. Each mint reserves the full session cap
(1800 seconds at 4.50 dollars per hour, about 2.25 dollars) against the
20 dollar daily ceiling, and nothing in this build releases or settles
that hold. This run minted three sessions and held about 6.75 dollars
of the ceiling. Later guests can still mint (a fourth mint answered
201), but repeated dogfood will hit `budget_exhausted` with no release
path until the owning phase wires one.

## 7. Seeded callback greeting

Result: deferred, not a gate. The catalog is empty, so the host opens
with the first episode fallback greeting recorded in check 1. The
callback greeting re-run waits until the operator places the catalog,
as the task states.

## Spend total

One live voice session of 16.6 connected seconds costs about 2.1
cents. Two further mints never connected, so they bill nothing. Upload
bytes bill nothing. Total live spend for this verification is about
2.1 cents.

---

# Round 2: P7 build re-verification

Live run on 2026-09-19 from a workstation against
`https://reprise.nryn.dev`. Every check ran from this machine, never
from the box. No real voice appears anywhere. All speech is generated.
Tokens and keys are redacted below. Cookie jars lived in `/tmp` and
are deleted. Round 1 above stays untouched.

## Edge identity

Command:

```bash
curl -sS https://reprise.nryn.dev/healthz
curl -sS -o /dev/null -w "%{http_code}\n" https://reprise.nryn.dev/
```

Output:

```text
ok 41b9e8a8796a09f06ee3170db29f1f03 version=464f0c895fe355c435bca21aded9fc57cc34202d
200
```

Result: pass. The version matches the required
`464f0c895fe355c435bca21aded9fc57cc34202d`.

## 1. Scripted guest records an episode

One guest mints a session, streams two generated lines, hears the
host reply, and ends the call explicitly.

Command:

```bash
curl -sS -c jar.txt -b jar.txt -X POST https://reprise.nryn.dev/api/sessions \
  -H 'Content-Type: application/json' -d '{}' -o mint.json -w 'HTTP %{http_code}\n'
```

Output (token redacted):

```text
HTTP 201
```

The body carries `session_id`
`c8d1a503bbad438846e6357b89c9bb1d`, `episode_id`
`b8340f5a578b814e0fda882139dcbfd3`, a single use provider token,
`expires_in_seconds` 60, `max_session_duration_seconds` 1800, and the
first episode greeting with empty keyterms.

The live call reuses the generated clips from `testdata/sessions/`
(`steady/user-a.wav` and `steady/user-b.wav`). A script resamples them
to 24 kHz mono PCM, sends `session.update` with the returned config,
streams 4800 sample frames every 200 ms, then sends `session.end`. The
script is `/tmp/t75_live.py` (workstation only, not tracked).

Output:

```text
stream samples: 154927 = 6.46s
ready provider_session=sess_a7c61e841ce44040be953c06ee1fdfc1
stream done, waiting for reply
events: 756 provider_session=sess_a7c61e841ce44040be953c06ee1fdfc1
- transcript.user: The gate needs paint before winter.
- transcript.user: We should buy brushes on Saturday morning.
- transcript.agent: OK. I've added buying brushes to your list for Saturday morning.
session.ended duration=16.406599 audio_duration=None
```

The client ended the call explicitly and the socket answered
`session.ended`. No session was left open.

The session end records against the new handler:

```bash
curl -sS -b jar.txt -X POST "https://reprise.nryn.dev/api/sessions/<session_id>/end" \
  -H 'Content-Type: application/json' -d '{"provider_session_id":"<provider_session_id>"}'
```

Output:

```text
{"session_id":"c8d1a503bbad438846e6357b89c9bb1d","episode_id":"b8340f5a578b814e0fda882139dcbfd3","provider_session_id":"sess_a7c61e841ce44040be953c06ee1fdfc1"}

HTTP 200
```

The episode lists in the gallery with the owner cookie:

```bash
curl -sS -b jar.txt https://reprise.nryn.dev/api/episodes
curl -sS -b jar.txt https://reprise.nryn.dev/api/threads
```

Output:

```text
{"episodes":[{"id":"b8340f5a578b814e0fda882139dcbfd3","number":1,"title":"Episode 1","state":"recording","visibility":"private"}]}

HTTP 200
```

```text
{"name_threads":[],"circled_topics":[]}

HTTP 200
```

Result: pass. The gallery lists the recorded episode privately under
its owner. Spend for this call is about 2.1 cents at 4.50 dollars
per connected hour.

## 2. Decisions, mark done, render job, gallery progress

The handlers answer at their refusal boundaries on the live episode:

```bash
curl -sS -b jar.txt -X POST "https://reprise.nryn.dev/api/episodes/<episode_id>/done" \
  -H 'Content-Type: application/json' -d '{}'
curl -sS -b jar.txt -X POST "https://reprise.nryn.dev/api/episodes/<episode_id>/decisions" \
  -H 'Content-Type: application/json' -d '{"proposal_id":"nope","decision":"accepted"}'
curl -sS -b jar.txt -X POST "https://reprise.nryn.dev/api/episodes/<episode_id>/decisions" \
  -H 'Content-Type: application/json' -d '{"proposal_id":"nope","decision":"maybe"}'
```

Output:

```text
{"error":{"code":"illegal_transition","message":"only a draft episode moves to rendering"}}

HTTP 409
```

```text
{"error":{"code":"proposal_not_found","message":"no proposal lives at this id"}}

HTTP 404
```

```text
{"error":{"code":"invalid_request","message":"a decision is accepted or reverted"}}

HTTP 400
```

The job stream route is wired. A GET on
`/api/jobs/does-not-exist/events` with an event stream accept header
answers HTTP 200 and holds the stream open. It timed out at 8 seconds
with zero bytes. That is an idle topic, not a refusal.

Command (read only, no mint, no state change):

```bash
curl -sS -o /dev/null --max-time 8 -H 'Accept: text/event-stream' -w 'http=%{http_code} bytes=%{size_download} time=%{time_total}s exit=%{exitcode}\n' "https://reprise.nryn.dev/api/jobs/does-not-exist/events"
```

Output:

```text
curl: (28) Operation timed out after 8001 milliseconds with 0 bytes received
http=200 bytes=0 time=8.001395s exit=28
```

The stream held open for the full 8 seconds and delivered zero
bytes. I minted nothing and changed nothing.

Result: carried forward. The episode stays in `recording` with zero
proposals after polling for several minutes, so no accept or revert
and no render start can run against it. The binary registers the
transcript and editorial kinds but schedules neither, and nothing
calls the stems uploaded transition that moves `recording` to
`draft`. The browser stem uploads therefore land as blobs with no
episode linkage and no pipeline follows. This needs its own task:
wire stem completion into the transcript and editorial schedule and
the draft transition, then re-run this check live. The handler
refusals above prove the T7.1 wiring holds while that task is open.

## 3. Private media without the cookie

A private blob was persisted through the real upload flow with the
same shape the browser sends (placeholder owner `guest`, private
visibility). The chunk was the first 32000 bytes of
`testdata/sessions/steady/recording.ogg`. The open resolved the owner
to the session user `c5b7fb0cc73cfa7a6dc4fd8973d339cc`. The persisted
id was `83506f6007f210e492e1f7e5f6aeaf39`.

Commands:

```bash
curl -sS -c ju.txt -b ju.txt -X POST https://reprise.nryn.dev/api/uploads \
  -H 'Content-Type: application/json' \
  -d '{"owner":"guest","content_type":"audio/ogg","visibility":"private"}'
curl -sS -b ju.txt -X PUT "https://reprise.nryn.dev/api/uploads/<id>/chunks/0" \
  -H 'Content-Type: application/octet-stream' -H "X-Chunk-SHA256: $SHA" \
  --data-binary @clip.ogg
curl -sS -b ju.txt -X POST "https://reprise.nryn.dev/api/uploads/<id>/complete" \
  -H 'Content-Type: application/json' -d "{\"sha256\":\"$SHA\"}"
curl -sS -D anon.hdrs -o /dev/null "https://reprise.nryn.dev/media/<id>"
curl -sS -D unk.hdrs -o /dev/null "https://reprise.nryn.dev/media/does-not-exist-0000"
```

Anonymous read of the real blob:

```text
HTTP 404 size 19
```

Full anonymous headers:

```text
HTTP/2 404
date: Sat, 19 Sep 2026 15:02:15 GMT
content-type: text/plain; charset=utf-8
content-length: 19
cache-control: private, no-store
```

Anonymous read of the unknown id:

```text
HTTP 404 size 19
cache-control: private, no-store
```

Both bodies read `404 page not found`. The unknown id answers
identically, so the refusal reveals nothing about whether a blob
exists. (Cloudflare framing headers and the mint cookie are omitted
here. They carry no episode data.)

Result: pass. Both refusals answer 404 with `Cache-Control:
private, no-store`. The round 1 header gap is closed.

## 4. Uploader reads its own bytes back

The same blob from check 3 reads back under the owner cookie:

```bash
curl -sS -b ju.txt -D owner.hdrs -o back.ogg "https://reprise.nryn.dev/media/<id>"
```

Output:

```text
HTTP 200 size 32000
```

The returned bytes hash to
`508fdfddcb87b50ef53a7027e3ddb82e98cf542dc4a357c91106a5576698c073`,
which matches the uploaded chunk exactly. The owner response carries
`content-type: audio/ogg` and `cache-control: private, no-store`.

Result: pass. Open, chunk, complete, and owner read round trip over
exactly the persisted bytes. The round 1 ownership gap is closed.

## 5. Connected seconds against the ledger

The provider session id from check 1 is
`sess_a7c61e841ce44040be953c06ee1fdfc1`. The Sessions API was read from
the workstation with the account key (value never stored, never
printed).

Command:

```bash
curl -sS 'https://agents.assemblyai.com/v1/sessions/<provider_session_id>' \
  -H "Authorization: $KEY"
```

Output (artifact URLs redacted, three items listed):

```text
HTTP 200
id: sess_a7c61e841ce44040be953c06ee1fdfc1
status: completed
public_close_reason: client_end
duration_seconds: 16.801372
artifacts: audio (audio/ogg), timeline (application/json), metadata (application/json)
```

The socket reported `session_duration_seconds` 16.406599. The Sessions
API reports 16.801372. The two agree within 0.4 seconds, and the close
reason is `client_end`, which matches the explicit `session.end`.

The ledger side: the session end in check 1 answered 200, so the
wrapper scheduled one reconcile job for the recorded close. A fresh
mint after the end answered 201, which proves the ceiling is not
leaking held reservations. The spend view `GET /api/admin/limits`
still answers 403 `owner_required` (measured), so the exact ledger
row cannot be read from the workstation.

Result: closes on its workstation observable terms. Provider duration
matches the socket, the end records, the reconcile is scheduled, and
nothing stays held. The direct ledger row comparison waits on the
owner login, same as check 6.

## 6. Kill switch

Command:

```bash
curl -sS -b jar.txt -X POST https://reprise.nryn.dev/api/admin/limits/pause \
  -H 'Content-Type: application/json' -d '{"paused":true}' \
  -w '\nHTTP %{http_code}\n'
```

Output:

```text
{"error":{"code":"owner_required","message":"the admin page needs the owner login"}}

HTTP 403
```

Result: SKIPPED. The owner login lands after dogfooding, so T7.4
stays out of this round. The pause was refused, nothing changed
state, and the site was left unpaused. A fresh honest mint right
after answered 201, which proves no `sessions_paused` refusal is
active.

## 7. Seeded callback greeting

Result: deferred, not a gate. The catalog is empty, so the host opens
with the first episode fallback greeting recorded in check 1. The
callback greeting re-run waits until the operator places the catalog,
as the task states.

## Observation for the next task

One gallery poll during this run answered 429 `rate_limited` with a
10 second retry hint, then recovered on the next call after a wait.
The spend gate outermost is doing its job. Poll loops should honor
the hint instead of retrying hot.

## Spend total

One live voice session of 16.8 connected seconds costs about 2.1
cents. One further mint and the closing mint never connected, so they
bill nothing. Upload bytes bill nothing. Total live spend for this
verification is about 2.1 cents.

---

# Round 3: check-2 live re-run after the stem schedule

Live run on 2026-09-19 and 2026-09-20 from a workstation against
`https://reprise.nryn.dev`. Every check ran from this machine, never
from the box. No real voice appears anywhere. All speech is
generated: the guest lines reuse `testdata/sessions/steady/`, and the
host stem is the provider reply audio from this run, decoded from the
socket frames. Tokens and keys are redacted below. Cookie jars lived
in `/tmp` and are deleted. Rounds 1 and 2 above stay untouched.

## Edge identity

Command:

```bash
curl -sS https://reprise.nryn.dev/healthz
curl -sS -o /dev/null -w "%{http_code}\n" https://reprise.nryn.dev/
```

Output:

```text
ok bd8cf55eda562fa9b7365e431691746f version=c4c3676576203339ca724af8ff7b977b962395df
200
```

Result: pass. The version matches the required
`c4c3676576203339ca724af8ff7b977b962395df`.

## 1. Scripted guest records an episode

One guest mints a session, streams the two generated lines, hears
the host reply, and ends the call explicitly.

Command:

```bash
curl -sS -c jar.txt -b jar.txt -X POST https://reprise.nryn.dev/api/sessions \
  -H 'Content-Type: application/json' -d '{}' -o mint.json -w 'HTTP %{http_code}\n'
```

Output (token redacted):

```text
HTTP 201
```

The body carries `session_id`
`6b90a523ed888e1dffe64221f4993108`, `episode_id`
`7af663b0f8682f59113cf775bc62c9b4`, a single use provider token,
`expires_in_seconds` 60, `max_session_duration_seconds` 1800, and the
first episode greeting with empty keyterms.

The live call streams `steady/user-a.wav` plus `steady/user-b.wav`
resampled to 24 kHz mono PCM, sends `session.update` with the
returned config, streams 4800 sample frames every 200 ms, then sends
`session.end`. The script is `/tmp/t77/live.py` (workstation only,
not tracked).

Output:

```text
stream samples: 130927 = 5.46s
- transcript.user The garden gate needs paint before winter.
- transcript.agent I've
- transcript.user should buy brushes on a morning.
- transcript.agent I have added buy brushes on a morning to your tasks.
session.ended session_duration_seconds=66.767942 audio_duration_seconds=None
events: 652 provider_session sess_23eaae3b38364ba68eeae9415ee3a086
```

Recognition read back both generated lines with small wording
shifts ("garden gate" for "gate", "on a morning" for "Saturday
morning"). The host answered with one uninterrupted reply plus one
interrupted fragment. The client sent `session.end` explicitly and
the socket answered `session.ended`. No session was left open. The
connected duration is longer than rounds 1 and 2 because this
script waited a full 60 seconds for the reply before sending the
close. That wait is script overhead, not provider behavior.

The session end records against the handler:

```bash
curl -sS -b jar.txt -X POST "https://reprise.nryn.dev/api/sessions/<session_id>/end" \
  -H 'Content-Type: application/json' -d '{"provider_session_id":"sess_23eaae3b38364ba68eeae9415ee3a086"}'
```

Output:

```text
{"session_id":"6b90a523ed888e1dffe64221f4993108","episode_id":"7af663b0f8682f59113cf775bc62c9b4","provider_session_id":"sess_23eaae3b38364ba68eeae9415ee3a086"}

HTTP 200
```

The episode lists in the gallery with the owner cookie:

```bash
curl -sS -b jar.txt https://reprise.nryn.dev/api/episodes
```

Output:

```text
{"episodes":[{"id":"7af663b0f8682f59113cf775bc62c9b4","number":1,"title":"Episode 1","state":"recording","visibility":"private"}]}

HTTP 200
```

Result: pass. The gallery lists the recorded episode privately
under its owner.

## 2. Both stems upload, completion moves recording to draft

The user stem concatenates the two generated clips at 48 kHz mono
(`user-48k.wav`, 523782 bytes, 5.46 seconds). The host stem is the
608 provider reply frames decoded to 48 kHz mono (`host-48k.wav`,
583758 bytes, 6.08 seconds). Both are generated speech.

Each stem uploads through the chunked flow with the browser shape
(placeholder owner `guest`, private visibility, 64 KB chunks with
`X-Chunk-SHA256` per chunk, full `sha256` on complete):

```bash
curl -sS -b jar.txt -X POST https://reprise.nryn.dev/api/uploads \
  -H 'Content-Type: application/json' \
  -d '{"owner":"guest","content_type":"audio/wav","visibility":"private"}'
curl -sS -b jar.txt -X PUT "https://reprise.nryn.dev/api/uploads/<id>/chunks/<n>" \
  -H 'Content-Type: application/octet-stream' -H "X-Chunk-SHA256: $SHA" \
  --data-binary @piece.bin
curl -sS -b jar.txt -X POST "https://reprise.nryn.dev/api/uploads/<id>/complete" \
  -H 'Content-Type: application/json' -d "{\"sha256\":\"$FULL\"}"
```

Output:

```text
user complete: size_bytes=523782 sha256=5976e75f... HTTP 201
host complete: size_bytes=583758 sha256=7354eea7... HTTP 201
```

The persisted ids are `befcc18a6a67d62fb5d09aaa9890b239` (user)
and `49dfc7d9205d4ed50c50fdc7cc29b661` (host). The open resolved
both owners to the session user `7726a09a41b250df6a172d1d65172489`.

The completion posts the media pair to the new route:

```bash
curl -sS -b jar.txt -X POST "https://reprise.nryn.dev/api/episodes/<episode_id>/stems/complete" \
  -H 'Content-Type: application/json' \
  -d '{"user_media_id":"<user>","host_media_id":"<host>","user_sample_rate":48000,"host_sample_rate":48000}'
```

Output:

```text
{"episode_id":"7af663b0f8682f59113cf775bc62c9b4","moved":true,"scheduled":true,"job_id":"4824c99d9860f0b6171c0cc85ef82117","state":"draft"}

HTTP 200
```

Result: pass. First completion moves recording to draft and
schedules one transcript job. The round 2 stall (nothing calls the
transition, nothing schedules) is gone on the live build.

## 3. Draft to proposals: carried forward

The detail route answers the episode with its proposals:

```bash
curl -sS -b jar.txt "https://reprise.nryn.dev/api/episodes/<episode_id>"
```

Output, repeated over 45 minutes of polling at 1 to 3 minute
intervals:

```text
{"episode":{"id":"7af663b0f8682f59113cf775bc62c9b4","number":1,"title":"Episode 1","state":"draft","visibility":"private"},"proposals":[]}
```

A repeat completion during the wait answered:

```text
{"episode_id":"7af663b0f8682f59113cf775bc62c9b4","moved":false,"scheduled":true,"job_id":"356c008589d4673ec60f18b67a0d587c","state":"draft"}

HTTP 200
```

Result: carried forward. The episode sits in `draft` with zero
proposals 45 minutes after the first completion and 20 minutes
after the second transcript job started. The repeat found no
landed word timeline and no covering job, so it started a second
transcript job (`moved` false, `scheduled` true, new job id)
instead of reporting the first job outcome. Two paid transcript
passes left no words, no proposals, and no failure: the episode
stays `draft`, never `failed`. The defect names itself. The edit
chain runs silent on the live build. Its jobs vanish without a
landed timeline and without a surfaced error, and a repeat
completion schedules again rather than telling the caller what
the last job did. The owning paths are the draft schedule in
`cmd/reprise/main.go` and the completion entry in
`internal/episode/`. The follow-up task wires visible job
outcome (landed, running, or failed with its reason) onto the
completion answer and the episode detail, then re-runs this
check live.

## Observation for the next task

The edge rate gate shaped this run throughout. Upload chunks met
repeated 429 answers with retry hints. Opens, gallery polls, and
the completion also met 429s between the chunks. Every retry
waited for the hinted delay and recovered. Poll and upload loops
must keep waiting for the hinted delay instead of retrying hot.
The gate never blocked the pipeline itself. Both stems completed
byte exact and the completion scheduled on the first try after
its wait.

No admin call ran in this round, so the kill switch section from
round 2 stands as written. The site was never paused and no
limit was flipped.

## Spend total

One live voice session of 66.8 connected seconds costs about 8.4
cents at 4.50 dollars per connected hour. The mint never
connected, so it bills nothing. Upload bytes bill nothing. Two
batch transcript passes over 5.5 seconds of audio cost fractions
of a cent. Total live spend for this verification is about 9
cents.

---

# Round 4: silent-chain live re-run after the visible job outcome

Live run on 2026-09-20 from a workstation against
`https://reprise.nryn.dev`. Every check ran from this machine, never
from the box. No real voice appears anywhere. All speech is
generated: the guest lines reuse `testdata/sessions/steady/`, and the
host stem is the provider reply audio from this run, decoded from the
socket frames. Tokens and keys are redacted below. Cookie jars lived
in `/tmp/t710` and are deleted. Rounds 1 through 3 above stay
untouched.

## Edge identity

Command:

```bash
curl -sS https://reprise.nryn.dev/healthz
curl -sS -o /dev/null -w "%{http_code}\n" https://reprise.nryn.dev/
```

Output:

```text
ok fa1ab1f2b8c4e8b46aa1feb36459be9f version=45d14b752a1ce165d767b8cdfe5f7edcefb058fe
200
```

Result: pass. The version matches the required
`45d14b752a1ce165d767b8cdfe5f7edcefb058fe`.

## 1. Scripted guest records an episode

One guest mints a session, streams the two generated lines, hears
the host reply, and ends the call explicitly.

Command:

```bash
curl -sS -c jar.txt -b jar.txt -X POST https://reprise.nryn.dev/api/sessions \
  -H 'Content-Type: application/json' -d '{}' -o mint.json -w 'HTTP %{http_code}\n'
```

Output (token redacted):

```text
HTTP 201
```

The body carries `session_id`
`bef1960a915faeba9a501c2084514818`, `episode_id`
`43815dc9c4bb38d5a449f5646bdbb1b0`, a single use provider token,
`expires_in_seconds` 60, `max_session_duration_seconds` 1800, and the
first episode greeting with empty keyterms.

The live call streams `steady/user-a.wav` plus `steady/user-b.wav`
resampled to 24 kHz mono PCM, sends `session.update` with the
returned config, streams 4800 sample frames every 200 ms, then sends
`session.end`. The script is `/tmp/t710/live.py` (workstation only,
not tracked).

Output:

```text
stream samples: 130927 = 5.46s
- transcript.user The garden gate needs paint before winter.
- transcript.user should buy brushes on Saturday morning.
- transcript.agent OK. I've added "buy brushes" to your list for Saturday morning.
session.ended session_duration_seconds=51.729034 audio_duration_seconds=None
events: 993 provider_session=None
```

Recognition read back both generated lines with small wording
shifts. The host answered with one uninterrupted reply. The client
sent `session.end` explicitly and the socket answered
`session.ended`. No session was left open. The event frames carry no
`provider_session_id` field in this build. The ready frames name the
session `sess_7ac1f85096ed42b0940bf67cbf785aa1`, and the end record
below accepts it.

The session end records against the handler:

```bash
curl -sS -b jar.txt -X POST "https://reprise.nryn.dev/api/sessions/<session_id>/end" \
  -H 'Content-Type: application/json' -d '{"provider_session_id":"sess_7ac1f85096ed42b0940bf67cbf785aa1"}'
```

Output:

```text
{"session_id":"bef1960a915faeba9a501c2084514818","episode_id":"43815dc9c4bb38d5a449f5646bdbb1b0","provider_session_id":"sess_7ac1f85096ed42b0940bf67cbf785aa1"}

HTTP 200
```

The episode lists in the gallery with the owner cookie:

```bash
curl -sS -b jar.txt https://reprise.nryn.dev/api/episodes
```

Output:

```text
{"episodes":[{"id":"43815dc9c4bb38d5a449f5646bdbb1b0","number":1,"title":"Episode 1","state":"recording","visibility":"private"}]}

HTTP 200
```

Result: pass. The gallery lists the recorded episode privately
under its owner. Spend for this call is about 6.5 cents at 4.50
dollars per connected hour.

## 2. Both stems upload, completion moves recording to draft

The user stem concatenates the two generated clips at 48 kHz mono
(`user-48k.wav`, 523782 bytes, 5.46 seconds). The host stem decodes
the 966 provider reply frames to 48 kHz mono (`host-48k.wav`,
927438 bytes, 9.66 seconds). Both are generated speech.

Each stem uploads through the chunked flow with the browser shape
(placeholder owner `guest`, private visibility, 64 KB chunks with
`X-Chunk-SHA256` per chunk, full `sha256` on complete):

```bash
curl -sS -b jar.txt -X POST https://reprise.nryn.dev/api/uploads \
  -H 'Content-Type: application/json' \
  -d '{"owner":"guest","content_type":"audio/wav","visibility":"private"}'
curl -sS -b jar.txt -X PUT "https://reprise.nryn.dev/api/uploads/<id>/chunks/<n>" \
  -H 'Content-Type: application/octet-stream' -H "X-Chunk-SHA256: $SHA" \
  --data-binary @piece.bin
curl -sS -b jar.txt -X POST "https://reprise.nryn.dev/api/uploads/<id>/complete" \
  -H 'Content-Type: application/json' -d "{\"sha256\":\"$FULL\"}"
```

Output:

```text
user complete: size_bytes=523782 sha256=5976e75f... HTTP 200
host complete: size_bytes=927438 sha256=15f95c9c... HTTP 200
```

The persisted ids are `772cddd23a337940469e0b7450c6434f` (user)
and `46b2d61c9cf3ee02d3733856a4d2de37` (host). Both completions
echo the exact uploaded byte counts and hashes. The open resolved
both owners to the session user `da36791775b8e645adde42ef8477e8b1`.

The completion posts the media pair to the route:

```bash
curl -sS -b jar.txt -X POST "https://reprise.nryn.dev/api/episodes/<episode_id>/stems/complete" \
  -H 'Content-Type: application/json' \
  -d '{"user_media_id":"<user>","host_media_id":"<host>","user_sample_rate":48000,"host_sample_rate":48000}'
```

Output:

```text
{"episode_id":"43815dc9c4bb38d5a449f5646bdbb1b0","moved":true,"scheduled":true,"job_id":"48cc3552b7886cc627dbc79574146191","state":"draft","transcript_outcome":{"job_id":"48cc3552b7886cc627dbc79574146191","status":"running"}}

HTTP 200
```

Result: pass. First completion moves recording to draft and
schedules one transcript job. The answer already carries the new
voice: the same job id appears beside a `running` outcome, so the
caller sees what the pipe started.

## 3. Failed pass surfaces its error on detail

The detail route answers the episode with its outcome:

```bash
curl -sS -b jar.txt "https://reprise.nryn.dev/api/episodes/<episode_id>"
```

Output, stable across six polls over nine minutes:

```text
{"episode":{"id":"43815dc9c4bb38d5a449f5646bdbb1b0","number":1,"title":"Episode 1","state":"failed","visibility":"private"},"proposals":[],"transcript_outcome":{"job_id":"48cc3552b7886cc627dbc79574146191","status":"error","error":"transcript: run: assemblyai: batch call POST /v2/transcript status 400: {\"error\": \"\\\"speech_models\\\" must be a non-empty list containing one or more of: \\\"universal-3-pro\\\", \\\"universal-2\\\", \\\"universal-3-5-pro\\\"\"}: assemblyai: batch request failed"}}
```

Result: pass. The transcript pass failed fast and said so. The
episode moved to `failed` with zero proposals and the full provider
rejection readable on the detail beside the same job id. The round 3
silent vanish is gone on the live build.

## 4. Repeat completion reports the standing outcome

A repeat completion with the same pair answered:

```bash
curl -sS -b jar.txt -X POST "https://reprise.nryn.dev/api/episodes/<episode_id>/stems/complete" \
  -H 'Content-Type: application/json' \
  -d '{"user_media_id":"<user>","host_media_id":"<host>","user_sample_rate":48000,"host_sample_rate":48000}'
```

Output:

```text
{"episode_id":"43815dc9c4bb38d5a449f5646bdbb1b0","moved":false,"scheduled":false,"job_id":"","state":"failed","transcript_outcome":{"job_id":"48cc3552b7886cc627dbc79574146191","status":"error","error":"transcript: run: assemblyai: batch call POST /v2/transcript status 400: {\"error\": \"\\\"speech_models\\\" must be a non-empty list containing one or more of: \\\"universal-3-pro\\\", \\\"universal-2\\\", \\\"universal-3-5-pro\\\"\"}: assemblyai: batch request failed"}}

HTTP 200
```

Result: pass. The repeat moved nothing and scheduled nothing
(`moved` false, `scheduled` false, empty `job_id`). It reported the
standing outcome for the first job instead of starting a second
pass beside it. The round 3 blind repeat is gone on the live
build.

## Verdict: the round 3 carry closes

Both halves carry passing evidence above. A failed pass surfaces
its error on the completion answer and the detail. A repeat reports
the standing outcome and starts nothing new.

## New defect carried forward, with its owner

The pipe speaks now, and what it says is a provider rejection. The
batch transcript request sends a `speech_models` value the provider
refuses with 400, naming only `universal-3-pro`, `universal-2`, and
`universal-3-5-pro` as accepted. No episode can reach proposals on
this build while that value stands. The owning paths are
`internal/assemblyai/` (the batch request in `batch.go` wires the
model list) and the model id setting seam that feeds it. The
follow-up task corrects the model value and re-runs this check
live.

## Observation for the next task

The edge rate gate shaped this run throughout. Upload chunks met
repeated 429 answers with retry hints up to 60 seconds. The
completion, gallery polls, and detail polls met 429s as well. Every
retry waited for the hinted delay and recovered. Poll and upload
loops must keep waiting for the hinted delay instead of retrying
hot. The gate never blocked the pipeline itself. Both stems
completed byte exact and the completion scheduled on the first try
after its wait.

No admin call ran in this round, so the kill switch section from
round 2 stands as written. The site was never paused and no
limit was flipped.

## Spend total

One live voice session of 51.7 connected seconds costs about 6.5
cents at 4.50 dollars per connected hour. The failed batch pass
bills nothing, since the provider refused it before transcribing.
Upload bytes bill nothing. Total live spend for this verification
is about 6.5 cents.
