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
status:     done:48ac233c6d3fe603ff255ad7b5b5b43397ffc623
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
* Secrets live in `/etc/reprise/env`, mode `0600`, mounted read-only, and owned by uid 65532.
  Root-owned was the original wording here and it is wrong: at `0600` only the owner reads the
  file, and the container is not root. The TOML's `env_file` references point at it, so no secret
  passes on the command line or through `docker run` environment flags.
  `/etc/reprise/gemini-sa.json` mounts the same way. The directory stays root-owned at `0700`,
  because each file is bind mounted by path and the container never searches the directory.
* A host bind mount carries SQLite and media, owned by uid 65532, the distroless nonroot user. The
  box config puts them under `/var/lib/reprise` inside the container.
* A CPU and memory limit, because the box is small, has no swap, and already runs seven containers.
  The render is the greedy one, so size the limit against ffmpeg rather than the idle server.
* A nightly `sqlite.Backup` copied off the box, plus the media directory. Deferred on
  2026-09-19: the script ships, no destination is set, and nothing runs it. See below.

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

* **How the image reaches the box.** Closed by operator override on 2026-09-19. See the handoff.
* **Where the nightly backup goes.** Closed on 2026-09-19 by deferring it. The owner's call: a
  backup destination is a future option and does not gate the deploy or anything after it.
  `deploy/backup.sh` ships, takes the destination as a parameter, and holds no credential. No
  destination is chosen, no cron runs it, and the restore drill is unrun. The box therefore holds
  the only copy of the database and the media. This is a known, accepted exposure, not an
  oversight, and no later task waits on it.

**Operator override, 2026-09-19.** The owner skipped the implement, review, remediate loop. A
private GitHub repository now exists at `nrynss/reprise`. `.github/workflows/image.yml` publishes
on a button. `.github/workflows/deploy.yml` loads the image over SSH and runs `redeploy.sh`.
The box never logs into the registry. Box secrets and `/srv/reprise` are in place. Commits
`8eb8e76` and `7a1a7d0` carry the workflows. They were not reviewed.

**First live deploy, 2026-09-19.** `https://reprise.nryn.dev` answers. The unreviewed kit held
four faults, and each one hid the next.

* `image.yml` ran `go vet` and `go test -race` with no ffmpeg on the runner, so the media
  packages failed and no image was ever published. The duplicate was a strict subset of the gate
  `ci.yml` already runs on the same commit, missing its pinned-tool check. Publish now reads the
  ci conclusion for the commit and refuses one that is red, still running, or never gated.
* `run.sh` built its preflight as `ENV_FILE=... env -i bash -c`. The prefix assignment lands in
  env's own environment, which `-i` wipes before it execs bash, so the inner `set -u` died on an
  unbound `ENV_FILE` and the container never started.
* `deploy.yml` piped `redeploy.sh` through `tee` with no `pipefail`, so the step took tee's exit
  code. The first deploy reported success while the box served nothing and the edge answered
  Traefik's 404. This is the one that mattered: it made the other three invisible.
* The secret files were root-owned at `0600` and the container runs as uid 65532, so the process
  could not open `/etc/reprise/env` and crash-looped. The preflight checked the mode and never
  the owner, which is exactly the pair that makes a file unreadable to anyone but root.

The health gate now prints the container's status and last fifty log lines before it rolls back.
The gate never lied; nothing read its exit code, and nothing carried the reason off the box.

**DNS, 2026-09-19.** `reprise.nryn.dev` is a proxied Cloudflare A record to `167.233.247.107`
in zone `nryn.dev`, record id `0536a8f581a446d53d6599ece030808c`. One level deep, which is what
the free Universal SSL certificate covers. The edge serves a Google Trust Services certificate
and the origin is reached over Traefik's `letsencrypt` resolver.

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
status:     done:c8f5ff69cb99a99d3ba5474df901736714e752d3
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
status:     in-progress:implement:t6.4b-impl
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
status:     done:3f129ea9619be18a9f50c659eca5eb1431bc285d
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
status:     done:33d01fae8c4c73a75475a528d3d3b6a35f14ab31
```
The submission needs a public repository that builds. The private remote already exists at
`nrynss/reprise`. This task flips visibility and writes the README a stranger can follow.

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

- [x] `reprise.nryn.dev` works end to end, verified from a workstation.
- [ ] Real voices on two browsers are recorded once.
- [ ] The public repository builds from a clean clone.
- [ ] Every submission item is ready for the owner.

---

## Handoff log

### What exists now
`https://reprise.nryn.dev` is live and verified from a workstation. `/healthz` answers
`ok <boot> version=3ab625a71b84dfd77723b1791d7df19ee5eafdf7`, `/` answers 200, and the
certificate verifies. DNS is a proxied A record. The deploy chain runs end to end: publish is
a button, a successful publish loads the image over SSH and redeploys, and a failed deploy now
fails the workflow.

The running build is `5d33a24`. T6.1b round 1 verification is recorded in
`dev-diary/probes/production.md`: the guest mint and live call pass, and
six gaps carry to phase P7 (stubbed episode routes, unwired settle,
uploader owner mismatch, admin stub, no Gemini path, bare media
refusals). Each mint holds about 2.25 dollars until the settle lands.
T7.5 re-verifies after P7 deploys. The seeded greeting re-run waits on
the operator catalog.

No owner items remain on this task. The backup destination was the last one and it is closed as
deferred, documented in `deploy/README.md` under a heading that says nothing is backing up today.
T6.1b production verification is next and nothing blocks it.

### What surprised us
The owner needed to ship from GitHub while away from the workstation. The loop would have
held the first box deploy on a review round. The override skipped that so dogfood can start
once DNS exists.

### Notes for the next developer
The box is up and the catalog starts empty. Seed files are dogfood from here. Images publish
from GitHub Actions with a button, and a publish only proceeds when the gate was green on that
exact commit. A successful publish loads the image on the box over SSH. The box never logs
into the registry. The private repository is `nrynss/reprise`.

Verify the edge from a workstation and never over SSH from the box, because Cloudflare
challenges the box's own address. A green deploy is not a working edge: the gate checks the
container IP and bypasses Traefik entirely, so read both. Task numbers keep their order of
creation, so T6.4b runs before T6.2 despite its number.
