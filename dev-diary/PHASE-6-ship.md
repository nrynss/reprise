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
requires:   T0.1, T1.5, T2.1
fixture-ok: no
size:       M · mid
owns:       deploy/, cmd/reprise/main.go, Dockerfile
status:     done:ca529d79afce44e8290bdb05250f681ea9f45acb
```
**Build on:** the image from T0.1, `config/reprise.box.toml` from T1.5, and the session registry from
T2.1, which is what a drain waits on. The box is documented in
`/home/nryn/work/hetzner/docs/foleyflow-server.md`. Read it first. It is a read-only source, like
Keel and Chaaya, and no task here edits it.

**The box, as recorded on 2026-09-04.** One Hetzner VPS named `foleyflow` at `167.233.247.107`.
Seven containers share the Docker network `proxy`. Traefik v3 is the only one publishing ports, 80
and 443, and it discovers services through the Docker socket. Every public hostname is a proxied
Cloudflare A record, and certificates come from Let's Encrypt over DNS-01. Roughly 2.9 GiB of memory
was free and the host runs no swap.

**Copy `thutapi`, which already does this.** It runs distroless nonroot, publishes no host port,
bind mounts `/srv/thutapi/data` to `/data` owned by uid 65532, and reports its commit at `/healthz`.
Reprise deploys the same shape at `reprise.nryn.dev`.

* The container joins the `proxy` network and publishes nothing. Traefik routes to it by label.
* The container reads `config/reprise.box.toml` through `REPRISE_CONFIG`.
* Secrets live in `/etc/reprise/env`, root-owned, mode `0600`, mounted read-only. The TOML's
  `env_file` references point at it, so no secret passes on the command line or through `docker run`
  environment flags. `/etc/reprise/gemini-sa.json` mounts the same way.
* A host bind mount carries SQLite and media, owned by uid 65532, the distroless nonroot user. The
  box config puts them under `/var/lib/reprise` inside the container.
* A CPU and memory limit, because the box is small, has no swap, and already runs seven containers.
  The render is the greedy one, so size the limit against ffmpeg rather than the idle server.
* A nightly `sqlite.Backup` copied off the box, plus the media directory.

**Three gaps this task closes before it can deploy anything.** Each one is code, not deployment,
which is why the `owns` line grew past `deploy/`.

1. **The binary never reads its own settings.** `cmd/reprise/main.go` does not import
   `internal/settings`, never looks at `REPRISE_CONFIG`, and never logs the plan. Load the settings
   at boot and log `Plan.String`, which names every source and carries no value.
2. **Nothing drains.** `main.go` calls `http.ListenAndServe` with no signal handling. Take SIGTERM,
   stop accepting new sessions, wait for open ones through the T2.1 registry, then shut down. The
   `docker run` stop timeout must be at least `session_max_seconds`, which is 1800.
3. **ffmpeg is not in the image.** The runtime stage is `distroless/static-debian12`, which carries
   no ffmpeg, while the render, the export and the waveform all shell out through `keel/ffmpeg`. Copy
   a pinned static `ffmpeg` and `ffprobe` into the runtime stage, the same pair CI installs.

**Two decisions the owner makes before this starts.**

* **How the image reaches the box.** `thutapi` pulls a public GHCR image, which a private repository
  cannot do without a registry credential on the host. Building on the box or shipping the image over
  SSH both avoid that. Reprise has no git remote at all today.
* **Where the nightly backup goes.** The box documentation found no backup job, no archives, and no
  verified restore. This task cannot invent a destination, and the run script must not hold a
  credential for one.

**Done when:** `reprise.nryn.dev/healthz` answers from a workstation, never from the box, because
Cloudflare challenges the box's own address. The boot log shows the resolution plan with every secret
resolved and no value. A redeploy during a session lets it finish.

---

### T6.1b: Production verification
```yaml
requires:   T6.1, T5.5
fixture-ok: no
size:       M · frontier
owns:       dev-diary/probes/production.md
status:     not-started
```
Automated, from a workstation, never from the box. No person needed. First deploy has no
catalog. This check does not wait on seed files.

* A scripted guest records the next episode from generated speech. With an empty catalog that
  is episode one.
* The episode renders, analyses and shows in the gallery.
* A private media URL without the cookie returns 404, with `Cache-Control: private, no-store`.
* The kill switch refuses a session.
* Outbound calls from the box to AssemblyAI and Gemini succeed, and Cloudflare does not block them.
* Connected seconds from the Sessions API match the ledger.
* The host greeting that names a seeded callback is a later re-run, after the operator places
  the catalog. It is not a first-deploy gate.

**Done when:** Every first-deploy check is recorded with its command and output. The seeded
callback re-run is recorded once the catalog exists, not before.

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
* `AGENTS.md`, `product.md` and `dev-diary/` are tracked. The gate skips them by path. The README
  describes the product and how to run it, and names Keel and Chaaya as the author's prior
  open-source work.
* Making the repository public is the owner's action, and so is the license. The submission
  requires one the terms allow, which is not the same as requiring a particular one. No task commits
  a `LICENSE` file or picks the terms.

**Done when:** A clean clone builds with no access to this machine. No source file names a planning
file or a task id. The owner flips visibility.

---

## Exit criteria

- [ ] `reprise.nryn.dev` works end to end, verified from a workstation.
- [ ] Real voices on two browsers are recorded once.
- [ ] The public repository builds from a clean clone.
- [ ] Every submission item is ready for the owner.

---

## Handoff log

### What exists now (Orchestrator-2)
T6.1 landed after round 2 APPROVE with zero residue against round 1. The deploy kit (run, redeploy
with stop-then-remove drain, backup with manifest, docs), the settings-loading SIGTERM-draining
binary, and the ffmpeg-baked image are on main with a green gate. Open owner/box items stand in the
handoff: first deploy, workstation edge check, redeploy drill, backup cron plus restore drill, DNS.
The dead CI ffmpeg URL stays an out-of-scope row on T0.1's workflow file. The other orchestrator's
PHASE-0 dirt sat through this landing and was left untouched.

### What surprised us
Nothing yet.

### Notes for the next developer
First deploy is the current priority. The catalog starts empty. Seed files are dogfood after
the box is up. Images publish from GitHub Actions with a button. A successful publish loads
the image on the box over SSH. The box never logs into the registry. The private repository
is `nrynss/reprise`. Add a proxied Cloudflare A record for `reprise.nryn.dev` before the
public edge check. Task numbers keep their order of creation, so T6.4b runs before T6.2
despite its number.
