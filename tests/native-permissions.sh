#!/usr/bin/env bash
# Reproduce old 0700 /etc/vacuumhub TLS failure, then prove repair for
# the non-root systemd user. Run as root inside an ephemeral CI runner.
set -Eeuo pipefail
[[ "${EUID:-$(id -u)}" == 0 ]] || { echo 'Run this test as root' >&2; exit 2; }
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/install.sh"

TMPROOT="$(mktemp -d)"
trap 'rm -rf -- "$TMPROOT"' EXIT
chmod 0755 "$TMPROOT"
CONFIG_DIR="$TMPROOT/etc/vacuumhub"
CERT_DIR="$CONFIG_DIR/tls"
STATE_DIR="$TMPROOT/var/lib/vacuumhub"
INSTALL_DIR="$TMPROOT/opt/vacuumhub"
ENV_FILE="$CONFIG_DIR/environment"

if ! id vacuumhub >/dev/null 2>&1;then
 useradd --system --home-dir "$STATE_DIR" --shell /usr/sbin/nologin --user-group vacuumhub
fi
install -d -m 0700 -o root -g root "$CONFIG_DIR"
install -d -m 0750 -o root -g vacuumhub "$CERT_DIR"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
 -sha256 -nodes -days 1 -subj '/CN=example.org' -addext 'subjectAltName=DNS:example.org' \
 -out "$TMPROOT/fixture.crt" -keyout "$TMPROOT/fixture.key" >/dev/null 2>&1
install_cert_copy "$TMPROOT/fixture.crt" "$TMPROOT/fixture.key"
printf '%s\n' 'ADMIN_BASIC_PASSWORD_B64=dGVzdA==' > "$ENV_FILE"
chmod 0600 "$ENV_FILE"

if runuser -u vacuumhub -- test -r "$CERT_DIR/key.pem";then
 echo 'Test invalid: old root-only parent directory should block TLS key' >&2
 exit 1
fi

prepare_directories
runuser -u vacuumhub -- test -r "$CERT_DIR/cert.pem"
runuser -u vacuumhub -- test -r "$CERT_DIR/key.pem"
runuser -u vacuumhub -- openssl x509 -in "$CERT_DIR/cert.pem" -noout
runuser -u vacuumhub -- openssl pkey -in "$CERT_DIR/key.pem" -noout >/dev/null
if runuser -u vacuumhub -- test -r "$ENV_FILE";then
 echo 'Root-only environment file was exposed to app user' >&2
 exit 1
fi
[[ "$(stat -c %a "$CONFIG_DIR")" == 750 ]]
[[ "$(stat -c %a "$CERT_DIR")" == 750 ]]
[[ "$(stat -c %a "$CERT_DIR/key.pem")" == 640 ]]
[[ "$(stat -c %a "$ENV_FILE")" == 600 ]]

# An old system update may also have left the key root-only. Repaired on rerun.
chown root:root "$CERT_DIR/key.pem"
chmod 0600 "$CERT_DIR/key.pem"
prepare_directories
runuser -u vacuumhub -- openssl pkey -in "$CERT_DIR/key.pem" -noout >/dev/null
echo 'TLS permission repair and root-only config isolation passed.'
