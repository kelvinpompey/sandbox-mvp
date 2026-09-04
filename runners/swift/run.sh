#!/bin/sh
# Swift runner: /runner/run.sh <workdir> <entrypoint> [stdin_file]
# MVP: single file or multi-file (no SwiftPM deps). Compiles all .swift files.
# NOTE: <workdir> is mounted read-only; build output goes to /tmp.
BOX="$1"
ENTRY="$2"
STDIN_FILE="$3"
if [ -z "$BOX" ] || [ -z "$ENTRY" ]; then
  echo "usage: run.sh <workdir> <entrypoint> [stdin_file]" >&2
  exit 2
fi
case "$ENTRY" in
  *..*|/*|*\\*) echo "bad entrypoint" >&2; exit 2;;
esac
FILE="$BOX/$ENTRY"
if [ ! -f "$FILE" ]; then
  echo "entrypoint not found: $ENTRY" >&2
  exit 2
fi
case "$ENTRY" in
  *.swift) ;;
  *) echo "entrypoint must be a .swift file" >&2; exit 2;;
esac
# Swift/clang write caches under $HOME/.cache -> /.cache with --read-only fails.
# Redirect all caches to writable /tmp (only /tmp is tmpfs-writable).
export HOME=/tmp
export XDG_CACHE_HOME=/tmp/.cache
export CLANG_MODULE_CACHE_PATH=/tmp/clang-cache
mkdir -p /tmp/.cache /tmp/clang-cache
# Collect all Swift sources (max depth 4, like TypeScript runner).
SWIFTFILES=$(find "$BOX" -maxdepth 4 -name '*.swift')
if [ -z "$SWIFTFILES" ]; then
  echo "no swift files found" >&2
  exit 2
fi
# Compile all sources into a single binary. -O for optimized build.
# shellcheck disable=SC2086
if ! swiftc -O -module-cache-path /tmp/clang-cache -o /tmp/app $SWIFTFILES; then
  exit 4
fi
if [ -n "$STDIN_FILE" ] && [ -f "$STDIN_FILE" ]; then
  exec /tmp/app < "$STDIN_FILE"
elif [ -f "$BOX/.stdin" ]; then
  exec /tmp/app < "$BOX/.stdin"
else
  exec /tmp/app
fi
