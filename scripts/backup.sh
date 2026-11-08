#!/bin/sh
# Consistent offline backup: stop the app, snapshot its complete data volume,
# verify the archive, and restart the app even when backup fails.
set -eu
umask 077

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUT=${1:-"$ROOT/backups"}
mkdir -p "$OUT"
OUT=$(CDPATH= cd -- "$OUT" && pwd)
[ -f "$ROOT/.env" ] || { echo 'Missing .env in project root' >&2; exit 1; }

compose() {
    docker compose --env-file "$ROOT/.env" -f "$ROOT/deploy/compose.yaml" "$@"
}

command -v docker >/dev/null 2>&1 || { echo 'Docker required' >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { echo 'sha256sum required' >&2; exit 1; }
CID=$(compose ps -a -q app)
[ -n "$CID" ] || { echo 'App container not found; start Compose once before backup' >&2; exit 1; }
VOLUME=$(docker inspect "$CID" --format '{{ range .Mounts }}{{ if eq .Destination "/data" }}{{ .Name }}{{ end }}{{ end }}')
[ -n "$VOLUME" ] || { echo 'App /data named volume not found' >&2; exit 1; }
RUNNING=$(docker inspect "$CID" --format '{{.State.Running}}')
NEEDS_RESTART=0
cleanup() {
    RC=$?
    trap - 0
    if [ "$NEEDS_RESTART" -eq 1 ]; then
        if ! compose start app; then
            echo 'CRITICAL: app failed to restart; restart it manually' >&2
            exit 1
        fi
    fi
    exit "$RC"
}
trap cleanup 0
trap 'exit 130' HUP INT TERM
if [ "$RUNNING" = true ]; then
    NEEDS_RESTART=1
    compose stop -t 120 app
fi
NAME="vacuumhub-$(date -u +%Y%m%dT%H%M%SZ)-$$.tar.gz"
docker run --rm -v "$VOLUME:/source:ro" -v "$OUT:/backup" alpine:3.21 \
    sh -ec 'umask 077; test -s /source/vacuumhub.sqlite; cd /source; tar czf "/backup/$1" .; chown "$2:$3" "/backup/$1"' sh "$NAME" "$(id -u)" "$(id -g)"
(
    cd "$OUT"
    sha256sum "$NAME" > "$NAME.sha256"
    sha256sum -c "$NAME.sha256"
)
docker run --rm -v "$OUT:/backup:ro" alpine:3.21 tar -tzf "/backup/$NAME" >/dev/null
echo "Backup ready: $OUT/$NAME"
echo "Copy the archive and checksum to encrypted storage outside this VPS."
