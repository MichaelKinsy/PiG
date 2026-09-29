package codingagent

import "testing"

func TestBareBashPrefixesAreNormalPrompts(t *testing.T) {
	// Pi interactive-mode.ts setupEditorSubmitHandler returns from the !/!! branch only when the stripped command is nonempty. Bare prefixes are ordinary model input, including while streaming.
	m, ctx, _ := newStreamingRoutingMode(t)
	onLoop(m, ctx, func() {
		m.setupEditorSubmitHandler(ctx)
		for _, text := range []string{"!", "!!"} {
			m.editor.SetText(text)
			if err := m.dispatchKey(ctx, "\r"); err != nil {
				t.Error(err)
			}
		}
		steering, _ := m.agent.PendingMessages()
		want := []string{"!", "!!"}
		if len(steering) != len(want) {
			t.Errorf("bare-prefix steering=%v, want %q", steering, want)
			return
		}
		for i, text := range want {
			if got := extractAgentMessageText(steering[i]); got != text {
				t.Errorf("steer[%d]=%q, want %q", i, got, text)
			}
		}
	})
}
