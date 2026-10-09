package coding

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi agent-session.ts:3485-3488 and :3515-3516 propagate appendMessage failures and refresh the Agent only after a successful append.
func TestExecuteBashPropagatesPersistenceFailureWithoutRefreshingAgent(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true}, fauxReply("seed", ai.StopReasonStop, 0))
	if _, err := h.session.Send(t.Context(), "seed"); err != nil {
		t.Fatal(err)
	}
	before := h.session.Messages()
	// A directory is unwritable as a JSONL file regardless of the process's user privileges.
	h.session.Inner().SetPath(t.TempDir())
	operations := bashPersistenceOperations(func(_ context.Context, _, _ string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
		options.OnData([]byte("output"))
		return extension.BashOperationsResult{ExitCode: new(0)}, nil
	})
	if _, err := h.session.ExecuteBash(t.Context(), "command", nil, &ExecuteBashOptions{Operations: operations}); err == nil {
		t.Error("Bash succeeded after its result failed to persist")
	}
	if got := h.session.Messages(); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed append refreshed live Agent context: before=%+v after=%+v", before, got)
	}
	if bashIsRunning(h.session) {
		t.Fatal("failed execution retained its abort controller")
	}
}

// Pi keeps pending Bash records if their append fails, and the run owns that failure rather than reporting successful settlement.
func TestDeferredBashPersistenceFailureReachesPromptCaller(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{emptySessionManager: true}, fauxReply("done", ai.StopReasonStop, 0))
	badPath := t.TempDir()
	var before []agent.AgentMessage
	h.session.Subscribe(func(event agent.AgentEvent) {
		if _, ok := event.(agent.AgentEndEvent); !ok {
			return
		}
		before = h.session.Messages()
		h.session.Inner().SetPath(badPath)
		if err := h.session.RecordBashResult("deferred", BashResult{Output: "output", ExitCode: new(0)}, false); err != nil {
			t.Errorf("record returned an error before deferred persistence: %v", err)
		}
	})
	if err := h.session.Prompt(t.Context(), "start"); err == nil {
		t.Fatal("Prompt succeeded after deferred Bash persistence failed")
	}
	if !bashHasPending(h.session) {
		t.Error("failed flush discarded pending Bash records")
	}
	if got := h.session.Messages(); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed flush refreshed Agent context: before=%+v after=%+v", before, got)
	}
}
