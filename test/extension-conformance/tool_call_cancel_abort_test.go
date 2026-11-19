package extensionconformance

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestConformance_NativeToolCallHandlerHonouringTheCancelEndsTheRunAfterTheAbort pins D650 claim 6. A Go, Python or Rust tool_call handler waits for the run's cancel
// signal (ctx.Done, is_cancelled). The host awaits it through the abort as Pi awaits a handler (runner.ts:1256-1274), forwards the abort to it as that signal, and the run ends
// once the handler has answered (the record measured about 305 ms after the abort).
func TestConformance_NativeToolCallHandlerHonouringTheCancelEndsTheRunAfterTheAbort(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	var cases []harnessCase
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		if slices.ContainsFunc([]string{"go", "python", "rust"}, func(l string) bool { return tc.name == "subprocess-"+l || tc.name == "subprocess-"+l+"-packed" }) {
			cases = append(cases, tc)
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			cwd := t.TempDir()
			if err := os.WriteFile(filepath.Join(cwd, "f.txt"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			configureFauxAuth(t, services)
			provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "faux-1"})
			provider.SetResponses([]ai.FauxResponseStep{
				ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxToolCall("read", map[string]any{"path": "f.txt"}, nil)}, ai.FauxAssistantMessageOptions{StopReason: string(ai.StopReasonToolUse)}),
				ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText("done")}, ai.FauxAssistantMessageOptions{}),
			})
			model := &ai.Model{ID: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 4000, MaxOutputTokens: 1000}}
			session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: h.runner, NoSession: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			coding.BindSessionExtensionActions(h.runner, h.bridge, func() *coding.Session { return session }, extension.ContextActions{}, nil)

			noticed := func(want string) bool { return slices.Contains(h.ui.Recorded(), want+":info") }
			runConformanceCommand(t, h, "tool_call_cancel_on")
			prompt := make(chan struct{})
			go func() {
				defer close(prompt)
				_ = session.Prompt(context.Background(), "go")
			}()
			pollUntilConformance(t, "the tool_call handler never started", func() bool { return noticed("cancel_wait") })
			if noticed("cancel_seen") {
				t.Fatal("the handler saw the cancel before the abort")
			}
			if err := session.Abort(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-prompt:
			case <-time.After(testbudget.Wait(t)):
				t.Fatalf("the run did not end after the abort although the handler honours the cancel (cancel seen: %v)", noticed("cancel_seen"))
			}
			if !noticed("cancel_seen") {
				t.Fatal("the run ended without the handler seeing the cancel")
			}
		})
	}
}
