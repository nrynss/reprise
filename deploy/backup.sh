#!/usr/bin/env bash
#
# deploy/backup.sh - nightly backup of the Reprise diary and media.
#
# Copies the SQLite file through its online backup API and then copies the
# media directory beside it. The destination is a parameter, never a default
# inside this script, and the script holds no credential of any kind. Run it
# from cron on the box. See deploy/README.md for the cron line and the
# restore drill.
#
# Usage (on the box):
#
#   ./deploy/backup.sh /mnt/backups/reprise
#   BACKUP_DEST=/mnt/backups/reprise ./deploy/backup.sh
#   RETAIN_COUNT=14 ./deploy/backup.sh /mnt/backups/reprise
#
# Layout under the destination:
#
#   <dest>/reprise-<UTC timestamp>/reprise.db
#   <dest>/reprise-<UTC timestamp>/media/...
#   <dest>/reprise-<UTC timestamp>/manifest.txt
#
# Only the newest RETAIN_COUNT runs are kept. Older ones are deleted.

set -euo pipefail

BACKUP_DEST="${1:-${BACKUP_DEST:-}}"
DATA_DIR="${DATA_DIR:-/srv/reprise}"
DB_FILE="${DB_FILE:-${DATA_DIR}/data/reprise.db}"
MEDIA_DIR="${MEDIA_DIR:-${DATA_DIR}/media}"
RETAIN_COUNT="${RETAIN_COUNT:-7}"

if [[ -z "${BACKUP_DEST}" ]]; then
  echo "error: no backup destination. Pass one as the first argument or set BACKUP_DEST." >&2
  echo "       Example: $0 /mnt/backups/reprise" >&2
  exit 1
fi
if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "error: sqlite3 is not on PATH. Install it, because the backup copies the live database through its online backup API." >&2
  exit 1
fi
if [[ ! -f "${DB_FILE}" ]]; then
  echo "error: database file ${DB_FILE} does not exist. Override with DB_FILE when the store lives elsewhere." >&2
  exit 1
fi

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_DIR="${BACKUP_DEST}/reprise-${STAMP}"
mkdir -p "${RUN_DIR}"

echo "Backing up ${DB_FILE} to ${RUN_DIR}..."

# A file copy of a live SQLite database can tear. The .backup command takes
# the copy through the online backup API, so the result is consistent even
# while the server writes.
if ! sqlite3 "${DB_FILE}" ".backup '${RUN_DIR}/reprise.db'"; then
  echo "error: sqlite backup failed." >&2
  exit 1
fi

# The copy must open clean before media follows it.
CHECK="$(sqlite3 "${RUN_DIR}/reprise.db" "PRAGMA integrity_check;" 2>&1 || true)"
if [[ "${CHECK}" != "ok" ]]; then
  echo "error: backup copy failed integrity_check: ${CHECK}" >&2
  exit 1
fi

if [[ -d "${MEDIA_DIR}" ]]; then
  mkdir -p "${RUN_DIR}/media"
  cp -a "${MEDIA_DIR}/." "${RUN_DIR}/media/"
else
  echo "warning: media directory ${MEDIA_DIR} does not exist, backing up the database alone." >&2
fi

{
  echo "run: reprise-${STAMP}"
  echo "database: reprise.db ($(du -sb "${RUN_DIR}/reprise.db" | cut -f1) bytes)"
  if [[ -d "${RUN_DIR}/media" ]]; then
    echo "media: media/ ($(du -sb "${RUN_DIR}/media" | cut -f1) bytes)"
  fi
  echo "integrity_check: ok"
} > "${RUN_DIR}/manifest.txt"

# Keep the newest runs and delete the rest. The listing sorts by name, and
# the UTC timestamp sorts in age order, so the tail is the newest.
if [[ "${RETAIN_COUNT}" -ge 1 ]]; then
  KEEP="$(mktemp)"
  ls -1 "${BACKUP_DEST}" | grep '^reprise-[0-9]\{8\}T[0-9]\{6\}Z$' | sort | tail -n "${RETAIN_COUNT}" > "${KEEP}" || true
  while IFS= read -r run; do
    [[ -z "${run}" ]] && continue
    if ! grep -qx "${run}" "${KEEP}"; then
      echo "Pruning ${BACKUP_DEST}/${run}..."
      rm -rf "${BACKUP_DEST}/${run}"
    fi
  done < <(ls -1 "${BACKUP_DEST}" | grep '^reprise-[0-9]\{8\}T[0-9]\{6\}Z$' | sort || true)
  rm -f "${KEEP}"
fi

echo "Backup complete: ${RUN_DIR}"
cat "${RUN_DIR}/manifest.txt"
