// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memS3Server struct {
	mu           sync.Mutex
	objects      map[string][]byte
	gets         atomic.Int64
	puts         atomic.Int64
	lastAuth     string
	forceStatus  atomic.Int32
	corruptBytes atomic.Bool
}

func newMemS3Server(t *testing.T) (*memS3Server, *httptest.Server) {
	t.Helper()
	m := &memS3Server{objects: make(map[string][]byte)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code := m.forceStatus.Load(); code != 0 {
			w.WriteHeader(int(code))
			return
		}
		m.mu.Lock()
		m.lastAuth = r.Header.Get("Authorization")
		m.mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			m.puts.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			m.mu.Lock()
			m.objects[r.URL.Path] = body
			m.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			m.gets.Add(1)
			m.mu.Lock()
			data, ok := m.objects[r.URL.Path]
			m.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			out := append([]byte(nil), data...)
			if m.corruptBytes.Load() && len(out) > envelopeHeaderSz {
				out[len(out)-1] ^= 0xff
			}
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(out)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return m, srv
}

type protocolClient struct {
	t      *testing.T
	inW    io.WriteCloser
	outDec *json.Decoder
	done   chan error
}

func startTestServer(t *testing.T, srv *Server) *protocolClient {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := srv.Serve(context.Background(), inR, outW)
		_ = outW.Close()
		done <- err
	}()
	c := &protocolClient{
		t:      t,
		inW:    inW,
		outDec: json.NewDecoder(bufio.NewReader(outR)),
		done:   done,
	}
	var caps Response
	if err := c.outDec.Decode(&caps); err != nil {
		t.Fatalf("read initial capabilities: %v", err)
	}
	if caps.ID != 0 || len(caps.KnownCommands) != 3 {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	return c
}

func (c *protocolClient) roundTrip(req Request, body []byte) Response {
	c.t.Helper()
	line, err := json.Marshal(req)
	if err != nil {
		c.t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.Write(line)
	buf.WriteByte('\n')
	if req.Command == CmdPut && req.BodySize > 0 {
		buf.WriteByte('"')
		buf.WriteString(base64.StdEncoding.EncodeToString(body))
		buf.WriteString("\"\n")
	}
	if _, err := c.inW.Write(buf.Bytes()); err != nil {
		c.t.Fatal(err)
	}
	var resp Response
	if err := c.outDec.Decode(&resp); err != nil {
		c.t.Fatalf("decode response for %d: %v", req.ID, err)
	}
	if resp.ID != req.ID {
		c.t.Fatalf("response ID = %d, want %d", resp.ID, req.ID)
	}
	return resp
}

func (c *protocolClient) close() {
	c.t.Helper()
	_ = c.roundTrip(Request{ID: 999, Command: CmdClose}, nil)
	_ = c.inW.Close()
	if err := <-c.done; err != nil {
		c.t.Fatalf("server returned error on close: %v", err)
	}
}

func TestRemoteReadWriteAndCrossRunnerReadOnlyHit(t *testing.T) {
	mem, httpSrv := newMemS3Server(t)

	action := sha256.Sum256([]byte("action-1"))
	payload := []byte("compiled-go-object-bytes")
	output := sha256.Sum256(payload)

	// Writer (main/cache-warm): uploads to mock S3.
	writerBackend, err := NewS3Backend(S3Config{
		Bucket:          "pig-cache",
		Endpoint:        httpSrv.URL,
		Region:          "auto",
		Prefix:          "v1/linux-amd64",
		AccessKeyID:     "AKIA_TEST",
		SecretAccessKey: "SECRET_TEST",
		ReadOnly:        false,
	})
	if err != nil {
		t.Fatal(err)
	}
	writerSrv, err := NewServer(ServerConfig{DiskDir: t.TempDir(), Remote: writerBackend, ReadOnly: false})
	if err != nil {
		t.Fatal(err)
	}
	wc := startTestServer(t, writerSrv)

	missResp := wc.roundTrip(Request{ID: 1, Command: CmdGet, ActionID: action[:]}, nil)
	if !missResp.Miss {
		t.Fatalf("expected initial miss, got %+v", missResp)
	}
	putResp := wc.roundTrip(Request{
		ID:       2,
		Command:  CmdPut,
		ActionID: action[:],
		OutputID: output[:],
		BodySize: int64(len(payload)),
	}, payload)
	if putResp.Err != "" || putResp.DiskPath == "" {
		t.Fatalf("put failed: %+v", putResp)
	}
	wc.close()

	if mem.puts.Load() != 1 {
		t.Fatalf("expected 1 remote PUT, got %d", mem.puts.Load())
	}
	mem.mu.Lock()
	auth := mem.lastAuth
	mem.mu.Unlock()
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIA_TEST/") {
		t.Fatalf("missing SigV4 Authorization header: %q", auth)
	}

	// Reader (PR runner): fresh local disk + readonly mode.
	// Must hit remote for action-1, AND MUST NEVER upload new entries on PUT.
	readerBackend, err := NewS3Backend(S3Config{
		Bucket:          "pig-cache",
		Endpoint:        httpSrv.URL,
		Region:          "auto",
		Prefix:          "v1/linux-amd64",
		AccessKeyID:     "AKIA_TEST",
		SecretAccessKey: "SECRET_TEST",
		ReadOnly:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	readerSrv, err := NewServer(ServerConfig{DiskDir: t.TempDir(), Remote: readerBackend, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	rc := startTestServer(t, readerSrv)

	hitResp := rc.roundTrip(Request{ID: 1, Command: CmdGet, ActionID: action[:]}, nil)
	if hitResp.Miss || hitResp.Err != "" {
		t.Fatalf("expected remote hit, got %+v", hitResp)
	}
	if !bytes.Equal(hitResp.OutputID, output[:]) || hitResp.Size != int64(len(payload)) {
		t.Fatalf("hit metadata mismatch: %+v", hitResp)
	}
	gotBytes, err := os.ReadFile(hitResp.DiskPath)
	if err != nil || !bytes.Equal(gotBytes, payload) {
		t.Fatalf("disk file mismatch: %q (%v)", gotBytes, err)
	}

	// PR tries to PUT a new action: succeeds locally on disk, ZERO remote PUTs.
	action2 := sha256.Sum256([]byte("pr-untrusted-action"))
	payload2 := []byte("pr-bytes")
	output2 := sha256.Sum256(payload2)
	prPut := rc.roundTrip(Request{
		ID:       2,
		Command:  CmdPut,
		ActionID: action2[:],
		OutputID: output2[:],
		BodySize: int64(len(payload2)),
	}, payload2)
	if prPut.Err != "" || prPut.DiskPath == "" {
		t.Fatalf("local disk put in readonly mode should succeed: %+v", prPut)
	}
	rc.close()

	if mem.puts.Load() != 1 {
		t.Fatalf("readonly PR runner performed a remote PUT (cache poisoning violation): puts=%d", mem.puts.Load())
	}
	st := readerSrv.Stats()
	if st.RemoteHits != 1 || st.RemotePuts != 0 || st.Puts != 1 {
		t.Fatalf("unexpected reader stats: %+v", st)
	}
}

func TestCorruptRemoteObjectRejectedAndUnreachableBucketFallsBackToLocal(t *testing.T) {
	mem, httpSrv := newMemS3Server(t)

	action := sha256.Sum256([]byte("corrupt-action"))
	payload := []byte("expected-bytes")
	output := sha256.Sum256(payload)
	framed, err := encodeEnvelope(output[:], payload)
	if err != nil {
		t.Fatal(err)
	}
	key := "/pig-cache/v1/" + hex.EncodeToString(action[:])[:2] + "/" + hex.EncodeToString(action[:])
	mem.mu.Lock()
	mem.objects[key] = framed
	mem.mu.Unlock()
	mem.corruptBytes.Store(true)

	backend, err := NewS3Backend(S3Config{
		Bucket:               "pig-cache",
		Endpoint:             httpSrv.URL,
		Region:               "auto",
		Prefix:               "v1",
		AccessKeyID:          "AKIA",
		SecretAccessKey:      "SECRET",
		ReadOnly:             false,
		MaxConsecutiveErrors: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(ServerConfig{DiskDir: t.TempDir(), Remote: backend})
	if err != nil {
		t.Fatal(err)
	}
	c := startTestServer(t, srv)

	// Corrupt payload must be rejected (treated as cache miss, never returned as hit).
	resp := c.roundTrip(Request{ID: 1, Command: CmdGet, ActionID: action[:]}, nil)
	if !resp.Miss || resp.Err != "" {
		t.Fatalf("corrupt remote object must be treated as miss without failing build, got %+v", resp)
	}

	// Simulate remote outage (HTTP 503): must fail open to local disk and trip circuit breaker.
	mem.forceStatus.Store(http.StatusServiceUnavailable)
	for i := int64(2); i <= 5; i++ {
		r := c.roundTrip(Request{ID: i, Command: CmdGet, ActionID: action[:]}, nil)
		if !r.Miss || r.Err != "" {
			t.Fatalf("outage get %d failed build instead of missing cleanly: %+v", i, r)
		}
	}
	if !backend.CircuitOpen() {
		t.Fatal("expected circuit breaker to open after consecutive remote 503 errors")
	}
	// Local PUT/GET must continue working seamlessly while remote is down.
	pResp := c.roundTrip(Request{
		ID:       6,
		Command:  CmdPut,
		ActionID: action[:],
		OutputID: output[:],
		BodySize: int64(len(payload)),
	}, payload)
	if pResp.Err != "" || pResp.DiskPath == "" {
		t.Fatalf("local put failed during remote outage: %+v", pResp)
	}
	gResp := c.roundTrip(Request{ID: 7, Command: CmdGet, ActionID: action[:]}, nil)
	if gResp.Miss || gResp.Err != "" {
		t.Fatalf("local hit failed during remote outage: %+v", gResp)
	}
	c.close()
}

// End-to-end verification with the real `go` toolchain speaking GOCACHEPROG to our binary.
func TestRealGoToolchainUsesGOCACHEPROG(t *testing.T) {
	_, httpSrv := newMemS3Server(t)

	binPath := filepath.Join(t.TempDir(), "pig-gocacheprog")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Env = append(os.Environ(), "GOCACHEPROG=")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build gocacheprog binary: %v\n%s", err, out)
	}

	// Create a tiny Go module to compile & test through our GOCACHEPROG binary.
	modDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(modDir, "go.mod"), []byte("module example.com/e2e\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "a_test.go"), []byte("package e2e\nimport \"testing\"\nfunc TestOK(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run 1 (writer): warms remote S3 mock.
	runGo := func(diskDir string, readOnly string) string {
		t.Helper()
		cmd := exec.Command("go", "test", ".")
		cmd.Dir = modDir
		cmd.Env = append(os.Environ(),
			"GOCACHEPROG="+binPath,
			"PIG_GOCACHE_DISK_DIR="+diskDir,
			"PIG_GOCACHE_BUCKET=pig-e2e",
			"PIG_GOCACHE_ENDPOINT="+httpSrv.URL,
			"PIG_GOCACHE_REGION=auto",
			"PIG_GOCACHE_PREFIX=v1/e2e",
			"PIG_GOCACHE_ACCESS_KEY_ID=AKIA_E2E",
			"PIG_GOCACHE_SECRET_ACCESS_KEY=SECRET_E2E",
			"PIG_GOCACHE_READONLY="+readOnly,
			"PIG_GOCACHE_VERBOSE=1",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go test (readonly=%s): %v\n%s", readOnly, err, out)
		}
		return string(out)
	}

	out1 := runGo(t.TempDir(), "0")
	if strings.Contains(out1, "(cached)") {
		t.Fatalf("cold run unexpectedly reported cached:\n%s", out1)
	}

	// Run 2 (fresh empty local disk, readonly PR mode): MUST get (cached) from remote S3 mock!
	out2 := runGo(t.TempDir(), "1")
	if !strings.Contains(out2, "(cached)") {
		t.Fatalf("second run with fresh disk did not hit remote GOCACHEPROG test cache:\n%s", out2)
	}
}

func TestSetupScriptFallbackAndPoisoningGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper test runs on Unix")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repoRoot, "automation", "ci", "setup-gocacheprog.sh")

	runWrapper := func(t *testing.T, extraEnv ...string) (envFileText, outFileText, stderr string) {
		t.Helper()
		tmp := t.TempDir()
		envFile := filepath.Join(tmp, "github.env")
		outFile := filepath.Join(tmp, "github.out")
		cmd := exec.Command("bash", script, "--github-env", envFile, "--github-output", outFile, "--bin-dir", filepath.Join(tmp, "bin"), "--disk-dir", filepath.Join(tmp, "disk"))
		cmd.Dir = repoRoot
		// Start from clean environment without ambient PIG_GOCACHE_* / AWS_* / GITHUB_* vars.
		var cleanEnv []string
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			if strings.HasPrefix(k, "PIG_GOCACHE") || strings.HasPrefix(k, "AWS_") || strings.HasPrefix(k, "GITHUB_") || k == "GOCACHEPROG" {
				continue
			}
			cleanEnv = append(cleanEnv, kv)
		}
		cleanEnv = append(cleanEnv, extraEnv...)
		cmd.Env = cleanEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("setup-gocacheprog.sh failed: %v\n%s", err, out)
		}
		eb, _ := os.ReadFile(envFile)
		ob, _ := os.ReadFile(outFile)
		return string(eb), string(ob), string(out)
	}

	t.Run("missing bucket falls back cleanly to local disk", func(t *testing.T) {
		envText, outText, _ := runWrapper(t)
		if strings.Contains(envText, "GOCACHEPROG=") {
			t.Fatalf("expected GOCACHEPROG unset on fallback, got:\n%s", envText)
		}
		if !strings.Contains(outText, "enabled=false") || !strings.Contains(outText, "mode=local-fallback") {
			t.Fatalf("unexpected outputs:\n%s", outText)
		}
	})

	t.Run("fork PR with bucket but no credentials falls back cleanly", func(t *testing.T) {
		envText, outText, _ := runWrapper(t,
			"PIG_GOCACHE_BUCKET=pig-ci-cache",
			"GITHUB_EVENT_NAME=pull_request",
		)
		if strings.Contains(envText, "GOCACHEPROG=") {
			t.Fatalf("expected GOCACHEPROG unset without credentials, got:\n%s", envText)
		}
		if !strings.Contains(outText, "enabled=false") {
			t.Fatalf("expected enabled=false, got:\n%s", outText)
		}
	})

	t.Run("PR forces readonly even if mode=readwrite requested", func(t *testing.T) {
		envText, outText, _ := runWrapper(t,
			"PIG_GOCACHE_BUCKET=pig-ci-cache",
			"PIG_GOCACHE_ACCESS_KEY_ID=AKIA",
			"PIG_GOCACHE_SECRET_ACCESS_KEY=SECRET",
			"PIG_GOCACHE_MODE=readwrite",
			"GITHUB_EVENT_NAME=pull_request",
			"GITHUB_REF=refs/pull/123/merge",
		)
		if !strings.Contains(envText, "GOCACHEPROG=") || !strings.Contains(envText, "PIG_GOCACHE_READONLY=1") {
			t.Fatalf("PR must force readonly GOCACHEPROG, got env:\n%s", envText)
		}
		if !strings.Contains(outText, "mode=remote-readonly") {
			t.Fatalf("expected remote-readonly output, got:\n%s", outText)
		}
	})

	t.Run("main push auto mode enables readwrite", func(t *testing.T) {
		envText, outText, _ := runWrapper(t,
			"PIG_GOCACHE_BUCKET=pig-ci-cache",
			"PIG_GOCACHE_ACCESS_KEY_ID=AKIA",
			"PIG_GOCACHE_SECRET_ACCESS_KEY=SECRET",
			"PIG_GOCACHE_MODE=auto",
			"GITHUB_EVENT_NAME=push",
			"GITHUB_REF=refs/heads/main",
		)
		if !strings.Contains(envText, "GOCACHEPROG=") || !strings.Contains(envText, "PIG_GOCACHE_READONLY=0") {
			t.Fatalf("main push should enable readwrite, got env:\n%s", envText)
		}
		if !strings.Contains(outText, "mode=remote-readwrite") {
			t.Fatalf("expected remote-readwrite output, got:\n%s", outText)
		}
	})
}
