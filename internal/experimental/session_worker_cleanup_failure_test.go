package experimental

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/durableadapter"
	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

type failingCloseWorkerHarness struct {
	*durableadapter.Session
	startupError error
}

var errWorkerCloseFailure = errors.New("harness close failed")

// TaskGraph is the first step after the Harness opened; session-worker.ts:535 fails startup there.
func (worker *failingCloseWorkerHarness) TaskGraph(ctx context.Context) (services.TaskGraphActivity, error) {
	if worker.startupError != nil {
		return nil, worker.startupError
	}
	return worker.Session.TaskGraph(ctx)
}

func (worker *failingCloseWorkerHarness) Close(ctx context.Context) error {
	return errors.Join(worker.Session.Close(ctx), errWorkerCloseFailure)
}

// runWorkerWithFailingClose runs the worker with a Harness whose close fails, and whose task graph fails when startupError is set.
func runWorkerWithFailingClose(t *testing.T, startupError error, stopAfterReady bool) ([]map[string]any, string, error) {
	t.Helper()
	return runWorkerAgainstFakeCoordinator(t, func(_ context.Context, databasePath string, _ SessionWorkerOptions) (SessionWorkerRuntime, error) {
		durable, err := durabletest.OpenFile(databasePath)
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		return SessionWorkerRuntime{Harness: &failingCloseWorkerHarness{Session: durable.Harness, startupError: startupError}, Conversation: durable.Conversation}, nil
	}, stopAfterReady, nil)
}

// runWorkerAgainstFakeCoordinator runs the real worker entry (returned, when not nil, receives its result the moment it returns, before the helper waits for late payloads) over a new Session against a fake coordinator control socket and returns the entry's result, every payload the worker sent, and what it wrote to stderr.
func runWorkerAgainstFakeCoordinator(t *testing.T, createHarness CreateSessionWorkerHarness, stopAfterReady bool, returned chan<- error) ([]map[string]any, string, error) {
	t.Helper()
	root := t.TempDir()
	sessionDir := filepath.Join(root, "sessions")
	metadata, err := CreateSession(sessionDir, CreateSessionOptions{ID: new("cleanup-failure"), Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	lockPath, err := filepath.EvalSymlinks(metadata.Path)
	if err != nil {
		t.Fatal(err)
	}
	lockPath += ".lock"
	// isolateExperimentalTest points TMPDIR/TEMP at a short directory, so this stays under sun_path on Unix and needs no /tmp on Windows.
	socketDirectory, err := os.MkdirTemp("", "pw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	listener, err := listenTestSocket(filepath.Join(socketDirectory, "c.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	t.Setenv(SessionWorkerControlAddressEnv, listener.Addr().String())
	t.Setenv(SessionWorkerControlTokenEnv, "token")
	t.Setenv(SessionWorkerSessionKeyEnv, base64.RawURLEncoding.EncodeToString([]byte("key")))
	t.Setenv(SessionWorkerPeerIDEnv, "peer")
	var mu sync.Mutex
	var sent []map[string]any
	// The worker never closes its control socket (process exit does), so the reader cannot observe end of stream; the wait is bounded and ends early once worker_failed arrives.
	failed := make(chan struct{})
	var failedOnce sync.Once
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		if _, err := reader.ReadBytes('\n'); err != nil {
			return
		}
		_, _ = conn.Write([]byte(`{"type":"peer_registered","peerId":"peer"}` + "\n"))
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return
			}
			var envelope struct {
				Payload map[string]any `json:"payload"`
			}
			if json.Unmarshal(line, &envelope) != nil || envelope.Payload == nil {
				continue
			}
			mu.Lock()
			sent = append(sent, envelope.Payload)
			mu.Unlock()
			if envelope.Payload["type"] == "worker_failed" {
				failedOnce.Do(func() { close(failed) })
			}
			if envelope.Payload["type"] == "worker_ready" && stopAfterReady {
				_, _ = conn.Write([]byte(`{"type":"message","from":"server","payload":{"type":"shutdown"}}` + "\n"))
			}
		}
	}()
	options, err := json.Marshal(SessionWorkerOptions{SessionDir: sessionDir, Metadata: metadata, PluginManifestPaths: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrText := make(chan string, 1)
	go func() {
		text, _ := io.ReadAll(stderrReader)
		stderrText <- string(text)
	}()
	previousStderr := os.Stderr
	// The worker holds a real Session ownership lock (2 s stale threshold) while os.Stderr is the pipe above. Its default compromise policy is os.Exit(1), which would end the whole test binary with the inspected error lost in the pipe: only an unrelated line and FAIL with no --- FAIL. Report a compromise as this test's failure instead, on the real stderr.
	previousTerminate := terminateOnLockCompromise
	terminateOnLockCompromise = func(err error) {
		t.Errorf("%s: Session ownership lock compromised while the in-process worker held it: %v", t.Name(), err)
	}
	os.Stderr = stderrWriter
	result := RunSessionWorkerWithHarness(t.Context(), []string{string(options)}, createHarness)
	// RunSessionWorkerWithHarness released the lock and joined its heartbeat before returning, so no compromise callback can run after the restore.
	terminateOnLockCompromise = previousTerminate
	if returned != nil {
		returned <- result
	}
	os.Stderr = previousStderr
	if _, err := os.Lstat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("session ownership lock after the worker returned: %v, want removed", err)
	}
	_ = stderrWriter.Close()
	stderr := <-stderrText
	_ = stderrReader.Close()
	select {
	case <-failed:
	case <-time.After(time.Second):
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]map[string]any(nil), sent...), stderr, result
}

func workerPayloadTypes(sent []map[string]any) []string {
	var types []string
	for _, payload := range sent {
		types = append(types, payload["type"].(string))
	}
	return types
}

// Opus review: session-worker.ts:591-599. Cleanup that fails after startup is closeAndExit's rejection: the error is printed with console.error and exit code 1, and no worker_failed is sent.
func TestSessionWorkerCleanupFailureAfterStartupSendsNoWorkerFailed(t *testing.T) {
	isolateExperimentalTest(t)
	sent, stderr, result := runWorkerWithFailingClose(t, nil, true)
	if !errors.Is(result, errWorkerCloseFailure) {
		t.Fatalf("result = %v, want the cleanup failure", result)
	}
	if !strings.Contains(stderr, errWorkerCloseFailure.Error()) {
		t.Fatalf("stderr = %q, want closeAndExit's console.error of the cleanup failure", stderr)
	}
	if types := workerPayloadTypes(sent); strings.Contains(strings.Join(types, ","), "worker_failed") {
		t.Fatalf("payloads = %v, want no worker_failed after startup", types)
	}
}

// session-worker.ts:562-572,790-800. A startup failure whose cleanup also fails reports AggregateError("Session worker startup and cleanup failed") through worker_failed.
func TestSessionWorkerStartupCleanupFailureReportsAggregateMessage(t *testing.T) {
	isolateExperimentalTest(t)
	startupError := errors.New("task graph unavailable")
	sent, stderr, result := runWorkerWithFailingClose(t, startupError, false)
	if !errors.Is(result, startupError) || !errors.Is(result, errWorkerCloseFailure) {
		t.Fatalf("result = %v, want both startup and cleanup failures", result)
	}
	// session-worker.ts:883 exits 1 without printing a failure the worker reported through worker_failed.
	if stderr != "" {
		t.Fatalf("stderr = %q, want no output for a reported startup failure", stderr)
	}
	var failed map[string]any
	for _, payload := range sent {
		if payload["type"] == "worker_failed" {
			failed = payload
		}
	}
	if failed == nil || failed["message"] != "Session worker startup and cleanup failed" {
		t.Fatalf("worker_failed = %v (payloads %v)", failed, workerPayloadTypes(sent))
	}
}

// Opus review residual: session-worker.ts:604-605 parses the grace delays after startup's try/catch, so an invalid
// delay rejects run() with its own message and never reaches closeResources; worker_failed carries that raw message
// even when cleanup would also fail (session-worker.ts:790-800).
func TestSessionWorkerInvalidGraceDelayReportsRawMessageEvenWhenCleanupFails(t *testing.T) {
	for _, name := range []string{SessionWorkerInitialDemandGraceEnv, SessionWorkerOrphanDemandGraceEnv} {
		t.Run(name, func(t *testing.T) {
			isolateExperimentalTest(t)
			t.Setenv(name, "-1")
			sent, stderr, result := runWorkerWithFailingClose(t, nil, false)
			want := name + " must be a non-negative safe integer"
			if result == nil || result.Error() != want {
				t.Fatalf("result = %v, want %q", result, want)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want no output for a reported failure", stderr)
			}
			var failed map[string]any
			for _, payload := range sent {
				if payload["type"] == "worker_failed" {
					failed = payload
				}
			}
			if failed == nil || failed["message"] != want {
				t.Fatalf("worker_failed = %v (payloads %v)", failed, workerPayloadTypes(sent))
			}
		})
	}
}

// session-worker.ts:604-605,790-800. An invalid grace delay rejects run() before closeResources, so the worker still holds proper-lockfile ownership while it sends worker_failed; only process exit releases it. Go releases after the send so a competing worker cannot acquire the session before the coordinator hears the failure.
func TestSessionWorkerReleasesDeferredOwnershipOnlyAfterWorkerFailedIsSent(t *testing.T) {
	for _, tt := range []struct {
		name        string
		result      error
		cleanupOnly bool
		wantSent    bool
	}{
		{"rejected run", errors.New("invalid delay"), false, true},
		{"post-startup cleanup failure", errors.New("cleanup"), true, false},
		{"no failure", nil, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			released, sent := false, false
			send := func(any) error {
				if released {
					t.Error("ownership was released before worker_failed was sent")
				}
				sent = true
				return nil
			}
			reportWorkerOutcome(send, "token", "key", tt.result, tt.cleanupOnly, func() error { released = true; return nil })
			if sent != tt.wantSent || !released {
				t.Fatalf("sent = %v (want %v), released = %v", sent, tt.wantSent, released)
			}
		})
	}
}
