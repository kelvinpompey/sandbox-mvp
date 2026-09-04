#!/bin/sh
# Python runner: /runner/run.sh <workdir> <entrypoint> [stdin_file]
# NOTE: <workdir> is mounted read-only; only /tmp is writable.
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
export PYTHONPYCACHEPREFIX=/tmp/pycache
mkdir -p /tmp/pycache
if ! python3 -m py_compile "$FILE"; then
  exit 4
fi
if [ -n "$STDIN_FILE" ] && [ -f "$STDIN_FILE" ]; then
  exec python3 "$FILE" < "$STDIN_FILE"
elif [ -f "$BOX/.stdin" ]; then
  exec python3 "$FILE" < "$BOX/.stdin"
else
  exec python3 "$FILE"
fi
