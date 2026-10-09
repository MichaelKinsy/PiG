package coding

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:1627-1631 — the manual compaction controller rejects prompts; an active run's automatic compaction still accepts queued input.
func TestSessionPromptDistinguishesManualAndAutomaticCompaction(t *testing.T) {
	t.Run("automatic run accepts steering", func(t *testing.T) {
		h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
		_, end := h.session.beginAgentRun(t.Context())
		defer end()
		if !h.session.beginCompaction() {
			t.Fatal("automatic compaction did not start")
		}
		defer h.session.finishCompaction()
		if err := h.session.Prompt(t.Context(), "queued", &PromptOptions{StreamingBehavior: extension.DeliverAsSteer}); err != nil {
			t.Fatal(err)
		}
		if pending := h.session.PendingMessageCount(); pending != 1 {
			t.Fatalf("pending=%d want1", pending)
		}
	})
	t.Run("manual compaction rejects prompt", func(t *testing.T) {
		h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
		if err := h.session.beginManualCompaction(t.Context()); err != nil {
			t.Fatal(err)
		}
		defer h.session.finishCompaction()
		err := h.session.Prompt(t.Context(), "queued", &PromptOptions{StreamingBehavior: extension.DeliverAsSteer})
		if !errors.Is(err, errPromptDuringCompaction) {
			t.Fatalf("error=%v", err)
		}
		if pending := h.session.PendingMessageCount(); pending != 0 {
			t.Fatalf("rejected pending=%d want0", pending)
		}
	})
}
