#!/usr/bin/env bash
set -Eeuo pipefail
[[ $# -eq 1 ]] || { echo "Usage: bash scripts/smoke.sh https://PUBLIC_IP_OR_DOMAIN:PORT" >&2; exit 2; }
BASE_URL="${1%/}"
[[ "$BASE_URL" == https://* ]] || { echo 'Only HTTPS is supported.' >&2; exit 2; }
[[ "$BASE_URL" != *127.0.0.1* && "$BASE_URL" != *localhost* && "$BASE_URL" != *'[::1]'* ]] ||
  { echo 'Local access is not a supported deployment mode.' >&2; exit 2; }
command -v curl >/dev/null || { echo 'curl is required' >&2; exit 1; }
curl --fail --show-error --silent --max-time 10 "$BASE_URL/api/v1/health"
echo
curl --fail --show-error --silent --max-time 10 "$BASE_URL/api/v1/ready"
echo
curl --fail --show-error --silent --max-time 10 "$BASE_URL/api/v1/models" | head -c 200
echo
echo 'Remote HTTPS checks completed'
