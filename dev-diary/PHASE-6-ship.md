# P6: Ship

```yaml
id:       P6
size:     M
requires: [P4, P5]
blocks:   submission
parallel: partly
```

**Goal:** A live, reachable application at a public URL, a public repository that builds, real
voices checked once, and the materials the submission form asks for.

---

### T6.1: Deploy
```yaml
requires:   T0.1, T1.5
fixture-ok: no
size:       S · mid
owns:       deploy/
status:     not-started
```
**Build on:** the image from T0.1 and `config/reprise.box.toml` from T1.5. The deploy kit is
Reprise's, because it deploys one binary to one box.

One container behind the existing Traefik on the Hetzner box, at `reprise.nryn.dev`.

* The container reads `config/reprise.box.toml` through `REPRISE_CONFIG`.
* Secrets live in `/etc/reprise/env`, root-owned, mode `0600`, mounted read-only. The TOML's
  `env_file` references point at it, so no secret passes on the command line or through `docker run`
  environment flags.
* SQLite and media on a host bind mount, owned by the distroless nonroot user.
* A resource limit on CPU and memory, so a render cannot starve the other services on the box.
* A nightly `sqlite.Backup` copied off the box, plus the media directory.

A redeploy must not cut a live session. The container waits for open sessions to end before it
stops, up to the session cap.

**Done when:** `reprise.nryn.dev/healthz` answers from a workstation. The boot log shows the
resolution plan with every secret resolved and no value. A redeploy during a session lets it finish.

---

### T6.1b: Production verification
```yaml
requires:   T6.1, T5.5
fixture-ok: no
size:       M · frontier
owns:       dev-diary/probes/production.md
status:     not-started
```
Automated, from a workstation, never from the box. No person needed.

* A scripted guest records episode five from generated speech and the host's greeting names the
  seeded callback, read from the session's `transcript.agent` events.
* The episode renders, analyses and shows in the gallery.
* A private media URL without the cookie returns 404, with `Cache-Control: private, no-store`.
* The kill switch refuses a session.
* Outbound calls from the box to AssemblyAI and Gemini succeed, and Cloudflare does not block them.
* Connected seconds from the Sessions API match the ledger.

**Done when:** Every check is recorded with its command and output.

---

### T6.4b: Real voices and real devices, once ★
```yaml
requires:   T6.1b
fixture-ok: no
size:       M · frontier
owns:       dev-diary/probes/live-sessions.md
status:     not-started
```
The only task that needs a person. It runs once, after everything else lands, and nothing earlier
waits on it.

Real sessions with a real voice on Chrome and Firefox, each with headphones and with speakers.
Record for each: host audio quality, echo in the user stem, alignment drift, whether `pagehide`
ended the session, and connected seconds from the Sessions API against the browser's own timer.
Listen to one untouched episode end to end and record whether it is worth playing as it is.

A defect found here becomes a task in the phase that owns the code.

**Done when:** Four runs are recorded with measurements, plus the listening verdict.

---

### T6.2: Submission materials
```yaml
requires:   T6.4b
fixture-ok: no
size:       M · frontier
owns:       dev-diary/submission/
status:     not-started
```
The video, the slide deck, the cover image, the short and long descriptions, and the tags.

The video is one thing. Play the episode where the host calls back. Everything else is context for
that moment.

The owner records and submits. This task drafts the script, the deck and the copy.

**Done when:** The owner has every item the submission form lists.

---

### T6.3: Public repository ★
```yaml
requires:   T6.1b
fixture-ok: no
size:       S · frontier
owns:       README.md
status:     not-started
```
The submission needs a public repository that builds.

* Keel `v0.3.0` and `@nrynss/chaaya` `0.2.0` are both public, so a stranger can fetch every
  dependency.
* A fresh clone on a clean machine builds the image with no access to this machine.
* `AGENTS.md`, `product.md` and `dev-diary/` stay gitignored. The README describes the product and
  how to run it, and names Keel and Chaaya as the author's prior open-source work.
* Making the repository public is the owner's action, and so is the license. The submission
  requires one the terms allow, which is not the same as requiring a particular one. No task commits
  a `LICENSE` file or picks the terms.

**Done when:** A clean clone builds with no access to this machine. No tracked file names a planning
file or a task id. The owner flips visibility.

---

## Exit criteria

- [ ] `reprise.nryn.dev` works end to end, verified from a workstation.
- [ ] Real voices on two browsers are recorded once.
- [ ] The public repository builds from a clean clone.
- [ ] Every submission item is ready for the owner.

---

## Handoff log

### What exists now
Not started.

### What surprised us
Nothing yet.

### Notes for the next developer
Task numbers keep their order of creation, so T6.4b runs before T6.2 despite its number.
