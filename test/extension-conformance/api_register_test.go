package extensionconformance

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// extensionFromRegistrations is what the loader builds from the registrations an extension makes through extension.API (loader.ts:289-372): the tool, command, flag, shortcut, renderer and transformer tables the runner reads.
func extensionFromRegistrations(f *extensiontest.Fake) extension.Extension {
	ext := extension.Extension{
		Path:             "api-reference",
		Tools:            map[string]extension.RegisteredTool{},
		Commands:         map[string]extension.RegisteredCommand{},
		Flags:            map[string]extension.ExtensionFlag{},
		Shortcuts:        map[extension.KeyID]extension.ExtensionShortcut{},
		MessageRenderers: map[string]extension.MessageRenderer{},
		EntryRenderers:   map[string]extension.EntryRenderer{},
		ToolRenderers:    f.RegisterToolRenderers,
	}
	for _, tool := range f.RegisterToolCalls {
		ext.Tools[tool.Name] = extension.RegisteredTool{Definition: tool}
		ext.ToolOrder = append(ext.ToolOrder, tool.Name)
	}
	for _, call := range f.RegisterCommandCalls {
		ext.Commands[call.Name] = extension.RegisteredCommand{Name: call.Name, Description: call.Options.Description, Handler: call.Options.Handler}
		ext.CommandOrder = append(ext.CommandOrder, call.Name)
	}
	for _, call := range f.RegisterFlagCalls {
		ext.Flags[call.Name] = extension.ExtensionFlag{Name: call.Name, Description: call.Options.Description, Type: call.Options.Type, Default: call.Options.Default, ExtensionPath: ext.Path}
		ext.FlagOrder = append(ext.FlagOrder, call.Name)
	}
	for _, call := range f.RegisterShortcutCalls {
		ext.Shortcuts[call.Shortcut] = extension.ExtensionShortcut{Shortcut: call.Shortcut, Description: call.Options.Description, Handler: call.Options.Handler, ExtensionPath: ext.Path}
	}
	for _, call := range f.RegisterMessageRendererCalls {
		ext.MessageRenderers[call.CustomType] = call.Renderer
	}
	for _, call := range f.RegisterEntryRendererCalls {
		ext.EntryRenderers[call.CustomType] = call.Renderer
	}
	if n := len(f.RegisterMarkdownTransformers); n > 0 {
		ext.MarkdownTransformer = f.RegisterMarkdownTransformers[n-1]
	}
	return ext
}

// TestConformanceAPIRegisterToolCommandFlagShortcut is the Go reference for pi.registerTool, pi.registerCommand, pi.registerFlag and pi.registerShortcut (extensions/types.ts:1641-1653, loader.ts:289-345): what an extension registers through extension.API is what the runner reports, in registration order (runner.ts:630 getAllRegisteredTools, :653 getFlags, :673 getShortcuts, :847 getRegisteredCommands).
//
// mutation-checked: the mutant "Runner.Tools reports only the first registered tool" fails it.
// mutation-checked: the mutant "Runner.Flags drops every flag" fails it.
func TestConformanceAPIRegisterToolCommandFlagShortcut(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	api.RegisterTool(extension.ToolDefinition{Name: "alpha", Label: "Alpha", Description: "first tool"})
	api.RegisterTool(extension.ToolDefinition{Name: "beta", Label: "Beta", Description: "second tool"})
	ran := ""
	api.RegisterCommand("hello", extension.CommandOptions{Description: "says hello", Handler: func(_ context.Context, args string) error { ran = args; return nil }})
	api.RegisterFlag("verbose", extension.FlagOptions{Description: "be loud", Type: extension.FlagBoolean, Default: true})
	api.RegisterShortcut("ctrl+shift+h", extension.ShortcutOptions{Description: "help", Handler: func(context.Context) error { ran = "shortcut"; return nil }})

	runner := inproc.NewRunner([]extension.Extension{extensionFromRegistrations(fake)}, t.TempDir())

	var names []string
	for _, tool := range runner.Tools() {
		names = append(names, tool.Definition.Name)
	}
	if len(names) != 2 || names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("tools %v, want [alpha beta] in registration order", names)
	}
	commands := runner.Commands()
	if len(commands) != 1 || commands[0].InvocationName != "hello" || commands[0].Description != "says hello" {
		t.Fatalf("commands %+v, want the hello command", commands)
	}
	if err := commands[0].Handler(t.Context(), "world"); err != nil || ran != "world" {
		t.Fatalf("command handler ran with %q (%v), want the registered handler to receive the arguments", ran, err)
	}
	flag, ok := runner.Flags()["verbose"]
	if !ok || flag.Type != extension.FlagBoolean || flag.Default != true || flag.Description != "be loud" || flag.ExtensionPath != "api-reference" {
		t.Fatalf("flags %+v, want the verbose boolean flag attributed to api-reference", runner.Flags())
	}
	shortcut, ok := shortcutsByKey(runner.Shortcuts(nil))["ctrl+shift+h"]
	if !ok || shortcut.Description != "help" {
		t.Fatalf("shortcuts %+v, want ctrl+shift+h", shortcutsByKey(runner.Shortcuts(nil)))
	}
}

// TestConformanceAPIRegisterRenderersAndTransformer is the Go reference for pi.registerMessageRenderer, pi.registerEntryRenderer, pi.registerToolRenderer and pi.registerMarkdownTransformer (extensions/types.ts:1679-1685, loader.ts:356-372): the runner hands out the renderer registered for a custom type (runner.ts:775 getMessageRenderer, :797 getEntryRenderer), the Markdown transformers in load order (:785) and a tool renderer resolver ahead of the base renderers (:790 resolveToolRenderers).
//
// mutation-checked: the mutant "Runner.MessageRenderer always returns nil" fails it.
// mutation-checked: the mutant "Runner.ResolveToolRenderers ignores the extension resolvers" fails it.
func TestConformanceAPIRegisterRenderersAndTransformer(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	rendered := ""
	api.RegisterMessageRenderer("note", func(extension.CustomMessage, extension.MessageRenderOptions, extension.Theme) extension.Component {
		rendered = "message"
		return nil
	})
	api.RegisterEntryRenderer("audit", func(extension.CustomEntry, extension.EntryRenderOptions, extension.Theme) extension.Component {
		rendered = "entry"
		return nil
	})
	api.RegisterMarkdownTransformer(func(markdown string, _ extension.MarkdownTransformContext) string { return "<" + markdown + ">" })
	api.RegisterToolRenderer(func(toolName string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
		if toolName == "alpha" {
			return &extension.ToolRenderers{RenderShell: "self"}
		}
		return next()
	})

	runner := inproc.NewRunner([]extension.Extension{extensionFromRegistrations(fake)}, t.TempDir())

	if renderer := runner.MessageRenderer("note"); renderer == nil {
		t.Fatal("no message renderer for note")
	} else if renderer(extension.CustomMessage{}, extension.MessageRenderOptions{}, nil); rendered != "message" {
		t.Fatalf("message renderer not the registered one: %q", rendered)
	}
	if runner.MessageRenderer("other") != nil {
		t.Fatal("a message renderer answered for a type nobody registered")
	}
	if renderer := runner.EntryRenderer("audit"); renderer == nil {
		t.Fatal("no entry renderer for audit")
	} else if renderer(extension.CustomEntry{}, extension.EntryRenderOptions{}, nil); rendered != "entry" {
		t.Fatalf("entry renderer not the registered one: %q", rendered)
	}
	transformers := runner.GetMarkdownTransformers()
	if len(transformers) != 1 || transformers[0]("x", extension.MarkdownTransformContext{}) != "<x>" {
		t.Fatalf("markdown transformers %d, want the registered one", len(transformers))
	}
	base := &extension.ToolRenderers{RenderShell: "base"}
	if got := runner.ResolveToolRenderers("alpha", func() *extension.ToolRenderers { return base }); got == nil || got.RenderShell != "self" {
		t.Fatalf("alpha renderers %+v, want the extension's resolver", got)
	}
	if got := runner.ResolveToolRenderers("beta", func() *extension.ToolRenderers { return base }); got != base {
		t.Fatalf("beta renderers %+v, want the base renderers through next()", got)
	}
}

// shortcutsByKey looks entries of Runner.Shortcuts up by key, as Pi's Map.get does.
func shortcutsByKey(entries []inproc.ShortcutEntry) map[extension.KeyID]extension.ExtensionShortcut {
	out := make(map[extension.KeyID]extension.ExtensionShortcut, len(entries))
	for _, entry := range entries {
		out[entry.Key] = entry.Shortcut
	}
	return out
}
