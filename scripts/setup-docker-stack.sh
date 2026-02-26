#!/usr/bin/env bash
set -euo pipefail

BASE_URL_DEFAULT="https://raw.githubusercontent.com/renja-g/RiftRelay/main"
TARGET_DIR="riftrelay-stack"
BASE_URL="${RIFTRELAY_BASE_URL:-$BASE_URL_DEFAULT}"
TOKEN="${RIOT_TOKEN:-}"
IMAGE="${RIFTRELAY_IMAGE:-}"
AUTO_UP="true"

print_usage() {
  cat <<'EOF'
Usage: setup-docker-stack.sh [options]

Options:
  --dir <path>                 Target directory (default: riftrelay-stack)
  --base-url <url>             Raw file base URL (default: official GitHub main branch)
  --token <value>              RIOT_TOKEN value
  --image <value>              RIFTRELAY_IMAGE value
  --no-up                      Download/configure only, do not start containers
  --help                       Show this help
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --dir)
      TARGET_DIR="$2"
      shift 2
      ;;
    --base-url)
      BASE_URL="$2"
      shift 2
      ;;
    --token)
      TOKEN="$2"
      shift 2
      ;;
    --image)
      IMAGE="$2"
      shift 2
      ;;
    --no-up)
      AUTO_UP="false"
      shift
      ;;
    --help)
      print_usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      print_usage >&2
      exit 1
      ;;
  esac
done

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

download_file() {
  local rel_path="$1"
  local destination="$2"
  curl -fsSL "${BASE_URL}/${rel_path}" -o "$destination"
}

set_env_value() {
  local file="$1"
  local key="$2"
  local value="$3"
  local tmp_file
  tmp_file="$(mktemp)"
  awk -F= -v key="$key" -v value="$value" '
    BEGIN { replaced = 0 }
    $1 == key {
      print key "=" value
      replaced = 1
      next
    }
    { print $0 }
    END {
      if (replaced == 0) {
        print key "=" value
      }
    }
  ' "$file" > "$tmp_file"
  mv "$tmp_file" "$file"
}

require_command curl
require_command docker

mkdir -p "$TARGET_DIR"

download_file "docker-compose.yml" "$TARGET_DIR/docker-compose.yml"
download_file ".env.example" "$TARGET_DIR/.env"

if [ -z "$TOKEN" ]; then
  printf "Enter RIOT_TOKEN: "
  read -r TOKEN
fi

if [ -z "$TOKEN" ]; then
  echo "RIOT_TOKEN is required." >&2
  exit 1
fi

set_env_value "$TARGET_DIR/.env" "RIOT_TOKEN" "$TOKEN"
if [ -n "$IMAGE" ]; then
  set_env_value "$TARGET_DIR/.env" "RIFTRELAY_IMAGE" "$IMAGE"
fi

if [ "$AUTO_UP" = "true" ]; then
  # Pull image first to avoid building (for production use)
  docker compose -f "$TARGET_DIR/docker-compose.yml" --env-file "$TARGET_DIR/.env" pull || true
  docker compose -f "$TARGET_DIR/docker-compose.yml" --env-file "$TARGET_DIR/.env" up -d
fi

echo
echo "RiftRelay stack ready in: $TARGET_DIR"
if [ "$AUTO_UP" = "true" ]; then
  echo "RiftRelay:   http://localhost:8985"
  echo
  echo "Tail logs:"
  echo "  docker compose -f \"$TARGET_DIR/docker-compose.yml\" --env-file \"$TARGET_DIR/.env\" logs -f riftrelay"
  echo
  echo "Stop stack:"
  echo "  docker compose -f \"$TARGET_DIR/docker-compose.yml\" --env-file \"$TARGET_DIR/.env\" down"
else
  echo "Start stack:"
  echo "  docker compose -f \"$TARGET_DIR/docker-compose.yml\" --env-file \"$TARGET_DIR/.env\" up -d"
fi
