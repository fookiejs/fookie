#!/usr/bin/env bash
# Thin wrapper — forwards to scripts/fookie.sh
exec "$(dirname "$0")/scripts/fookie.sh" "$@"
