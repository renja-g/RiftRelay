#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ ! -f .env ]]; then
	echo "Missing .env — copy .env.example to .env and set RIOT_TOKEN." >&2
	exit 1
fi
set -a
# shellcheck source=/dev/null
source .env
set +a
exec go run .
