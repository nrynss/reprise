#!/usr/bin/env bash
#
# Reprise on the foleyflow box.
#
# Brings up the reprise container behind the existing Traefik v3 instance so
# it serves https://reprise.nryn.dev. TLS, cert renewal and routing are
# handled by the existing letsencrypt certresolver. The five labels below
# are the only contract between this container and Traefik.
#
# Usage (on the box):
#
#   ./deploy/run.sh                        # image built on the box
#   IMAGE=reprise:abc1234 ./deploy/run.sh  # a named local build
#   IMAGE=ghcr.io/example/reprise:abc1234 ./deploy/run.sh  # a registry ref
#
# To stop gracefully so open sessions drain:
#
#   docker stop reprise
#   docker rm reprise
#
# SECRETS. The process reads every secret from two root-owned 0600 files on
# the box, mounted read-only into the container:
#
#   /etc/reprise/env            ASSEMBLYAI_API_KEY and SESSION_SIGNING_KEY
#   /etc/reprise/gemini-sa.json the Vertex AI service account key
#
# The settings file baked into the image points at those paths, so no secret
# passes on the command line or through a docker environment flag. No secret
# value is ever printed by this script. Do NOT export a secret in an
# interactive shell. It lands in shell history in plaintext and stays there.
#
# IMAGE DELIVERY. This repository keeps no registry and no remote, so the
# script takes either path without preferring one. A value with a slash is
# treated as a registry reference and pulled. A plain tag is used as a local
# image, which covers both a build on the box and an image shipped over SSH.
# See deploy/README.md for both flows.
#
# STOP TIMEOUT. The process drains open sessions on SIGTERM for up to the
# session cap (1800 seconds). STOP_TIMEOUT must stay above that cap, or the
# daemon kills a drain that was still letting a session finish. The default
# adds a 30 second margin for the process to exit after the last session.

set -euo pipefail

IMAGE="${IMAGE:-reprise:local}"
NAME="${NAME:-reprise}"
DATA_DIR="${DATA_DIR:-/srv/reprise}"
NETWORK="${NETWORK:-proxy}"
STOP_TIMEOUT="${STOP_TIMEOUT:-1830}"
MEMORY="${MEMORY:-1g}"
CPUS="${CPUS:-1.0}"
ENV_FILE="${ENV_FILE:-/etc/reprise/env}"
KEY_FILE="${KEY_FILE:-/etc/reprise/gemini-sa.json}"

# The uid the distroless runtime stage runs as. The secret files are bind
# mounted, so this uid must be able to read them from inside the container.
RUNTIME_UID=65532

# The settings file baked into the image. It carries inline values and
# secret references only, so passing its path on the command line is safe.
CONFIG_PATH="/srv/config/reprise.box.toml"

# A secrets file the whole box can read is not a secrets file. Abort rather
# than start a container whose key material was already exposed.
for secret_file in "${ENV_FILE}" "${KEY_FILE}"; do
  if [[ ! -f "${secret_file}" ]]; then
    echo "error: ${secret_file} does not exist or is not a regular file." >&2
    echo "       See deploy/README.md for the one-time setup." >&2
    exit 1
  fi
  mode="$(stat -c '%a' "${secret_file}" 2>/dev/null || echo '')"
  case "${mode}" in
    600|400) ;;
    *)
      echo "error: ${secret_file} is mode ${mode}, want 600 or 400." >&2
      echo "       Fix it with: sudo chmod 600 ${secret_file}" >&2
      exit 1
      ;;
  esac

  # Mode alone is not enough. At 600 or 400 only the owner reads the file,
  # and the container is not root. A root-owned 0600 secret mounts without
  # complaint, the process cannot open it, and it crash-loops on
  # "permission denied" long after this script reported success.
  owner="$(stat -c '%u' "${secret_file}" 2>/dev/null || echo '')"
  if [[ "${owner}" != "${RUNTIME_UID}" ]]; then
    echo "error: ${secret_file} is owned by uid ${owner}, want ${RUNTIME_UID}." >&2
    echo "       The container runs as uid ${RUNTIME_UID} and cannot read it." >&2
    echo "       Fix it with: sudo chown ${RUNTIME_UID}:${RUNTIME_UID} ${secret_file}" >&2
    exit 1
  fi
done

# The service account key must parse as JSON. This check reads the file and
# reports only whether it parses, never its contents.
if command -v python3 >/dev/null 2>&1; then
  if ! python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "${KEY_FILE}" 2>/dev/null; then
    echo "error: ${KEY_FILE} does not parse as JSON." >&2
    exit 1
  fi
else
  echo "warning: python3 is not on PATH, skipping the key file parse check." >&2
fi

# The env file must provide both variables. Sourcing runs in a clean
# environment so outer shell variables cannot mask a missing key, and only
# missing names are reported, never values.
# The assignment goes after `env -i`, never before it. A prefix assignment
# sets the variable in env's own environment, which `-i` then wipes before
# it execs bash, so the inner `set -u` dies on an unbound ENV_FILE and the
# container never starts.
PREFLIGHT_ERR="$(env -i ENV_FILE="${ENV_FILE}" bash -c '
  set -eu
  . "$ENV_FILE"
  missing=()
  [[ -z "${ASSEMBLYAI_API_KEY:-}" ]] && missing+=("ASSEMBLYAI_API_KEY")
  [[ -z "${SESSION_SIGNING_KEY:-}" ]] && missing+=("SESSION_SIGNING_KEY")
  if [[ ${#missing[@]} -gt 0 ]]; then
    echo "required variable(s) missing or empty: ${missing[*]}"
    exit 1
  fi
' 2>&1 || true)"
if [[ -n "${PREFLIGHT_ERR}" ]]; then
  echo "error: preflight failed: ${ENV_FILE}: ${PREFLIGHT_ERR}" >&2
  exit 1
fi

# The distroless runtime stage runs as uid 65532. The bind mount is created
# by root here, so hand it to that uid or every write under /var/lib/reprise
# fails. 1000 is NOT the right uid for this image.
mkdir -p "${DATA_DIR}"
chown -R 65532:65532 "${DATA_DIR}"

DOCKER_ARGS=(
  --detach
  --restart unless-stopped
  --name "${NAME}"
  --hostname "${NAME}"

  # Traefik discovers containers through the Docker socket but routes only
  # to addresses on its provider network. A container outside `proxy` gets
  # the labels and still 404s or 502s.
  --network "${NETWORK}"

  # A drain runs for the session cap, so the daemon must wait longer than
  # that before it kills the container.
  --stop-timeout "${STOP_TIMEOUT}"

  # The box is small, has no swap, and already runs seven containers. The
  # render is the greedy process, so the limit is sized for ffmpeg rather
  # than for the idle server.
  --memory "${MEMORY}"
  --memory-swap "${MEMORY}"
  --cpus "${CPUS}"

  # Persistence: SQLite and media live on a host bind mount. The container
  # sees it at /var/lib/reprise, which is where the box settings point.
  --mount "type=bind,source=${DATA_DIR},target=/var/lib/reprise"

  # Secrets reach the container as read-only mounts, never as environment.
  # The settings file references these paths, so the process resolves every
  # secret from the files at boot.
  --mount "type=bind,source=${ENV_FILE},target=/etc/reprise/env,readonly"
  --mount "type=bind,source=${KEY_FILE},target=/etc/reprise/gemini-sa.json,readonly"

  # A path, not a secret, so an environment flag is the right carrier.
  --env "REPRISE_CONFIG=${CONFIG_PATH}"

  # --- Traefik v3 labels ---
  --label "traefik.enable=true"
  --label "traefik.http.routers.reprise.entrypoints=websecure"
  --label "traefik.http.routers.reprise.rule=Host(\`reprise.nryn.dev\`)"
  --label "traefik.http.routers.reprise.tls.certresolver=letsencrypt"
  --label "traefik.http.services.reprise.loadbalancer.server.port=8080"
)

echo "Starting ${NAME} from ${IMAGE}..."
# A registry reference is pulled only when it is not already on the box.
# The GitHub deploy workflow loads the image over SSH, so a pull would
# fail here. A later docker login on the box still works, because a
# missing image falls through to pull.
if [[ "${IMAGE}" == *"/"* ]]; then
  if docker image inspect "${IMAGE}" >/dev/null 2>&1; then
    echo "Using local image ${IMAGE}."
  else
    docker pull "${IMAGE}"
  fi
fi
if docker inspect "${NAME}" >/dev/null 2>&1; then
  echo "Replacing existing container ${NAME}..."
  docker stop -t "${STOP_TIMEOUT}" "${NAME}" >/dev/null
  docker rm "${NAME}" >/dev/null
fi
docker run "${DOCKER_ARGS[@]}" "${IMAGE}"

echo
echo "Image digest actually running:"
docker inspect "${NAME}" --format '  {{.Image}}' 2>/dev/null || true
docker image inspect "${IMAGE}" --format '  {{index .RepoDigests 0}}' 2>/dev/null || true

echo "Container started. Useful follow-ups:"
echo "  docker logs -f ${NAME}"
echo "  curl -fsS https://reprise.nryn.dev/healthz   # from a workstation, never from the box"
