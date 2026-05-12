#!/usr/bin/env bash

# Added: fail immediately on errors
set -e

# Added: require runtime name
if [ -z "$1" ]; then
  echo "Usage: ./scripts/dev/run-agent.sh <runtime>"
  exit 1
fi

# Added: runtime argument
RUNTIME="$1"

# Added: runtime path
RUNTIME_SCRIPT="./runtimes/$RUNTIME/start.sh"

# Added: ensure runtime exists
if [ ! -f "$RUNTIME_SCRIPT" ]; then
  echo "Runtime not found: $RUNTIME"
  exit 1
fi

# Added: launch runtime
"$RUNTIME_SCRIPT"
