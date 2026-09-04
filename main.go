package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------- Types ----------

type FileSpec struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type Limits struct {
	TimeoutMs int `json:"timeout_ms"`
	MemoryMb  int `json:"memory_mb"`
	OutputKb  int `json:"output_kb"`
}

type ExecRequest struct {
	Language   string     `json:"language"`
	Files      []FileSpec `json:"files"`
	Entrypoint string     `json:"entrypoint"`
	Stdin      string     `json:"stdin"`
	Limits     *Limits    `json:"limits"`
}

type Job struct {
	ID         string         `json:"id"`
	Language   string         `json:"language"`
	Files      []FileSpec     `json:"files,omitempty"`
	Entrypoint string         `json:"entrypoint"`
	Stdin      string         `json:"stdin,omitempty"`
	Limits     Limits         `json:"limits"`
	Status     string         `json:"status"` // queued|running|succeeded|failed|timeout|oom|cancelled
	ExitCode   *int           `json:"exit_code"`
	Stdout     string         `json:"stdout"`
	Stderr     string         `json:"stderr"`
	TimedOut   bool           `json:"timed_out"`
	Truncated  bool           `json:"truncated"`
	Usage      map[string]any `json:"usage,omitempty"`
	Error      string         `json:"error,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// ---------- Globals ----------

var (
	jobs      = map[string]*Job{}
	mu        sync.Mutex
	queue     = make(chan string, 128)
	cancels   = map[string]context.CancelFunc{}
	cancelReq = map[string]bool{}

	rateMu   sync.Mutex
	rateHits = map[string][]time.Time{}

	dataDir = "./data/jobs"
	apiKey  = ""

	images = map[string]string{
		"python":     "sandbox-python:3.12",
		"typescript": "sandbox-typescript:node22",
		"go":         "sandbox-go:1.23",
		"java":       "sandbox-java:21",
		"rust":       "sandbox-rust:1.78",
	}
	langOrder = []string{"python", "typescript", "go", "java", "rust"}
)

// ---------- Helpers ----------

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// cappedWriter truncates at max bytes, remembers truncation.
type cappedWriter struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func newCapped(max int) *cappedWriter {
	if max <= 0 {
		max = 256 * 1024
	}
	return &cappedWriter{max: max}
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	remaining := c.max - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil // discard
	}
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

func (c *cappedWriter) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// ---------- Validation ----------

func validPath(p string) error {
	if p == "" {
		return fmt.Errorf("empty path")
	}
	if len(p) > 256 {
		return fmt.Errorf("path too long: %s", p)
	}
	if strings.ContainsRune(p, 0) {
		return fmt.Errorf("path contains null byte")
	}
	if strings.Contains(p, "\\") {
		return fmt.Errorf("backslashes not allowed: %s", p)
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("absolute paths not allowed: %s", p)
	}
	if p == ".stdin" || strings.HasSuffix(p, "/.stdin") {
		return fmt.Errorf("reserved filename: %s", p)
	}
	parts := strings.Split(p, "/")
	if len(parts) > 5 {
		return fmt.Errorf("path nesting exceeds 5: %s", p)
	}
	for _, part := range parts {
		if part == ".." {
			return fmt.Errorf(".. not allowed: %s", p)
		}
		if part == "" {
			return fmt.Errorf("empty segment in path: %s", p)
		}
	}
	return nil
}

func validateExec(req *ExecRequest) (Limits, error) {
	if _, ok := images[req.Language]; !ok {
		return Limits{}, fmt.Errorf("unsupported language: %q", req.Language)
	}
	if len(req.Files) == 0 {
		return Limits{}, fmt.Errorf("files must not be empty")
	}
	if len(req.Files) > 20 {
		return Limits{}, fmt.Errorf("too many files (max 20)")
	}
	total := 0
	seen := map[string]bool{}
	for _, f := range req.Files {
		if err := validPath(f.Path); err != nil {
			return Limits{}, err
		}
		if seen[f.Path] {
			return Limits{}, fmt.Errorf("duplicate path: %s", f.Path)
		}
		seen[f.Path] = true
		total += len(f.Content)
	}
	if total > 512*1024 {
		return Limits{}, fmt.Errorf("total file size exceeds 512KB")
	}
	if len(req.Stdin) > 512*1024 {
		return Limits{}, fmt.Errorf("stdin exceeds 512KB")
	}
	if err := validPath(req.Entrypoint); err != nil {
		return Limits{}, fmt.Errorf("bad entrypoint: %v", err)
	}
	if !seen[req.Entrypoint] {
		return Limits{}, fmt.Errorf("entrypoint must match one of files[]")
	}
	lim := Limits{TimeoutMs: 10000, MemoryMb: 256, OutputKb: 256}
	if req.Limits != nil {
		if req.Limits.TimeoutMs != 0 {
			lim.TimeoutMs = req.Limits.TimeoutMs
		}
		if req.Limits.MemoryMb != 0 {
			lim.MemoryMb = req.Limits.MemoryMb
		}
		if req.Limits.OutputKb != 0 {
			lim.OutputKb = req.Limits.OutputKb
		}
	}
	if lim.TimeoutMs <= 0 || lim.TimeoutMs > 120000 {
		return Limits{}, fmt.Errorf("timeout_ms must be 1..120000")
	}
	if lim.MemoryMb < 64 || lim.MemoryMb > 512 {
		return Limits{}, fmt.Errorf("memory_mb must be 64..512")
	}
	if lim.OutputKb < 1 || lim.OutputKb > 1024 {
		return Limits{}, fmt.Errorf("output_kb must be 1..1024")
	}
	return lim, nil
}

// ---------- Persistence ----------

func saveLocked(j *Job) {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(dataDir, j.ID+".json.tmp")
	dst := filepath.Join(dataDir, j.ID+".json")
	_ = os.WriteFile(tmp, data, 0644)
	_ = os.Rename(tmp, dst)
}

func saveJob(j *Job) {
	mu.Lock()
	defer mu.Unlock()
	saveLocked(j)
}

func loadExisting() {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dataDir, e.Name()))
		if err != nil {
			continue
		}
		var j Job
		if err := json.Unmarshal(data, &j); err != nil {
			continue
		}
		// Any job left non-terminal across restart cannot be resumed safely.
		switch j.Status {
		case "queued", "running":
			j.Status = "cancelled"
			j.Error = "cancelled on server restart"
		}
		jobs[j.ID] = &j
		// Rewrite so restart state is durable.
		saveLocked(&j)
	}
}

// ---------- Middleware ----------

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func checkAuth(r *http.Request) bool {
	if apiKey == "" {
		return true
	}
	if r.Header.Get("X-API-Key") == apiKey {
		return true
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		if strings.TrimPrefix(auth, "Bearer ") == apiKey {
			return true
		}
	}
	return false
}

func checkRateN(r *http.Request, limit int) bool {
	ip := clientIP(r)
	now := time.Now()
	rateMu.Lock()
	defer rateMu.Unlock()
	key := ip + "|" + rateBucket(r)
	hits := rateHits[key]
	fresh := hits[:0]
	for _, t := range hits {
		if now.Sub(t) < time.Minute {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= limit {
		rateHits[key] = fresh
		return false
	}
	rateHits[key] = append(fresh, now)
	return true
}

// Separate buckets so bursty polling doesn't consume the expensive
// job-creation budget and vice versa.
func rateBucket(r *http.Request) string {
	if r.Method == http.MethodPost && r.URL.Path == "/v1/executions" {
		return "exec"
	}
	return "read"
}

// guardN enforces auth + a per-bucket rate limit.
// Job creation (POST /v1/executions) is the expensive op: 10/min/IP.
// Polling + reads: 300/min/IP.
func guardN(w http.ResponseWriter, r *http.Request) bool {
	if !checkAuth(r) {
		writeErr(w, http.StatusUnauthorized, "invalid or missing API key")
		return false
	}
	limit := 300
	msg := "rate limit exceeded (300 req/min/IP)"
	if r.Method == http.MethodPost && r.URL.Path == "/v1/executions" {
		limit = 10
		msg = "rate limit exceeded (10 exec/min/IP)"
	}
	if !checkRateN(r, limit) {
		writeErr(w, 429, msg)
		return false
	}
	return true
}

func guard(w http.ResponseWriter, r *http.Request) bool {
	return guardN(w, r)
}

// ---------- Handlers ----------

func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handleLanguages(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method not allowed")
		return
	}
	type lang struct {
		ID    string `json:"id"`
		Image string `json:"image"`
	}
	out := make([]lang, 0, len(langOrder))
	for _, id := range langOrder {
		out = append(out, lang{ID: id, Image: images[id]})
	}
	writeJSON(w, 200, map[string]any{"languages": out})
}

func handlePostExec(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20) // 2MB cap on request body
	var req ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	lim, err := validateExec(&req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	j := &Job{
		ID:         newID(),
		Language:   req.Language,
		Files:      req.Files,
		Entrypoint: req.Entrypoint,
		Stdin:      req.Stdin,
		Limits:     lim,
		Status:     "queued",
		CreatedAt:  time.Now().UTC(),
	}
	mu.Lock()
	jobs[j.ID] = j
	saveLocked(j)
	mu.Unlock()

	select {
	case queue <- j.ID:
	default:
		mu.Lock()
		j.Status = "failed"
		j.Error = "server overloaded, queue full"
		saveLocked(j)
		mu.Unlock()
		writeErr(w, 503, "server overloaded, queue full")
		return
	}
	writeJSON(w, 202, map[string]any{"id": j.ID, "status": j.Status})
}

func handleExecByID(w http.ResponseWriter, r *http.Request) {
	if !guard(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/executions/")
	if id == "" || strings.Contains(id, "/") {
		writeErr(w, 404, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		mu.Lock()
		j, ok := jobs[id]
		mu.Unlock()
		if !ok {
			writeErr(w, 404, "job not found")
			return
		}
		writeJSON(w, 200, j)
	case http.MethodDelete:
		mu.Lock()
		j, ok := jobs[id]
		if !ok {
			mu.Unlock()
			writeErr(w, 404, "job not found")
			return
		}
		switch j.Status {
		case "queued":
			j.Status = "cancelled"
			cancelReq[id] = true
			saveLocked(j)
			mu.Unlock()
			writeJSON(w, 200, j)
			return
		case "running":
			cancelReq[id] = true
			if cancel, ok := cancels[id]; ok {
				cancel()
			}
			saveLocked(j)
			mu.Unlock()
			writeJSON(w, 200, map[string]any{"id": id, "status": "running"})
			return
		default:
			mu.Unlock()
			writeJSON(w, 200, j)
			return
		}
	default:
		writeErr(w, 405, "method not allowed")
	}
}

// ---------- Worker ----------

func worker(n int) {
	for id := range queue {
		runJob(id)
	}
}

func runJob(id string) {
	mu.Lock()
	j, ok := jobs[id]
	if !ok {
		mu.Unlock()
		return
	}
	if j.Status != "queued" {
		mu.Unlock()
		return
	}
	j.Status = "running"
	saveLocked(j)
	limits := j.Limits
	mu.Unlock()

	baseCtx, baseCancel := context.WithCancel(context.Background())
	mu.Lock()
	cancels[id] = baseCancel
	mu.Unlock()
	defer func() {
		mu.Lock()
		delete(cancels, id)
		mu.Unlock()
		baseCancel()
	}()

	// Fast-path: cancelled between enqueue and start.
	mu.Lock()
	cancelledEarly := cancelReq[id]
	mu.Unlock()
	if cancelledEarly {
		mu.Lock()
		j.Status = "cancelled"
		saveLocked(j)
		mu.Unlock()
		return
	}

	workdir, err := os.MkdirTemp("", "job-*")
	if err != nil {
		mu.Lock()
		j.Status = "failed"
		j.Error = "mkdtemp: " + err.Error()
		saveLocked(j)
		mu.Unlock()
		return
	}
	defer os.RemoveAll(workdir)
	// MkdirTemp is 0700; the container runs as uid 65532 and mounts
	// this dir read-only, so it must be traversable.
	_ = os.Chmod(workdir, 0755)

	// Write files (paths already validated).
	for _, f := range j.Files {
		dst := filepath.Join(workdir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			mu.Lock()
			j.Status = "failed"
			j.Error = "mkdir: " + err.Error()
			saveLocked(j)
			mu.Unlock()
			return
		}
		if err := os.WriteFile(dst, []byte(f.Content), 0644); err != nil {
			mu.Lock()
			j.Status = "failed"
			j.Error = "write: " + err.Error()
			saveLocked(j)
			mu.Unlock()
			return
		}
	}
	if j.Stdin != "" {
		_ = os.WriteFile(filepath.Join(workdir, ".stdin"), []byte(j.Stdin), 0644)
	}

	image := images[j.Language]
	mem := fmt.Sprintf("%dm", limits.MemoryMb)
	args := []string{
		"run", "--rm", "-i",
		"--network", "none",
		"--user", "65532:65532",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges:true",
		"--memory", mem, "--memory-swap", mem,
		"--cpus", "1.0",
		"--pids-limit", "128",
		"--read-only",
		"--tmpfs", "/tmp:rw,size=128m,exec",
		"-v", workdir + ":/box:ro",
		image, "/runner/run.sh", "/box", j.Entrypoint,
	}
	if j.Stdin != "" {
		args = append(args, "/box/.stdin")
	}

	timeout := time.Duration(limits.TimeoutMs) * time.Millisecond
	ctx, cancelTimeout := context.WithTimeout(baseCtx, timeout)
	defer cancelTimeout()

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = strings.NewReader(j.Stdin)
	maxOut := limits.OutputKb * 1024
	outW := newCapped(maxOut)
	errW := newCapped(maxOut)
	cmd.Stdout = outW
	cmd.Stderr = errW

	start := time.Now()
	runErr := cmd.Run()
	wallMs := time.Since(start).Milliseconds()

	exitCode := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			// e.g. context deadline / killed, docker missing
			exitCode = -1
		}
	}

	stdout := outW.String()
	stderr := errW.String()
	truncated := outW.truncated || errW.truncated
	if v := ctx.Err(); v == context.DeadlineExceeded {
		// ensure timeout flag even if docker already exited 137 etc.
	}

	mu.Lock()
	defer mu.Unlock()
	// Re-fetch in case of restart edge; j pointer is still valid.
	if cancelReq[id] || baseCtx.Err() == context.Canceled {
		ec := exitCode
		j.ExitCode = &ec
		j.Stdout = stdout
		j.Stderr = stderr
		j.Truncated = truncated
		j.TimedOut = false
		j.Status = "cancelled"
		j.Usage = map[string]any{"wall_ms": wallMs}
		delete(cancelReq, id)
		saveLocked(j)
		return
	}
	if ctx.Err() == context.DeadlineExceeded {
		ec := exitCode
		j.ExitCode = &ec
		j.Stdout = stdout
		j.Stderr = stderr
		j.Truncated = truncated
		j.TimedOut = true
		j.Status = "timeout"
		j.Usage = map[string]any{"wall_ms": wallMs}
		saveLocked(j)
		return
	}
	ec := exitCode
	j.ExitCode = &ec
	j.Stdout = stdout
	j.Stderr = stderr
	j.Truncated = truncated
	j.TimedOut = false
	j.Usage = map[string]any{"wall_ms": wallMs}
	switch {
	case exitCode == 137:
		j.Status = "oom"
	case exitCode == 0:
		j.Status = "succeeded"
	default:
		j.Status = "failed"
	}
	saveLocked(j)
}

// ---------- Main ----------

func main() {
	if v := os.Getenv("PORT"); v != "" {
		// handled below
		_ = v
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if d := os.Getenv("DATA_DIR"); d != "" {
		dataDir = d
	}
	apiKey = os.Getenv("API_KEY")
	workers := 4
	if v := os.Getenv("WORKERS"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n >= 1 && n <= 32 {
			workers = n
		}
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("mkdir data dir: %v", err)
	}
	mu.Lock()
	loadExisting()
	mu.Unlock()

	for i := 0; i < workers; i++ {
		go worker(i)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/v1/languages", handleLanguages)
	mux.HandleFunc("/v1/executions", handlePostExec)
	mux.HandleFunc("/v1/executions/", handleExecByID)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	log.Printf("sandbox-mvp listening on :%s (workers=%d, data=%s)", port, workers, dataDir)
	log.Fatal(srv.ListenAndServe())
}
