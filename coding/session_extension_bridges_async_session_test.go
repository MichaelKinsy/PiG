package coding

// pi: packages/coding-agent/src/core/agent-session.ts

import (
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session.ts:3598-3603: without a bound abort handler ctx.abort() is `void this.abort()`: it requests the abort and returns
// at once, even when called from a handler the running turn is waiting on, and the run ends aborted.
func TestExtensionAbortWithoutAHandlerRequestsAbortWithoutWaitingForTheRun(t *testing.T) {
	returned := make(chan struct{})
	var once sync.Once
	h := runStateHarness(t, map[string]func(*Session){
		"agent_start": func(s *Session) {
			once.Do(func() {
				s.extensionAbort()
				close(returned)
			})
		},
	}, fauxReply("never delivered", ai.StopReasonStop, 0))
	sent := make(chan error, 1)
	go func() {
		_, err := h.session.Send(t.Context(), "start")
		sent <- err
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("ctx.abort() waited for the run it was called from")
	}
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("the run did not end after ctx.abort()")
	}
	last := lastAssistantMessage(h.session.Messages())
	if last == nil || last.StopReason != ai.StopReasonAborted {
		t.Fatalf("last assistant message = %+v, want an aborted response", last)
	}
}

// agent-session.ts:3544-3550: a failing sendMessage rejects inside the bridge's catch, which reports it through runner.emitError
// (`<runtime>`, `send_message`); the extension's call itself returns without an error.
func TestExtensionSendMessageFailureIsReportedNotReturned(t *testing.T) {
	h := runStateHarness(t, map[string]func(*Session){}, fauxReply("x", ai.StopReasonStop, 0))
	var mu sync.Mutex
	var reported []*extension.ExtensionError
	runner := h.session.currentRunner()
	runner.AddErrorListener(func(err *extension.ExtensionError) {
		mu.Lock()
		reported = append(reported, err)
		mu.Unlock()
	})
	if err := h.session.Close(); err != nil {
		t.Fatal(err)
	}
	trigger := true
	if err := h.session.SendMessage(extension.CustomMessageRef{CustomType: "note", Content: "hi", Display: true}, &extension.SendMessageOptions{TriggerTurn: &trigger}); err != nil {
		t.Fatalf("SendMessage returned %v; the failure belongs to the error listeners", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 || reported[0].ExtensionPath != "<runtime>" || reported[0].Event != "send_message" {
		t.Fatalf("reported = %+v, want one <runtime> send_message error", reported)
	}
}
