#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT_DIR/webui"
npm ci
npm run build

cd "$ROOT_DIR"
rm -rf internal/webui/dist
cp -r webui/dist internal/webui/dist

echo "WebUI build copied to internal/webui/dist"

