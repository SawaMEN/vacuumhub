#!/bin/sh
# Restore a backup into an isolated disposable Docker volume, verify SQLite and
# SHA-256 of every referenced file, then remove the temporary volume.
set -eu
umask 077
[ "$#" -eq 1 ] || { echo "Usage: sh scripts/restore-drill.sh /path/to/backup.tar.gz" >&2; exit 2; }
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
[ -f "$ROOT/.env" ] || { echo 'Missing project .env' >&2; exit 1; }
ARCHIVE=$(readlink -f "$1")
[ -s "$ARCHIVE" ] || { echo "Archive missing" >&2; exit 1; }
DIR=$(dirname -- "$ARCHIVE")
NAME=$(basename -- "$ARCHIVE")
[ -f "$ARCHIVE.sha256" ] || { echo "Checksum file missing" >&2; exit 1; }
(
    cd "$DIR"
    sha256sum -c "$NAME.sha256"
)
compose() {
    docker compose --env-file "$ROOT/.env" -f "$ROOT/deploy/compose.yaml" "$@"
}
IMAGE=$(compose images -q app | head -n 1)
[ -n "$IMAGE" ] || { echo "Build Compose app image first" >&2; exit 1; }
TEMP_VOLUME="vacuumhub-restore-drill-$(date -u +%Y%m%d%H%M%S)-$$"
docker volume create "$TEMP_VOLUME" >/dev/null
cleanup() {
    docker volume rm "$TEMP_VOLUME" >/dev/null || echo "WARNING: clean volume $TEMP_VOLUME manually" >&2
}
trap cleanup 0
trap 'exit 130' HUP INT TERM
docker run --rm -v "$TEMP_VOLUME:/data" -v "$DIR:/backup:ro" alpine:3.21 \
    sh -ec 'cd /data; tar xzf "/backup/$1"; test -s vacuumhub.sqlite; chown -R 10001:10001 /data' sh "$NAME"
# Temporary restored volume only. The verification command runs as the same
# non-root user as production and checks the archive before this test volume is removed.
docker run --rm --user 10001:10001 -v "$TEMP_VOLUME:/data" \
    -e DATA_DIR=/data --entrypoint /usr/local/bin/vacuumhub "$IMAGE" --verify-data
echo "Restore drill passed: SQLite and all file hashes verified in a disposable volume."
echo "The live VPS data volume was NOT modified."
