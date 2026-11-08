#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
SERVICE=vacuumhub.service
DATA_DIR=/var/lib/vacuumhub
OUT="${1:-/var/backups/vacuumhub}"
[[ ${EUID:-$(id -u)} == 0 ]] || { echo 'Use sudo.' >&2; exit 1; }
[[ -s "$DATA_DIR/vacuumhub.sqlite" ]] || { echo 'Database not found.' >&2; exit 1; }
install -d -m 0700 "$OUT"
OUT="$(cd -- "$OUT" && pwd)"
ACTIVE=0
systemctl is-active --quiet "$SERVICE" && ACTIVE=1 || true
restore_service(){
 local rc=$?
 trap - EXIT
 if [[ "$ACTIVE" == 1 ]]; then
  if ! systemctl start "$SERVICE"; then echo 'CRITICAL: service restart failed' >&2; rc=1; fi
 fi
 exit "$rc"
}
trap restore_service EXIT
trap 'exit 130' INT TERM HUP
if [[ "$ACTIVE" == 1 ]]; then systemctl stop "$SERVICE";fi
[[ -s "$DATA_DIR/vacuumhub.sqlite" ]] || { echo 'Database missing after stop.' >&2; exit 1; }
name="vacuumhub-$(date -u +%Y%m%dT%H%M%SZ)-$@.tar.gz"
tmp="$OUT/$name.part"
trap 'rm -f "$tmp"; restore_service' EXIT
tar -C "$DATA_DIR" -czf "$tmp" .
tar -tzf "$tmp" >/dev/null
mv -f -- "$tmp" "$OUT/$name"
(
 cd "$OUT"
 sha256sum "$name" > "$name.sha256"
 sha256sum -c "$name.sha256"
)
chmod 0600 "$OUT/$name" "$OUT/$name.sha256"
echo "Создана копия: $OUT/$name"
echo 'Скопируйте архив в отдельное шифрованное хранилище за пределами VDS.'
