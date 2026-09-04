#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
docker build -t sandbox-python:3.12 ./runners/python
docker build -t sandbox-typescript:node22 ./runners/typescript
docker build -t sandbox-go:1.23 ./runners/go
docker build -t sandbox-java:21 ./runners/java
docker build -t sandbox-rust:1.78 ./runners/rust
docker build -t sandbox-swift:5.10 ./runners/swift
docker images | grep -E 'sandbox-(python|typescript|go|java|rust|swift)'
