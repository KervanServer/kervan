#!/bin/sh
set -eu

CONFIG_PATH="${KERVAN_CONFIG:-/var/lib/kervan/kervan.yaml}"

init_config() {
  if [ ! -f "$CONFIG_PATH" ]; then
    mkdir -p "$(dirname "$CONFIG_PATH")"
    /usr/local/bin/kervan init --config "$CONFIG_PATH"
  fi
}

bootstrap_admin() {
  if [ -z "${KERVAN_ADMIN_PASSWORD:-}" ]; then
    return 0
  fi

  admin_username="${KERVAN_ADMIN_USERNAME:-admin}"
  if output=$(/usr/local/bin/kervan admin create --config "$CONFIG_PATH" --username "$admin_username" --password "$KERVAN_ADMIN_PASSWORD" 2>&1); then
    printf '%s\n' "$output"
    return 0
  fi

  case "$output" in
    *"already exists"*)
      printf 'Admin user "%s" already exists; skipping bootstrap.\n' "$admin_username"
      ;;
    *)
      printf '%s\n' "$output" >&2
      exit 1
      ;;
  esac
}

run_default_server() {
  init_config
  bootstrap_admin
  exec /usr/local/bin/kervan -config "$CONFIG_PATH" "$@"
}

if [ "$#" -eq 0 ]; then
  run_default_server
fi

case "$1" in
  kervan)
    shift
    if [ "$#" -eq 0 ]; then
      run_default_server
    fi
    ;;
esac

case "$1" in
  -*)
    run_default_server "$@"
    ;;
  version|--version|-v|init|keygen|admin|user|backup|check|migrate|mcp|status)
    init_config
    exec /usr/local/bin/kervan "$@"
    ;;
  *)
    exec "$@"
    ;;
esac
