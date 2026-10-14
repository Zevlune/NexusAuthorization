#!/usr/bin/env sh
set -eu

# Compatibility entry point for the first-deployment initializer.
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec sh "$SCRIPT_DIR/init-db.sh" "$@"
