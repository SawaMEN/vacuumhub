#!/bin/sh
# Destructive recovery, intentionally NOT automatic and NOT used by CI.
# Always performs an isolated restore drill and a fresh pre-restore backup.
set -eu
umask 077
[ "$#" -eq 2 ] && [ "$2" = "--confirm-overwrite" ] || {
  echo "Usage: sh scripts/restore.sh /path/to/backup.tar.gz --confirm-overwrite" >&2
  exit 2
}
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ARCHIVE=$(readlink -f "$1")
[ -f "$ROOT/.env" ] || { echo 'Missing .env' >&2; exit 1; }
[ -s "$ARCHIVE" ] || { echo 'Backup missing' >&2; exit 1; }
compose() {
  docker compose --env-file "$ROOT/.env" -f "$ROOT/deploy/compose.yaml" "$@"
}
# Validate archive in a separate empty volume BEFORE touching the live volume.
sh "$ROOT/scripts/restore-drill.sh" "$ARCHIVE"
# Save a current-state backup, which automatically restarts the running app.
sh "$ROOT/scripts/backup.sh" "$ROOT/backups/pre-restore"
CID=$(compose ps -a -q app)
[ -n "$CID" ] || { echo 'App container missing' >&2; exit 1; }
VOLUME=$(docker inspect "$CID" --format '{{ range .Mounts }}{{ if eq .Destination "/data" }}{{ .Name }}{{ end }}{{ end }}')
[ -n "$VOLUME" ] || { echo 'App data volume missing' >&2; exit 1; }
IMAGE=$(compose images -q app | head -n 1)
[ -n "$IMAGE" ] || { echo 'App image missing' >&2; exit 1; }
DIR=$(dirname -- "$ARCHIVE")
NAME=$(basename -- "$ARCHIVE")

# If any subsequent command fails, leave the app stopped for operator recovery.
compose stop -t 120 app
echo 'Restoring live volume. If interrupted, the app remains stopped.' >&2
docker run --rm -v "$VOLUME:/target" -v "$DIR:/backup:ro" alpine:3.21 \
    sh -ec 'cd /target; find . -mindepth 1 -maxdepth 1 -exec rm -rf {} \;; tar xzf "/backup/$1"; test -s vacuumhub.sqlite' sh "$NAME"
docker run --rm --user 0 -v "$VOLUME:/data" -e DATA_DIR=/data \
    --entrypoint /usr/local/bin/vacuumhub "$IMAGE" --verify-data
compose start app
echo "Restore complete. Verify HTTPS, catalog and admin access before serving traffic."
echo "Pre-restore backup remains under $ROOT/backups/pre-restore."
