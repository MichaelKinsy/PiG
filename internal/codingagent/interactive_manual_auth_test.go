package codingagent

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// packages/coding-agent/src/modes/interactive/interactive-mode.ts:6085-6093 routes manual_code to showManualInput, not showPrompt.
func TestProviderOwnedManualAuthPrompt(t *testing.T) {
	m := newPostLoginTestMode(t)
	ctx, cancel := context.WithCancelCause(t.Context())
	m.runCtx = ctx
	dialog := m.newLoginDialog("Prompt Repro", func() { cancel(errLoginAborted) })
	requests := make(chan authPromptRequest)
	done := make(chan error, 1)
	finished := make(chan error, 1)
	go func() { finished <- m.runAuthDialog(ctx, cancel, dialog, requests, nil, done) }()
	defer func() {
		done <- nil
		if err := <-finished; err != nil {
			t.Error(err)
		}
		cancel(nil)
	}()
	request := authPromptRequest{ctx: ctx, prompt: ai.AuthManualCodePrompt{Message: "Paste callback URL:", Placeholder: "not shown for manual input"}, reply: make(chan authPromptReply, 1)}
	requests <- request
	observed := make(chan string, 1)
	m.uiTaskCh <- func() { observed <- plainRender(dialog) }
	frame := <-observed
	if !strings.Contains(frame, "Paste callback URL:") || !strings.Contains(frame, "to cancel)") || strings.Contains(frame, "to submit") || strings.Contains(frame, "not shown for manual input") {
		t.Fatalf("manual prompt frame=%q", frame)
	}
	m.uiTaskCh <- func() { dialog.HandleInput("callback-value"); dialog.HandleInput("\n") }
	if reply := <-request.reply; reply.err != nil || reply.value != "callback-value" {
		t.Fatalf("manual reply=%+v", reply)
	}
	request = authPromptRequest{ctx: ctx, prompt: ai.AuthTextPrompt{Message: "Second prompt:"}, reply: make(chan authPromptReply, 1)}
	requests <- request
	m.uiTaskCh <- func() {
		dialog.HandleInput("second-secret-demo")
		observed <- plainRender(dialog)
	}
	frame = <-observed
	for _, value := range []string{"callback-value", "second-secret-demo"} {
		count := 0
		for line := range strings.SplitSeq(frame, "\n") {
			if strings.TrimSpace(line) == "> "+value {
				count++
			}
		}
		if count != 1 {
			t.Errorf("value %q appears %d times: %q", value, count, frame)
		}
	}
	m.uiTaskCh <- func() { dialog.HandleInput("\n") }
	if reply := <-request.reply; reply.err != nil || reply.value != "second-secret-demo" {
		t.Fatalf("second reply=%+v", reply)
	}
}
