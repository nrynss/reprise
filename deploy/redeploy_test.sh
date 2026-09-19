#!/usr/bin/env bash
# deploy/redeploy_test.sh - pin the redeploy rollback path.
#
# It builds stub docker, curl and run.sh binaries on PATH, then drives
# deploy/redeploy.sh through three scenarios. A failed gate must restore
# the recorded previous image, stop the failed container fast, verify the
# restore, and exit 1. A passing gate must exit 0 with no restore.
#
# Usage (from the repository root):
#
#   ./deploy/redeploy_test.sh

set -euo pipefail
cd "$(dirname "$0")/.."

STATE="$(mktemp -d)"
trap 'rm -rf "${STATE}"' EXIT
STUBBIN="${STATE}/bin"
mkdir -p "${STUBBIN}"

# Fake docker. It serves container state from files and records every
# stop, remove, run and prune call for the assertions below.
cat > "${STUBBIN}/docker" <<'STUB'
#!/usr/bin/env bash
state="${STATE:?}"
cmd="${1:-}"
case "${cmd}" in
  inspect)
    hit=0
    for a in "$@"; do
      if [[ "${a}" == *"IPAddress"* ]]; then
        hit=1
      fi
    done
    if [[ ${hit} -eq 1 ]]; then
      cat "${state}/IP" 2>/dev/null || true
    else
      cat "${state}/IMAGE" 2>/dev/null || true
    fi
    ;;
  image)
    if [[ "${2:-}" == "prune" ]]; then
      echo "prune" >> "${state}/CALLS"
    else
      cat "${state}/DIGEST" 2>/dev/null || true
    fi
    ;;
  ps)
    echo "status: Up 2 minutes"
    ;;
  logs)
    echo "stub log line"
    ;;
  stop|rm|run)
    echo "${cmd} $*" >> "${state}/CALLS"
    ;;
  *)
    echo "stub docker: unknown ${cmd}" >&2
    exit 1
    ;;
esac
STUB

# Fake curl. It answers healthz and the root code from the state files.
cat > "${STUBBIN}/curl" <<'STUB'
#!/usr/bin/env bash
state="${STATE:?}"
url="${@: -1}"
code=0
for a in "$@"; do
  if [[ "${a}" == *http_code* ]]; then
    code=1
  fi
done
healthy="$(cat "${state}/HEALTHY")"
want_ip="$(cat "${state}/IP")"
build="$(cat "${state}/BUILD")"
if [[ ${code} -eq 1 ]]; then
  if [[ "${healthy}" == "1" && "${url}" == *"${want_ip}"* ]]; then
    printf '200'
  else
    printf '000'
  fi
  exit 0
fi
if [[ "${healthy}" == "1" && "${url}" == *"healthz"* && "${url}" == *"${want_ip}"* ]]; then
  printf 'ok stubboot version=%s' "${build}"
fi
exit 0
STUB

# Fake run.sh. It records its IMAGE and flips the fake container to the
# result the scenario maps for that image.
cat > "${STUBBIN}/run-stub.sh" <<'STUB'
#!/usr/bin/env bash
state="${STATE:?}"
echo "run IMAGE=${IMAGE:-} NAME=${NAME:-}" >> "${state}/CALLS"
if [[ "${IMAGE:-}" == "${MAP_NEW_IMAGE:-__none__}" ]]; then
  printf '%s' "${MAP_NEW_RESULT_IMAGE}" > "${state}/IMAGE"
  printf '%s' "${MAP_NEW_BUILD}" > "${state}/BUILD"
  printf '%s' "${MAP_NEW_HEALTHY}" > "${state}/HEALTHY"
elif [[ "${IMAGE:-}" == "${MAP_PREV_IMAGE:-__none__}" ]]; then
  printf '%s' "${MAP_PREV_RESULT_IMAGE}" > "${state}/IMAGE"
  printf '%s' "${MAP_PREV_BUILD}" > "${state}/BUILD"
  printf '%s' "${MAP_PREV_HEALTHY}" > "${state}/HEALTHY"
fi
exit 0
STUB

chmod 0755 "${STUBBIN}/docker" "${STUBBIN}/curl" "${STUBBIN}/run-stub.sh"
export PATH="${STUBBIN}:${PATH}"
export STATE

failures=0

# set_state prepares the fake running container before one scenario.
set_state() {
  printf '%s' "$1" > "${STATE}/IMAGE"
  printf '%s' "$2" > "${STATE}/IP"
  printf '%s' "$3" > "${STATE}/BUILD"
  printf '%s' "$4" > "${STATE}/HEALTHY"
  printf '%s' "$5" > "${STATE}/DIGEST"
  : > "${STATE}/CALLS"
}

pass() {
  echo "PASS: $1"
}

fail() {
  echo "FAIL: $1" >&2
  failures=$((failures + 1))
}

expect_contains() {
  if printf '%s' "$2" | grep -qF "$3"; then
    pass "$1"
  else
    fail "$1 (missing ${3})"
  fi
}

expect_call() {
  if grep -qF "$2" "${STATE}/CALLS"; then
    pass "$1"
  else
    fail "$1 (no call ${2})"
  fi
}

echo "--- case 1: failed gate restores the previous image ---"
set_state "prev-id-aaa" "172.18.0.9" "prevbuild" "1" ""
export MAP_NEW_IMAGE="new-image-bbb" MAP_NEW_RESULT_IMAGE="new-image-bbb"
export MAP_NEW_BUILD="newbuild" MAP_NEW_HEALTHY="0"
export MAP_PREV_IMAGE="prev-id-aaa" MAP_PREV_RESULT_IMAGE="prev-id-aaa"
export MAP_PREV_BUILD="prevbuild" MAP_PREV_HEALTHY="1"
start=$(date +%s)
set +e
out="$(HEALTH_TIMEOUT=3 NAME=t7box RUN_SCRIPT="${STUBBIN}/run-stub.sh" ./deploy/redeploy.sh new-image-bbb newbuild 2>&1)"
code=$?
set -e
elapsed=$(( $(date +%s) - start ))
if [[ ${code} -eq 1 ]]; then
  pass "failed gate exits 1"
else
  fail "failed gate exits 1 (got ${code})"
fi
expect_contains "rollback names the recorded image" "${out}" "Restoring previous image: prev-id-aaa"
expect_contains "rollback verifies the restore" "${out}" "Rollback answers healthz at build 'prevbuild'"
expect_call "failed container stops fast" "stop -t 30 t7box"
expect_call "previous image starts again" "run IMAGE=prev-id-aaa NAME=t7box"
if [[ ${elapsed} -lt 30 ]]; then
  pass "failed deploy reads as red in ${elapsed}s"
else
  fail "failed deploy reads as red in ${elapsed}s (took ${elapsed}s)"
fi

echo "--- case 2: passing gate starts the target and exits 0 ---"
set_state "prev-id-aaa" "172.18.0.9" "prevbuild" "1" ""
export MAP_NEW_IMAGE="new-image-bbb" MAP_NEW_RESULT_IMAGE="new-image-bbb"
export MAP_NEW_BUILD="newbuild777" MAP_NEW_HEALTHY="1"
export MAP_PREV_IMAGE="prev-id-aaa" MAP_PREV_RESULT_IMAGE="prev-id-aaa"
export MAP_PREV_BUILD="prevbuild" MAP_PREV_HEALTHY="1"
set +e
out="$(HEALTH_TIMEOUT=3 NAME=t7box RUN_SCRIPT="${STUBBIN}/run-stub.sh" ./deploy/redeploy.sh new-image-bbb newbuild 2>&1)"
code=$?
set -e
if [[ ${code} -eq 0 ]]; then
  pass "passing gate exits 0"
else
  fail "passing gate exits 0 (got ${code})"
fi
expect_contains "passing gate reports success" "${out}" "Deployment successful"
expect_call "passing gate prunes" "prune"
if grep -qF "run IMAGE=prev-id-aaa" "${STATE}/CALLS"; then
  fail "passing gate starts no restore"
else
  pass "passing gate starts no restore"
fi

echo "--- case 3: failed gate with no previous image cannot roll back ---"
set_state "" "172.18.0.9" "" "0" ""
export MAP_NEW_IMAGE="new-image-bbb" MAP_NEW_RESULT_IMAGE="new-image-bbb"
export MAP_NEW_BUILD="newbuild" MAP_NEW_HEALTHY="0"
export MAP_PREV_IMAGE="prev-id-aaa" MAP_PREV_RESULT_IMAGE="prev-id-aaa"
export MAP_PREV_BUILD="prevbuild" MAP_PREV_HEALTHY="1"
set +e
out="$(HEALTH_TIMEOUT=3 NAME=t7box RUN_SCRIPT="${STUBBIN}/run-stub.sh" ./deploy/redeploy.sh new-image-bbb newbuild 2>&1)"
code=$?
set -e
if [[ ${code} -eq 1 ]]; then
  pass "missing rollback exits 1"
else
  fail "missing rollback exits 1 (got ${code})"
fi
expect_contains "missing rollback says so" "${out}" "cannot roll back"
if grep -qF "stop -t" "${STATE}/CALLS"; then
  fail "missing rollback stops nothing"
else
  pass "missing rollback stops nothing"
fi

if [[ ${failures} -gt 0 ]]; then
  echo "${failures} check(s) failed" >&2
  exit 1
fi
echo "OK redeploy rollback path pinned"
