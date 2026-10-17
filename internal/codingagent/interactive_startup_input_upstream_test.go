package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestUpstreamInteractiveStartupInput(t *testing.T) {
	t.Run("restores a prompt submitted while managed-tool setup is running", func(t *testing.T) {
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-startup-input.test.ts:57
		m := &InteractiveMode{editor: tui.NewEditor(), chatContainer: tui.NewContainer()}
		m.handleStartupSubmit("early prompt")
		if got := m.editor.Text(); got != "early prompt" {
			t.Fatalf("setText=%q", got)
		}
		// Upstream asserts the complete showStatus argument; a substring would accept extra diagnostic text.
		status := tui.NewPaddedText(tui.ActiveTheme().Fg("dim", "Startup is still in progress"), 1, 0, nil)
		want := append(tui.NewSpacer(1).Render(100), status.Render(100)...)
		if got := m.chatContainer.Render(100); !slices.Equal(got, want) {
			t.Fatalf("showStatus=%q, want %q", got, want)
		}
	})
	t.Run("queues a normal prompt submitted before the input callback is installed", func(t *testing.T) {
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-startup-input.test.ts:69
		m := &InteractiveMode{editor: tui.NewEditor()}
		original := flushPendingBashComponents
		t.Cleanup(func() { flushPendingBashComponents = original })
		flushes := 0
		flushPendingBashComponents = func(mode *InteractiveMode) {
			if mode != m {
				t.Fatal("wrong submit context")
			}
			flushes++
			original(mode)
		}
		m.setupEditorSubmitHandler(t.Context())
		m.editor.OnSubmit(" early prompt ")
		if !slices.Equal(m.pendingUserInputs, []string{"early prompt"}) {
			t.Fatalf("pendingUserInputs=%q", m.pendingUserInputs)
		}
		if flushes != 1 {
			t.Fatalf("flushPendingBashComponents calls=%d, want 1", flushes)
		}
		m.editor.HandleInput("\x1b[A")
		if got := m.editor.Text(); got != "early prompt" {
			t.Fatalf("addToHistory=%q", got)
		}
	})
	t.Run("returns queued startup input before installing a new input callback", func(t *testing.T) {
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-startup-input.test.ts:81
		m := &InteractiveMode{pendingUserInputs: []string{"queued prompt"}}
		select {
		case got := <-m.getUserInput():
			if got != "queued prompt" {
				t.Fatalf("getUserInput=%q", got)
			}
		default:
			t.Fatal("queued input did not resolve immediately")
		}
		if m.onInputCallback != nil {
			t.Fatal("onInputCallback installed despite queued input")
		}
		if len(m.pendingUserInputs) != 0 {
			t.Fatalf("pendingUserInputs=%q", m.pendingUserInputs)
		}
	})
}
