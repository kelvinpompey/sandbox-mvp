# Code Sandbox MVP

Single-host, multi-language code execution sandbox. A Go API server takes
execution jobs over HTTP, runs each one in a locked-down Docker container
(`--network none`, read-only root, non-root, no-new-privs, memory/CPU/pids
limits), and exposes async poll-based results.

Sandboxed languages: **Python 3.12, TypeScript (Node 22), Go 1.23,
Java 21, Rust 1.78**. The API server itself needs only Go + Docker.

## Prerequisites

- Go 1.27+ and Docker
- Your user must be in the `docker` group:

```sh
sudo usermod -aG docker $USER
newgrp docker   # or log out and back in
docker run --rm hello-world
```

## 1. Build the runner images

```sh
cd sandbox-mvp
./scripts/build-all.sh
```

This builds `sandbox-python:3.12`, `sandbox-typescript:node22`,
`sandbox-go:1.23`, `sandbox-java:21`, `sandbox-rust:1.78`.

## 2. Build and run the API server

```sh
cd sandbox-mvp
go build -o /tmp/sandbox-mvp .
PORT=8080 DATA_DIR=./data/jobs /tmp/sandbox-mvp
```

Environment variables:

| Var       | Default      | Meaning                                    |
|-----------|--------------|--------------------------------------------|
| `PORT`    | `8080`       | HTTP listen port                           |
| `DATA_DIR`| `./data/jobs`| Job JSON persistence dir (created if missing) |
| `WORKERS` | `4`          | Concurrent `docker run` workers (1–32)     |
| `API_KEY` | _(empty)_    | If set, clients must send `X-API-Key: <key>` (or `Authorization: Bearer <key>`); `/healthz` stays open |

Health check:

```sh
curl localhost:8080/healthz
# {"ok":true}
```

## 3. API reference

### `GET /v1/languages`

```sh
curl localhost:8080/v1/languages
# {"languages":[{"id":"python","image":"sandbox-python:3.12"}, ...]}
```

### `POST /v1/executions` → `202 {id, status: queued}`

```json
{
  "language": "python|typescript|go|java|rust",
  "files": [{"path": "main.py", "content": "print('hi')"}],
  "entrypoint": "main.py",
  "stdin": "",
  "limits": {"timeout_ms": 10000, "memory_mb": 256, "output_kb": 256}
}
```

Rules:

- `files`: 1–20 files, ≤512KB total. Paths must be repo-relative
  (no absolute paths, no `..`, no backslashes, max 5 levels deep).
- `entrypoint` must match one entry in `files[]`.
- `limits` (all optional): `timeout_ms` 1–120000 (default 10000),
  `memory_mb` 64–512 (default 256), `output_kb` 1–1024 (default 256).
  Compiled languages (Go/Java/Rust) are slow on first build — request
  e.g. `{"timeout_ms": 90000, "memory_mb": 512}` for them.
- `stdin` (optional, ≤512KB) is fed to the program on stdin.

### `GET /v1/executions/:id`

```json
{
  "id": "…",
  "status": "queued|running|succeeded|failed|timeout|oom|cancelled",
  "exit_code": 0,
  "stdout": "…",
  "stderr": "…",
  "timed_out": false,
  "truncated": false,
  "usage": {"wall_ms": 123}
}
```

`truncated: true` means stdout/stderr hit the `output_kb` cap.
`exit_code` is `null` while queued/running.

### `DELETE /v1/executions/:id`

Cancels a queued job immediately; interrupts a running one
(it flips to `cancelled` once the container is killed).

### Rate limits (per IP)

- Job creation (`POST /v1/executions`): 10/min → `429` when exceeded.
- Polling/reads: 300/min.

## 4. Usage examples

Python, then poll until done:

```sh
ID=$(curl -s -X POST localhost:8080/v1/executions \
  -H 'Content-Type: application/json' \
  -d '{"language":"python",
       "files":[{"path":"main.py","content":"print(42)"}],
       "entrypoint":"main.py"}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "job $ID"
while true; do
  R=$(curl -s localhost:8080/v1/executions/$ID)
  echo "$R" | python3 -c 'import sys,json; print(json.load(sys.stdin)["status"])'
  echo "$R" | python3 -c 'import sys,json; s=json.load(sys.stdin)["status"]; sys.exit(0 if s not in ("queued","running") else 1)' && break
  sleep 2
done
curl -s localhost:8080/v1/executions/$ID | python3 -m json.tool
```

With stdin:

```sh
curl -s -X POST localhost:8080/v1/executions \
  -H 'Content-Type: application/json' \
  -d '{"language":"python",
       "files":[{"path":"main.py","content":"import sys\nprint(\"echo:\" + sys.stdin.read().strip())"}],
       "entrypoint":"main.py", "stdin":"hi\n"}'
```

Multi-file (TypeScript):

```sh
curl -s -X POST localhost:8080/v1/executions \
  -H 'Content-Type: application/json' \
  -d '{"language":"typescript",
       "files":[{"path":"main.ts","content":"import {greet} from \"./util\";\nconsole.log(greet());"},
                {"path":"util.ts","content":"export function greet(){ return \"hello from util\"; }"}],
       "entrypoint":"main.ts"}'
```

Go (needs the bigger limits on a cold cache):

```sh
curl -s -X POST localhost:8080/v1/executions \
  -H 'Content-Type: application/json' \
  -d '{"language":"go",
       "files":[{"path":"main.go","content":"package main\nimport \"fmt\"\nfunc main(){ fmt.Println(\"hello go\") }"}],
       "entrypoint":"main.go",
       "limits":{"timeout_ms":90000,"memory_mb":512}}'
```

Java / Rust:

```sh
# Java: single public class matching the filename, default package
curl -s -X POST localhost:8080/v1/executions \
  -H 'Content-Type: application/json' \
  -d '{"language":"java",
       "files":[{"path":"Main.java","content":"public class Main { public static void main(String[] a){ System.out.println(\"hello java\"); } }"}],
       "entrypoint":"Main.java",
       "limits":{"timeout_ms":90000,"memory_mb":512}}'

# Rust: single file, no cargo deps
curl -s -X POST localhost:8080/v1/executions \
  -H 'Content-Type: application/json' \
  -d '{"language":"rust",
       "files":[{"path":"main.rs","content":"fn main(){ println!(\"hello rust\"); }"}],
       "entrypoint":"main.rs",
       "limits":{"timeout_ms":90000,"memory_mb":512}}'
```

Timeout demo (→ `"status":"timeout"`):

```sh
curl -s -X POST localhost:8080/v1/executions \
  -H 'Content-Type: application/json' \
  -d '{"language":"python",
       "files":[{"path":"main.py","content":"import time\ntime.sleep(30)"}],
       "entrypoint":"main.py", "limits":{"timeout_ms":3000}}'
```

With an API key configured:

```sh
curl -s localhost:8080/v1/languages -H 'X-API-Key: secret'
```

## 5. Tests

End-to-end smoke (needs the server running + images built):

```sh
./tests/smoke.sh
# optional: BASE_URL=http://host:8080 ./tests/smoke.sh
```

Covers: health, languages, path-traversal rejects (400), hello-world in
all 5 languages, stdin echo, multi-file, and timeout.

## 6. Notes / limits (MVP)

- One `docker run --rm` per execution; workspace (`mkdtemp`) deleted after
  the run. No persistence between runs, no package installs at runtime.
- Job records persist as JSON under `DATA_DIR` (`{id}.json`); non-terminal
  jobs found at startup are marked `cancelled`.
- Single module programs only: one `package main` dir (Go), one public
  class matching the filename (Java), single file no cargo deps (Rust).
- Single host, no auth beyond the static key, no streaming/SSE.
