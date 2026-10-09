package extensionconformance

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestRegisterRenderersAcrossSDKs: what an extension registers with ExtensionAPI.registerMessageRenderer
// (packages/coding-agent/src/core/extensions/types.ts:1676), registerMarkdownTransformer
// (packages/coding-agent/src/core/extensions/types.ts:1679) and registerEntryRenderer
// (packages/coding-agent/src/core/extensions/types.ts:1682) renders the host's message, entry and Markdown in every SDK, with
// the render options and width the host passes. The width and collapsed options differ from the common recording's, so a
// renderer that ignores them fails.
func TestRegisterRenderersAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	const width = 41
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})

			registerMessageRenderer := h.runner.MessageRenderer("conformance-message")
			if registerMessageRenderer == nil {
				t.Fatal("registerMessageRenderer: conformance-message has no renderer")
			}
			message, ok := registerMessageRenderer(extension.CustomMessage{CustomType: "conformance-message", Content: "second-message", Display: true}, extension.MessageRenderOptions{}, nil).(interface{ Render(int) []string })
			if !ok {
				t.Fatal("registerMessageRenderer: the renderer returned no component")
			}
			wantMessage := []string{"renderer:second-message:expanded=false:width=41"}
			var gotMessage []string
			waitFor(t, func() bool { gotMessage = message.Render(width); return slices.Equal(gotMessage, wantMessage) })

			registerEntryRenderer := h.runner.EntryRenderer("conformance-entry")
			if registerEntryRenderer == nil {
				t.Fatal("registerEntryRenderer: conformance-entry has no renderer")
			}
			entry, ok := registerEntryRenderer(extension.CustomEntry{CustomType: "conformance-entry", Data: "second-entry"}, extension.EntryRenderOptions{}, nil).(interface{ Render(int) []string })
			if !ok {
				t.Fatal("registerEntryRenderer: the renderer returned no component")
			}
			wantEntry := []string{"entryrenderer:second-entry:expanded=false:width=41"}
			var gotEntry []string
			waitFor(t, func() bool { gotEntry = entry.Render(width); return slices.Equal(gotEntry, wantEntry) })

			registerMarkdownTransformer := h.runner.GetMarkdownTransformers()
			if len(registerMarkdownTransformer) != 1 {
				t.Fatalf("registerMarkdownTransformer: %d transformers, want the fixture's one", len(registerMarkdownTransformer))
			}
			ctx := extension.MarkdownTransformContext{Context: t.Context(), MessageType: extension.MarkdownMessageAssistant, IsStreaming: true, AvailableWidth: width}
			if got, want := registerMarkdownTransformer[0]("# Title", ctx), "md:# Title:assistant:streaming=true:width=41"; got != want {
				t.Fatalf("registerMarkdownTransformer: got %q, want %q", got, want)
			}
		})
	}
}
