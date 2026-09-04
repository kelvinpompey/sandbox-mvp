#!/bin/bash
set -euo pipefail
BASE="${BASE_URL:-http://localhost:8080}"

echo "== health =="
curl -fsS "$BASE/healthz" | grep -q '"ok": *true'

echo "== languages =="
curl -fsS "$BASE/v1/languages" | grep -q python

echo "== traversal rejects (400) =="
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/v1/executions" \
  -H 'Content-Type: application/json' \
  -d '{"language":"python","files":[{"path":"../escape","content":"x"}],"entrypoint":"../escape"}')
[ "$code" = "400" ] || { echo "expected 400 for ../escape, got $code"; exit 1; }
code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/v1/executions" \
  -H 'Content-Type: application/json' \
  -d '{"language":"python","files":[{"path":"/abs","content":"x"}],"entrypoint":"/abs"}')
[ "$code" = "400" ] || { echo "expected 400 for /abs, got $code"; exit 1; }

# Full matrix (hello + stdin + multi-file + timeout) runs in stdlib python
# so this script needs only python3 + curl, no jq.
python3 - "$BASE" <<'PY'
import json, sys, time, urllib.request

BASE = sys.argv[1]

def api(method, path, body=None):
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            return r.status, json.loads(r.read().decode())
    except urllib.error.HTTPError as e:
        try:
            return e.code, json.loads(e.read().decode())
        except Exception:
            return e.code, {}

import urllib.error

def submit(payload):
    code, resp = api("POST", "/v1/executions", payload)
    assert code == 202, f"POST -> {code} {resp}"
    return resp["id"]

def wait_done(jid, timeout_s=75):
    deadline = time.time() + timeout_s
    while time.time() < deadline:
        code, resp = api("GET", f"/v1/executions/{jid}")
        assert code == 200, f"GET {jid} -> {code} {resp}"
        if resp["status"] not in ("queued", "running"):
            return resp
        time.sleep(2.0)
    raise AssertionError(f"job {jid} did not finish in time")

def check(name, payload, expect_status="succeeded", expect_stdout=None, timeout_s=75):
    jid = submit(payload)
    resp = wait_done(jid, timeout_s)
    assert resp["status"] == expect_status, f"{name}: status={resp['status']} resp={resp}"
    if expect_stdout is not None:
        assert expect_stdout in (resp.get("stdout") or ""), f"{name}: stdout={resp.get('stdout')!r} resp={resp}"
    print(f"PASS {name} ({jid} -> {resp['status']})")

HELLOS = {
    "python": ({"language": "python", "files": [{"path": "main.py", "content": "print('hello python')\n"}], "entrypoint": "main.py"},
               "hello python"),
    "typescript": ({"language": "typescript", "files": [{"path": "main.ts", "content": "console.log('hello typescript');\n"}], "entrypoint": "main.ts"},
                   "hello typescript"),
    "go": ({"language": "go", "files": [{"path": "main.go", "content": "package main\nimport \"fmt\"\nfunc main(){ fmt.Println(\"hello go\") }\n"}], "entrypoint": "main.go",
            "limits": {"timeout_ms": 90000, "memory_mb": 512}},
           "hello go"),
    "java": ({"language": "java", "files": [{"path": "Main.java", "content": "public class Main { public static void main(String[] a){ System.out.println(\"hello java\"); } }\n"}], "entrypoint": "Main.java",
              "limits": {"timeout_ms": 90000, "memory_mb": 512}},
             "hello java"),
    "rust": ({"language": "rust", "files": [{"path": "main.rs", "content": "fn main(){ println!(\"hello rust\"); }\n"}], "entrypoint": "main.rs",
              "limits": {"timeout_ms": 90000, "memory_mb": 512}},
             "hello rust"),
}
for lang, (payload, needle) in HELLOS.items():
    check(f"hello-{lang}", payload, "succeeded", needle)

# stdin echo (python)
check("stdin-python",
      {"language": "python",
       "files": [{"path": "main.py", "content": "import sys\nprint('echo:' + sys.stdin.read().strip())\n"}],
       "entrypoint": "main.py", "stdin": "hi\n"},
      "succeeded", "echo:hi")

# multi-file (python)
check("multifile-python",
      {"language": "python",
       "files": [{"path": "main.py", "content": "from util import greet\nprint(greet())\n"},
                 {"path": "util.py", "content": "def greet():\n    return 'hello from util'\n"}],
       "entrypoint": "main.py"},
      "succeeded", "hello from util")

# timeout: sleep 30 with 3s limit
check("timeout-python",
      {"language": "python",
       "files": [{"path": "main.py", "content": "import time\ntime.sleep(30)\nprint('done')\n"}],
       "entrypoint": "main.py", "limits": {"timeout_ms": 3000}},
      "timeout", None, timeout_s=60)

print("ALL SMOKE TESTS PASSED")
PY

echo "SMOKE OK"
