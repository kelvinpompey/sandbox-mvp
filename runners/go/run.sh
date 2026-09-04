#!/bin/sh
# Go runner: /runner/run.sh <workdir> <entrypoint> [stdin_file]
# MVP: single-module, one `package main` dir. Entrypoint ex main.go (module dir).
# NOTE: <workdir> is mounted read-only; build in /tmp.
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
# Module dir = dirname of entrypoint (usually .)
MODDIR=$(dirname "$ENTRY")
if [ "$MODDIR" = "." ]; then
  MODDIR="$BOX"
else
  MODDIR="$BOX/$MODDIR"
fi
rm -rf /tmp/gobuild /tmp/gocache /tmp/gomod
mkdir -p /tmp/gobuild /tmp/gocache /tmp/gomod
cp -a "$MODDIR/." /tmp/gobuild/
cd /tmp/gobuild || exit 2
export GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomod GOTMPDIR=/tmp HOME=/tmp GOFLAGS=-mod=mod
if [ ! -f go.mod ]; then
  GO111MODULE=on go mod init sandbox >/dev/null 2>&1
fi
if ! GO111MODULE=on go build -o /tmp/app .; then
  exit 4
fi
if [ -n "$STDIN_FILE" ] && [ -f "$STDIN_FILE" ]; then
  exec /tmp/app < "$STDIN_FILE"
elif [ -f "$BOX/.stdin" ]; then
  exec /tmp/app < "$BOX/.stdin"
else
  exec /tmp/app
fi
