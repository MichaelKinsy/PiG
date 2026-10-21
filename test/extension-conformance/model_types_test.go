package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/modeltypes"
)

// The typed model operations of Pi 0.99.1 for every SDK against the real subprocess Host: ctx.modelRegistry's typed reads, getAvailableOfType, classify and registerVirtualModel (.upstream/v0.99.1/packages/coding-agent/src/core/model-registry.ts:71-79,135-177), and the image and classifier implementations of a provider config and of a Provider object (core/extensions/types.ts:1896-1898, core/provider-composer.ts:632-663).
//
// The Go fixture (testfixture/modeltypes), the Rust fixture (testdata/modeltypes-rust) and the Python fixture below carry the same tools, so one table of rows proves each SDK. Every asserted value is one the host callbacks or the fixture's own logic produce and no SDK fallback can: the host's state, the reverse of the question order, the length of the state's text, the key the host resolved. An SDK that never makes the call, or answers a constant, fails.

var modelTypesChat = []map[string]any{
	{"id": "gpt", "provider": "openai", "api": "openai-completions"},
	{"id": "opus", "provider": "anthropic", "api": "anthropic-messages"},
}

var modelTypesTyped = []map[string]any{
	{"type": "image", "id": "flux", "provider": "openrouter", "api": "openrouter-images", "output": []string{"image"}},
	{"type": "classifier", "id": "jev-latest", "provider": "typesafe", "api": "typesafe-system-one", "contextWindow": 64000},
	{"type": "classifier", "id": "~typesafe/jev-latest", "provider": "openrouter", "api": "typesafe-system-one", "contextWindow": 64000},
}

type providerRegistration struct {
	Name   string
	Config extension.ProviderConfig
}

type classifyCall struct {
	Model   map[string]any
	Request json.RawMessage
	Options map[string]any
}

// modelTypesRig is a host whose callbacks answer from the tables above and record what the extension sent.
type modelTypesRig struct {
	t      *testing.T
	host   *subprocess.Host
	bridge *subprocess.UIBridge
	ext    extension.Extension
	dir    string
	// keepLoaded makes a placement that loads its members with a reload (python shared-ok) load them with LoadAll instead, so an extension loaded before the fixture stays: a reload removes the extensions its config loader does not name.
	keepLoaded bool

	mu        sync.Mutex
	providers map[string]extension.ProviderConfig
	// registrations lists each provider registration the host applied, in order.
	registrations []providerRegistration
	natives       map[string]*extension.NativeProvider
	classify      []classifyCall
	images        []classifyCall
	virtual       []string
	// virtualModels are the virtual models the host holds, in registration order: the registry's catalog lists them, as Pi's does (virtual-models.ts withVirtualModels).
	virtualModels []map[string]any
	available     []string
	theme         any
	keybindings   any
	capabilities  subprocess.TerminalCapabilitiesPayload
}

// setTheme is the palette the host publishes with its state before each call.
func (r *modelTypesRig) setTheme(palette any) {
	r.mu.Lock()
	r.theme = palette
	r.mu.Unlock()
}

// setTuiState is the keybinding table and terminal capabilities the host publishes with its state; the next state push carries them to the extension's pi-tui.
func (r *modelTypesRig) setTuiState(keybindings any, capabilities subprocess.TerminalCapabilitiesPayload) {
	r.mu.Lock()
	r.keybindings, r.capabilities = keybindings, capabilities
	r.mu.Unlock()
	r.bridge.SetKeybindingsFunc(func() any {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.keybindings
	})
	r.bridge.SetTerminalCapabilitiesFunc(func() subprocess.TerminalCapabilitiesPayload {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.capabilities
	})
	r.host.BroadcastStateUpdate()
}

func newModelTypesRig(t *testing.T) *modelTypesRig {
	t.Helper()
	r := &modelTypesRig{t: t, dir: t.TempDir(), providers: map[string]extension.ProviderConfig{}, natives: map[string]*extension.NativeProvider{}}
	r.host = subprocess.NewHostWithConfigRoot(r.dir, t.TempDir())
	t.Cleanup(func() { r.host.Shutdown("test complete") })
	r.bridge = subprocess.NewUIBridge(func() {})
	r.host.SetUIBridge(r.bridge)
	// The Node runtime runs a setWidget component factory only in the interactive mode (rpc-mode.ts ignores it), so the rig reports the mode its widget rows are for.
	r.host.SetMode(conformanceMode)
	r.bridge.SetThemeFunc(func() any {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.theme
	})
	r.bridge.SetActions(&subprocess.HostCallbacks{
		// The catalog the ready frame carries is the one the registry state publishes; the Node runtime installs it from either.
		GetModels: func() []json.RawMessage { return modelJSONs(r.chatModels()...) },
		GetModel: func(provider, id string) json.RawMessage {
			for _, model := range r.chatModels() {
				if model["provider"] == provider && model["id"] == id {
					return modelJSON(model)
				}
			}
			return nil
		},
		GetModelRegistryState: func() map[string]any {
			return map[string]any{
				"models": r.chatModels(), "typedModels": modelTypesTyped,
				"providers":  map[string]any{"typesafe": map[string]any{"name": "TypeSafe", "configured": true}},
				"registered": []any{},
			}
		},
		GetAvailableOfType: func(_ context.Context, modelType, provider string) ([]json.RawMessage, error) {
			r.mu.Lock()
			r.available = append(r.available, modelType+"/"+provider)
			r.mu.Unlock()
			var models []json.RawMessage
			for _, model := range modelTypesTyped {
				if model["type"] == modelType && model["provider"] == "typesafe" && (provider == "" || provider == "typesafe") {
					models = append(models, modelJSON(model))
				}
			}
			return models, nil
		},
		Classify: func(_ context.Context, model map[string]any, request json.RawMessage, options map[string]any) (json.RawMessage, error) {
			r.mu.Lock()
			r.classify = append(r.classify, classifyCall{model, request, options})
			r.mu.Unlock()
			// The answers are the questions' in reverse; the probability is the state's length plus ten per image, so only a request the SDK built from the caller's context, images included, produces them.
			var context struct {
				State     map[string]any    `json:"state"`
				Images    []json.RawMessage `json:"images"`
				Questions json.RawMessage
			}
			if err := json.Unmarshal(request, &context); err != nil {
				return nil, err
			}
			keys := orderedKeys(context.Questions)
			answers := make([]string, 0, len(keys))
			for _, key := range slices.Backward(keys) {
				answers = append(answers, fmt.Sprintf("%q:{\"type\":\"bool\",\"probability\":%v}", key, float64(len(fmt.Sprint(context.State["text"]))+10*len(context.Images))/100))
			}
			return json.RawMessage(fmt.Sprintf(`{"api":%q,"provider":%q,"model":%q,"answers":{%s},"stopReason":"stop","timestamp":3}`, model["api"], model["provider"], model["id"], strings.Join(answers, ","))), nil
		},
	})
	r.bridge.SetHostAction("generateImages", func(_ context.Context, model map[string]any, request json.RawMessage, options map[string]any) (json.RawMessage, error) {
		r.mu.Lock()
		r.images = append(r.images, classifyCall{model, request, options})
		r.mu.Unlock()
		if model["id"] != "flux" {
			return nil, fmt.Errorf("no image model %v/%v", model["provider"], model["id"])
		}
		// The image data names the model, the prompt and the API key option, so only a request the SDK built from the caller's arguments produces it.
		var context struct {
			Input []struct {
				Text string `json:"text"`
			} `json:"input"`
		}
		if err := json.Unmarshal(request, &context); err != nil {
			return nil, fmt.Errorf("bad images context %s: %w", request, err)
		}
		if len(context.Input) == 0 {
			return nil, fmt.Errorf("images context %s has no input", request)
		}
		data := fmt.Sprintf("img:%v:%s:%v", model["id"], context.Input[0].Text, options["apiKey"])
		return json.Marshal(map[string]any{"api": model["api"], "provider": model["provider"], "model": model["id"], "output": []any{map[string]any{"type": "image", "data": data, "mimeType": "image/png"}}, "stopReason": "stop", "timestamp": 4})
	})
	r.host.SetProviderCallbacks(r.recordProvider, func(string) {})
	r.host.SetNativeProviderCallback(func(_ context.Context, provider *extension.NativeProvider) error {
		r.mu.Lock()
		r.natives[provider.ID] = provider
		r.mu.Unlock()
		return nil
	})
	return r
}

// chatModels is the catalog the host answers with: the chat models and the virtual models registered so far.
func (r *modelTypesRig) chatModels() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append(slices.Clone(modelTypesChat), r.virtualModels...)
}

// recordProvider is the registry's RegisterProvider: it keeps the config the host applied.
func (r *modelTypesRig) recordProvider(name string, config extension.ProviderConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = config
	r.registrations = append(r.registrations, providerRegistration{name, config})
	return nil
}

// orderedKeys reads the keys of a JSON object in order.
func orderedKeys(object json.RawMessage) []string {
	decoder := json.NewDecoder(strings.NewReader(string(object)))
	if _, err := decoder.Token(); err != nil {
		return nil
	}
	var keys []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return keys
		}
		keys = append(keys, key.(string))
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}

// finish binds a runner the way a session binds it, so the host's virtual model calls have a registry to reach.
func (r *modelTypesRig) finish(ext extension.Extension) {
	r.t.Helper()
	r.ext = ext
	runner := inproc.NewRunner([]extension.Extension{ext}, r.dir, r.host.Runtime())
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, &extension.ProviderActions{
		RegisterProvider:   r.recordProvider,
		UnregisterProvider: func(string) {},
		RegisterVirtualModel: func(definition extension.VirtualModelDefinition) error {
			if definition.ID == "claimed" {
				return fmt.Errorf("virtual model %s/%s is the id of a physical model", definition.Provider, definition.ID)
			}
			r.mu.Lock()
			r.virtual = append(r.virtual, "register "+definition.Provider+"/"+definition.ID)
			r.virtualModels = slices.DeleteFunc(r.virtualModels, func(model map[string]any) bool {
				return model["provider"] == definition.Provider && model["id"] == definition.ID
			})
			r.virtualModels = append(r.virtualModels, map[string]any{"id": definition.ID, "provider": definition.Provider, "name": definition.Name, "api": "pi-virtual", "baseUrl": "", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": definition.ContextWindow, "maxTokens": definition.MaxTokens})
			r.mu.Unlock()
			return nil
		},
		UnregisterVirtualModel: func(provider, id string) {
			r.mu.Lock()
			r.virtual = append(r.virtual, "unregister "+provider+"/"+id)
			r.virtualModels = slices.DeleteFunc(r.virtualModels, func(model map[string]any) bool { return model["provider"] == provider && model["id"] == id })
			r.mu.Unlock()
		},
	})
}

// tool runs one of the fixture's tools and decodes the JSON it answers.
func (r *modelTypesRig) tool(name string) map[string]any {
	r.t.Helper()
	return r.toolWith(name, map[string]any{})
}

// toolWith runs one of the fixture's tools with arguments.
func (r *modelTypesRig) toolWith(name string, args map[string]any) map[string]any {
	r.t.Helper()
	def, ok := r.ext.Tools[name]
	if !ok {
		r.t.Fatalf("the fixture has no tool %q (tools: %v)", name, r.ext.ToolOrder)
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		r.t.Fatal(err)
	}
	// A row whose extension never answers (a mutated SDK that left a request unserved) fails here instead of holding the package to its test timeout.
	ctx, cancel := context.WithTimeout(r.t.Context(), 45*time.Second)
	defer cancel()
	result, err := def.Definition.Execute(ctx, "call-"+name, encoded, nil)
	if err != nil {
		r.t.Fatalf("%s: %v", name, err)
	}
	typed, ok := result, true
	if !ok {
		r.t.Fatalf("%s returned %T", name, result)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(typed.Text()), &report); err != nil {
		r.t.Fatalf("%s = %q: %v", name, typed.Text(), err)
	}
	return report
}

// modelTypesLoader loads one SDK's fixture in one placement into the rig.
type modelTypesLoader func(t *testing.T, r *modelTypesRig)

type modelTypesSDK struct {
	name       string
	placements map[string]modelTypesLoader
}

func modelTypesSDKs() []modelTypesSDK {
	return []modelTypesSDK{
		{"go", map[string]modelTypesLoader{"fused": loadModelTypesGo("fused"), "strict": loadModelTypesGo("strict"), "packed": loadModelTypesGo("packed")}},
		{"rust", map[string]modelTypesLoader{"isolated": loadModelTypesRust(false), "packed": loadModelTypesRust(true)}},
		{"python", map[string]modelTypesLoader{"strict": loadModelTypesPython("strict"), "shared-ok": loadModelTypesPython("shared-ok")}},
		{"node", map[string]modelTypesLoader{"isolated": loadModelTypesNode(false), "packed": loadModelTypesNode(true)}},
	}
}

func eachModelTypesPlacement(t *testing.T, body func(t *testing.T, r *modelTypesRig)) {
	t.Helper()
	eachModelTypesPlacementOf(t, nil, body)
}

// eachModelTypesPlacementOf runs body for every placement of the named SDKs, or of every SDK when sdks is empty.
func eachModelTypesPlacementOf(t *testing.T, sdks []string, body func(t *testing.T, r *modelTypesRig)) {
	t.Helper()
	eachModelTypesPlacementAfter(t, sdks, nil, body)
}

// eachModelTypesPlacementAfter is eachModelTypesPlacementOf with a step that runs on each fresh rig before the fixture loads: it loads the extensions that must be there first.
func eachModelTypesPlacementAfter(t *testing.T, sdks []string, before func(t *testing.T, r *modelTypesRig), body func(t *testing.T, r *modelTypesRig)) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts SDK subprocesses)")
	}
	for _, kit := range modelTypesSDKs() {
		if len(sdks) != 0 && !slices.Contains(sdks, kit.name) {
			continue
		}
		for placement, load := range kit.placements {
			t.Run(kit.name+"-"+placement, func(t *testing.T) {
				r := newModelTypesRig(t)
				if before != nil {
					before(t, r)
				}
				load(t, r)
				body(t, r)
			})
		}
	}
}

func loadModelTypesGo(placement string) modelTypesLoader {
	return func(t *testing.T, r *modelTypesRig) {
		t.Helper()
		root := findModuleRoot(t)
		if placement == "fused" {
			// A fused Piglet Binary serves a factory over the host's pipe, and the pipe is the factory's own connection while it runs (fusepack.registerFactory).
			serve := func(conn net.Conn) (err error) {
				defer func() {
					if recovered := recover(); recovered != nil {
						err = fmt.Errorf("the fixture factory failed: %v", recovered)
					}
				}()
				return sdk.ServeFactory(conn, modeltypes.Extension)
			}
			ext, err := r.host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: modeltypes.Name, Path: "/ext/" + modeltypes.Name + ".go", Enabled: true, SourceInfo: hostSurfaceSource}, serve)
			if err != nil {
				t.Fatal(err)
			}
			r.finish(*ext)
			return
		}
		cfg := subprocess.ExtConfig{
			Name: modeltypes.Name, Source: filepath.Join(root, "test/extension-conformance/testdata/model-types-go"), Enabled: true, Isolation: "strict",
			RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "factory", Factory: "Extension",
			SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", ModulePath: "example.com/model-types-go", Package: "example.com/model-types-go",
			SourceInfo: hostSurfaceSource,
		}
		configs := []subprocess.ExtConfig{cfg}
		if placement == "packed" {
			cfg.Isolation = "shared-ok"
			configs = []subprocess.ExtConfig{cfg, providerPeer(t, root, cfg)}
		}
		loaded, failures := r.host.LoadAll(t.Context(), configs)
		if len(failures) != 0 || len(loaded) != len(configs) {
			t.Fatalf("load: %v", failures)
		}
		r.finish(loaded[0])
	}
}

func loadModelTypesNode(packed bool) modelTypesLoader {
	return func(t *testing.T, r *modelTypesRig) {
		t.Helper()
		source, err := filepath.Abs("testdata/model-types.mjs")
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := subprocess.ResolveExtConfigWithIdentity(source, "model-types")
		if err != nil {
			t.Fatal(err)
		}
		cfg.SourceInfo = hostSurfaceSource
		configs := []subprocess.ExtConfig{cfg}
		if packed {
			sibling, err := filepath.Abs("testdata/provider-sibling.mjs")
			if err != nil {
				t.Fatal(err)
			}
			siblingCfg, _, err := subprocess.ResolveExtConfigWithIdentity(sibling, "provider-sibling")
			if err != nil {
				t.Fatal(err)
			}
			configs = append(configs, siblingCfg)
		} else {
			configs[0].Isolation = "isolated"
		}
		loaded, failures := r.host.LoadAll(t.Context(), configs)
		if len(failures) != 0 {
			t.Fatal(failures[0])
		}
		for _, ext := range loaded {
			if ext.Name == "model-types" {
				r.finish(ext)
				return
			}
		}
		t.Fatalf("model-types not loaded: %v", loaded)
	}
}

func loadModelTypesRust(packed bool) modelTypesLoader {
	return func(t *testing.T, r *modelTypesRig) {
		t.Helper()
		source, err := filepath.Abs("testdata/modeltypes-rust")
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, err := subprocess.ResolveExtConfigWithIdentity(source, "model-types")
		if err != nil {
			t.Fatal(err)
		}
		cfg.SourceInfo = hostSurfaceSource
		configs := []subprocess.ExtConfig{cfg}
		if packed {
			sibling, err := filepath.Abs("testdata/provider-rust-sibling")
			if err != nil {
				t.Fatal(err)
			}
			siblingCfg, _, err := subprocess.ResolveExtConfigWithIdentity(sibling, "provider-sibling")
			if err != nil {
				t.Fatal(err)
			}
			configs = append(configs, siblingCfg)
		} else {
			configs[0].Isolation = "isolated"
		}
		loaded, failures := r.host.LoadAll(t.Context(), configs)
		if len(failures) != 0 {
			t.Fatal(failures[0])
		}
		for _, ext := range loaded {
			if ext.Name == "model-types" {
				r.finish(ext)
				return
			}
		}
		t.Fatalf("model-types not loaded: %v", loaded)
	}
}

const modelTypesPython = `import json
import time
import pig_sdk

CANCELLED = {"n": 0}
ZERO = {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}


def theme_attempt(fn):
    try:
        return fn()
    except Exception as error:
        return "throw:" + str(error)


def theme_queries(theme, args):
    # The rest of ctx.ui.theme: modifiers, get_fg_ansi/get_bg_ansi/bg for the tokens the test passes, get_color_mode, the border colors and source_path.
    if not args.get("queries"):
        return None
    tokens = args.get("ansiTokens", [])
    modifier_styles = {
        "bold": lambda text: theme.bold(text), "italic": lambda text: theme.italic(text), "underline": lambda text: theme.underline(text),
        "inverse": lambda text: theme.inverse(text), "strikethrough": lambda text: theme.strikethrough(text),
    }
    return {
        "modifiers": {name: style("x") for name, style in modifier_styles.items()},
        "modifierTexts": {name: [style(text) for text in args.get("modifierTexts", [])] for name, style in modifier_styles.items()},
        "fgAnsi": {token: theme_attempt(lambda token=token: theme.get_fg_ansi(token)) for token in tokens},
        "bgAnsi": {token: theme_attempt(lambda token=token: theme.get_bg_ansi(token)) for token in tokens},
        "bgs": {token: theme_attempt(lambda token=token: theme.bg(token, "x")) for token in tokens},
        "mode": theme.get_color_mode(),
        "thinking": {level: theme_attempt(lambda level=level: theme.get_thinking_border_color(level)("x")) for level in args.get("levels", [])},
        "bash": theme_attempt(lambda: theme.get_bash_mode_border_color()("x")),
        "sourcePath": theme.source_path,
        "sourceInfo": theme.source_info,
    }


def theme_fg(theme, token):
    # theme.fg(token, "x"), or "throw:" and the message when the SDK raises, so one unknown token does not hide the others.
    try:
        return theme.fg(token, "x")
    except Exception as error:
        return "throw:" + str(error)


def tui_theme_probe(ctx, args):
    # pi-coding-agent's theme helpers and key hints over the host's theme and keybindings: the styles of each on the samples the test passes, and the lines
    # highlight_code gets from the host.
    samples = args.get("samples", [])

    def apply(style):
        return [style(sample) for sample in samples]

    op = args.get("op")
    if op == "markdown":
        theme = pig_sdk.get_markdown_theme()
        styles = {
            "heading": apply(theme.heading), "link": apply(theme.link), "linkUrl": apply(theme.link_url), "code": apply(theme.code), "codeBlock": apply(theme.code_block),
            "codeBlockBorder": apply(theme.code_block_border), "quote": apply(theme.quote), "quoteBorder": apply(theme.quote_border), "hr": apply(theme.hr),
            "listBullet": apply(theme.list_bullet), "bold": apply(theme.bold), "italic": apply(theme.italic), "underline": apply(theme.underline),
            "strikethrough": apply(theme.strikethrough),
        }
        return json.dumps({"styles": styles, "highlight": [theme.highlight_code(code, lang) for code, lang in args.get("highlight", [])]})
    if op == "select":
        theme = pig_sdk.get_select_list_theme()
        return json.dumps({"styles": {"selectedPrefix": apply(theme.selected_prefix), "selectedText": apply(theme.selected_text), "description": apply(theme.description),
                                      "scrollInfo": apply(theme.scroll_info), "noMatch": apply(theme.no_match)}})
    if op == "settings":
        theme = pig_sdk.get_settings_list_theme()
        return json.dumps({"styles": {"label": [[theme.label(s, False), theme.label(s, True)] for s in samples], "value": [[theme.value(s, False), theme.value(s, True)] for s in samples],
                                      "description": apply(theme.description), "cursor": theme.cursor, "hint": apply(theme.hint)}})
    if op == "highlight":
        if args.get("theme"):
            pig_sdk.init_theme(args["theme"])
        return json.dumps({"lines": pig_sdk.highlight_code(args.get("code", ""), args.get("lang"))})
    if op == "init_theme":
        pig_sdk.init_theme(args.get("theme"))
        theme = pig_sdk.get_markdown_theme()
        return json.dumps({"bold": apply(theme.bold), "heading": apply(theme.heading)})
    if op == "key_hint":
        return json.dumps({"keyHint": pig_sdk.key_hint(args["id"], args["description"]), "keyText": pig_sdk.key_text(args["id"])})
    if op == "raw_key_hint":
        return json.dumps({"rawKeyHint": pig_sdk.raw_key_hint(args["key"], args["description"])})
    raise RuntimeError("unknown op " + str(op))


def image_impl(model, request, options):
    prompt = request["input"][0]["text"]
    if prompt == "fail":
        raise RuntimeError("image failed")
    return {"api": model["api"], "provider": model["provider"], "model": model["id"], "responseId": options.values.get("apiKey"),
            "output": [{"type": "image", "data": "img:" + model["id"] + ":" + prompt, "mimeType": "image/png"}], "stopReason": "stop", "timestamp": 1}


def classify_impl(model, request, options):
    state = request["state"]["text"]
    if state == "fail":
        raise RuntimeError("classifier failed")
    if state == "hang":
        options.signal.wait()
        CANCELLED["n"] += 1
        raise RuntimeError("classifier cancelled")
    answers = {key: {"type": "bool", "probability": (len(state) + 10 * len(request.get("images") or [])) / 100} for key in reversed(list(request["questions"]))}
    return {"api": model["api"], "provider": model["provider"], "model": model["id"], "answers": answers, "stopReason": "stop", "timestamp": 2}


def approval(text):
    return {"state": {"text": text}, "questions": {
        "tone": {"type": "choice", "instructions": "Which tone?", "criteria": {"warm": "Warm", "cold": "Cold"}},
        "approved": {"type": "bool", "instructions": "Does this express approval?", "criteria": {"true": "Approval", "false": "No approval"}}}}


def ids(models):
    return [m["provider"] + "/" + m["id"] for m in models]


def route(ctx, request):
    return {"model": {"provider": "p", "id": "m"}, "thinkingLevel": "off"}


def register_host_surface(ext):
    # The tools of the host-surface rows (host_surface_test.go): on_theme_change, on_tools_expanded_change, set_widget_renderer, bell, tool_call_index, tool_input and tool_params, extension_source_info, on_loaded and a factory-time events.emit. testfixture/modeltypes (Go), testdata/modeltypes-rust and testdata/model-types.mjs carry the same tools.
    log = {"theme": [], "expanded": [], "heard": [], "loaded": None, "theme_off": None, "expanded_off": None, "heard_at_emit": None, "error_emit_failed": None, "cross_own_at": None, "cross_returned_at": None, "ack_heard": [], "ack_at_cross": None}
    # A factory's pi.events.emit reaches the listeners subscribed before it, its own included.
    ext.events.on("c3:factory", lambda ctx, data: log["heard"].append(data))
    ext.events.emit("c3:factory", {"n": 1, "from": "factory"})
    # Pi's emit is synchronous: the listener has run when emit returns, and an emit on "error" that nobody hears throws in the factory.
    log["heard_at_emit"] = len(log["heard"])
    try:
        ext.events.emit("error", {"from": "factory"})
        log["error_emit_failed"] = False
    except RuntimeError:
        log["error_emit_failed"] = True
    # An extension loaded before this one that listens on c3:cross runs first and finishes before emit returns (milliseconds, as the other fixtures report them).
    # An extension loaded before this one answers the c3:cross emit, from inside its listener, with a c3:ack emit: the listener subscribed here, before the emit, hears it.
    ext.events.on("c3:ack", lambda ctx, data: log["ack_heard"].append(data))
    ext.events.on("c3:cross", lambda ctx, data: log.__setitem__("cross_own_at", int(time.time() * 1000)))
    ext.events.emit("c3:cross", {"from": "factory"})
    log["cross_returned_at"] = int(time.time() * 1000)
    log["ack_at_cross"] = len(log["ack_heard"])

    def loaded(ctx):
        # The handler takes its time; a request that ran before it finished would read None.
        time.sleep(0.3)
        expanded = ctx.get_tools_expanded()
        log["loaded"] = {"cwd": ctx.cwd, "mode": ctx.mode, "hostCall": isinstance(expanded, bool), "expanded": expanded}

    ext.on_loaded(loaded)

    def theme_arm(ctx, args):
        log["theme_off"] = ctx.on_theme_change(lambda theme: log["theme"].append(theme.name + "|" + theme.fg("accent", "x")))
        return json.dumps({"ok": True})

    def theme_disarm(ctx, args):
        log["theme_off"]()
        return json.dumps({"ok": True})

    def expanded_arm(ctx, args):
        log["expanded_off"] = ctx.on_tools_expanded_change(lambda expanded: log["expanded"].append(expanded))
        return json.dumps({"ok": True})

    def expanded_disarm(ctx, args):
        log["expanded_off"]()
        return json.dumps({"ok": True})

    def widget_arm(ctx, args):
        ctx.set_widget_renderer("c3w", lambda width: ["w=%d|%s" % (width, ctx.theme.name)], {"placement": args.get("placement")})
        return json.dumps({"ok": True})

    def widget_clear(ctx, args):
        ctx.set_widget_renderer("c3w", None)
        return json.dumps({"ok": True})

    def bell(ctx, args):
        ctx.bell()
        return json.dumps({"error": "<nil>"})

    def tool_call(ctx, args):
        params = ctx.tool_params()
        meta = params.get("meta") if params is not None else None
        return json.dumps({"index": ctx.tool_call_index(), "input": ctx.tool_input(), "keys": list(params) if params is not None else [], "meta": None if meta is None else ctx_json(meta)})

    handlers = {
        "c3_theme_arm": theme_arm, "c3_theme_disarm": theme_disarm, "c3_theme_log": lambda ctx, args: json.dumps({"log": log["theme"]}),
        "c3_expanded_arm": expanded_arm, "c3_expanded_disarm": expanded_disarm, "c3_expanded_log": lambda ctx, args: json.dumps({"log": log["expanded"]}),
        "c3_widget_arm": widget_arm, "c3_widget_clear": widget_clear, "c3_bell": bell, "c3_tool_call": tool_call,
        "c3_source": lambda ctx, args: json.dumps({"info": ctx.extension_source_info()}),
        "c3_loaded": lambda ctx, args: json.dumps({"loaded": log["loaded"], "factoryHeard": log["heard"], "heardAtEmit": log["heard_at_emit"], "errorEmitFailed": log["error_emit_failed"], "crossOwnAt": log["cross_own_at"], "crossReturnedAt": log["cross_returned_at"], "ackHeard": log["ack_heard"], "ackAtCross": log["ack_at_cross"]}),
    }
    for name, handler in handlers.items():
        ext.register_tool(pig_sdk.ToolDefinition(name=name, label=name, description=name, parameters={"type": "object"}, execute=handler))


def ctx_json(value):
    # JSON.stringify of a decoded value, as the Node fixture's JSON.stringify(params.meta).
    from pig_sdk.serialize_conversation import _json_stringify

    return _json_stringify(value)


def new_extension():
    ext = pig_sdk.Extension("model-types")
    ext.register_provider("ops", {
        "baseUrl": "https://ops.test/v1", "apiKey": "ops-key",
        "models": [
            {"id": "flux", "name": "Flux", "type": "image", "api": "test-images", "input": ["text"], "output": ["image", "text"], "cost": ZERO},
            {"id": "cls", "name": "Cls", "type": "classifier", "api": "test-classifier", "input": ["text"], "contextWindow": 1000, "cost": ZERO},
        ],
        "images": {"test-images": image_impl},
        "classifiers": {"test-classifier": classify_impl},
    })
    ext.register_provider(pig_sdk.Provider(
        id="pixels", name="Pixels",
        auth=pig_sdk.ProviderAuth(api_key=pig_sdk.APIKeyAuth(name="Pixels key", resolve=lambda _input: pig_sdk.AuthResult(auth={"apiKey": "pixels-key"}))),
        get_models=lambda: [
            {"id": "flux", "name": "Flux", "type": "image", "api": "test-images", "input": ["text"], "output": ["image"], "cost": ZERO, "baseUrl": "https://pixels.test/v1"},
            {"id": "cls", "name": "Cls", "type": "classifier", "api": "test-classifier", "input": ["text"], "contextWindow": 1000, "cost": ZERO, "baseUrl": "https://pixels.test/v1"}],
        stream=lambda *_: None, stream_simple=lambda *_: None,
        generate_images=image_impl, classify=classify_impl,
    ))

    def typed_reads(ctx, args):
        registry = ctx.model_registry
        found = registry.find_of_type("classifier", "typesafe", "jev-latest")
        of_type = registry.get_model_of_type("classifier", "typesafe", "jev-latest")
        return json.dumps({
            "ofTypeWindow": of_type["contextWindow"], "ofTypeMissing": registry.get_model_of_type("classifier", "typesafe", "missing"),
            "classifiers": ids(registry.get_models_of_type("classifier")), "chat": ids(registry.get_models_of_type("chat")),
            "images": ids(registry.get_models_of_type("image", "openrouter")), "available": ids(registry.get_available_of_type("classifier", "typesafe")),
            "foundWindow": found["contextWindow"], "missing": registry.find_of_type("classifier", "typesafe", "missing"),
        })

    def classify_probe(ctx, args):
        registry = ctx.model_registry
        model = registry.find_of_type("classifier", "typesafe", "jev-latest")
        result = registry.classify(model, approval("Looks good"), {"apiKey": "sk-conf"})
        failed = registry.classify(model, approval("fail"))
        with_images = registry.classify(model, {**approval("Looks good"), "images": [{"type": "image", "data": "aW1hZ2U=", "mimeType": "image/png"}]})
        return json.dumps({"stop": result["stopReason"], "answers": list(result["answers"]), "approved": result["answers"]["approved"]["probability"], "imagesApproved": with_images["answers"]["approved"]["probability"], "model": result["model"],
                           "failedStop": failed["stopReason"], "failedMessage": failed.get("errorMessage"), "failedProvider": failed.get("provider")})

    def images_probe(ctx, args):
        registry = ctx.model_registry
        model = registry.find_of_type("image", "openrouter", "flux")
        prompt = {"input": [{"type": "text", "text": "a red circle"}]}
        result = registry.generate_images(model, prompt, {"apiKey": "sk-img"})
        failed = registry.generate_images({**model, "id": "gone"}, prompt)
        return json.dumps({"stop": result["stopReason"], "model": result["model"], "output": result["output"],
                           "failedStop": failed["stopReason"], "failedMessage": failed.get("errorMessage"), "failedModel": failed.get("model"), "failedOutput": failed.get("output")})

    def virtual_probe(ctx, args):
        registry = ctx.model_registry
        registry.register_virtual_model(pig_sdk.VirtualModel(provider="router", id="late", name="Late", route=route, context_window=1000))
        refused = ""
        try:
            registry.register_virtual_model(pig_sdk.VirtualModel(provider="router", id="claimed", name="Claimed", route=route))
        except Exception as error:
            refused = str(error).strip()
        registry.unregister_virtual_model("router", "late")
        return json.dumps({"first": True, "refused": refused})

    def virtual_visible(ctx, args):
        registry = ctx.model_registry
        registry.register_virtual_model(pig_sdk.VirtualModel(provider="router", id="seen", name="Seen", route=route, context_window=1000))
        def listed():
            return any(model.get("provider") == "router" and model.get("id") == "seen" for model in registry.get_all())
        registered, is_listed = registry.find("router", "seen") is not None, listed()
        registry.unregister_virtual_model("router", "seen")
        return json.dumps({"registered": registered, "listed": is_listed, "gone": registry.find("router", "seen") is None, "unlisted": not listed()})

    def theme_probe(ctx, args):
        theme = ctx.theme
        styles = []
        for options in args.get("cases", []):
            try:
                styles.append({"ok": theme.style("x", options)})
            except Exception as error:
                styles.append({"error": str(error)})
        colors = theme.colors
        return json.dumps({"appearance": theme.appearance, "colors": {token: colors.get(token) for token in args.get("tokens", [])}, "styles": styles, "fgs": {token: theme_fg(theme, token) for token in args.get("fgTokens", [])}, "queries": theme_queries(theme, args)})

    def late_provider(ctx, args):
        registry = ctx.model_registry

        def stream_simple(stream_ctx, model, request, options):
            stream = pig_sdk.ModelEventStream()
            message = {"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": [{"type": "text", "text": "late:" + model["id"] + ":" + str(options["apiKey"])}], "stopReason": "stop", "timestamp": 1}
            stream.push({"type": "done", "reason": "stop", "message": message})
            return stream

        registry.register_provider("late", {
            "api": "late-chat-api", "baseUrl": "https://late.test/v1", "apiKey": "late-key",
            "models": [
                {"id": "chat", "name": "Chat", "reasoning": False, "input": ["text"], "cost": ZERO, "contextWindow": 1000, "maxTokens": 100},
                {"id": "flux", "name": "Flux", "type": "image", "api": "late-images", "input": ["text"], "output": ["image"], "cost": ZERO},
                {"id": "cls", "name": "Cls", "type": "classifier", "api": "late-classifier", "input": ["text"], "contextWindow": 1000, "cost": ZERO},
            ],
            "images": {"late-images": image_impl},
            "classifiers": {"late-classifier": classify_impl},
            "streamSimple": stream_simple,
        })
        if args.get("mode") == "again":
            registry.register_provider("late", {"baseUrl": "https://late.test/v2"})
        if args.get("mode") == "gone":
            registry.unregister_provider("late")
        return json.dumps({"mode": args.get("mode")})

    def tui_probe(ctx, args):
        from pig_sdk import tui

        def as_list(keys):
            if keys is None:
                return []
            return [keys] if isinstance(keys, str) else list(keys)

        def caps_json(value):
            return {"images": value.images, "trueColor": value.true_color, "hyperlinks": value.hyperlinks}

        ids = args.get("ids", [])
        keybindings = tui.get_keybindings()
        original = tui.get_capabilities()
        definitions = {}
        for id in ids:
            definition = keybindings.get_definition(id)
            definitions[id] = definition.description if definition is not None else None
        report = {
            "keys": {id: keybindings.get_keys(id) for id in ids},
            "matches": [keybindings.matches(item["data"], item["id"]) for item in args.get("inputs", [])],
            "definitions": definitions,
            "user": {id: as_list(keys) for id, keys in keybindings.get_user_bindings().items()},
            "conflicts": [{"key": c.key, "keybindings": list(c.keybindings)} for c in keybindings.get_conflicts()],
            "resolvedIds": sorted(keybindings.get_resolved_bindings()),
            "defaults": {"count": len(tui.TUI_KEYBINDINGS), "tui.input.submit": as_list(tui.TUI_KEYBINDINGS["tui.input.submit"].default_keys), "tui.select.confirm": as_list(tui.TUI_KEYBINDINGS["tui.select.confirm"].default_keys)},
            "key": {"ctrl": tui.Key.ctrl("x"), "alt": tui.Key.alt(tui.Key.enter), "shift": tui.Key.shift(tui.Key.tab), "escape": tui.Key.escape, "ctrlShift": tui.Key.ctrl_shift("p")},
            "capabilities": caps_json(original), "colorMode": tui.get_terminal_color_mode(),
        }
        tui.set_keybindings(tui.KeybindingsManager({"x.probe": tui.KeybindingDefinition(default_keys=["ctrl+q"])}))
        installed = {"keys": tui.get_keybindings().get_keys("x.probe"), "hostKeys": tui.get_keybindings().get_keys("tui.input.submit")}
        tui.get_keybindings().set_user_bindings({"x.probe": ["ctrl+w"]})
        installed["rebound"] = tui.get_keybindings().get_keys("x.probe")
        tui.set_keybindings(keybindings)
        installed["restored"] = tui.get_keybindings().get_keys("tui.input.submit")
        report["installed"] = installed

        tui.set_capabilities(tui.TerminalCapabilities("kitty", False, False))
        swapped = {"capabilities": caps_json(tui.get_capabilities()), "colorMode": tui.get_terminal_color_mode()}
        tui.set_capability_overrides(tui.CapabilityOverrides(hyperlinks=True))
        swapped["overridden"] = tui.get_capabilities().hyperlinks
        tui.set_capability_overrides(tui.CapabilityOverrides())
        tui.reset_capabilities_cache()
        tui.detect_capabilities(lambda: False)
        swapped["detected"] = True
        tui.set_capabilities(original)
        swapped["restored"] = caps_json(tui.get_capabilities())
        before = tui.is_kitty_protocol_active()
        tui.set_kitty_protocol_active(True)
        after = tui.is_kitty_protocol_active()
        tui.set_kitty_protocol_active(before)
        swapped["kitty"] = [before, after, tui.is_kitty_protocol_active()]
        report["swapped"] = swapped
        return json.dumps(report)

    for name, handler in (("late_provider", late_provider), ("typed_reads", typed_reads), ("classify_probe", classify_probe), ("images_probe", images_probe), ("virtual_probe", virtual_probe), ("virtual_visible", virtual_visible), ("theme_probe", theme_probe), ("tui_probe", tui_probe), ("tui_theme_probe", tui_theme_probe), ("ops_status", lambda ctx, args: json.dumps({"cancelled": CANCELLED["n"]}))):
        ext.register_tool(pig_sdk.ToolDefinition(name=name, label=name, description=name, parameters={"type": "object"}, execute=handler))
    register_host_surface(ext)
    return ext
`

func loadModelTypesPython(isolation string) modelTypesLoader {
	return func(t *testing.T, r *modelTypesRig) {
		t.Helper()
		root := findModuleRoot(t)
		cfg := packedFlagFactory(t, root, "python", "model-types", false)
		cfg.Isolation = isolation
		cfg.ContentHash = "python-model-types-" + fmt.Sprint(len(modelTypesPython))
		cfg.SourceInfo = hostSurfaceSource
		if err := os.WriteFile(filepath.Join(cfg.Source, cfg.Package+".py"), []byte(modelTypesPython), 0o600); err != nil {
			t.Fatal(err)
		}
		configs := []subprocess.ExtConfig{cfg}
		if isolation == "shared-ok" {
			peer := packedFlagFactory(t, root, "python", "model-types-peer", false)
			peer.Isolation = isolation
			configs = append(configs, peer)
		}
		if isolation == "shared-ok" && !r.keepLoaded {
			r.host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return configs, nil })
			loaded, err := r.host.Reload(t.Context())
			if err != nil {
				t.Fatalf("reload: %v (%+v)", err, r.host.LastReloadReport())
			}
			for _, ext := range loaded {
				if ext.Name == "model-types" {
					r.finish(ext)
					return
				}
			}
			t.Fatalf("model-types not loaded: %v", loaded)
		}
		loaded, failures := r.host.LoadAll(t.Context(), configs)
		if len(failures) != 0 || len(loaded) != len(configs) {
			t.Fatalf("load: %v", failures)
		}
		r.finish(loaded[0])
	}
}

// model-registry.ts:145-161 and model-runtime.ts getModelsOfType: the typed reads answer from the registry state the host published, chat models from "models" and the others from "typedModels", in the host's order; getAvailableOfType is the host's answer for the type and provider it was asked for.
func TestModelTypesReadsAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		report := r.tool("typed_reads")
		want := map[string]any{
			"classifiers": []any{"typesafe/jev-latest", "openrouter/~typesafe/jev-latest"},
			"chat":        []any{"openai/gpt", "anthropic/opus"},
			"images":      []any{"openrouter/flux"},
			"available":   []any{"typesafe/jev-latest"},
			"foundWindow": float64(64000),
			"missing":     nil,
			// getModelOfType (model-registry.ts:164-169) answers the same model as findOfType, by provider and id, and nil for a miss.
			"ofTypeWindow":  float64(64000),
			"ofTypeMissing": nil,
		}
		if !reflect.DeepEqual(report, want) {
			t.Fatalf("typed_reads = %v\nwant %v", report, want)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if !reflect.DeepEqual(r.available, []string{"classifier/typesafe"}) {
			t.Fatalf("the host was asked for %v", r.available)
		}
	})
}

// model-registry.ts:170-177 and model-runtime.ts:800-815: classify sends the found model, the state and the questions in their order, and the options; the answers come back in the host's order; a failure is a result that names the model.
func TestModelTypesClassifyAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		report := r.tool("classify_probe")
		// "Looks good" has ten characters; the host answers each question with 0.1, in the reverse of the questions' order.
		if report["stop"] != "stop" || !reflect.DeepEqual(report["answers"], []any{"approved", "tone"}) || report["approved"] != 0.1 || report["model"] != "jev-latest" {
			t.Fatalf("classify_probe = %v", report)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if len(r.classify) != 3 {
			t.Fatalf("the host classified %d times: %+v", len(r.classify), r.classify)
		}
		// types.ts ClassifierContext.images: the images the caller passed reach the host with the state and the questions. The host adds ten to each probability per image, so an SDK that drops them answers 0.1 instead of 0.2.
		if report["imagesApproved"] != 0.2 {
			t.Fatalf("classify_probe with an image answered %v, want 0.2: %v", report["imagesApproved"], report)
		}
		var withImages struct {
			Images []map[string]any `json:"images"`
		}
		if err := json.Unmarshal(r.classify[2].Request, &withImages); err != nil || !reflect.DeepEqual(withImages.Images, []map[string]any{{"type": "image", "data": "aW1hZ2U=", "mimeType": "image/png"}}) {
			t.Fatalf("the images reached the host as %s (%v)", r.classify[2].Request, err)
		}
		first := r.classify[0]
		if first.Model["id"] != "jev-latest" || first.Model["type"] != "classifier" || first.Model["contextWindow"] != float64(64000) || first.Options["apiKey"] != "sk-conf" {
			t.Fatalf("host saw model %v, options %v", first.Model, first.Options)
		}
		var sent struct {
			Questions json.RawMessage `json:"questions"`
		}
		if err := json.Unmarshal(first.Request, &sent); err != nil {
			t.Fatal(err)
		}
		if got := orderedKeys(sent.Questions); !reflect.DeepEqual(got, []string{"tone", "approved"}) {
			t.Fatalf("the questions reached the host as %s (order %v)", first.Request, got)
		}
		if r.classify[1].Options != nil || r.classify[2].Options != nil {
			t.Fatalf("omitted options reached the host: %v", r.classify[1].Options)
		}
	})
}

// model-registry.ts:181-188 and model-runtime.ts:783-798 (1.0.0): generateImages sends the found model, the images context and the options; the result comes back as the host answered it; a failure is a result that names the model and has no output.
func TestModelTypesGenerateImagesAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		report := r.tool("images_probe")
		wantOutput := []any{map[string]any{"type": "image", "data": "img:flux:a red circle:sk-img", "mimeType": "image/png"}}
		if report["stop"] != "stop" || report["model"] != "flux" || !reflect.DeepEqual(report["output"], wantOutput) {
			t.Fatalf("images_probe = %v", report)
		}
		if report["failedStop"] != "error" || report["failedModel"] != "gone" || !strings.Contains(fmt.Sprint(report["failedMessage"]), "no image model openrouter/gone") || !reflect.DeepEqual(report["failedOutput"], []any{}) {
			t.Fatalf("images_probe failure = %v", report)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if len(r.images) != 2 {
			t.Fatalf("the host generated images %d times: %+v", len(r.images), r.images)
		}
		first := r.images[0]
		if first.Model["id"] != "flux" || first.Model["type"] != "image" || first.Model["api"] != "openrouter-images" || first.Options["apiKey"] != "sk-img" {
			t.Fatalf("host saw model %v, options %v", first.Model, first.Options)
		}
		if r.images[1].Options != nil {
			t.Fatalf("omitted options reached the host: %v", r.images[1].Options)
		}
	})
}

// model-registry.ts:161-168: registerVirtualModel and unregisterVirtualModel of the facade are the runtime's; a refusal is the caller's error.
// packages/coding-agent/src/core/extensions/types.ts:1872-1875 (ExtensionAPI.registerVirtualModel, unregisterVirtualModel).
func TestModelTypesVirtualModelsAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		report := r.tool("virtual_probe")
		if report["first"] != true || !strings.Contains(fmt.Sprint(report["refused"]), "router/claimed is the id of a physical model") {
			t.Fatalf("registerVirtualModel probe virtual_probe = %v", report)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if want := []string{"register router/late", "unregister router/late"}; !reflect.DeepEqual(r.virtual, want) {
			t.Fatalf("registerVirtualModel/unregisterVirtualModel host registrations = %v, want %v", r.virtual, want)
		}
	})
}

// model-runtime.ts:964-992 and model-registry.ts:161-168: registerVirtualModel and unregisterVirtualModel update the model snapshot before they return, so find and getAll, read on the
// next line of the same handler, see the write. Pi 1.3.0 prints {"registered":true,"listed":true,"gone":true,"unlisted":true} for the same sequence. A realization that answers from
// a registry older than the call reads false for "registered" and "listed", which the fallback of a stale read never produces for a model it was just given.
func TestModelTypesVirtualModelIsVisibleInTheSameTurnAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		report := r.tool("virtual_visible")
		want := map[string]any{"registered": true, "listed": true, "gone": true, "unlisted": true}
		if !reflect.DeepEqual(report, want) {
			t.Fatalf("virtual_visible = %v, want %v: the registry read after registerVirtualModel/unregisterVirtualModel must see the write", report, want)
		}
	})
}

var modelTypesPrompt = ai.ImagesContext{Input: []ai.ContentBlock{ai.TextContent{Text: "a red circle"}}}

func modelTypesClassifierContext(text string) ai.ClassifierContext {
	return ai.ClassifierContext{State: ai.JsonObject{"text": text}, Questions: ai.ClassifierQuestions{
		{ID: "first", Question: ai.ClassifierBoolQuestion{Instructions: "One?"}},
		{ID: "second", Question: ai.ClassifierBoolQuestion{Instructions: "Two?"}},
		{ID: "third", Question: ai.ClassifierBoolQuestion{Instructions: "Three?"}},
	}}
}

// types.ts:1896-1898 and provider-composer.ts:632-663: a provider config's images and classifiers run in the extension. The host builds the ProviderImages and ProviderClassifier of the config from the register payload, and each call runs the extension's callback and carries its result or its error back.
func TestModelTypesProviderConfigImplementationsAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		r.mu.Lock()
		config, ok := r.providers["ops"]
		r.mu.Unlock()
		if !ok {
			t.Fatalf("the host did not register provider ops (registered: %v)", r.providers)
		}
		if len(config.Models) != 2 || config.Models[0].Type != ai.ModelTypeImage || config.Models[1].Type != ai.ModelTypeClassifier || config.Models[1].ContextWindow != 1000 {
			t.Fatalf("models = %+v", config.Models)
		}
		images := config.Images["test-images"]
		classifier := config.Classifiers["test-classifier"]
		if images == nil || images.GenerateImages == nil || classifier == nil || classifier.Classify == nil {
			t.Fatalf("images = %+v, classifiers = %+v", config.Images, config.Classifiers)
		}
		imageModel := &ai.ImageModel{ID: "flux", Name: "Flux", API: "test-images", Provider: "ops", BaseURL: "https://ops.test/v1", Input: []string{"text"}, Output: []string{"image"}}
		result, err := images.GenerateImages(t.Context(), imageModel, modelTypesPrompt, ai.ImagesOptions{APIKey: "sk-ops"})
		if err != nil {
			t.Fatal(err)
		}
		if result.StopReason != ai.ImagesStopReasonStop || result.Model != "flux" || result.ResponseID != "sk-ops" || len(result.Output) != 1 || result.Output[0].(ai.ImageContent).Data != "img:flux:a red circle" {
			t.Fatalf("image result = %+v", result)
		}
		if _, err := images.GenerateImages(t.Context(), imageModel, ai.ImagesContext{Input: []ai.ContentBlock{ai.TextContent{Text: "fail"}}}, ai.ImagesOptions{}); err == nil || !strings.Contains(err.Error(), "image failed") {
			t.Fatalf("an extension error must be the call's error: %v", err)
		}

		classifierModel := &ai.ClassifierModel{ID: "cls", Name: "Cls", API: "test-classifier", Provider: "ops", BaseURL: "https://ops.test/v1", Input: []string{"text"}, ContextWindow: 1000}
		classified, err := classifier.Classify(t.Context(), classifierModel, modelTypesClassifierContext("four"), ai.ClassifierOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var order []string
		for _, entry := range classified.Answers {
			order = append(order, entry.ID)
			if entry.Answer != (ai.ClassifierBoolAnswer{Probability: 0.04}) {
				t.Fatalf("answer %s = %+v", entry.ID, entry.Answer)
			}
		}
		if classified.StopReason != ai.ClassifierStopReasonStop || !reflect.DeepEqual(order, []string{"third", "second", "first"}) || classified.Model != "cls" {
			t.Fatalf("classifier result = %+v (answer order %v)", classified, order)
		}
		if _, err := classifier.Classify(t.Context(), classifierModel, modelTypesClassifierContext("fail"), ai.ClassifierOptions{}); err == nil || !strings.Contains(err.Error(), "classifier failed") {
			t.Fatalf("an extension error must be the call's error: %v", err)
		}
		// types.ts ClassifierContext.images: the host's context reaches the extension's classify with its images; the implementation adds ten per image to the state's length, so an SDK that drops them answers 0.04.
		shown := modelTypesClassifierContext("four")
		shown.Images = []ai.ImageContent{{Data: "aW1hZ2U=", MimeType: "image/png"}, {Data: "aW1hZ2U=", MimeType: "image/jpeg"}}
		withImages, err := classifier.Classify(t.Context(), classifierModel, shown, ai.ClassifierOptions{})
		if err != nil || len(withImages.Answers) != 3 || withImages.Answers[0].Answer != (ai.ClassifierBoolAnswer{Probability: 0.24}) {
			t.Fatalf("classifier result with images = %+v, %v", withImages, err)
		}
	})
}

// A provider Provider object's generateImages and classify (pi-ai Provider): the host's carrier runs them in the extension over provider_call.
func TestModelTypesProviderObjectAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		r.mu.Lock()
		native, ok := r.natives["pixels"]
		r.mu.Unlock()
		if !ok {
			t.Fatalf("the host did not register the pixels provider object (registered: %v)", r.natives)
		}
		if native.GenerateImages == nil || native.Classify == nil {
			t.Fatalf("the carrier lacks generateImages (%v) or classify (%v)", native.GenerateImages != nil, native.Classify != nil)
		}
		imageModel := &ai.ImageModel{ID: "flux", Name: "Flux", API: "test-images", Provider: "pixels", BaseURL: "https://pixels.test/v1", Input: []string{"text"}, Output: []string{"image"}}
		result, err := native.GenerateImages(t.Context(), imageModel, modelTypesPrompt, ai.ImagesOptions{APIKey: "sk-pixels"})
		if err != nil || result.ResponseID != "sk-pixels" || len(result.Output) != 1 || result.Output[0].(ai.ImageContent).Data != "img:flux:a red circle" {
			t.Fatalf("image result = %+v, %v", result, err)
		}
		classifierModel := &ai.ClassifierModel{ID: "cls", Name: "Cls", API: "test-classifier", Provider: "pixels", BaseURL: "https://pixels.test/v1", Input: []string{"text"}, ContextWindow: 1000}
		classified, err := native.Classify(t.Context(), classifierModel, modelTypesClassifierContext("four"), ai.ClassifierOptions{})
		if err != nil || len(classified.Answers) != 3 || classified.Answers[0].ID != "third" {
			t.Fatalf("classifier result = %+v, %v", classified, err)
		}
		if _, err := native.Classify(t.Context(), classifierModel, modelTypesClassifierContext("fail"), ai.ClassifierOptions{}); err == nil || !strings.Contains(err.Error(), "classifier failed") {
			t.Fatalf("an extension error must be the call's error: %v", err)
		}
	})
}

// Cancellation: the request's context cancels the callback in the extension (its signal), and the host's call returns without waiting for the callback's own answer.
func TestModelTypesProviderOperationCancellationAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		r.mu.Lock()
		classifier := r.providers["ops"].Classifiers["test-classifier"]
		r.mu.Unlock()
		if classifier == nil {
			t.Fatal("no classifier implementation")
		}
		before := r.tool("ops_status")["cancelled"].(float64)
		ctx, cancel := context.WithCancel(t.Context())
		model := &ai.ClassifierModel{ID: "cls", API: "test-classifier", Provider: "ops"}
		done := make(chan error, 1)
		go func() {
			_, err := classifier.Classify(ctx, model, modelTypesClassifierContext("hang"), ai.ClassifierOptions{})
			done <- err
		}()
		time.Sleep(200 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("a cancelled classification returned no error")
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a cancelled classification did not return")
		}
		waitFor(t, func() bool { return r.tool("ops_status")["cancelled"].(float64) == before+1 })
	})
}
