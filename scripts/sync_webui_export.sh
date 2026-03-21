#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEBUI_DIR="$ROOT_DIR/webui"
TARGET_DIR="$ROOT_DIR/internal/web/frontend"

cd "$WEBUI_DIR"

if [[ "${SKIP_NPM_INSTALL:-0}" != "1" ]]; then
  npm install
fi

npm run build

rm -rf "$TARGET_DIR"
mkdir -p "$TARGET_DIR"
cp -R "$WEBUI_DIR/out/." "$TARGET_DIR/"

echo "Synced static WebUI export to: $TARGET_DIR"
