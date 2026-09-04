FROM golang:1.23-bookworm
# Hermetic, single-shot builds: no toolchain downloads, no module fetches
# (the sandbox runs with --network none), no cgo gcc fork.
ENV CGO_ENABLED=0 GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
RUN mkdir -p /runner /opt/gocache /opt/gomod /tmp/seed
COPY run.sh /runner/run.sh
RUN chmod +x /runner/run.sh
# Pre-warm the Go build cache for common stdlib imports. Each job container
# starts with an empty /tmp tmpfs, so without this every run recompiles
# stdlib from scratch (~8s cold, OOM-killed under the 256m default limit).
# Runtime copies /opt/gocache -> /tmp/gocache, turning that into ~0.2s.
# Flags here MUST match run.sh or the cache misses.
RUN printf '%s\n' 'package main' 'import (' '_ "bytes"' '_ "context"' '_ "crypto/sha256"' '_ "encoding/json"' '_ "errors"' '_ "fmt"' '_ "io"' '_ "math"' '_ "math/rand"' '_ "os"' '_ "regexp"' '_ "sort"' '_ "strconv"' '_ "strings"' '_ "sync"' '_ "time"' ')' 'func main() {}' > /tmp/seed/main.go
RUN cd /tmp/seed && \
    export GOCACHE=/opt/gocache GOMODCACHE=/opt/gomod GOTMPDIR=/tmp HOME=/tmp && \
    go mod init seed >/dev/null 2>&1 && \
    go build -o /tmp/seedapp . && \
    rm -rf /tmp/seed /tmp/seedapp /tmp/.cache && \
    chmod -R a+rX /opt/gocache /opt/gomod && \
    du -sh /opt/gocache
