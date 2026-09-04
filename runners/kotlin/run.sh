#!/bin/sh
# Kotlin runner: /runner/run.sh <workdir> <entrypoint> [stdin_file]
# MVP: compiles all .kt files under workdir (no gradle/maven deps),
# links the Kotlin runtime into a single jar, runs it with java.
# Entrypoint ex Main.kt (must be a .kt file present in files[]).
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
  *.kt) ;;
  *) echo "entrypoint must be a .kt file" >&2; exit 2;;
esac
# kotlinc writes caches under $HOME; / is read-only so redirect to /tmp.
export HOME=/tmp
mkdir -p /tmp
KFILES=$(find "$BOX" -maxdepth 4 -name '*.kt')
if [ -z "$KFILES" ]; then
  echo "no kotlin files found" >&2
  exit 2
fi
rm -f /tmp/app.jar
# shellcheck disable=SC2086
if ! kotlinc $KFILES -include-runtime -d /tmp/app.jar; then
  exit 4
fi
if [ -n "$STDIN_FILE" ] && [ -f "$STDIN_FILE" ]; then
  exec java -jar /tmp/app.jar < "$STDIN_FILE"
elif [ -f "$BOX/.stdin" ]; then
  exec java -jar /tmp/app.jar < "$BOX/.stdin"
else
  exec java -jar /tmp/app.jar
fi
