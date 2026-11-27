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

	t.Run("missing bucket falls back cleanly to local disk and clears GOCACHEPROG", func(t *testing.T) {
		envText, outText, _ := runWrapper(t)
		if strings.TrimSpace(envText) != "GOCACHEPROG=" {
			t.Fatalf("expected fallback to clear GOCACHEPROG= in GITHUB_ENV, got:\n%s", envText)
		}
		if !strings.Contains(outText, "enabled=false") || !strings.Contains(outText, "mode=local-fallback") {
			t.Fatalf("unexpected outputs:\n%s", outText)
		}
	})

	t.Run("fork PR with bucket but no credentials falls back cleanly and clears GOCACHEPROG", func(t *testing.T) {
		envText, outText, _ := runWrapper(t,
			"PIG_GOCACHE_BUCKET=pig-ci-cache",
			"GITHUB_EVENT_NAME=pull_request",
		)
		if strings.TrimSpace(envText) != "GOCACHEPROG=" {
			t.Fatalf("expected fallback to clear GOCACHEPROG= without credentials, got:\n%s", envText)
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

func TestSelfCheckProbesDiskWritability(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0555 directory read-only check runs on Unix")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses POSIX read-only directory permissions")
	}
	readOnlyDir := filepath.Join(t.TempDir(), "ro-cache")
	if err := os.MkdirAll(filepath.Join(readOnlyDir, "obj"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(readOnlyDir, "obj"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readOnlyDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(readOnlyDir, 0o755)
		_ = os.Chmod(filepath.Join(readOnlyDir, "obj"), 0o755)
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{"-self-check", "-disk-dir", readOnlyDir}, strings.NewReader(""), &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit when disk-dir is not writable, got 0 (stdout=%q stderr=%q)", stdout.String(), stderr.String())
	}

	// Writable directory passes self-check and leaves no probe file behind.
	writableDir := t.TempDir()
	stdout.Reset()
	stderr.Reset()
	code = run([]string{"-self-check", "-disk-dir", writableDir}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 || !strings.HasPrefix(stdout.String(), "ok ") {
		t.Fatalf("expected ok self-check on writable dir, got code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	entries, err := os.ReadDir(filepath.Join(writableDir, "obj"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("self-check left probe files behind: %v", entries)
	}
}

func TestWriteLocalRepairsCorruptExistingObjectOnRemoteHit(t *testing.T) {
	mem, httpSrv := newMemS3Server(t)

	action := sha256.Sum256([]byte("repair-corrupt-action"))
	payload := []byte("full-valid-compiled-artifact-payload")
	output := sha256.Sum256(payload)
	framed, err := encodeEnvelope(output[:], payload)
	if err != nil {
		t.Fatal(err)
	}
	key := "/pig-cache/v1/" + hex.EncodeToString(action[:])[:2] + "/" + hex.EncodeToString(action[:])
	mem.mu.Lock()
	mem.objects[key] = framed
	mem.mu.Unlock()

	backend, err := NewS3Backend(S3Config{
		Bucket:          "pig-cache",
		Endpoint:        httpSrv.URL,
		Region:          "auto",
		Prefix:          "v1",
		AccessKeyID:     "AKIA",
		SecretAccessKey: "SECRET",
		ReadOnly:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	diskDir := t.TempDir()
	srv, err := NewServer(ServerConfig{DiskDir: diskDir, Remote: backend, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}

	// Seed a truncated/corrupt local object AND matching meta with the true size,
	// so handleGet misses on size and fetches the verified bytes from remote.
	objPath := srv.objectPath(hex.EncodeToString(output[:]))
	if err := os.MkdirAll(filepath.Dir(objPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(objPath, []byte("trunc"), 0o644); err != nil {
		t.Fatal(err)
	}
	metaPath, _ := srv.entryPaths(hex.EncodeToString(action[:]))
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	metaJSON, _ := json.Marshal(localMeta{OutputID: hex.EncodeToString(output[:]), Size: int64(len(payload)), TimeUnix: time.Now().Unix()})
	if err := os.WriteFile(metaPath, metaJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	c := startTestServer(t, srv)
	resp := c.roundTrip(Request{ID: 1, Command: CmdGet, ActionID: action[:]}, nil)
	if resp.Miss || resp.Err != "" {
		t.Fatalf("expected repaired remote hit, got %+v", resp)
	}
	got, err := os.ReadFile(resp.DiskPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("local object was not repaired: got %q, want %q", got, payload)
	}
	c.close()
}

func TestPruneDiskStoreBoundsSizeAndPreservesLivePaths(t *testing.T) {
	diskDir := t.TempDir()
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	srv, err := NewServer(ServerConfig{
		DiskDir:      diskDir,
		MaxDiskBytes: 256,
		MaxDiskAge:   24 * time.Hour,
		LiveGrace:    time.Hour,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	// Seed an old entry (modified 3 days ago) that exceeds both age and size budget.
	oldAction := sha256.Sum256([]byte("old-action"))
	oldBody := bytes.Repeat([]byte("A"), 200)
	oldOut := sha256.Sum256(oldBody)
	oldObjPath, err := srv.writeLocal(hex.EncodeToString(oldAction[:]), oldOut[:], oldBody, now.Add(-72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	oldMetaPath, _ := srv.entryPaths(hex.EncodeToString(oldAction[:]))
	// Unpin and backdate the old files as if written by a prior process days ago.
	srv.mu.Lock()
	delete(srv.inUseObj, oldObjPath)
	delete(srv.inUseObj, oldMetaPath)
	srv.mu.Unlock()
	oldTime := now.Add(-72 * time.Hour)
	if err := os.Chtimes(oldObjPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldMetaPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	// Write a fresh entry through the live protocol; its DiskPath is pinned and must survive close pruning.
	c := startTestServer(t, srv)
	liveAction := sha256.Sum256([]byte("live-action"))
	liveBody := bytes.Repeat([]byte("B"), 200)
	liveOut := sha256.Sum256(liveBody)
	putResp := c.roundTrip(Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: liveAction[:],
		OutputID: liveOut[:],
		BodySize: int64(len(liveBody)),
	}, liveBody)
	if putResp.Err != "" {
		t.Fatalf("live put failed: %+v", putResp)
	}
	c.close()

	// Old entry (both meta and object) must be evicted.
	if _, err := os.Stat(oldMetaPath); !os.IsNotExist(err) {
		t.Errorf("expected old meta %s to be evicted, stat err=%v", oldMetaPath, err)
	}
	if _, err := os.Stat(oldObjPath); !os.IsNotExist(err) {
		t.Errorf("expected old object %s to be evicted, stat err=%v", oldObjPath, err)
	}
	// Live DiskPath must be preserved.
	if _, err := os.Stat(putResp.DiskPath); err != nil {
		t.Errorf("expected live DiskPath %s to remain intact, stat err=%v", putResp.DiskPath, err)
	}
}

func TestS3GetRejectsOversizedStreamWithoutDraining(t *testing.T) {
	const (
		chunkSize  = 4096
		totalChunk = 200
		fullStream = chunkSize * totalChunk // 819,200 bytes
	)
	var bytesWritten atomic.Int64
	handlerDone := make(chan struct{})
	srvHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		// Stream chunked response (no Content-Length) much larger than MaxObjectBytes.
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		chunk := bytes.Repeat([]byte("X"), chunkSize)
		for range totalChunk {
			n, err := w.Write(chunk)
			bytesWritten.Add(int64(n))
			if err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			// Yield briefly so TCP backpressure propagates when the client closes early.
			time.Sleep(1 * time.Millisecond)
		}
	}))
	t.Cleanup(srvHTTP.Close)

	backend, err := NewS3Backend(S3Config{
		Bucket:          "pig-cache",
		Endpoint:        srvHTTP.URL,
		Region:          "auto",
		AccessKeyID:     "AKIA",
		SecretAccessKey: "SECRET",
		ReadOnly:        true,
		MaxObjectBytes:  1024, // tiny limit
	})
	if err != nil {
		t.Fatal(err)
	}

	action := sha256.Sum256([]byte("oversized"))
	outID, body, _, hit, err := backend.Get(context.Background(), hex.EncodeToString(action[:]))
	if err != nil {
		t.Fatalf("oversized object should be treated as miss, not error: %v", err)
	}
	if hit || outID != nil || body != nil {
		t.Fatalf("expected cache miss for oversized object, got hit=%v len=%d", hit, len(body))
	}
	if backend.CircuitOpen() {
		t.Fatalf("oversized miss must not trip the circuit breaker")
	}
	// Wait for the server handler to observe the closed connection and exit.
	<-handlerDone
	// Verify the client closed the connection without draining the full 819,200-byte stream
	// (and without draining the 64 KiB keep-alive drain budget).
	const maxExpectedBytes = 1024 + int64(envelopeHeaderSz) + 32*1024
	if written := bytesWritten.Load(); written >= fullStream || written > maxExpectedBytes {
		t.Fatalf("client drained too much of oversized stream: server wrote %d bytes (limit %d, full %d)", written, maxExpectedBytes, fullStream)
	}
}

func TestSetupScriptEnableThenLocalClearsEnvAndQuotesSpacePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper test runs on Unix")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repoRoot, "automation", "ci", "setup-gocacheprog.sh")
	tmp := t.TempDir()
	envFile := filepath.Join(tmp, "github.env")
	outFile := filepath.Join(tmp, "github.out")
	// Use a bin-dir containing spaces to test Go quoted.Split quoting.
	binDirWithSpaces := filepath.Join(tmp, "runner temp with spaces", "bin")
	diskDir := filepath.Join(tmp, "runner temp with spaces", "disk")

	baseCleanEnv := func() []string {
		var env []string
		for _, kv := range os.Environ() {
			k, _, _ := strings.Cut(kv, "=")
			if strings.HasPrefix(k, "PIG_GOCACHE") || strings.HasPrefix(k, "AWS_") || strings.HasPrefix(k, "GITHUB_") || k == "GOCACHEPROG" {
				continue
			}
			env = append(env, kv)
		}
		return env
	}

	// Step 1: Enable remote cache into a path containing spaces.
	cmd1 := exec.Command("bash", script,
		"--github-env", envFile,
		"--github-output", outFile,
		"--bin-dir", binDirWithSpaces,
		"--disk-dir", diskDir,
		"--print-env",
	)
	cmd1.Dir = repoRoot
	cmd1.Env = append(baseCleanEnv(),
		"PIG_GOCACHE_BUCKET=pig-ci-cache",
		"PIG_GOCACHE_ACCESS_KEY_ID=AKIA",
		"PIG_GOCACHE_SECRET_ACCESS_KEY=SECRET",
		"PIG_GOCACHE_MODE=readonly",
		"GITHUB_EVENT_NAME=pull_request",
	)
	out1, err := cmd1.CombinedOutput()
	if err != nil {
		t.Fatalf("enable step failed: %v\n%s", err, out1)
	}
	env1, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	out1Text, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	expectedRawBin := filepath.Join(binDirWithSpaces, "pig-gocacheprog")
	if !strings.Contains(string(env1), "GOCACHEPROG='"+expectedRawBin+"'") {
		t.Fatalf("expected single-quoted GOCACHEPROG for path with spaces, got env:\n%s", env1)
	}
	if !strings.Contains(string(out1Text), "gocacheprog="+expectedRawBin+"\n") {
		t.Fatalf("expected raw unquoted gocacheprog action output, got:\n%s", out1Text)
	}

	// Verify real `go env` / `go test` accepts the quoted GOCACHEPROG with spaces!
	var exportedProg string
	for line := range strings.SplitSeq(string(env1), "\n") {
		if val, ok := strings.CutPrefix(line, "GOCACHEPROG="); ok {
			exportedProg = val
		}
	}
	modDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(modDir, "go.mod"), []byte("module example.com/spacetest\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "s_test.go"), []byte("package spacetest\nimport \"testing\"\nfunc TestS(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goCmd := exec.Command("go", "test", ".")
	goCmd.Dir = modDir
	goCmd.Env = append(baseCleanEnv(),
		"GOCACHEPROG="+exportedProg,
		"PIG_GOCACHE_DISK_DIR="+diskDir,
	)
	if goOut, err := goCmd.CombinedOutput(); err != nil {
		t.Fatalf("go test failed with quoted space path GOCACHEPROG=%s: %v\n%s", exportedProg, err, goOut)
	}

	// Step 2: Re-run with --mode local using the same GITHUB_ENV file and --print-env.
	// Must clear previously exported GOCACHEPROG in GITHUB_ENV and emit `unset GOCACHEPROG`.
	cmd2 := exec.Command("bash", script,
		"--mode", "local",
		"--github-env", envFile,
		"--github-output", outFile,
		"--print-env",
	)
	cmd2.Dir = repoRoot
	cmd2.Env = append(baseCleanEnv(), "GOCACHEPROG="+exportedProg)
	out2, err := cmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("local fallback step failed: %v\n%s", err, out2)
	}
	if !strings.Contains(string(out2), "unset GOCACHEPROG") {
		t.Fatalf("expected --print-env on local fallback to emit unset GOCACHEPROG, got:\n%s", out2)
	}
	env2, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	// Last GOCACHEPROG assignment in GITHUB_ENV must be empty ("GOCACHEPROG=").
	lastVal := "<unset>"
	for line := range strings.SplitSeq(string(env2), "\n") {
		if val, ok := strings.CutPrefix(line, "GOCACHEPROG="); ok {
			lastVal = val
		}
	}
	if lastVal != "" {
		t.Fatalf("expected last GITHUB_ENV entry for GOCACHEPROG to be cleared empty, got %q\nfull env:\n%s", lastVal, env2)
	}
}
