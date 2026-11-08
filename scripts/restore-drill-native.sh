#!/usr/bin/env bash
# Validate backup without starting the app or changing its live files.
set -Eeuo pipefail
umask 077
[[ ${EUID:-$(id -u)} == 0 ]] || { echo 'Use sudo.' >&2; exit 1; }
[[ $# == 1 ]] || { echo 'Usage: restore-drill-native.sh /path/backup.tar.gz' >&2; exit 2; }
ARCHIVE="$(readlink -f -- "$1")"
[[ -s "$ARCHIVE" && -s "$ARCHIVE.sha256" ]] || { echo 'Archive or checksum missing.' >&2; exit 1; }
[[ -x /opt/vacuumhub/vacuumhub ]] || { echo 'VacuumHub binary not installed.' >&2; exit 1; }
NAME="$(basename -- "$ARCHIVE")";DIR="$(dirname -- "$ARCHIVE")"
( cd "$DIR"; sha256sum -c -- "$NAME.sha256" )
while IFS= read -r entry;do
 case "$entry" in
  /*|..|../*|*/../*|*/..) echo "Unsafe archive path: $entry" >&2; exit 1;;
 esac
done < <(tar -tzf "$ARCHIVE")
while IFS= read -r entry;do
 [[ "${entry:0:1}" == '-' || "${entry:0:1}" == 'd' ]] || {
  echo 'Archive contains links or special files.' >&2; exit 1;
 }
done < <(tar -tvzf "$ARCHIVE")
TMP="$(mktemp -d /var/tmp/vacuumhub-verify-XXXXXXXX)"
cleanup(){ rm -rf -- "$TMP"; }
trap cleanup EXIT
trap 'exit 130' INT TERM HUP
tar --no-same-owner --no-same-permissions -xzf "$ARCHIVE" -C "$TMP"
[[ -s "$TMP/vacuumhub.sqlite" ]] || { echo 'Database missing.' >&2; exit 1; }
DATA_DIR="$TMP" /opt/vacuumhub/vacuumhub --verify-data
echo 'Проверочное восстановление прошло успешно. Рабочие данные не изменены.'
