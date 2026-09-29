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

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/agentharness"
	envpkg "github.com/MichaelKinsy/PiG/agent/harness/env"
	hruntime "github.com/MichaelKinsy/PiG/agent/harness/runtime"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

type failingCloseWorkerHarness struct {
	*codingWorkerHarness
	laneError error
}

var errWorkerCloseFailure = errors.New("harness close failed")

func (worker *failingCloseWorkerHarness) Lane(ctx context.Context, name string) (services.SessionWorkerServiceLane, error) {
	if worker.laneError != nil {
		return nil, worker.laneError
	}
	return worker.codingWorkerHarness.Lane(ctx, name)
}

func (worker *failingCloseWorkerHarness) Close(ctx context.Context) error {
	return errors.Join(worker.codingWorkerHarness.Close(ctx), errWorkerCloseFailure)
}

// runWorkerWithFailingClose runs the real worker entry against a fake coordinator control socket and returns the entry's result, every payload the worker sent, and what it wrote to stderr.
func runWorkerWithFailingClose(t *testing.T, laneError error, stopAfterReady bool) ([]map[string]any, string, error) {
	t.Helper()
	root := t.TempDir()
	executionEnv := envpkg.NewNodeExecutionEnv(envpkg.NodeExecutionEnvOptions{Cwd: root})
	t.Cleanup(func() { executionEnv.Cleanup(context.Background()) })
	sessionDir := filepath.Join(root, "sessions")
	repo := session.NewJsonlSessionRepo(session.JsonlSessionRepoOptions{FileSystem: executionEnv, SessionsRoot: sessionDir})
	created, err := repo.Create(t.Context(), session.SessionCreateOptions{ID: "cleanup-failure", Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	metadata := created.Metadata()
	lockPath, err := filepath.EvalSymlinks(metadata.Path)
	if err != nil {
		t.Fatal(err)
	}
	lockPath += ".lock"
	if err := created.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
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
	os.Stderr = stderrWriter
	result := RunSessionWorkerWithHarness(t.Context(), []string{string(options)}, func(ctx context.Context, stored session.Session, _ SessionWorkerOptions, _ *envpkg.NodeExecutionEnv) (SessionWorkerRuntime, error) {
		faux := ai.NewFauxProvider(ai.FauxConfig{})
		t.Cleanup(func() { _ = faux.Close() })
		models := ai.CreateModels()
		models.SetProvider(faux.Provider())
		harnessInstance, err := hruntime.CreateAgentHarness(ctx, hruntime.AgentHarnessOptions{
			Session: stored, Models: models, Model: faux.GetModel(),
			Tools: []harness.AgentHarnessTool{}, Resources: agentharness.Resources{},
		})
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		return SessionWorkerRuntime{Harness: &failingCloseWorkerHarness{codingWorkerHarness: &codingWorkerHarness{harness: harnessInstance.Harness}, laneError: laneError}}, nil
	})
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
	laneError := errors.New("lane unavailable")
	sent, stderr, result := runWorkerWithFailingClose(t, laneError, false)
	if !errors.Is(result, laneError) || !errors.Is(result, errWorkerCloseFailure) {
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
