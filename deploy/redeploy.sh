#!/usr/bin/env bash
#
# deploy/redeploy.sh - redeploy Reprise on the foleyflow box with rollback.
#
# 1. Preflight the secret files through deploy/run.sh, which refuses to
#    start on a missing or wrongly permissioned secret.
# 2. Record the running image for rollback.
# 3. Start the target image through deploy/run.sh.
# 4. Health gate on the container IP, bypassing the edge, bounded by
#    HEALTH_TIMEOUT (60 seconds unless set):
#    - GET /healthz answers "ok <boot> version=<build>" and the build
#      matches the expected commit when one is given.
#    - GET / answers 200, so the app shell is actually serving.
#    - On expiry the failed container stops fast (ROLLBACK_STOP_TIMEOUT,
#      30 seconds unless set), the previous image is restored, the
#      restore is health checked within the same bound, and the script
#      exits 1. A stuck deploy reads as red in minutes.
# 5. Prune dangling images.
#
# Usage (on the box):
#
#   ./deploy/redeploy.sh
#   IMAGE=reprise:abc1234 EXPECTED_SHA=abc1234 ./deploy/redeploy.sh
#   ./deploy/redeploy.sh reprise:abc1234 abc1234
#
# The public edge check still happens from a workstation afterwards, because
# Cloudflare challenges requests from the box address. See deploy/README.md.

set -euo pipefail

NAME="${NAME:-reprise}"
RUN_SCRIPT="${RUN_SCRIPT:-/srv/reprise/deploy/run.sh}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-60}"
ROLLBACK_STOP_TIMEOUT="${ROLLBACK_STOP_TIMEOUT:-30}"

TARGET_IMAGE="${1:-${IMAGE:-reprise:local}}"
EXPECTED_SHA="${2:-${EXPECTED_SHA:-}}"

echo "=== Starting Reprise redeploy ==="
echo "Target image: ${TARGET_IMAGE}"
if [[ -n "${EXPECTED_SHA}" ]]; then
  echo "Expected build: ${EXPECTED_SHA}"
fi

# Locate run.sh next to this script when no installed copy exists.
if [[ ! -x "${RUN_SCRIPT}" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -x "${SCRIPT_DIR}/run.sh" ]]; then
    RUN_SCRIPT="${SCRIPT_DIR}/run.sh"
  else
    echo "error: run script not found or not executable at ${RUN_SCRIPT}" >&2
    exit 1
  fi
fi

echo "--- Inspecting running container ---"
PREV_IMAGE_ID="$(docker inspect "${NAME}" --format '{{.Image}}' 2>/dev/null || true)"
PREV_DIGEST=""
if [[ -n "${PREV_IMAGE_ID}" ]]; then
  PREV_DIGEST="$(docker image inspect "${PREV_IMAGE_ID}" --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' 2>/dev/null || true)"
fi
ROLLBACK_IMAGE="${PREV_DIGEST:-${PREV_IMAGE_ID}}"
if [[ -n "${PREV_IMAGE_ID}" ]]; then
  echo "Rollback target: ${ROLLBACK_IMAGE}"
else
  echo "No existing ${NAME} container running; rollback unavailable if deploy fails."
fi

# HEALTH_VERSION carries the build the last health check saw, or empty
# when nothing answered.
HEALTH_VERSION=""

# check_health polls /healthz on one container IP until it answers with the
# wanted build or the timeout passes. An empty wanted build accepts any
# answering build. It returns 0 on a pass and 1 on expiry.
check_health() {
  local ip="$1"
  local timeout="$2"
  local wanted="$3"
  HEALTH_VERSION=""
  local start now resp
  start=$(date +%s)
  while true; do
    now=$(date +%s)
    if [[ $((now - start)) -ge ${timeout} ]]; then
      return 1
    fi
    resp="$(curl -s -S --max-time 2 "http://${ip}:8080/healthz" 2>/dev/null || true)"
    case "${resp}" in
      "ok "*)
        HEALTH_VERSION="$(printf '%s' "${resp}" | sed -n 's/^ok [^ ]* version=//p')"
        if [[ -z "${wanted}" || ( -n "${HEALTH_VERSION}" && ( "${HEALTH_VERSION}" == "${wanted}"* || "${wanted}" == "${HEALTH_VERSION}"* ) ) ]]; then
          return 0
        fi
        ;;
    esac
    sleep 1
  done
}

# container_ip prints the container IP or empty when it never appears.
container_ip() {
  local ip=""
  for _ in $(seq 1 10); do
    ip="$(docker inspect "${NAME}" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' 2>/dev/null || true)"
    if [[ -n "${ip}" ]]; then
      break
    fi
    sleep 1
  done
  printf '%s' "${ip}"
}

rollback() {
  local reason="$1"
  echo "error: ${reason}" >&2

  # A gate that fails without saying why is undiagnosable from CI, where
  # nobody has a shell on the box. Print what the container was doing
  # before the rollback replaces it. The boot log names sources, never
  # values, so this is safe to put in a workflow log.
  {
    echo "--- container state ---"
    docker ps -a --filter "name=^/${NAME}$" --format 'status: {{.Status}}' 2>/dev/null || true
    echo "--- last 50 log lines from ${NAME} ---"
    docker logs --tail 50 "${NAME}" 2>&1 || echo "(no logs available)"
    echo "--- end of container log ---"
  } >&2

  if [[ -n "${ROLLBACK_IMAGE}" ]]; then
    # The failed container already missed its gate, so it stops fast
    # instead of draining. The restore below starts the previous image
    # with the normal stop timeout, so honest sessions keep their drain.
    echo "Stopping the failed container..." >&2
    docker stop -t "${ROLLBACK_STOP_TIMEOUT}" "${NAME}" >/dev/null 2>&1 || true
    docker rm "${NAME}" >/dev/null 2>&1 || true
    echo "Restoring previous image: ${ROLLBACK_IMAGE}..." >&2
    if IMAGE="${ROLLBACK_IMAGE}" NAME="${NAME}" "${RUN_SCRIPT}"; then
      echo "Rollback container started." >&2
    else
      echo "CRITICAL: rollback to ${ROLLBACK_IMAGE} failed!" >&2
      exit 1
    fi
    restored_ip="$(container_ip)"
    if [[ -n "${restored_ip}" ]] && check_health "${restored_ip}" "${HEALTH_TIMEOUT}" ""; then
      echo "Rollback answers healthz at build '${HEALTH_VERSION}'." >&2
    else
      echo "CRITICAL: rolled back container answers no healthz." >&2
    fi
  else
    echo "No previous running image recorded; cannot roll back." >&2
  fi
  exit 1
}

echo "--- Starting new container from ${TARGET_IMAGE} ---"
if ! IMAGE="${TARGET_IMAGE}" NAME="${NAME}" "${RUN_SCRIPT}"; then
  rollback "failed to start container from ${TARGET_IMAGE}"
fi

echo "--- Health gate ---"
CONTAINER_IP="$(container_ip)"
if [[ -z "${CONTAINER_IP}" ]]; then
  rollback "could not read container IP for ${NAME}"
fi
echo "Container IP: ${CONTAINER_IP}"

if ! check_health "${CONTAINER_IP}" "${HEALTH_TIMEOUT}" "${EXPECTED_SHA}"; then
  rollback "health gate failed after ${HEALTH_TIMEOUT}s (version='${HEALTH_VERSION}', expected='${EXPECTED_SHA}')"
fi
echo "healthz answers, build '${HEALTH_VERSION}'"

ROOT_CODE="$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 "http://${CONTAINER_IP}:8080/" 2>/dev/null || echo "000")"
if [[ "${ROOT_CODE}" != "200" ]]; then
  rollback "GET / returned HTTP ${ROOT_CODE}, want 200"
fi
echo "GET / answers 200"

echo "--- Pruning dangling images ---"
docker image prune -f || true

FINAL_IMAGE_ID="$(docker inspect "${NAME}" --format '{{.Image}}' 2>/dev/null || echo "unknown")"

echo
echo "================ Deployment successful ================"
echo "Container name: ${NAME}"
echo "Image:          ${FINAL_IMAGE_ID}"
echo "Build:          ${HEALTH_VERSION}"
echo "========================================================"
echo "Confirm from a workstation: curl -fsS https://reprise.nryn.dev/healthz"
