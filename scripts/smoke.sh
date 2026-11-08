#!/usr/bin/env bash
set -euo pipefail
BASE_URL="${1:-http://127.0.0.1:8080}"
command -v curl >/dev/null || { echo 'curl is required' >&2; exit 1; }
curl --fail --show-error --silent "${BASE_URL}/api/v1/health"
echo
curl --fail --show-error --silent "${BASE_URL}/api/v1/models" | head -c 200
echo
echo 'Smoke checks completed'
