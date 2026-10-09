package extensionconformance

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// loaderAPI is the Go reference for the `pi` object packages/coding-agent/src/core/extensions/loader.ts:289-372 hands one extension: each
// registration writes the extension's own table (a JS Map, so re-registering a name replaces the entry and keeps its place), and the
// production Runner reads those tables. It embeds extension.API so only the members under test exist.
type loaderAPI struct {
	extension.API
	ext extension.Extension
}

func newLoaderAPI(path string) *loaderAPI {
	return &loaderAPI{ext: extension.Extension{
		Path:      path,
		Tools:     map[string]extension.RegisteredTool{},
		Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		Flags:     map[string]extension.ExtensionFlag{},

		MessageRenderers: map[string]extension.MessageRenderer{},
	}}
}

func (l *loaderAPI) RegisterTool(tool extension.ToolDefinition) { // loader.ts:289
	if _, replaced := l.ext.Tools[tool.Name]; !replaced {
		l.ext.ToolOrder = append(l.ext.ToolOrder, tool.Name)
	}
	l.ext.Tools[tool.Name] = extension.RegisteredTool{Definition: tool}
}

func (l *loaderAPI) RegisterShortcut(shortcut extension.KeyID, options extension.ShortcutOptions) { // loader.ts:326
	l.ext.Shortcuts[shortcut] = extension.ExtensionShortcut{Shortcut: shortcut, Description: options.Description, Handler: options.Handler, ExtensionPath: l.ext.Path}
}

func (l *loaderAPI) RegisterToolRenderer(resolver extension.ToolRendererResolver) { // loader.ts:366
	l.ext.ToolRenderers = append(l.ext.ToolRenderers, resolver)
}

// TestConformanceAPIRegisterToolShortcutAndToolRenderer: a tool, a shortcut and a tool renderer an extension registers through
// extension.API reach the production Runner the way Pi's runner reads them (types.ts:1641-1653, 1679-1685; loader.ts:289-372; runner.ts:630
// getAllRegisteredTools: a tool name is reported once and an extension's later registration of it replaces its earlier one in place, :673
// getShortcuts: a key keeps its last registration, :790 resolveToolRenderers: the resolvers run in registration order and next() falls through).
func TestConformanceAPIRegisterToolShortcutAndToolRenderer(t *testing.T) {
	t.Parallel()
	loader := newLoaderAPI("api-reference")
	var api extension.API = loader
	api.RegisterTool(extension.ToolDefinition{Name: "alpha", Description: "first"})
	api.RegisterTool(extension.ToolDefinition{Name: "beta", Description: "second"})
	api.RegisterTool(extension.ToolDefinition{Name: "alpha", Description: "replaced"})
	ran := ""
	api.RegisterShortcut("ctrl+shift+h", extension.ShortcutOptions{Description: "one", Handler: func(context.Context) error { ran = "one"; return nil }})
	api.RegisterShortcut("ctrl+shift+h", extension.ShortcutOptions{Description: "two", Handler: func(context.Context) error { ran = "two"; return nil }})
	api.RegisterToolRenderer(func(toolName string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
		if toolName == "alpha" {
			return &extension.ToolRenderers{RenderShell: "first"}
		}
		return next()
	})
	api.RegisterToolRenderer(func(toolName string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
		if toolName == "beta" {
			return &extension.ToolRenderers{RenderShell: "second"}
		}
		return next()
	})

	runner := inproc.NewRunner([]extension.Extension{loader.ext}, t.TempDir())

	tools := runner.Tools()
	if len(tools) != 2 || tools[0].Definition.Name != "alpha" || tools[0].Definition.Description != "replaced" || tools[1].Definition.Name != "beta" {
		t.Fatalf("tools %+v, want [alpha(replaced) beta]", tools)
	}
	shortcuts := runner.Shortcuts(nil)
	shortcut, ok := shortcuts["ctrl+shift+h"]
	if !ok || len(shortcuts) != 1 || shortcut.Description != "two" || shortcut.ExtensionPath != "api-reference" {
		t.Fatalf("shortcuts %+v, want one ctrl+shift+h with the last registration", shortcuts)
	}
	if err := shortcut.Handler(t.Context()); err != nil || ran != "two" {
		t.Fatalf("shortcut handler ran %q (%v), want the last registered handler", ran, err)
	}
	base := &extension.ToolRenderers{RenderShell: "base"}
	for tool, want := range map[string]string{"alpha": "first", "beta": "second"} {
		if got := runner.ResolveToolRenderers(tool, func() *extension.ToolRenderers { return base }); got == nil || string(got.RenderShell) != want {
			t.Errorf("%s renderers %+v, want %q", tool, got, want)
		}
	}
	if got := runner.ResolveToolRenderers("gamma", func() *extension.ToolRenderers { return base }); got != base {
		t.Errorf("gamma renderers %+v, want the base renderers through both resolvers' next()", got)
	}
}

func (l *loaderAPI) RegisterFlag(name string, options extension.FlagOptions) { // loader.ts:305
	if _, replaced := l.ext.Flags[name]; !replaced {
		l.ext.FlagOrder = append(l.ext.FlagOrder, name)
	}
	l.ext.Flags[name] = extension.ExtensionFlag{Name: name, Description: options.Description, Type: options.Type, Default: options.Default, ExtensionPath: l.ext.Path}
}

func (l *loaderAPI) RegisterMessageRenderer(customType string, renderer extension.MessageRenderer) { // loader.ts:336
	l.ext.MessageRenderers[customType] = renderer
}

func (l *loaderAPI) RegisterMarkdownTransformer(transformer extension.MarkdownTransformer) { // loader.ts:340
	l.ext.MarkdownTransformer = transformer
}

// TestConformanceAPIRegisterFlagMessageRendererAndMarkdownTransformer: a flag, a message renderer and a Markdown transformer an extension
// registers through extension.API reach the production Runner as runner.ts reads them (types.ts:1641-1685; loader.ts:305-345; runner.ts:653
// getFlags: a flag keeps its type, default and the extension that declared it, :775 getMessageRenderer: the renderer of that custom type
// and nothing for another, :785 getMarkdownTransformers: one transformer per extension, the last one it registered, in load order).
func TestConformanceAPIRegisterFlagMessageRendererAndMarkdownTransformer(t *testing.T) {
	t.Parallel()
	first, second := newLoaderAPI("first"), newLoaderAPI("second")
	var api, other extension.API = first, second
	api.RegisterFlag("verbose", extension.FlagOptions{Description: "be loud", Type: extension.FlagBoolean, Default: true})
	api.RegisterFlag("name", extension.FlagOptions{Type: extension.FlagString, Default: "x"})
	api.RegisterFlag("name", extension.FlagOptions{Description: "replaced", Type: extension.FlagString, Default: "y"})
	rendered := ""
	api.RegisterMessageRenderer("note", func(extension.CustomMessage, extension.MessageRenderOptions, extension.Theme) extension.Component {
		rendered = "first"
		return nil
	})
	api.RegisterMessageRenderer("note", func(extension.CustomMessage, extension.MessageRenderOptions, extension.Theme) extension.Component {
		rendered = "last"
		return nil
	})
	api.RegisterMarkdownTransformer(func(markdown string, _ extension.MarkdownTransformContext) string { return "1" + markdown })
	api.RegisterMarkdownTransformer(func(markdown string, _ extension.MarkdownTransformContext) string { return "2" + markdown })
	other.RegisterMarkdownTransformer(func(markdown string, _ extension.MarkdownTransformContext) string { return "3" + markdown })

	runner := inproc.NewRunner([]extension.Extension{first.ext, second.ext}, t.TempDir())

	flags := runner.Flags()
	verbose, name := flags["verbose"], flags["name"]
	if len(flags) != 2 || verbose.Type != extension.FlagBoolean || verbose.Default != true || verbose.Description != "be loud" || verbose.ExtensionPath != "first" {
		t.Fatalf("flags %+v, want verbose boolean true declared by first", flags)
	}
	if name.Default != "y" || name.Description != "replaced" {
		t.Errorf("flag name %+v, want the later registration", name)
	}
	renderer := runner.MessageRenderer("note")
	if renderer == nil {
		t.Fatal("no message renderer for note")
	}
	renderer(extension.CustomMessage{}, extension.MessageRenderOptions{}, nil)
	if rendered != "last" {
		t.Errorf("message renderer ran %q, want the last registered", rendered)
	}
	if runner.MessageRenderer("other") != nil {
		t.Error("a message renderer answered for a custom type nobody registered")
	}
	transformers := runner.GetMarkdownTransformers()
	if len(transformers) != 2 || transformers[0]("x", extension.MarkdownTransformContext{}) != "2x" || transformers[1]("x", extension.MarkdownTransformContext{}) != "3x" {
		t.Fatalf("markdown transformers %d, want [2x 3x]: the last of each extension in load order", len(transformers))
	}
}

// providerRegistryAPI is the Go reference for extension.API.RegisterProvider and UnregisterProvider: the production ModelRegistry a Session
// shares with its extensions (model-registry.ts registerProvider / unregisterProvider, which loader.ts reaches through runtime.registerProvider).
type providerRegistryAPI struct {
	extension.API
	registry *coding.ModelRegistry
	t        *testing.T
}

func (a providerRegistryAPI) RegisterProvider(name string, config extension.ProviderConfig) {
	if err := a.registry.RegisterExtensionProvider(name, config); err != nil {
		a.t.Fatal(err)
	}
}

// RegisterNativeProvider is loader.ts:456-464's Provider branch: the Provider object reaches the production registry (model-registry.ts:210-218
// registerProvider(provider), which hands it to runtime.registerNativeProvider).
func (a providerRegistryAPI) RegisterNativeProvider(provider *ai.ModelsProvider) {
	if err := a.registry.RegisterProviderObject(provider); err != nil {
		a.t.Fatal(err)
	}
}

func (a providerRegistryAPI) UnregisterProvider(name string) { a.registry.UnregisterProvider(name) }

// TestConformanceAPIRegisterAndUnregisterProvider pins pi.registerProvider(name, config) and pi.unregisterProvider(name)
// (packages/coding-agent/src/core/extensions/types.ts:1831, 1846) against the production ModelRegistry: the models a provider config
// declares appear in the registry under that provider, and unregistering the provider removes them again.
func TestConformanceAPIRegisterAndUnregisterProvider(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	registry := services.Registry()
	var api extension.API = providerRegistryAPI{registry: registry, t: t}
	has := func() bool {
		for _, entry := range registry.GetAll() {
			if entry.ProviderID() == "conformance-provider" && entry.ModelID() == "conformance-model" {
				return true
			}
		}
		return false
	}
	if has() {
		t.Fatal("the registry lists the provider before it is registered")
	}
	api.RegisterProvider("conformance-provider", extension.ProviderConfig{
		BaseURL: "http://127.0.0.1:9",
		APIKey:  "conformance-key",
		API:     "openai-completions",
		Models:  []extension.ProviderModelConfig{{ID: "conformance-model", Name: "Conformance Model", Input: []string{"text"}}},
	})
	if !has() {
		t.Fatal("registerProvider did not add conformance-model to the registry")
	}
	api.UnregisterProvider("conformance-provider")
	if has() {
		t.Error("unregisterProvider left conformance-model in the registry")
	}
}

// TestConformanceAPIRegisterProviderObject pins pi.registerProvider(provider: Provider) (types.ts:1830; loader.ts:456-464 hands the
// object to runtime.registerNativeProvider) against the production ModelRegistry: the provider's models appear in the registry under
// the provider's id, and unregistering the provider removes the models.
// Every SDK's registration of the same overload is driven through the production host by TestProviderObjectsAcrossSDKs.
func TestConformanceAPIRegisterProviderObject(t *testing.T) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	registry := services.Registry()
	var api extension.API = providerRegistryAPI{registry: registry, t: t}
	has := func() bool { return services.ModelRuntime().GetModel("native-conformance", "native-model") != nil }
	if has() {
		t.Fatal("the registry lists the provider before it is registered")
	}
	api.RegisterNativeProvider(&ai.ModelsProvider{
		ID:   "native-conformance",
		Name: "Native Conformance",
		GetModels: func() ([]*ai.Model, error) {
			return []*ai.Model{{ID: "native-model", DisplayName: "Native Model", Input: []string{"text"},
				ProviderMeta: ai.ProviderMetadata{ProviderID: "native-conformance", API: ai.APIOpenAICompletions, BaseURL: "http://127.0.0.1:9"},
				Capabilities: ai.ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100}}}, nil
		},
		Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "native", Check: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
			return &ai.AuthCheck{Type: ai.CredentialAPIKey, Source: "native"}, nil
		}}},
	})
	if !has() {
		t.Fatal("registerProvider(provider) did not add native-model to the registry")
	}
	api.UnregisterProvider("native-conformance")
	if has() {
		t.Error("unregisterProvider left native-model in the registry")
	}
}
