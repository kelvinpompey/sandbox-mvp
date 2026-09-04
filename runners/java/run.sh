#!/bin/sh
# Java runner: /runner/run.sh <workdir> <entrypoint> [stdin_file]
# MVP: one public class matching filename, default package. Entrypoint ex Main.java
# NOTE: <workdir> is mounted read-only; compile output goes to /tmp/classes.
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
if [ ! -f "$BOX/$ENTRY" ]; then
  echo "entrypoint not found: $ENTRY" >&2
  exit 2
fi
rm -rf /tmp/classes
mkdir -p /tmp/classes
JFILES=$(find "$BOX" -maxdepth 2 -name '*.java')
if [ -z "$JFILES" ]; then
  echo "no java files found" >&2
  exit 2
fi
# shellcheck disable=SC2086
if ! javac -d /tmp/classes $JFILES; then
  exit 4
fi
BASENAME=$(basename "$ENTRY" .java)
if [ -n "$STDIN_FILE" ] && [ -f "$STDIN_FILE" ]; then
  exec java -cp /tmp/classes "$BASENAME" < "$STDIN_FILE"
elif [ -f "$BOX/.stdin" ]; then
  exec java -cp /tmp/classes "$BASENAME" < "$BOX/.stdin"
else
  exec java -cp /tmp/classes "$BASENAME"
fi
