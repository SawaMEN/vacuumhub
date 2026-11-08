#!/usr/bin/env bash
# This command overwrites the live native data directory after mandatory drill.
set -Eeuo pipefail
umask 077
[[ ${EUID:-$(id -u)} == 0 ]] || { echo 'Use sudo.' >&2; exit 1; }
[[ $# == 2 && "$2" == --confirm-overwrite ]] || {
 echo 'Usage: restore-native.sh /path/to/backup.tar.gz --confirm-overwrite' >&2
 exit 2
}
ARCHIVE="$(readlink -f -- "$1")"
[[ -s "$ARCHIVE" ]] || { echo 'Archive missing.' >&2; exit 1; }
SERVICE=vacuumhub.service
DATA_DIR=/var/lib/vacuumhub
[[ -d "$DATA_DIR" ]] || { echo 'Data directory missing.' >&2; exit 1; }
bash /opt/vacuumhub/scripts/restore-drill-native.sh "$ARCHIVE"
bash /opt/vacuumhub/scripts/backup-native.sh /var/backups/vacuumhub/pre-restore
NEXT="$(mktemp -d /var/lib/vacuumhub-restore-XXXXXXXX)"
PREVIOUS="/var/lib/vacuumhub.pre-restore-$(date -u +%Y%m%dT%H%M%S)-$$"
MOVED=0
SUCCESS=0
cleanup(){
 local rc=$?
 trap - EXIT
 if [[ "$SUCCESS" != 1 && "$MOVED" == 1 ]];then
  echo 'Восстановление не завершено; возвращаем прежнюю папку.' >&2
  systemctl stop "$SERVICE" 2>/dev/null || true
  [[ ! -d "$DATA_DIR" ]] || mv "$DATA_DIR" "$NEXT.failed" || true
  if [[ -d "$PREVIOUS" ]];then
   mv "$PREVIOUS" "$DATA_DIR" || true
   systemctl start "$SERVICE" || true
  fi
 fi
 [[ -d "$NEXT" ]] && rm -rf -- "$NEXT"
 exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM
tar --no-same-owner --no-same-permissions -xzf "$ARCHIVE" -C "$NEXT"
DATA_DIR="$NEXT" /opt/vacuumhub/vacuumhub --verify-data
chown -R vacuumhub:vacuumhub "$NEXT"
chmod 0700 "$NEXT"
systemctl stop "$SERVICE"
mv "$DATA_DIR" "$PREVIOUS"
MOVED=1
mv "$NEXT" "$DATA_DIR"
systemctl start "$SERVICE"
systemctl is-active --quiet "$SERVICE" || { echo 'Service did not start after recovery.' >&2; exit 1; }
SUCCESS=1
echo 'Данные восстановлены и служба запущена.'
echo "Предыдущая папка сохранена для ручной проверки: $PREVIOUS"
echo 'Проверьте HTTPS, каталог и файлы. Только после этого удаляйте старую папку.'
