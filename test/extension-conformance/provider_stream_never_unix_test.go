//go:build unix

package extensionconformance

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestAbortedProviderTurnWhoseAuthorNeverReactsLeavesNothingBehind: an extension provider's author that never reacts to the abort of its request (it neither pushes nor ends
// its stream) holds the turn: Pi's consumer has no check of the abort signal and waits for the stream (packages/agent/src/agent-loop.ts:424-478; Pi 1.5.2 in an RPC session
// gave no agent_end in 12 s, isStreaming true, and nothing persisted). PiG adds no bound. The way out is the end of the host, as the exit of the process is in Pi: in every
// SDK and load mode the turn then ends, the abort returns, the extension's process is gone, and no goroutine that the Session, the host or the turn started is alive.
//
// The test runs in a process of its own, where the goroutines alive are its own.
func TestAbortedProviderTurnWhoseAuthorNeverReactsLeavesNothingBehind(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	for _, tc := range providerRealizations("cancel-never-provider") {
		t.Run(tc.name, func(t *testing.T) {
			baseline := testenv.BaselineGoroutines()
			pid := neverEndingProviderTurn(t, tc)
			if !tc.fused {
				// The host reaped the process: no process has its id.
				deadline := time.Now().Add(10 * time.Second)
				for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
					if time.Now().After(deadline) {
						t.Errorf("the extension's process %d is alive 10 s after its host shut down", pid)
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			baseline.RequireEnded(t, 10*time.Second)
		})
	}
}

// neverEndingProviderTurn runs one turn of a Session whose model is the cancel-never provider, aborts it when its text shows, sees that it does not end, and shuts the host
// down. It returns the id of the provider's process, which is the text the provider showed.
func neverEndingProviderTurn(t *testing.T, tc providerCancelCase) int {
	t.Helper()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer services.Close()
	host := subprocess.NewHost(t.TempDir())
	shutdown := sync.OnceFunc(func() { host.Shutdown("test done") })
	defer shutdown()
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	if err := loadProviderFixture(t, host, tc.name, tc.source, tc.fused, tc.packed); err != nil {
		t.Fatal(err)
	}
	applyQueuedRegistrations(host)
	model := services.ModelRuntime().GetModel(tc.provider, "faux")
	if model == nil {
		t.Fatalf("%s/faux is not registered", tc.provider)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	var mu sync.Mutex
	shown := ""
	requested, aborted := make(chan struct{}), make(chan error, 1)
	abort := sync.OnceFunc(func() {
		close(requested)
		go func() { aborted <- session.Abort(context.Background()) }()
	})
	unsubscribe := session.Subscribe(func(event agent.AgentEvent) {
		update, ok := event.(agent.MessageUpdateEvent)
		if !ok || update.AssistantMessageEvent == nil || update.AssistantMessageEvent.EventType() != ai.EventTextDelta {
			return
		}
		text, err := partialText(update.AssistantMessageEvent)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		shown = text
		mu.Unlock()
		abort()
	})
	defer unsubscribe()
	ended := make(chan error, 1)
	go func() { ended <- session.Prompt(context.Background(), "go") }()
	select {
	case <-requested:
	case err := <-ended:
		t.Fatalf("the turn ended before its provider showed text: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("the provider showed no text within 60 s")
	}
	// The author does nothing after the abort, and the turn waits for its stream. One second is what this row watches; Pi was watched for 12 s.
	select {
	case err := <-ended:
		t.Fatalf("the aborted turn ended (%v) though its author's stream had not: the host or the SDK ended the request at the abort", err)
	case <-time.After(time.Second):
	}
	// The way out: the host ends, as the process does when the CLI exits.
	shutdown()
	select {
	case err := <-ended:
		t.Logf("the turn ended with its host: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the turn did not end within 30 s of its host's shutdown")
	}
	select {
	case err := <-aborted:
		if err != nil {
			t.Errorf("abort: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the abort did not return within 30 s of the host's shutdown")
	}
	mu.Lock()
	defer mu.Unlock()
	pid, err := strconv.Atoi(shown)
	if err != nil || pid <= 0 {
		t.Fatalf("the provider showed %q, want the id of its process", shown)
	}
	if tc.fused != (pid == os.Getpid()) {
		t.Errorf("the provider runs in process %d and the test in %d: fused is %v", pid, os.Getpid(), tc.fused)
	}
	return pid
}
