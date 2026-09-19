# Reprise deployment - operator notes

These notes describe how the binary in this repository ships to the Hetzner
box `foleyflow` and serves **https://reprise.nryn.dev**. They are written
for the operator. They are not user docs.

## At a glance

| | |
|---|---|
| Host | Hetzner `foleyflow` (Ubuntu, 2 vCPU, 3.7 GiB RAM, no swap) |
| URL | https://reprise.nryn.dev |
| Edge | Traefik v3 on 80/443 with a `letsencrypt` certresolver |
| Routing | Docker labels on the container, no published host port |
| Image base | `gcr.io/distroless/static-debian12:nonroot` |
| Listen | `0.0.0.0:8080` inside the container |
| Settings | Baked in at `/srv/config/reprise.box.toml`, selected by `REPRISE_CONFIG` |
| Secrets | `/etc/reprise/env` and `/etc/reprise/gemini-sa.json`, mounted read-only |
| State | Host `/srv/reprise` bind mounted to `/var/lib/reprise`, owned by uid 65532 |

## How the process boots

The binary loads its settings once, from the file named by `REPRISE_CONFIG`.
The deploy script sets that variable to the baked box file. The boot log
prints the resolution plan. Every secret shows as resolved and no value
appears. A missing or unreadable secret stops the process before it listens.

The health endpoint reports the build:

```
curl -fsS https://reprise.nryn.dev/healthz
```

It answers `ok <boot> version=<build>`. The build is the commit the image
was built from, stamped in through the `VERSION` build argument.

## One-time setup on the box

Create the secret files. Both are readable by owner only, and the owner is
uid 65532, the nonroot user the container runs as. Mode alone is not enough:
a root-owned `0600` file bind mounts without complaint and the process then
cannot open it, so it crash-loops on `permission denied`. The run script
refuses to start on a secret owned by anyone else, and the deploy workflow
converges the ownership on every run.

```
sudo install -d -m 0700 /etc/reprise
sudo install -o 65532 -g 65532 -m 0600 /dev/null /etc/reprise/env
sudo $EDITOR /etc/reprise/env
```

The directory stays root-owned at `0700`. Each file is bind mounted by
path, so the container never needs to search the directory.

The env file carries two lines and nothing else:

```
ASSEMBLYAI_API_KEY=...
SESSION_SIGNING_KEY=...
```

Place the Vertex AI service account key beside it:

```
sudo install -o 65532 -g 65532 -m 0600 /path/to/downloaded-key.json /etc/reprise/gemini-sa.json
```

Never export a secret in an interactive shell. It persists in shell history
in plaintext. The run script reads the files and never prints a value.

Add the DNS record. It is a proxied Cloudflare A record:

| Type | Name | Target | Proxy |
|---|---|---|---|
| A | `reprise.nryn.dev` | the box address | Proxied |

Copy this directory to the box so the scripts run where Docker lives:

```
/srv/reprise/deploy/
```

## Getting the image to the box

Publishing is a button. `.github/workflows/image.yml` checks that the gate
passed on the commit being published, then builds and pushes to GHCR. It
runs no tests of its own: the gate already ran every one of them, plus the
pinned-tool check, and a weaker second copy would only disagree. A commit
whose gate is red, still running, or never ran refuses to publish. A
successful publish starts `.github/workflows/deploy.yml`, which loads the
image over SSH and runs `redeploy.sh` on the box. The box never logs into
the registry.

```
gh workflow run image.yml
gh workflow run image.yml -f latest=true
```

The package is private while this repository is private. The runner pulls
with `GITHUB_TOKEN` and pipes the image to the box. No registry credential
lives on the host.

Manual fallbacks still work.

**Build on the box.** Clone the repository on the box and build there.

```
docker build --build-arg VERSION=$(git rev-parse HEAD) -t reprise:local .
IMAGE=reprise:local ./deploy/run.sh
```

**Ship over SSH.** Build on a workstation, save the image, and load it on
the box.

```
docker build --build-arg VERSION=$(git rev-parse HEAD) -t reprise:local .
docker save reprise:local | ssh root@<box> 'docker load'
IMAGE=reprise:local ./deploy/run.sh
```

A value with a slash is pulled only when that image is not already local.
A plain tag is used as a local image.

## Deploy and redeploy

First start:

```
./deploy/run.sh
```

Every later deploy:

```
./deploy/redeploy.sh
```

The redeploy script records the running image, starts the target, and gates
on the container IP. It checks that `/healthz` answers with the expected
build and that `/` answers 200. A failed gate restores the previous image
and exits 1. It never touches the public edge.

Pin a build explicitly when it matters:

```
IMAGE=reprise:abc1234 EXPECTED_SHA=abc1234 ./deploy/redeploy.sh
```

To stop and remove:

```
docker rm -f reprise
```

## Limits and the stop timeout

The container runs with `--memory 1g --memory-swap 1g --cpus 1.0`. The box
is small and already runs seven containers. The render is the greedy
process, so the limit is sized for ffmpeg rather than the idle server.
Override with `MEMORY=` and `CPUS=` when the render proves otherwise.

`--stop-timeout` defaults to 1830. The process drains open sessions for up
to the session cap of 1800 seconds after SIGTERM, then exits. The timeout
must stay above the cap, or the daemon kills a drain that was still letting
a session finish. The 30 second margin covers the exit after the last
session. A redeploy during a session lets it finish instead of cutting it.

## Verification

Verify from a workstation, never from the box. Cloudflare challenges
requests from the box address, so the same URL that answers 200 at home
answers 403 over SSH on the host.

```
curl -fsS https://reprise.nryn.dev/healthz
curl -fsS https://reprise.nryn.dev/ -o /dev/null -w "%{http_code}\n"
docker logs --tail 50 reprise   # on the box, shows the plan with no values
```

To reach the origin from the box and bypass the edge:

```
curl -k -sS --resolve reprise.nryn.dev:443:127.0.0.1 https://reprise.nryn.dev/healthz
```

## Backup, not yet in service

**Nothing is backing up today, by decision.** `backup.sh` is written and
ready, no destination is chosen, and no cron runs it. The box holds the only
copy of the database and the media. Treat the deployment as replaceable and
its data as not.

The rest of this section is the instruction sheet for the day that changes.
It is not a description of anything currently running.

The script copies the live database through the SQLite online backup API,
checks the copy with `integrity_check`, and copies the media directory
beside it. It takes the destination as a parameter and holds no credential,
so choosing off-box storage is the whole of the remaining decision.

To put it in service, pick a destination, add the cron, then run the restore
drill below before trusting it.

Nightly cron on the box:

```
0 3 * * * /srv/reprise/deploy/backup.sh /mnt/backups/reprise
```

Each run lands in a UTC timestamped directory with a manifest. The newest
seven runs are kept. Set `RETAIN_COUNT=` to keep more or fewer.

Restore drill, to run before trusting the cron. Until someone has run this
against a real archive, the backup is an untested claim:

1. Stop the container: `docker rm -f reprise`.
2. Copy the database back: `cp <run>/reprise.db /srv/reprise/data/reprise.db`.
3. Copy the media back: `cp -a <run>/media/. /srv/reprise/media/`.
4. Fix ownership: `chown -R 65532:65532 /srv/reprise`.
5. Start and verify from a workstation.

The database filename default is `reprise.db`. When the store lands under a
different name, override with `DB_FILE=` and `MEDIA_DIR=` stays as is.

## Safety

| Exposure | Status |
|---|---|
| Committed to the repository | Closed. The settings file carries references, never values. The scripts print names, never values. |
| Operator shell history on the box | Closed. Secrets are typed once into the files, then only read. |
| `docker run` arguments, visible to `ps` | Closed. No secret travels on the command line or through environment flags. Only `REPRISE_CONFIG`, a path, uses an environment flag. |
| `docker inspect` and container state on disk | Accepted. Anyone reading those already holds Docker or root access on the box. |
| Image layers | Closed. Secrets mount at run time and never bake into the image. |

## Media tools

The image carries a static `ffmpeg` and `ffprobe` pair at `/usr/local/bin`,
copied from the `mwader/static-ffmpeg` image the `Dockerfile` pins by digest.
The `Dockerfile` holds that version and digest, so this file names neither and
cannot drift from them. The build gate installs the same pair from the same
digest, and it refuses a machine whose build differs, because loudness and peak
numbers compare only when the measuring tool matches. The render passes those
absolute paths, because the distroless stage sets no `PATH`.
