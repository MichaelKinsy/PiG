package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
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

	mu        sync.Mutex
	providers map[string]extension.ProviderConfig
	// registrations lists each provider registration the host applied, in order.
	registrations []providerRegistration
	natives       map[string]*extension.NativeProvider
	classify      []classifyCall
	images        []classifyCall
	virtual       []string
	available     []string
	theme         any
}

// setTheme is the palette the host publishes with its state before each call.
func (r *modelTypesRig) setTheme(palette any) {
	r.mu.Lock()
	r.theme = palette
	r.mu.Unlock()
}

func newModelTypesRig(t *testing.T) *modelTypesRig {
	t.Helper()
	r := &modelTypesRig{t: t, dir: t.TempDir(), providers: map[string]extension.ProviderConfig{}, natives: map[string]*extension.NativeProvider{}}
	r.host = subprocess.NewHostWithConfigRoot(r.dir, t.TempDir())
	t.Cleanup(func() { r.host.Shutdown("test complete") })
	r.bridge = subprocess.NewUIBridge(func() {})
	r.host.SetUIBridge(r.bridge)
	r.bridge.SetThemeFunc(func() any {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.theme
	})
	r.bridge.SetActions(&subprocess.HostCallbacks{
		// The catalog the ready frame carries is the one the registry state publishes; the Node runtime installs it from either.
		GetModels: func() []map[string]any { return modelTypesChat },
		GetModelRegistryState: func() map[string]any {
			return map[string]any{
				"models": modelTypesChat, "typedModels": modelTypesTyped,
				"providers":  map[string]any{"typesafe": map[string]any{"name": "TypeSafe", "configured": true}},
				"registered": []any{},
			}
		},
		GetAvailableOfType: func(_ context.Context, modelType, provider string) ([]map[string]any, error) {
			r.mu.Lock()
			r.available = append(r.available, modelType+"/"+provider)
			r.mu.Unlock()
			var models []map[string]any
			for _, model := range modelTypesTyped {
				if model["type"] == modelType && model["provider"] == "typesafe" && (provider == "" || provider == "typesafe") {
					models = append(models, model)
				}
			}
			return models, nil
		},
		Classify: func(_ context.Context, model map[string]any, request json.RawMessage, options map[string]any) (json.RawMessage, error) {
			r.mu.Lock()
			r.classify = append(r.classify, classifyCall{model, request, options})
			r.mu.Unlock()
			// The answers are the questions' in reverse; the probability is the state's length, so only a request the SDK built from the caller's context produces them.
			var context struct {
				State     map[string]any `json:"state"`
				Questions json.RawMessage
			}
			if err := json.Unmarshal(request, &context); err != nil {
				return nil, err
			}
			keys := orderedKeys(context.Questions)
			answers := make([]string, 0, len(keys))
			for _, key := range slices.Backward(keys) {
				answers = append(answers, fmt.Sprintf("%q:{\"type\":\"bool\",\"probability\":%v}", key, float64(len(fmt.Sprint(context.State["text"])))/100))
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
			r.mu.Unlock()
			return nil
		},
		UnregisterVirtualModel: func(provider, id string) {
			r.mu.Lock()
			r.virtual = append(r.virtual, "unregister "+provider+"/"+id)
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
	result, err := def.Definition.Execute(r.t.Context(), "call-"+name, encoded, nil)
	if err != nil {
		r.t.Fatalf("%s: %v", name, err)
	}
	typed, ok := result.(agent.AgentToolResult)
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
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts SDK subprocesses)")
	}
	for _, kit := range modelTypesSDKs() {
		for placement, load := range kit.placements {
			t.Run(kit.name+"-"+placement, func(t *testing.T) {
				r := newModelTypesRig(t)
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
			var factory *sdk.Extension
			func() {
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Fatalf("the fixture factory failed: %v", recovered)
					}
				}()
				factory = modeltypes.Extension()
			}()
			ext, err := r.host.LoadInProcess(t.Context(), subprocess.ExtConfig{Name: modeltypes.Name, Path: "/ext/" + modeltypes.Name + ".go", Enabled: true}, factory.RunWithConn)
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
import pig_sdk

CANCELLED = {"n": 0}
ZERO = {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}


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
    answers = {key: {"type": "bool", "probability": len(state) / 100} for key in reversed(list(request["questions"]))}
    return {"api": model["api"], "provider": model["provider"], "model": model["id"], "answers": answers, "stopReason": "stop", "timestamp": 2}


def approval(text):
    return {"state": {"text": text}, "questions": {
        "tone": {"type": "choice", "instructions": "Which tone?", "criteria": {"warm": "Warm", "cold": "Cold"}},
        "approved": {"type": "bool", "instructions": "Does this express approval?", "criteria": {"true": "Approval", "false": "No approval"}}}}


def ids(models):
    return [m["provider"] + "/" + m["id"] for m in models]


def route(ctx, request):
    return {"model": {"provider": "p", "id": "m"}, "thinkingLevel": "off"}


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
        return json.dumps({
            "classifiers": ids(registry.get_models_of_type("classifier")), "chat": ids(registry.get_models_of_type("chat")),
            "images": ids(registry.get_models_of_type("image", "openrouter")), "available": ids(registry.get_available_of_type("classifier", "typesafe")),
            "foundWindow": found["contextWindow"], "missing": registry.find_of_type("classifier", "typesafe", "missing"),
        })

    def classify_probe(ctx, args):
        registry = ctx.model_registry
        model = registry.find_of_type("classifier", "typesafe", "jev-latest")
        result = registry.classify(model, approval("Looks good"), {"apiKey": "sk-conf"})
        failed = registry.classify(model, approval("fail"))
        return json.dumps({"stop": result["stopReason"], "answers": list(result["answers"]), "approved": result["answers"]["approved"]["probability"], "model": result["model"],
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

    def theme_probe(ctx, args):
        theme = ctx.theme
        styles = []
        for options in args.get("cases", []):
            try:
                styles.append({"ok": theme.style("x", options)})
            except Exception as error:
                styles.append({"error": str(error)})
        colors = theme.colors
        return json.dumps({"appearance": theme.appearance, "colors": {token: colors.get(token) for token in args.get("tokens", [])}, "styles": styles, "fgs": {token: theme.fg(token, "x") for token in args.get("fgTokens", [])}})

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

    for name, handler in (("late_provider", late_provider), ("typed_reads", typed_reads), ("classify_probe", classify_probe), ("images_probe", images_probe), ("virtual_probe", virtual_probe), ("theme_probe", theme_probe), ("ops_status", lambda ctx, args: json.dumps({"cancelled": CANCELLED["n"]}))):
        ext.register_tool(pig_sdk.ToolDefinition(name=name, label=name, description=name, parameters={"type": "object"}, execute=handler))
    return ext
`

func loadModelTypesPython(isolation string) modelTypesLoader {
	return func(t *testing.T, r *modelTypesRig) {
		t.Helper()
		root := findModuleRoot(t)
		cfg := packedFlagFactory(t, root, "python", "model-types", false)
		cfg.Isolation = isolation
		cfg.ContentHash = "python-model-types-" + fmt.Sprint(len(modelTypesPython))
		if err := os.WriteFile(filepath.Join(cfg.Source, cfg.Package+".py"), []byte(modelTypesPython), 0o600); err != nil {
			t.Fatal(err)
		}
		configs := []subprocess.ExtConfig{cfg}
		if isolation == "shared-ok" {
			peer := packedFlagFactory(t, root, "python", "model-types-peer", false)
			peer.Isolation = isolation
			configs = append(configs, peer)
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
		if len(failures) != 0 || len(loaded) != 1 {
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
		if len(r.classify) != 2 {
			t.Fatalf("the host classified %d times: %+v", len(r.classify), r.classify)
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
		if r.classify[1].Options != nil {
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
func TestModelTypesVirtualModelsAcrossSDKs(t *testing.T) {
	t.Parallel()
	eachModelTypesPlacement(t, func(t *testing.T, r *modelTypesRig) {
		report := r.tool("virtual_probe")
		if report["first"] != true || !strings.Contains(fmt.Sprint(report["refused"]), "router/claimed is the id of a physical model") {
			t.Fatalf("virtual_probe = %v", report)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if want := []string{"register router/late", "unregister router/late"}; !reflect.DeepEqual(r.virtual, want) {
			t.Fatalf("host registrations = %v, want %v", r.virtual, want)
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
