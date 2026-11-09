#!/usr/bin/env bash
set -Eeuo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/install.sh"
FIXTURE="$(mktemp -d)"
trap 'rm -rf -- "$FIXTURE"' EXIT
ENV_FILE="$FIXTURE/environment"
valid_port 443
valid_port 8443
if valid_port 0 || valid_port 65536 || valid_port abc;then echo 'Invalid port accepted';exit 1;fi
valid_ip 203.0.113.15
if valid_ip 127.0.0.1 || valid_ip 10.0.0.1 || valid_ip invalid;then echo 'Local IP accepted';exit 1;fi
valid_domain example.com
if valid_domain localhost || valid_domain 127.0.0.1 || valid_domain example.local;then
 echo 'Local hostname accepted';exit 1
fi
suggest_ip(){ printf '203.0.113.15'; }
VACUUMHUB_NONINTERACTIVE=1
VACUUMHUB_MODE=ip
VACUUMHUB_HOST=203.0.113.15
VACUUMHUB_PORT=8443
VACUUMHUB_TLS_MODE=selfsigned
VACUUMHUB_ADMIN_USER=testadmin
VACUUMHUB_ADMIN_PASSWORD=some-secure-test-password
config_access
config_credentials
CERT_NAME=''
write_environment "$ENV_FILE"
grep -qx 'ACCESS_MODE=ip' "$ENV_FILE"
grep -qx 'LISTEN_HOST=0.0.0.0' "$ENV_FILE"
grep -qx 'ADMIN_BASIC_USER=testadmin' "$ENV_FILE"
grep -qx 'LISTEN_PORT=8443' "$ENV_FILE"
[[ "$PASSWORD_B64" == "$(printf %s "$VACUUMHUB_ADMIN_PASSWORD" | base64 | tr -d '\n')" ]]
VACUUMHUB_MODE=domain
VACUUMHUB_HOST=vacuum.example.com
VACUUMHUB_TLS_MODE=selfsigned
config_access
[[ "$ACCESS_MODE" == domain && "$PUBLIC_HOST" == vacuum.example.com ]]
if (VACUUMHUB_MODE=local config_access) >/dev/null 2>&1;then
 echo 'Installer unexpectedly permits local access'; exit 1
fi
bash -n "$ROOT/install.sh"
bash -n "$ROOT/deploy/vacuumhub"
bash -n "$ROOT/scripts/backup-native.sh"
# Backup names must include the process ID to avoid same-second collisions.
grep -Fq '$.tar.gz' "$ROOT/scripts/backup-native.sh"
bash -n "$ROOT/scripts/restore-drill-native.sh"
bash -n "$ROOT/scripts/restore-native.sh"
echo 'Native installer choice, validation and shell syntax tests passed.'
