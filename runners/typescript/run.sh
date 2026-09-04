#!/bin/sh
# TypeScript runner: /runner/run.sh <workdir> <entrypoint> [stdin_file]
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
# Compile all .ts files under workdir, preserving relative paths into /tmp/dist
TSFILES=$(find "$BOX" -maxdepth 4 -name '*.ts' -not -name '*.d.ts')
if [ -z "$TSFILES" ]; then
  echo "no typescript files found" >&2
  exit 2
fi
rm -rf /tmp/dist
mkdir -p /tmp/dist
# shellcheck disable=SC2086
if ! tsc --strict --module commonjs --target ES2020 --outDir /tmp/dist --rootDir "$BOX" $TSFILES; then
  exit 4
fi
# Map entrypoint main.ts (or sub/dir.ts) -> /tmp/dist/main.js
REL_NOEXT=$(echo "$ENTRY" | sed 's/\.ts$//')
JS="/tmp/dist/$REL_NOEXT.js"
if [ ! -f "$JS" ]; then
  # Fallback: top-level main.js
  JS="/tmp/dist/main.js"
fi
if [ ! -f "$JS" ]; then
  echo "compiled output not found for $ENTRY" >&2
  exit 4
fi
if [ -n "$STDIN_FILE" ] && [ -f "$STDIN_FILE" ]; then
  exec node "$JS" < "$STDIN_FILE"
elif [ -f "$BOX/.stdin" ]; then
  exec node "$JS" < "$BOX/.stdin"
else
  exec node "$JS"
fi
