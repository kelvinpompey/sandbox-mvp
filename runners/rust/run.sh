#!/bin/sh
# Rust runner: /runner/run.sh <workdir> <entrypoint> [stdin_file]
# MVP: single file, no cargo deps. Entrypoint ex main.rs
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
if ! rustc -O -o /tmp/app "$FILE"; then
  exit 4
fi
if [ -n "$STDIN_FILE" ] && [ -f "$STDIN_FILE" ]; then
  exec /tmp/app < "$STDIN_FILE"
elif [ -f "$BOX/.stdin" ]; then
  exec /tmp/app < "$BOX/.stdin"
else
  exec /tmp/app
fi
