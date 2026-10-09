package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports AgentSession.createReplacedSessionContext (packages/coding-agent/src/core/agent-session.ts:4375-4383): the context copies the runner's
// command context and rebinds sendMessage to sendCustomMessage and sendUserMessage to sendUserMessage of this session, both awaited.
func TestCreateReplacedSessionContextUpstream(t *testing.T) {
	t.Run("sendMessage persists a custom message on this session without a model turn", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
		replaced := h.session.CreateReplacedSessionContext(t.Context())
		if err := replaced.SendMessage(extension.CustomMessageRef{CustomType: "note", Content: "hello", Display: true}, nil); err != nil {
			t.Fatal(err)
		}
		var custom int
		for _, entry := range h.session.Inner().GetEntries() {
			if entry.Base().Type == "custom_message" {
				custom++
			}
		}
		if custom != 1 {
			t.Errorf("custom_message entries = %d, want 1", custom)
		}
		if got := h.provider.callCount(); got != 0 {
			t.Errorf("an idle custom message requested %d model turns", got)
		}
	})

	t.Run("sendUserMessage awaits the turn it starts", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
		replaced := h.session.CreateReplacedSessionContext(t.Context())
		if err := replaced.SendUserMessage("hello there", nil); err != nil {
			t.Fatal(err)
		}
		// The call returned, so the turn is finished: the model ran once and the transcript holds the user text and the reply.
		if got := h.provider.callCount(); got != 1 {
			t.Errorf("model turns = %d, want 1", got)
		}
		var users []string
		for _, message := range h.session.Messages() {
			if message.User != nil {
				users = append(users, extractUserMessageText(message.User.Content))
			}
		}
		if !slices.Equal(users, []string{"hello there"}) {
			t.Errorf("user messages = %v, want [hello there]", users)
		}
	})

	t.Run("the context is the runner's command context", func(t *testing.T) {
		h := newRecoveryHarness(t, harnessOptions{}, fauxReply("done", ai.StopReasonStop, 0))
		replaced := h.session.CreateReplacedSessionContext(t.Context())
		cwd, err := replaced.CWD()
		if err != nil || cwd != h.session.CWD() {
			t.Errorf("CWD = %q, %v, want the session's %q", cwd, err, h.session.CWD())
		}
	})
}
