package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// registrationReference is the Go reference for the registration members of extension.API: each RegisterX records what the extension registered on a
// loaded extension.Extension, the structure the production Runner reads its tools, commands, flags, shortcuts, renderers and transformers from.
type registrationReference struct {
	extension.API
	ext *extension.Extension
}

func (r *registrationReference) RegisterTool(tool extension.ToolDefinition) {
	if r.ext.Tools == nil {
		r.ext.Tools = map[string]extension.RegisteredTool{}
	}
	r.ext.Tools[tool.Name] = extension.RegisteredTool{Definition: tool, SourceInfo: r.ext.SourceInfo}
	r.ext.ToolOrder = append(r.ext.ToolOrder, tool.Name)
}

func (r *registrationReference) RegisterCommand(name string, options extension.CommandOptions) {
	if r.ext.Commands == nil {
		r.ext.Commands = map[string]extension.RegisteredCommand{}
	}
	r.ext.Commands[name] = extension.RegisteredCommand{Name: name, SourceInfo: r.ext.SourceInfo, Description: options.Description, GetArgumentCompletions: options.GetArgumentCompletions, Handler: options.Handler}
	r.ext.CommandOrder = append(r.ext.CommandOrder, name)
}

func (r *registrationReference) RegisterShortcut(shortcut extension.KeyID, options extension.ShortcutOptions) {
	if r.ext.Shortcuts == nil {
		r.ext.Shortcuts = map[extension.KeyID]extension.ExtensionShortcut{}
	}
	r.ext.Shortcuts[shortcut] = extension.ExtensionShortcut{Shortcut: shortcut, Description: options.Description, Handler: options.Handler, ExtensionPath: r.ext.Path}
}

func (r *registrationReference) RegisterFlag(name string, options extension.FlagOptions) {
	if r.ext.Flags == nil {
		r.ext.Flags = map[string]extension.ExtensionFlag{}
	}
	r.ext.Flags[name] = extension.ExtensionFlag{Name: name, Description: options.Description, Type: options.Type, Default: options.Default, ExtensionPath: r.ext.Path}
	r.ext.FlagOrder = append(r.ext.FlagOrder, name)
}

func (r *registrationReference) RegisterMessageRenderer(customType string, renderer extension.MessageRenderer) {
	if r.ext.MessageRenderers == nil {
		r.ext.MessageRenderers = map[string]extension.MessageRenderer{}
	}
	r.ext.MessageRenderers[customType] = renderer
}

func (r *registrationReference) RegisterEntryRenderer(customType string, renderer extension.EntryRenderer) {
	if r.ext.EntryRenderers == nil {
		r.ext.EntryRenderers = map[string]extension.EntryRenderer{}
	}
	r.ext.EntryRenderers[customType] = renderer
}

func (r *registrationReference) RegisterToolRenderer(resolver extension.ToolRendererResolver) {
	r.ext.ToolRenderers = append(r.ext.ToolRenderers, resolver)
}

func (r *registrationReference) RegisterMarkdownTransformer(transformer extension.MarkdownTransformer) {
	r.ext.MarkdownTransformer = transformer
}

// registrationView is what the production Runner reports about the registrations the conformance fixtures make: the SDKs and the Go reference register
// the same tool, command, flag, shortcut, renderers and Markdown transformer, so their views must be equal.
func registrationView(runner *inproc.Runner) map[string]string {
	view := map[string]string{}
	for _, tool := range runner.Tools() {
		if tool.Definition.Name == "render_probe" {
			view["tool"] = tool.Definition.Description
		}
	}
	for _, command := range runner.Commands() {
		if command.InvocationName == "/ping" || command.InvocationName == "ping" {
			view["command"] = command.Description
		}
	}
	if flag, ok := runner.Flags()["flag-string"]; ok {
		view["flag"] = fmt.Sprintf("%v/%v", flag.Type, flag.Default)
	}
	if shortcut, ok := runner.Shortcuts(nil)["ctrl+alt+y"]; ok {
		view["shortcut"] = shortcut.Description
	}
	view["messageRenderer"] = fmt.Sprint(runner.MessageRenderer("conformance-message") != nil)
	view["entryRenderer"] = fmt.Sprint(runner.EntryRenderer("conformance-entry") != nil)
	// A subprocess extension's resolver answers asynchronously: until it does, the runner returns the base renderers.
	base := &extension.ToolRenderers{}
	resolved := runner.ResolveToolRenderers("conformance_tool_renderer", func() *extension.ToolRenderers { return base })
	view["toolRenderer"] = fmt.Sprint(resolved != nil && resolved != base)
	var transformed []string
	for _, transformer := range runner.GetMarkdownTransformers() {
		transformed = append(transformed, transformer("probe", extension.MarkdownTransformContext{Context: context.Background(), MessageType: "assistant", IsStreaming: true, AvailableWidth: 33}))
	}
	view["markdown"] = strings.Join(transformed, "|")
	return view
}

// TestConformance_ExtensionAPIRegistrations pins Pi's pi.registerTool, registerCommand, registerShortcut, registerFlag, registerMessageRenderer,
// registerEntryRenderer, registerToolRenderer and registerMarkdownTransformer (packages/coding-agent/src/core/extensions/types.ts:1593-1690). Every SDK
// registers them while it loads, and the production Runner reports what each one registered; a Go reference of extension.API.RegisterX that registers the
// same things on a loaded extension must produce the same report, field for field.
func TestConformance_ExtensionAPIRegistrations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	reference := extension.Extension{Path: "go-reference", ResolvedPath: "go-reference"}
	var api extension.API = &registrationReference{ext: &reference}
	api.RegisterTool(extension.ToolDefinition{Name: "render_probe", Label: "render_probe", Description: "Render its own tool card", Parameters: json.RawMessage(`{"type":"object"}`)})
	api.RegisterCommand("ping", extension.CommandOptions{Description: "Respond with pong", Handler: func(context.Context, string) error { return nil }})
	api.RegisterFlag("flag-string", extension.FlagOptions{Type: extension.FlagString, Default: "default"})
	api.RegisterShortcut("ctrl+alt+y", extension.ShortcutOptions{Description: "Conformance shortcut", Handler: func(context.Context) error { return nil }})
	api.RegisterMessageRenderer("conformance-message", func(extension.CustomMessage, extension.MessageRenderOptions, extension.Theme) extension.Component {
		return nil
	})
	api.RegisterEntryRenderer("conformance-entry", func(extension.CustomEntry, extension.EntryRenderOptions, extension.Theme) extension.Component {
		return nil
	})
	api.RegisterToolRenderer(func(tool string, _ func() *extension.ToolRenderers) *extension.ToolRenderers {
		if tool == "conformance_tool_renderer" {
			return &extension.ToolRenderers{}
		}
		return nil
	})
	api.RegisterMarkdownTransformer(func(markdown string, context extension.MarkdownTransformContext) string {
		return fmt.Sprintf("md:%s:%s:streaming=%t:width=%d", markdown, context.MessageType, context.IsStreaming, context.AvailableWidth)
	})
	want := registrationView(inproc.NewRunner([]extension.Extension{reference}, t.TempDir()))
	if want["tool"] != "Render its own tool card" || want["flag"] != "string/default" || want["markdown"] != "md:probe:assistant:streaming=true:width=33" {
		t.Fatalf("the reference view = %v", want)
	}

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
			var got map[string]string
			waitFor(t, func() bool {
				got = registrationView(h.runner)
				return got["toolRenderer"] == want["toolRenderer"]
			})
			for key, value := range want {
				if got[key] != value {
					t.Errorf("%s: the SDK registered %q, the Go reference %q", key, got[key], value)
				}
			}
		})
	}
}
