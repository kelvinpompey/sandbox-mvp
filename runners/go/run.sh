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
rm -rf /tmp/gobuild
mkdir -p /tmp/gobuild /tmp/gocache /tmp/gomod
# Seed the build cache pre-warmed at image build time (/opt/gocache).
# A fresh container starts with an empty /tmp tmpfs; without this copy
# every job recompiles stdlib cold (~8s, OOM-killed under 256m).
cp -a /opt/gocache/. /tmp/gocache/ 2>/dev/null || true
cp -a /opt/gomod/. /tmp/gomod/ 2>/dev/null || true
cp -a "$MODDIR/." /tmp/gobuild/
cd /tmp/gobuild || exit 2
# MUST match the Dockerfile seed-build flags or the cache misses.
export GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomod GOTMPDIR=/tmp HOME=/tmp GOFLAGS=-mod=mod
export CGO_ENABLED=0 GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GO111MODULE=on
if [ ! -f go.mod ]; then
  go mod init sandbox >/dev/null 2>&1
fi
if ! go build -o /tmp/app .; then
  exit 4
fi
if [ -n "$STDIN_FILE" ] && [ -f "$STDIN_FILE" ]; then
  exec /tmp/app < "$STDIN_FILE"
elif [ -f "$BOX/.stdin" ]; then
  exec /tmp/app < "$BOX/.stdin"
else
  exec /tmp/app
fi
