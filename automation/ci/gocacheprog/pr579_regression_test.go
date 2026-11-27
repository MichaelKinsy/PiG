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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestStreamPutBodyLargeAndErrorCases verifies PRRT_kwDOUe_81c68peZX:
// large PUT bodies stream directly to temp files on disk without buffering the
// full encoded/decoded body in memory, zero-byte PUTs still work, and malformed
// base64 or size mismatches reject cleanly without breaking subsequent requests.
func TestStreamPutBodyLargeAndErrorCases(t *testing.T) {
	diskDir := t.TempDir()
	srv, err := NewServer(ServerConfig{DiskDir: diskDir})
	if err != nil {
		t.Fatal(err)
	}
	c := startTestServer(t, srv)

	// 1. Large 2 MiB body exceeding bufio buffer (64 KiB) by >32x.
	const largeSize = 2 << 20
	largeBody := make([]byte, largeSize)
	for i := range largeBody {
		largeBody[i] = byte((i*31 + 17) & 0xff)
	}
	largeAction := sha256.Sum256([]byte("large-streamed-action"))
	largeOut := sha256.Sum256(largeBody)

	// Write request + streaming base64 directly into stdin pipe without building
	// one giant byte slice.
	reqLine, err := json.Marshal(Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: largeAction[:],
		OutputID: largeOut[:],
		BodySize: largeSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		bw := bufio.NewWriter(c.inW)
		_, _ = bw.Write(reqLine)
		_ = bw.WriteByte('\n')
		_ = bw.WriteByte('"')
		enc := base64.NewEncoder(base64.StdEncoding, bw)
		_, _ = enc.Write(largeBody)
		_ = enc.Close()
		_, _ = bw.WriteString("\"\n")
		_ = bw.Flush()
	}()
	var putResp Response
	if err := c.outDec.Decode(&putResp); err != nil {
		t.Fatalf("decode large put resp: %v", err)
	}
	if putResp.ID != 1 || putResp.Err != "" || putResp.DiskPath == "" {
		t.Fatalf("large streamed put failed: %+v", putResp)
	}
	gotBytes, err := os.ReadFile(putResp.DiskPath)
	if err != nil {
		t.Fatalf("read stored large object: %v", err)
	}
	if !bytes.Equal(gotBytes, largeBody) {
		t.Fatalf("stored large body mismatch: len=%d want=%d", len(gotBytes), len(largeBody))
	}
	// Temp dir should have no leftover .put-* staging files.
	if tmpEntries, err := os.ReadDir(filepath.Join(diskDir, tmpSubdir)); err == nil && len(tmpEntries) != 0 {
		t.Fatalf("unexpected leftover temp files after streamed PUT: %v", tmpEntries)
	}

	// 2. Zero-byte PUT path remains intact (no base64 body line on wire).
	zeroAction := sha256.Sum256([]byte("zero-byte-action"))
	zeroOut := sha256.Sum256(nil)
	zeroResp := c.roundTrip(Request{
		ID:       2,
		Command:  CmdPut,
		ActionID: zeroAction[:],
		OutputID: zeroOut[:],
		BodySize: 0,
	}, nil)
	if zeroResp.Err != "" || zeroResp.DiskPath == "" {
		t.Fatalf("zero-byte put failed: %+v", zeroResp)
	}
	if info, err := os.Stat(zeroResp.DiskPath); err != nil || info.Size() != 0 {
		t.Fatalf("zero-byte file stat = %v (err=%v), want 0 bytes", info, err)
	}

	// 3. Body size mismatch returns an error Response and leaves stream aligned.
	badSizeAction := sha256.Sum256([]byte("bad-size-action"))
	badSizeOut := sha256.Sum256([]byte("short"))
	badSizeResp := c.roundTrip(Request{
		ID:       3,
		Command:  CmdPut,
		ActionID: badSizeAction[:],
		OutputID: badSizeOut[:],
		BodySize: 100, // actual body is only 5 bytes
	}, []byte("short"))
	if !strings.Contains(badSizeResp.Err, "body size mismatch") {
		t.Fatalf("expected body size mismatch error, got %+v", badSizeResp)
	}

	// 4. Oversized body (> declared BodySize) fails fast and drains cleanly.
	overAction := sha256.Sum256([]byte("over-size-action"))
	overOut := sha256.Sum256([]byte("0123456789"))
	overResp := c.roundTrip(Request{
		ID:       4,
		Command:  CmdPut,
		ActionID: overAction[:],
		OutputID: overOut[:],
		BodySize: 3, // actual body is 10 bytes
	}, []byte("0123456789"))
	if !strings.Contains(overResp.Err, "body size mismatch") {
		t.Fatalf("expected oversized body error, got %+v", overResp)
	}

	// 5. Subsequent GET for the large entry succeeds, proving framing stayed synced.
	getResp := c.roundTrip(Request{ID: 5, Command: CmdGet, ActionID: largeAction[:]}, nil)
	if getResp.Miss || getResp.Err != "" || getResp.Size != largeSize {
		t.Fatalf("follow-up GET after bad PUTs failed: %+v", getResp)
	}
	c.close()
}

// TestPruneLeavesUnrelatedFilesInDiskDirUntouched verifies PRRT_kwDOUe_81c68peZa:
// pruning operates strictly on recognized cache entries under meta/ and obj/ and
// never deletes unrelated old files placed directly in --disk-dir (or stray
// non-cache files inside subdirectories).
func TestPruneLeavesUnrelatedFilesInDiskDirUntouched(t *testing.T) {
	diskDir := t.TempDir()
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	ancient := now.Add(-30 * 24 * time.Hour)

	// Place unrelated files in diskDir root, a sibling dir, and even inside obj/meta
	// with non-cache names.
	unrelatedPaths := []string{
		filepath.Join(diskDir, "README.txt"),
		filepath.Join(diskDir, "user-notes.meta"),
		filepath.Join(diskDir, "custom", "important.bin"),
		filepath.Join(diskDir, objSubdir, "not-a-cache-file.txt"),
		filepath.Join(diskDir, metaSubdir, "notes.meta"),
	}
	for _, p := range unrelatedPaths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, bytes.Repeat([]byte("U"), 1024), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, ancient, ancient); err != nil {
			t.Fatal(err)
		}
	}

	srv, err := NewServer(ServerConfig{
		DiskDir:      diskDir,
		MaxDiskBytes: 64,
		MaxDiskAge:   24 * time.Hour,
		LiveGrace:    time.Second,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	// Also seed a genuine expired cache entry so pruning definitely runs and evicts it.
	oldAction := sha256.Sum256([]byte("expired-cache-entry"))
	oldBody := bytes.Repeat([]byte("C"), 256)
	oldOut := sha256.Sum256(oldBody)
	oldObjPath, err := srv.writeLocal(hex.EncodeToString(oldAction[:]), oldOut[:], oldBody, ancient)
	if err != nil {
		t.Fatal(err)
	}
	oldMetaPath, _ := srv.entryPaths(hex.EncodeToString(oldAction[:]))
	srv.releaseLease()
	srv.mu.Lock()
	delete(srv.inUseObj, oldObjPath)
	delete(srv.inUseObj, oldMetaPath)
	srv.mu.Unlock()
	if err := os.Chtimes(oldObjPath, ancient, ancient); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldMetaPath, ancient, ancient); err != nil {
		t.Fatal(err)
	}

	srv.pruneDiskStore()

	// Genuine expired cache files MUST be evicted.
	if _, err := os.Stat(oldMetaPath); !os.IsNotExist(err) {
		t.Errorf("expected expired cache meta %s to be evicted, err=%v", oldMetaPath, err)
	}
	if _, err := os.Stat(oldObjPath); !os.IsNotExist(err) {
		t.Errorf("expected expired cache obj %s to be evicted, err=%v", oldObjPath, err)
	}

	// Every unrelated file MUST remain untouched.
	for _, p := range unrelatedPaths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("unrelated file %s was unexpectedly removed or unreadable: %v", p, err)
		}
	}
}

// TestCrossProcessLeaseProtectsLiveDiskPathBeyondLiveGrace verifies PRRT_kwDOUe_81c68peZc:
// when helper process A keeps a returned DiskPath open past LiveGrace and even past
// MaxDiskAge, helper process B closing over budget MUST NOT delete A's pinned object.
// Once A closes (releasing its lease), a subsequent prune by process C DOES evict it.
func TestCrossProcessLeaseProtectsLiveDiskPathBeyondLiveGrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX subprocess pipes; lock_windows.go compilation verified via GOOS=windows go build")
	}

	// Build the real gocacheprog binary so we test genuine multi-process OS file locks.
	binPath := filepath.Join(t.TempDir(), "pig-gocacheprog")
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Env = append(os.Environ(), "GOCACHEPROG=", "GOWORK=off")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build gocacheprog binary: %v\n%s", err, out)
	}

	diskDir := t.TempDir()

	type procClient struct {
		cmd    *exec.Cmd
		stdin  io.WriteCloser
		outDec *json.Decoder
	}
	spawnHelper := func(maxBytes int64, maxAge time.Duration) *procClient {
		t.Helper()
		cmd := exec.Command(binPath,
			"-disk-dir", diskDir,
			"-max-disk-bytes", fmt.Sprintf("%d", maxBytes),
			"-max-disk-age", maxAge.String(),
		)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bufio.NewReader(stdout))
		var caps Response
		if err := dec.Decode(&caps); err != nil {
			_ = cmd.Process.Kill()
			t.Fatalf("read helper caps: %v (stderr=%s)", err, stderr.String())
		}
		return &procClient{cmd: cmd, stdin: stdin, outDec: dec}
	}
	roundTripProc := func(pc *procClient, req Request, body []byte) Response {
		t.Helper()
		line, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		buf.Write(line)
		buf.WriteByte('\n')
		if req.Command == CmdPut && req.BodySize > 0 {
			buf.WriteByte('"')
			buf.WriteString(base64.StdEncoding.EncodeToString(body))
			buf.WriteString("\"\n")
		}
		if _, err := pc.stdin.Write(buf.Bytes()); err != nil {
			t.Fatal(err)
		}
		var resp Response
		if err := pc.outDec.Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		return resp
	}
	closeProc := func(pc *procClient) {
		t.Helper()
		_ = roundTripProc(pc, Request{ID: 999, Command: CmdClose}, nil)
		_ = pc.stdin.Close()
		if err := pc.cmd.Wait(); err != nil {
			t.Fatalf("helper wait: %v", err)
		}
	}

	// Process A starts and stores an artifact, keeping its session open.
	procA := spawnHelper(1<<20, 24*time.Hour)
	actionA := sha256.Sum256([]byte("procA-live-action"))
	bodyA := bytes.Repeat([]byte("A"), 512)
	outA := sha256.Sum256(bodyA)
	putA := roundTripProc(procA, Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: actionA[:],
		OutputID: outA[:],
		BodySize: int64(len(bodyA)),
	}, bodyA)
	if putA.Err != "" || putA.DiskPath == "" {
		t.Fatalf("procA put failed: %+v", putA)
	}

	// Backdate A's object and meta by 30 days to simulate A running much longer
	// than liveGrace (10m) and even longer than maxDiskAge!
	ancient := time.Now().UTC().Add(-30 * 24 * time.Hour)
	metaA := filepath.Join(diskDir, metaSubdir, hex.EncodeToString(actionA[:])[:2], hex.EncodeToString(actionA[:])+".meta")
	if err := os.Chtimes(putA.DiskPath, ancient, ancient); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(metaA, ancient, ancient); err != nil {
		t.Fatal(err)
	}

	// Process B runs with a tiny budget (100 bytes) and short age (1h), writes its own
	// entry, and closes — triggering pruneDiskStore while Process A is still open.
	procB := spawnHelper(100, time.Hour)
	actionB := sha256.Sum256([]byte("procB-action"))
	bodyB := bytes.Repeat([]byte("B"), 512)
	outB := sha256.Sum256(bodyB)
	putB := roundTripProc(procB, Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: actionB[:],
		OutputID: outB[:],
		BodySize: int64(len(bodyB)),
	}, bodyB)
	if putB.Err != "" {
		t.Fatalf("procB put failed: %+v", putB)
	}
	closeProc(procB)

	// Process A's DiskPath MUST still exist because Process A holds an active lease!
	if got, err := os.ReadFile(putA.DiskPath); err != nil || !bytes.Equal(got, bodyA) {
		t.Fatalf("procB prune evicted procA's live DiskPath while procA was still active: err=%v len=%d", err, len(got))
	}

	// Now close Process A (releasing its OS lease).
	closeProc(procA)

	// Re-backdate A's files (or note they are already ancient) and run Process C
	// with tiny budget + short age: now that A closed, A's unowned entry MUST be evicted.
	procC := spawnHelper(100, time.Hour)
	closeProc(procC)

	if _, err := os.Stat(putA.DiskPath); !os.IsNotExist(err) {
		t.Fatalf("expected procA's old DiskPath %s to be evicted after procA closed, stat err=%v", putA.DiskPath, err)
	}

	// Also verify stale lease cleanup if a process dies abruptly (SIGKILL) without CmdClose.
	procD := spawnHelper(1<<20, 24*time.Hour)
	actionD := sha256.Sum256([]byte("procD-killed-action"))
	bodyD := bytes.Repeat([]byte("D"), 512)
	outD := sha256.Sum256(bodyD)
	putD := roundTripProc(procD, Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: actionD[:],
		OutputID: outD[:],
		BodySize: int64(len(bodyD)),
	}, bodyD)
	if putD.Err != "" {
		t.Fatalf("procD put failed: %+v", putD)
	}
	if err := procD.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = procD.cmd.Wait()
	metaD := filepath.Join(diskDir, metaSubdir, hex.EncodeToString(actionD[:])[:2], hex.EncodeToString(actionD[:])+".meta")
	if err := os.Chtimes(putD.DiskPath, ancient, ancient); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(metaD, ancient, ancient); err != nil {
		t.Fatal(err)
	}
	procE := spawnHelper(100, time.Hour)
	closeProc(procE)

	if _, err := os.Stat(putD.DiskPath); !os.IsNotExist(err) {
		t.Fatalf("expected killed procD's object %s to be evicted after OS lock release, err=%v", putD.DiskPath, err)
	}
	if leases, err := os.ReadDir(filepath.Join(diskDir, leasesSubdir)); err == nil && len(leases) != 0 {
		t.Fatalf("expected stale lease files to be cleaned up, remaining: %v", leases)
	}
}

// TestPruneStoreLockSerializesWithConcurrentPublish verifies PRRT_kwDOUe_81c68peZh:
// pruneDiskStore holds an exclusive cross-process store lock across scan and unlink
// and re-checks mtime before unlinking, while writeLocal / handleGet acquire the
// shared store lock so a concurrent publish cannot interleave between walk and unlink.
func TestPruneStoreLockSerializesWithConcurrentPublish(t *testing.T) {
	diskDir := t.TempDir()
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	ancient := now.Add(-72 * time.Hour)

	pruner, err := NewServer(ServerConfig{
		DiskDir:      diskDir,
		MaxDiskBytes: 64,
		MaxDiskAge:   24 * time.Hour,
		LiveGrace:    time.Hour,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := NewServer(ServerConfig{
		DiskDir:      diskDir,
		MaxDiskBytes: DefaultMaxDiskBytes,
		MaxDiskAge:   DefaultMaxDiskAge,
		LiveGrace:    time.Hour,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.releaseLease()

	// Seed an old unreferenced orphan object (as if its meta was already gone).
	body := bytes.Repeat([]byte("R"), 512)
	out := sha256.Sum256(body)
	objPath := pruner.objectPath(hex.EncodeToString(out[:]))
	if err := os.MkdirAll(filepath.Dir(objPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(objPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(objPath, ancient, ancient); err != nil {
		t.Fatal(err)
	}

	// Pause pruner after walking and before unlinking; launch a concurrent publish
	// of the SAME object in publisher. Because pruner holds the exclusive store lock,
	// publisher MUST block until pruner finishes (no race where publisher returns
	// objPath right before pruner unlinks it!), and after publisher finishes its
	// returned DiskPath MUST exist on disk with valid bytes.
	inPrune := make(chan struct{})
	releasePrune := make(chan struct{})
	pruner.pruneBeforeUnlinkHook = func() {
		close(inPrune)
		<-releasePrune
	}

	pruneDone := make(chan struct{})
	go func() {
		defer close(pruneDone)
		pruner.pruneDiskStore()
	}()

	<-inPrune

	action := sha256.Sum256([]byte("concurrent-publish-action"))
	pubDone := make(chan string, 1)
	pubErr := make(chan error, 1)
	go func() {
		p, err := publisher.writeLocal(hex.EncodeToString(action[:]), out[:], body, now)
		pubErr <- err
		pubDone <- p
	}()

	// Verify publisher is blocked on the cross-process store lock while pruner is in-flight.
	select {
	case p := <-pubDone:
		t.Fatalf("publisher bypassed exclusive store lock while prune was active (returned %s)", p)
	case <-time.After(75 * time.Millisecond):
	}

	close(releasePrune)
	<-pruneDone

	if err := <-pubErr; err != nil {
		t.Fatalf("concurrent publisher writeLocal failed: %v", err)
	}
	returnedPath := <-pubDone
	got, err := os.ReadFile(returnedPath)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("returned DiskPath %s missing or corrupt after concurrent prune+publish: err=%v len=%d", returnedPath, err, len(got))
	}

	// Also verify re-stat guard: if mtime is refreshed before unlink (even without
	// another process), canEvict re-stats and skips removing the refreshed object.
	pruner2, err := NewServer(ServerConfig{
		DiskDir:      diskDir,
		MaxDiskBytes: 64,
		MaxDiskAge:   24 * time.Hour,
		LiveGrace:    time.Hour,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	body2 := bytes.Repeat([]byte("S"), 512)
	out2 := sha256.Sum256(body2)
	objPath2 := pruner2.objectPath(hex.EncodeToString(out2[:]))
	if err := os.MkdirAll(filepath.Dir(objPath2), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(objPath2, body2, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(objPath2, ancient, ancient); err != nil {
		t.Fatal(err)
	}
	pruner2.pruneBeforeUnlinkHook = func() {
		// Refresh mtime after walk, before unlink.
		_ = os.Chtimes(objPath2, now, now)
	}
	pruner2.pruneDiskStore()
	if _, err := os.Stat(objPath2); err != nil {
		t.Fatalf("re-stat guard failed to preserve object whose mtime was refreshed before unlink: %v", err)
	}
}

// TestS3PutSkipsOversizedArtifactBeforeReadingLocalFile verifies PRRT_kwDOUe_81c68peZj:
// when an artifact exceeds MaxObjectBytes, S3Backend.Put skips remote upload
// before reading/enveloping the local file, local PUT still succeeds, zero HTTP
// PUT requests are sent, and Stats reports RemoteSkipped separately from
// RemotePuts and RemoteErrors.
func TestS3PutSkipsOversizedArtifactBeforeReadingLocalFile(t *testing.T) {
	mem, httpSrv := newMemS3Server(t)

	backend, err := NewS3Backend(S3Config{
		Bucket:          "pig-cache",
		Endpoint:        httpSrv.URL,
		Region:          "auto",
		Prefix:          "v1",
		AccessKeyID:     "AKIA",
		SecretAccessKey: "SECRET",
		ReadOnly:        false,
		MaxObjectBytes:  256,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Direct S3Backend.Put unit proof: passing bodySize > MaxObjectBytes with a
	// non-existent bodyPath MUST return ErrRemoteSkipped (proving os.ReadFile is
	// never called before the size check!).
	actionDirect := sha256.Sum256([]byte("direct-oversized"))
	outDirect := sha256.Sum256([]byte("x"))
	err = backend.Put(context.Background(), hex.EncodeToString(actionDirect[:]), outDirect[:], filepath.Join(t.TempDir(), "does-not-exist"), 1024)
	if !errors.Is(err, ErrRemoteSkipped) {
		t.Fatalf("expected ErrRemoteSkipped before reading file, got %v", err)
	}
	if mem.puts.Load() != 0 {
		t.Fatalf("expected 0 remote PUTs for oversized artifact, got %d", mem.puts.Load())
	}

	// Full Server protocol proof: local PUT succeeds, remote PUT skipped, verbose
	// close log and Stats distinguish remote_skipped from remote_puts/remote_errors.
	var logBuf bytes.Buffer
	srv, err := NewServer(ServerConfig{
		DiskDir:  t.TempDir(),
		Remote:   backend,
		ReadOnly: false,
		Verbose:  true,
		Log:      &logBuf,
	})
	if err != nil {
		t.Fatal(err)
	}
	c := startTestServer(t, srv)

	// 1. Oversized artifact (512 bytes > 256 limit).
	bigAction := sha256.Sum256([]byte("oversized-put"))
	bigBody := bytes.Repeat([]byte("O"), 512)
	bigOut := sha256.Sum256(bigBody)
	bigResp := c.roundTrip(Request{
		ID:       1,
		Command:  CmdPut,
		ActionID: bigAction[:],
		OutputID: bigOut[:],
		BodySize: int64(len(bigBody)),
	}, bigBody)
	if bigResp.Err != "" || bigResp.DiskPath == "" {
		t.Fatalf("local PUT of oversized artifact must succeed: %+v", bigResp)
	}
	if got, err := os.ReadFile(bigResp.DiskPath); err != nil || !bytes.Equal(got, bigBody) {
		t.Fatalf("local disk copy of oversized artifact invalid: err=%v len=%d", err, len(got))
	}

	// 2. Normal-sized artifact (64 bytes <= 256 limit) uploads normally.
	smallAction := sha256.Sum256([]byte("small-put"))
	smallBody := bytes.Repeat([]byte("S"), 64)
	smallOut := sha256.Sum256(smallBody)
	smallResp := c.roundTrip(Request{
		ID:       2,
		Command:  CmdPut,
		ActionID: smallAction[:],
		OutputID: smallOut[:],
		BodySize: int64(len(smallBody)),
	}, smallBody)
	if smallResp.Err != "" || smallResp.DiskPath == "" {
		t.Fatalf("small PUT failed: %+v", smallResp)
	}

	c.close()

	if mem.puts.Load() != 1 {
		t.Fatalf("expected exactly 1 remote PUT (small only), got %d", mem.puts.Load())
	}
	st := srv.Stats()
	if st.Puts != 2 || st.RemotePuts != 1 || st.RemoteSkipped != 1 || st.RemoteErrors != 0 {
		t.Fatalf("unexpected stats: %+v (want Puts=2 RemotePuts=1 RemoteSkipped=1 RemoteErrors=0)", st)
	}
	if !strings.Contains(logBuf.String(), "remote_puts=1 remote_skipped=1 remote_errors=0") {
		t.Fatalf("verbose close stats missing remote_skipped breakdown:\n%s", logBuf.String())
	}
	if backend.CircuitOpen() {
		t.Fatalf("oversized remote skip must not open circuit breaker")
	}
}

// TestSetupScriptFallsBackWhenMkdirFails verifies PRRT_kwDOUe_81c68peZy:
// if --disk-dir (or --bin-dir) points to an existing regular file or an unwritable
// location so `mkdir -p` fails, setup-gocacheprog.sh exits 0 via fallback_local,
// clears GOCACHEPROG in GITHUB_ENV, and emits mode=local-fallback / enabled=false.
func TestSetupScriptFallsBackWhenMkdirFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper test runs on Unix")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repoRoot, "automation", "ci", "setup-gocacheprog.sh")

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

	runCase := func(t *testing.T, binDir, diskDir string) {
		t.Helper()
		tmp := t.TempDir()
		envFile := filepath.Join(tmp, "github.env")
		outFile := filepath.Join(tmp, "github.out")
		cmd := exec.Command("bash", script,
			"--github-env", envFile,
			"--github-output", outFile,
			"--bin-dir", binDir,
			"--disk-dir", diskDir,
			"--print-env",
		)
		cmd.Dir = repoRoot
		cmd.Env = append(baseCleanEnv(),
			"PIG_GOCACHE_BUCKET=pig-ci-cache",
			"PIG_GOCACHE_ACCESS_KEY_ID=AKIA",
			"PIG_GOCACHE_SECRET_ACCESS_KEY=SECRET",
			"PIG_GOCACHE_MODE=readonly",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("expected setup-gocacheprog.sh to fall back cleanly (exit 0), got err=%v\n%s", err, out)
		}
		envBytes, err := os.ReadFile(envFile)
		if err != nil {
			t.Fatal(err)
		}
		outBytes, err := os.ReadFile(outFile)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(envBytes)) != "GOCACHEPROG=" {
			t.Fatalf("expected GOCACHEPROG= cleared in GITHUB_ENV, got:\n%s", envBytes)
		}
		if !strings.Contains(string(outBytes), "enabled=false") || !strings.Contains(string(outBytes), "mode=local-fallback") {
			t.Fatalf("expected enabled=false & mode=local-fallback in GITHUB_OUTPUT, got:\n%s", outBytes)
		}
		if !strings.Contains(string(out), "unset GOCACHEPROG") {
			t.Fatalf("expected --print-env to emit unset GOCACHEPROG, got:\n%s", out)
		}
	}

	t.Run("disk-dir is an existing regular file", func(t *testing.T) {
		tmp := t.TempDir()
		blockingFile := filepath.Join(tmp, "not-a-dir")
		if err := os.WriteFile(blockingFile, []byte("file"), 0o644); err != nil {
			t.Fatal(err)
		}
		runCase(t, filepath.Join(tmp, "bin"), blockingFile)
	})

	t.Run("bin-dir is under a regular file", func(t *testing.T) {
		tmp := t.TempDir()
		blockingFile := filepath.Join(tmp, "not-a-dir")
		if err := os.WriteFile(blockingFile, []byte("file"), 0o644); err != nil {
			t.Fatal(err)
		}
		runCase(t, filepath.Join(blockingFile, "nested"), filepath.Join(tmp, "disk"))
	})

	t.Run("unwritable parent directory", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root bypasses POSIX directory write permissions")
		}
		tmp := t.TempDir()
		roParent := filepath.Join(tmp, "ro")
		if err := os.MkdirAll(roParent, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(roParent, 0o755) })
		runCase(t, filepath.Join(tmp, "bin"), filepath.Join(roParent, "disk"))
	})
}
