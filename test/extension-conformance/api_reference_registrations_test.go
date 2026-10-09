package extensionconformance

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_RegistrationsAcrossSDKs pins Pi's pi.registerCommand, registerTool, registerFlag, registerShortcut,
// registerMessageRenderer, registerMarkdownTransformer and registerEntryRenderer (packages/coding-agent/src/core/extensions/types.ts:1599-1653
// and :1670, :1736). Every SDK fixture registers the same command, tool, flag, shortcut, renderers and markdown transformer
// (testfixture/fixture.go, main.mjs, main.rs, main.py), and the production Runner must list them by name with the flag's type and
// default, the shortcut's description and the transformer's answer. The fixtures describe their `echo` tool differently, so the tool is
// matched by name.
func TestConformance_RegistrationsAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	const (
		commandName        = "ping"
		commandDescription = "Respond with pong"
		toolName           = "echo"
		flagName           = "flag-string"
		flagDefault        = "default"
		shortcutKey        = "ctrl+alt+y"
		shortcutText       = "Conformance shortcut"
		messageRenderer    = "conformance-message"
		entryRenderer      = "conformance-entry"
		wantMarkdown       = "md:hello *md*:assistant:streaming=true:width=72"
	)
	for want, member := range map[string]any{
		"RegisterCommand":             extension.API.RegisterCommand,
		"RegisterTool":                extension.API.RegisterTool,
		"RegisterFlag":                extension.API.RegisterFlag,
		"RegisterShortcut":            extension.API.RegisterShortcut,
		"RegisterMessageRenderer":     extension.API.RegisterMessageRenderer,
		"RegisterMarkdownTransformer": extension.API.RegisterMarkdownTransformer,
		"RegisterEntryRenderer":       extension.API.RegisterEntryRenderer,
	} {
		requireAPIMember(t, want, member)
	}
	transformContext := extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, IsStreaming: true, AvailableWidth: 72}

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
			pollUntilConformance(t, 5*time.Second, "the host never listed the registered command "+commandName, func() bool {
				for _, c := range h.runner.Commands() {
					if c.Name == commandName && c.Description == commandDescription {
						return true
					}
				}
				return false
			})
			listed := false
			for _, registered := range h.runner.Tools() {
				if registered.Definition.Name == toolName {
					listed = true
				}
			}
			if !listed {
				t.Fatalf("tool %s is not listed: %+v", toolName, h.runner.Tools())
			}
			if got, ok := h.runner.Flags()[flagName]; !ok || got.Type != extension.FlagString || got.Default != flagDefault {
				t.Fatalf("flag %s = %+v (listed %v), want type %s default %v", flagName, got, ok, extension.FlagString, flagDefault)
			}
			if got, ok := h.runner.Shortcuts(map[string][]string{})[extension.KeyID(shortcutKey)]; !ok || got.Description != shortcutText {
				t.Fatalf("shortcut %s = %+v (listed %v), want description %q", shortcutKey, got, ok, shortcutText)
			}
			transformers := h.runner.GetMarkdownTransformers()
			if len(transformers) != 1 {
				t.Fatalf("markdown transformers = %d, want 1", len(transformers))
			}
			// A subprocess transformer answers off the render loop: the first call keeps the input.
			var got string
			pollUntilConformance(t, 5*time.Second, "the SDK's markdown transformer never answered", func() bool {
				got = transformers[0]("hello *md*", transformContext)
				return got != "hello *md*"
			})
			if got != wantMarkdown {
				t.Fatalf("markdown transform = %q, want %q", got, wantMarkdown)
			}
			if h.runner.MessageRenderer(messageRenderer) == nil {
				t.Fatalf("message renderer %s is not registered", messageRenderer)
			}
			if h.runner.EntryRenderer(entryRenderer) == nil {
				t.Fatalf("entry renderer %s is not registered", entryRenderer)
			}
		})
	}
}
