package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type registryOracleOp struct {
	Op       string          `json:"op"`
	Provider string          `json:"provider,omitempty"`
	Ref      string          `json:"ref,omitempty"`
	Name     string          `json:"name,omitempty"`
	Models   *string         `json:"models,omitempty"`
	Config   *registryConfig `json:"config,omitempty"`
	Key      string          `json:"key,omitempty"`
	Type     string          `json:"type,omitempty"`
	Via      string          `json:"via,omitempty"`
}

type registryModelConfig struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Reasoning     bool               `json:"reasoning"`
	Input         []string           `json:"input"`
	Cost          registryModelPrice `json:"cost"`
	ContextWindow int                `json:"contextWindow"`
	MaxTokens     int                `json:"maxTokens"`
}

type registryModelPrice struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

type registryConfig struct {
	Name       string                `json:"name,omitempty"`
	BaseURL    string                `json:"baseUrl,omitempty"`
	APIKey     string                `json:"apiKey,omitempty"`
	API        string                `json:"api,omitempty"`
	Headers    map[string]string     `json:"headers,omitempty"`
	AuthHeader *bool                 `json:"authHeader,omitempty"`
	Models     []registryModelConfig `json:"models,omitempty"`
}

type registryOracleScenario struct {
	Env    map[string]string  `json:"env"`
	Auth   *string            `json:"auth"`
	Models *string            `json:"models"`
	Ops    []registryOracleOp `json:"ops"`
}

func registryOracleScenarios() []registryOracleScenario {
	rng := rand.New(rand.NewPCG(5, 77))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	customProvider := func() string {
		parts := []string{`"baseUrl":"https://example.test/v1"`, `"api":"openai-completions"`}
		if rng.IntN(5) > 0 {
			parts = append(parts, fmt.Sprintf(`"name":%q`, pick([]string{"Custom", "日本語", "My Provider"})))
		}
		switch rng.IntN(5) {
		case 0:
			parts = append(parts, `"apiKey":"literal-key"`)
		case 1:
			parts = append(parts, `"apiKey":"CUSTOM_ENV_KEY"`)
		case 2:
			parts = append(parts, `"apiKey":"!echo cmd-key"`)
		case 3:
			parts = append(parts, `"apiKey":"!exit 1"`)
		}
		if rng.IntN(3) == 0 {
			parts = append(parts, `"authHeader":true`)
		}
		if rng.IntN(3) == 0 {
			parts = append(parts, `"headers":{"X-One":"1","X-Env":"CUSTOM_ENV_KEY","X-Cmd":"!echo hdr"}`)
		}
		models := []string{`{"id":"m1","name":"M1","reasoning":true,"input":["text","image"],"cost":{"input":1,"output":2,"cacheRead":0.5,"cacheWrite":0.25},"contextWindow":5000,"maxTokens":300}`, `{"id":"m2"}`, `{"id":"m3","name":"M3","headers":{"X-M":"y"}}`}
		parts = append(parts, `"models":[`+strings.Join(models[:1+rng.IntN(3)], ",")+`]`)
		return "{" + strings.Join(parts, ",") + "}"
	}
	modelsJSON := func() *string {
		var doc string
		switch rng.IntN(12) {
		case 0:
			return nil
		case 1:
			doc = `{"providers":`
		case 2:
			doc = `{"providers":{"custom":{"models":[{"id":""}]}}}`
		case 3:
			doc = `{"providers":{"openai":{"baseUrl":"https://proxy.test/v1"}}}`
		case 4:
			doc = `{"providers":{"openai":{"modelOverrides":{"gpt-4o":{"name":"Renamed","cost":{"input":9},"contextWindow":1234}},"headers":{"X-P":"1"},"apiKey":"OPENAI_API_KEY"}}}`
		case 5:
			doc = `{"providers":{"anthropic":{"apiKey":"!echo anth","authHeader":true}}}`
		case 6:
			doc = `{"providers":{"custom":{"models":[{"id":"m1"}]}}}`
		default:
			doc = `{"providers":{"custom":` + customProvider() + `}}`
			if rng.IntN(3) == 0 {
				doc = strings.TrimSuffix(doc, "}}") + `},"second":` + customProvider() + `}}`
			}
		}
		return &doc
	}
	authJSON := func() *string {
		var doc string
		switch rng.IntN(8) {
		case 0, 1:
			return nil
		case 2:
			doc = `{}`
		case 3:
			doc = `{"openai":{"type":"api_key","key":"sk-stored"},"custom":{"type":"api_key","key":"custom-stored"}}`
		case 4:
			doc = `{"anthropic":{"type":"oauth","access":"a","refresh":"r","expires":99999999999999}}`
		case 5:
			doc = `{"anthropic":{"type":"api_key","key":"!echo stored-cmd"},"openai":{"type":"api_key","key":"OPENAI_API_KEY"}}`
		default:
			doc = `{"anthropic":{"type":"api_key","key":"ak"}}`
		}
		return &doc
	}
	refs := []string{"custom/m1", "custom/m2", "custom/m3", "second/m1", "openai/gpt-4o", "openai/gpt-5", "anthropic/claude-opus-4-6", "anthropic/claude-sonnet-4-5", "nope/x", "registered/r1", "registered/r2"}
	providers := []string{"custom", "second", "openai", "anthropic", "registered", "nope", "google", "openrouter"}
	var scenarios []registryOracleScenario
	for range 700 {
		scenario := registryOracleScenario{Env: map[string]string{}, Auth: authJSON(), Models: modelsJSON()}
		if rng.IntN(2) == 0 {
			scenario.Env["OPENAI_API_KEY"] = "sk-env"
		}
		if rng.IntN(3) == 0 {
			scenario.Env["CUSTOM_ENV_KEY"] = "env-custom"
		}
		scenario.Ops = []registryOracleOp{{Op: "error"}, {Op: "available"}}
		rewritten, unregistered := false, false
		for range 3 + rng.IntN(14) {
			kind := rng.IntN(15)
			if rng.IntN(3) == 0 {
				switch rng.IntN(17) {
				case 12:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: pick([]string{"typed", "typedAvailable"}), Type: pick([]string{"chat", "image", "classifier"}), Provider: pick(append(providers, ""))})
				case 13:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "typedOne", Type: pick([]string{"chat", "image", "classifier"}), Ref: pick(refs)})
				case 14:
					switch rng.IntN(4) {
					case 0:
						// A virtual model is listed beside physical ones, or alone under a provider that needs no credentials; getPhysicalModel and every listing must tell them apart.
						scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "virtual", Provider: pick(append(providers, "virt", " ")), Ref: pick([]string{"v1", "v2", "m1", "gpt-4o", "claude-opus-4-6", "r1", " "})})
					case 1:
						scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "unvirtual", Provider: pick(append(providers, "virt")), Ref: pick([]string{"v1", "v2", "m1"})})
					case 2:
						scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "physical", Ref: pick(append(refs, "virt/v1", "virt/v2", "custom/v1", "openai/v2", "openai/v1"))})
					default:
						scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "compat", Ref: pick(refs)})
					}
				case 15:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "provider", Provider: pick(providers)})
				case 16:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "registeredConfig", Provider: pick(providers)})
				case 0:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "providers"})
				case 1:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "allModels", Provider: pick(append(providers, ""))})
				case 2:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "allAvailable", Provider: pick(append(providers, ""))})
				case 3, 4:
					// The registry's getProviderAuth and the runtime's getAuth resolve the same credentials; both are compared.
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "getAuth", Provider: pick(providers), Via: pick([]string{"", "registry"})})
				case 5:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "checkAuth", Provider: pick(providers)})
				case 6:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: pick([]string{"rtHasAuth", "rtOAuth", "subscription"}), Provider: pick(providers)})
				case 7:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "setKey", Provider: pick(providers), Key: pick([]string{"rt-key", "OPENAI_API_KEY", "!echo rt"})})
				case 8:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "removeKey", Provider: pick(providers)})
				case 9:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "credentials"})
				default:
					scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "logout", Provider: pick(providers)})
				}
				continue
			}
			// Pi keeps a stale entry in its availability snapshot after a provider is unregistered or models.json is rewritten and refreshed (Q14), so those reads are not generated afterwards.
			if (kind == 2 && rewritten) || ((kind == 8 || kind == 5 || kind == 10) && unregistered) {
				kind = 0
			}
			switch kind {
			case 0:
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "error"})
			case 1:
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "all", Provider: pick(providers)})
			case 2:
				// The registry's getAvailable and the runtime's getAvailableSnapshot read the same published list; both are compared.
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: pick([]string{"available", "snapshot"})})
			case 3, 4:
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "find", Ref: pick(refs)})
			case 5:
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "hasAuth", Ref: pick(refs)})
			case 6, 7:
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "headers", Ref: pick(refs)})
			case 8:
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "status", Provider: pick(providers)})
			case 9:
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: pick([]string{"display", "apiKey"}), Provider: pick(providers)})
			case 10:
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "oauth", Ref: pick(refs)})
			case 11:
				config := registryConfig{BaseURL: pick([]string{"https://reg.test/v1", ""}), API: "openai-completions", APIKey: pick([]string{"", "reg-key", "OPENAI_API_KEY", "!echo reg"})}
				if rng.IntN(2) == 0 {
					config.Name = pick([]string{"Registered", "日本語"})
				}
				if rng.IntN(3) == 0 {
					config.Headers = map[string]string{"X-R": "1"}
				}
				if rng.IntN(3) == 0 {
					b := rng.IntN(2) == 0
					config.AuthHeader = &b
				}
				for i := range rng.IntN(3) {
					config.Models = append(config.Models, registryModelConfig{ID: fmt.Sprintf("r%d", i+1), Name: pick([]string{"R", "Réponse"}), Reasoning: rng.IntN(2) == 0, Input: []string{"text"}, Cost: registryModelPrice{Input: float64(rng.IntN(5)), Output: float64(rng.IntN(5))}, ContextWindow: 1000 * (1 + rng.IntN(5)), MaxTokens: 100 * (1 + rng.IntN(5))})
				}
				name := pick([]string{"registered", "registered", "openai", "anthropic"})
				// Pi starts the refresh of a registration unawaited, so a read before it settles depends on how long a command-backed key takes to resolve (D84 records Pig's counterpart); an awaited refresh follows every registration and removal.
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "register", Name: name, Config: &config}, registryOracleOp{Op: "refresh"})
			case 12:
				unregistered = true
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "unregister", Name: pick([]string{"registered", "registered", "nope"})}, registryOracleOp{Op: "refresh"})
			case 13:
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "registered"})
			default:
				doc := `{"providers":{"custom":` + customProvider() + `}}`
				if rng.IntN(3) == 0 {
					doc = `{"providers":{"custom":{"models":[{"id":"fresh"}]}`
				}
				rewritten = true
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "rewrite", Models: &doc}, registryOracleOp{Op: "refresh"}, registryOracleOp{Op: "all", Provider: "custom"})
			}
		}
		if rng.IntN(3) == 0 {
			// Virtual models: registered beside physical models (including a replaced id), alone under a new provider, then listed and queried every way.
			provider := pick([]string{"virt", "openai", "custom", "anthropic", "registered", "second"})
			ids := []string{"v1", "v2", pick([]string{"m1", "gpt-4o", "claude-opus-4-6", "r1"})}
			for _, id := range ids[:1+rng.IntN(len(ids))] {
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "virtual", Provider: provider, Ref: id})
			}
			scenario.Ops = append(scenario.Ops, []registryOracleOp{{Op: "allModels"}, {Op: "allModels", Provider: provider}, {Op: "allAvailable"}, {Op: "allAvailable", Provider: provider}, {Op: "providers"}, {Op: "provider", Provider: provider}, {Op: "snapshot"}, {Op: "available"}, {Op: "physical", Ref: provider + "/" + ids[0]}, {Op: "physical", Ref: provider + "/" + ids[2]}, {Op: "find", Ref: provider + "/" + ids[0]}, {Op: "rtHasAuth", Provider: provider}, {Op: "all", Provider: provider}}...)
			if rng.IntN(2) == 0 {
				scenario.Ops = append(scenario.Ops, registryOracleOp{Op: "unvirtual", Provider: provider, Ref: ids[0]}, registryOracleOp{Op: "allModels", Provider: provider}, registryOracleOp{Op: "providers"}, registryOracleOp{Op: "physical", Ref: provider + "/" + ids[0]})
			}
		}
		// Pi's availability snapshot follows a virtual model registration only once its refresh lands.
		withRefresh := scenario.Ops[:0:0]
		for k, op := range scenario.Ops {
			withRefresh = append(withRefresh, op)
			if (op.Op == "virtual" || op.Op == "unvirtual") && (k+1 == len(scenario.Ops) || scenario.Ops[k+1].Op != "refresh") {
				withRefresh = append(withRefresh, registryOracleOp{Op: "refresh"})
			}
		}
		scenario.Ops = withRefresh
		scenarios = append(scenarios, scenario)
	}
	// A corrupt auth.json fails the availability refresh with JSON.parse's message, which getError reports after any models.json error; only the error text is compared because the per-provider refresh results list every provider.
	for _, corrupt := range []string{`{"anthropic":`, `{"a":1,}`, `{"anthropic":{"type":"api_key","key":"k"}} x`, `[`, `{`} {
		for _, models := range []*string{nil, new(`{"providers":`), new(`{"providers":{"custom":{"baseUrl":"https://example.test/v1","api":"openai-completions","apiKey":"k","models":[{"id":"m1"}]}}}`)} {
			doc := corrupt
			scenarios = append(scenarios, registryOracleScenario{Env: map[string]string{}, Auth: &doc, Models: models, Ops: []registryOracleOp{{Op: "error"}, {Op: "error"}}})
		}
	}
	// A provider of only virtual models needs no credentials: its auth status is configured with source "virtual", and its auth resolves with that source (model-runtime.ts registerVirtualModel, virtual-models.ts withVirtualModels).
	// A provider that something else defines keeps its own credential rules.
	for _, provider := range []string{"virt", "second", "anthropic", "custom"} {
		for _, models := range []*string{nil, new(`{"providers":{"custom":{"baseUrl":"https://example.test/v1","api":"openai-completions","models":[{"id":"m1"}]}}}`)} {
			for _, auth := range []*string{nil, new(`{"anthropic":{"type":"api_key","key":"ak"}}`)} {
				scenarios = append(scenarios, registryOracleScenario{Env: map[string]string{}, Auth: auth, Models: models, Ops: []registryOracleOp{
					{Op: "error"}, {Op: "status", Provider: provider}, {Op: "getAuth", Provider: provider, Via: "registry"}, {Op: "getAuth", Provider: provider},
					{Op: "virtual", Provider: provider, Ref: "v1"}, {Op: "refresh"},
					{Op: "status", Provider: provider}, {Op: "getAuth", Provider: provider, Via: "registry"}, {Op: "getAuth", Provider: provider}, {Op: "available"},
				}})
			}
		}
	}
	// A provider of only virtual models keeps the place of its ID in provider order (a loaded models.json lists it even when it composes to nothing), and the virtual models of an extension provider follow that provider's credentials (model-runtime.ts providerIds, virtual-models.ts withVirtualModels).
	for _, models := range []*string{nil, new(`{"providers":{"custom":{"models":[{"id":"m1"}]}}}`), new(`{"providers":{"custom":{"models":[{"id":""}]}}}`)} {
		for _, config := range []string{`{"name":"Registered","baseUrl":"https://reg.test/v1","api":"openai-completions"}`, `{"name":"Registered","baseUrl":"https://reg.test/v1","apiKey":"reg-key","api":"openai-completions"}`, `{"baseUrl":"https://reg.test/v1","apiKey":"reg-key","api":"openai-completions","models":[{"id":"r1"}]}`} {
			var register registryOracleOp
			register.Op, register.Name = "register", "registered"
			register.Config = &registryConfig{}
			if err := json.Unmarshal([]byte(config), register.Config); err != nil {
				panic(err)
			}
			scenarios = append(scenarios, registryOracleScenario{Env: map[string]string{}, Models: models, Ops: []registryOracleOp{
				{Op: "error"}, register, {Op: "refresh"},
				{Op: "virtual", Provider: "custom", Ref: "v1"}, {Op: "refresh"}, {Op: "virtual", Provider: "registered", Ref: "v2"}, {Op: "refresh"},
				{Op: "providers"}, {Op: "allModels"}, {Op: "allModels", Provider: "custom"}, {Op: "allModels", Provider: "registered"}, {Op: "allAvailable"}, {Op: "allAvailable", Provider: "registered"}, {Op: "available"},
			}})
		}
	}
	return scenarios
}

func registryModelProjection(model *ai.Model) any {
	if model == nil {
		return nil
	}
	var headers any
	if model.ProviderMeta.Headers != nil {
		headers = model.ProviderMeta.Headers
	}
	cost := model.CostRates()
	return map[string]any{"provider": model.ProviderID(), "id": model.ID, "name": model.DisplayName, "api": string(model.ProviderMeta.API), "baseUrl": model.ProviderMeta.BaseURL, "reasoning": model.ProviderMeta.Reasoning, "input": model.Input, "cost": map[string]any{"input": cost.Input, "output": cost.Output, "cacheRead": cost.CacheRead, "cacheWrite": cost.CacheWrite}, "contextWindow": model.Capabilities.ContextWindow, "maxTokens": model.Capabilities.MaxOutputTokens, "headers": headers}
}

func runRegistryOracleWithPig(t *testing.T, scenario registryOracleScenario) []any {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	modelsPath := filepath.Join(dir, "models.json")
	for key := range registryOracleClearedEnv() {
		t.Setenv(key, "")
		_ = os.Unsetenv(key)
	}
	t.Setenv("PI_OFFLINE", "1")
	for key, value := range scenario.Env {
		t.Setenv(key, value)
	}
	if scenario.Auth != nil {
		if err := os.WriteFile(authPath, []byte(*scenario.Auth), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if scenario.Models != nil {
		if err := os.WriteFile(modelsPath, []byte(*scenario.Models), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := modelsPath
	pathPointer := &path
	runtime, err := CreateModelRuntime(context.Background(), CreateModelRuntimeOptions{AuthPath: authPath, ModelsPath: &pathPointer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	registry := NewModelRegistry(runtime)
	find := func(ref string) *ai.Model {
		for _, model := range registry.GetAll() {
			if model.ProviderID()+"/"+model.ID == ref {
				return model
			}
		}
		return nil
	}
	refs := func(models []*ai.Model) []string {
		out := make([]string, 0, len(models))
		for _, model := range models {
			out = append(out, model.ProviderID()+"/"+model.ID)
		}
		return out
	}
	var outs []any
	for _, op := range scenario.Ops {
		var out any
		switch op.Op {
		case "error":
			out = nil
			if text := registry.GetError(); text != "" {
				out = strings.ReplaceAll(text, dir, "<dir>")
			}
		case "all":
			list := []string{}
			for _, ref := range refs(registry.GetAll()) {
				if op.Provider == "" || strings.HasPrefix(ref, op.Provider+"/") {
					list = append(list, ref)
				}
			}
			out = list
		case "available":
			out = append([]string{}, refs(registry.GetAvailable())...)
		case "snapshot":
			out = append([]string{}, refs(runtime.GetAvailableSnapshot())...)
		case "find":
			provider, id, _ := strings.Cut(op.Ref, "/")
			out = registryModelProjection(registry.Find(provider, id))
		case "hasAuth":
			out = "missing"
			if model := find(op.Ref); model != nil {
				out = registry.HasConfiguredAuth(model)
			}
		case "headers":
			out = "missing"
			if model := find(op.Ref); model != nil {
				result := registry.GetAPIKeyAndHeaders(context.Background(), model)
				record := map[string]any{"ok": result.OK, "apiKey": nil, "headers": nil, "baseUrl": nil, "env": nil, "error": nil}
				if result.APIKey != nil {
					record["apiKey"] = *result.APIKey
				}
				if result.Headers != nil {
					names := make([]string, 0, len(result.Headers))
					for name := range result.Headers {
						names = append(names, name)
					}
					slices.Sort(names)
					pairs := [][2]any{}
					for _, name := range names {
						pairs = append(pairs, [2]any{name, result.Headers[name]})
					}
					record["headers"] = pairs
				}
				if result.BaseURL != "" {
					record["baseUrl"] = result.BaseURL
				}
				if result.Env != nil {
					names := make([]string, 0, len(result.Env))
					for name := range result.Env {
						names = append(names, name)
					}
					slices.Sort(names)
					pairs := [][2]string{}
					for _, name := range names {
						pairs = append(pairs, [2]string{name, result.Env[name]})
					}
					record["env"] = pairs
				}
				if result.Error != "" {
					record["error"] = result.Error
				}
				out = record
			}
		case "status":
			status := registry.GetProviderAuthStatus(op.Provider)
			record := map[string]any{"configured": status.Configured, "source": nil, "label": nil}
			if status.Source != "" {
				record["source"] = string(status.Source)
			}
			if status.Label != "" {
				record["label"] = status.Label
			}
			out = record
		case "display":
			out = registry.GetProviderDisplayName(op.Provider)
		case "oauth":
			out = "missing"
			if model := find(op.Ref); model != nil {
				out = registry.IsUsingOAuth(model)
			}
		case "apiKey":
			out = nil
			if key := registry.GetAPIKeyForProvider(context.Background(), op.Provider); key != nil {
				out = *key
			}
		case "register":
			input := ProviderConfigInput{Name: op.Config.Name, BaseURL: op.Config.BaseURL, APIKey: op.Config.APIKey, API: ai.API(op.Config.API), AuthHeader: op.Config.AuthHeader}
			if len(op.Config.Headers) > 0 {
				input.Headers = op.Config.Headers
			}
			for _, config := range op.Config.Models {
				model := &ai.Model{ID: config.ID, DisplayName: config.Name, Input: config.Input}
				model.ProviderMeta.Reasoning = config.Reasoning
				model.Capabilities.ContextWindow = config.ContextWindow
				model.Capabilities.MaxOutputTokens = config.MaxTokens
				model.Capabilities.InputCostPer1M, model.Capabilities.OutputCostPer1M = config.Cost.Input, config.Cost.Output
				input.Models = append(input.Models, model)
			}
			out = nil
			if err := registry.RegisterProvider(op.Name, input); err != nil {
				out = map[string]any{"thrown": err.Error()}
			}
		case "typed":
			var list []ai.AnyModel
			if op.Provider == "" {
				list = runtime.GetModelsOfType(ai.ModelType(op.Type))
			} else {
				list = runtime.GetModelsOfType(ai.ModelType(op.Type), op.Provider)
			}
			out = anyModelRefs(list)
		case "typedOne":
			provider, id, _ := strings.Cut(op.Ref, "/")
			out = nil
			if model := registry.FindOfType(ai.ModelType(op.Type), provider, id); model != nil {
				out = model.ProviderID() + "/" + model.ModelID()
			}
		case "typedAvailable":
			var list []ai.AnyModel
			var err error
			if op.Provider == "" {
				list, err = runtime.GetAvailableOfType(context.Background(), ai.ModelType(op.Type))
			} else {
				list, err = runtime.GetAvailableOfType(context.Background(), ai.ModelType(op.Type), op.Provider)
			}
			out = anyModelRefs(list)
			if err != nil {
				out = map[string]any{"thrown": err.Error()}
			}
		case "compat":
			out = "missing"
			if model := find(op.Ref); model != nil {
				config, err := runtime.GetCompatibilityRequestConfig(model)
				if err != nil {
					out = map[string]any{"thrown": err.Error()}
				} else {
					record := map[string]any{"headers": nil, "authHeader": config.AuthHeader}
					if config.Headers != nil {
						record["headers"] = sortedPairs(len(config.Headers), func(yield func(string, any) bool) {
							for name, value := range config.Headers {
								if !yield(name, value) {
									return
								}
							}
						})
					}
					out = record
				}
			}
		case "provider":
			out = nil
			if provider := runtime.GetProvider(op.Provider); provider != nil {
				record := map[string]any{"id": provider.ID, "name": provider.Name, "baseUrl": nil, "models": []string{}}
				if provider.BaseURL != "" {
					record["baseUrl"] = provider.BaseURL
				}
				// The models the provider itself lists: virtual models follow its physical ones.
				if models, err := provider.GetModels(); err == nil {
					ids := make([]string, 0, len(models))
					for _, model := range models {
						ids = append(ids, model.ID)
					}
					record["models"] = ids
				}
				out = record
			}
		case "registeredConfig":
			out = nil
			if config := runtime.GetRegisteredProviderConfig(op.Provider); config != nil {
				record := map[string]any{"name": nil, "baseUrl": nil, "apiKey": nil, "authHeader": nil, "api": nil, "models": nil}
				if config.Name != "" {
					record["name"] = config.Name
				}
				if config.BaseURL != "" {
					record["baseUrl"] = config.BaseURL
				}
				if config.APIKey != "" {
					record["apiKey"] = config.APIKey
				}
				if config.AuthHeader != nil {
					record["authHeader"] = *config.AuthHeader
				}
				if config.API != "" {
					record["api"] = string(config.API)
				}
				if config.Models != nil {
					ids := []string{}
					for _, model := range config.Models {
						ids = append(ids, model.ModelID())
					}
					record["models"] = ids
				}
				out = record
			}
		case "providers":
			ids := []string{}
			for _, provider := range runtime.GetProviders() {
				ids = append(ids, provider.ID)
			}
			out = ids
		case "allModels":
			var list []ai.AnyModel
			if op.Provider == "" {
				list = runtime.GetAllModels()
			} else {
				list = runtime.GetAllModels(op.Provider)
			}
			out = anyModelRefs(list)
		case "allAvailable":
			var list []ai.AnyModel
			var err error
			if op.Provider == "" {
				list, err = runtime.GetAllAvailable(context.Background())
			} else {
				list, err = runtime.GetAllAvailable(context.Background(), op.Provider)
			}
			out = anyModelRefs(list)
			if err != nil {
				out = map[string]any{"thrown": err.Error()}
			}
		case "virtual":
			out = nil
			err := runtime.RegisterVirtualModel(VirtualModelDefinition{Provider: op.Provider, ID: op.Ref, Name: "Virtual " + op.Ref, Route: func(context.Context, ModelRouteRequest) (ModelRoute, error) {
				return ModelRoute{}, errors.New("unrouted")
			}})
			if err != nil {
				out = map[string]any{"thrown": err.Error()}
			}
		case "unvirtual":
			runtime.UnregisterVirtualModel(op.Provider, op.Ref)
			out = nil
		case "physical":
			provider, id, _ := strings.Cut(op.Ref, "/")
			out = nil
			if model := runtime.GetPhysicalModel(provider, id); model != nil {
				out = model.ProviderID() + "/" + model.ID
			}
		case "getAuth":
			var result *ai.AuthResult
			var err error
			if op.Via == "registry" {
				result, err = registry.GetProviderAuth(context.Background(), op.Provider)
			} else {
				result, err = runtime.GetAuth(context.Background(), op.Provider)
			}
			switch {
			case err != nil:
				out = map[string]any{"thrown": strings.ReplaceAll(err.Error(), dir, "<dir>")}
			case result == nil:
				out = nil
			default:
				record := map[string]any{"apiKey": nil, "headers": nil, "baseUrl": nil, "env": nil, "source": nil}
				if result.Auth.APIKey != "" {
					record["apiKey"] = result.Auth.APIKey
				}
				if result.Auth.Headers != nil {
					record["headers"] = sortedPairs(len(result.Auth.Headers), func(yield func(string, any) bool) {
						for name, value := range result.Auth.Headers {
							if !yield(name, value) {
								return
							}
						}
					})
				}
				if result.Auth.BaseURL != "" {
					record["baseUrl"] = result.Auth.BaseURL
				}
				if result.Env != nil {
					record["env"] = sortedPairs(len(result.Env), func(yield func(string, any) bool) {
						for name, value := range result.Env {
							if !yield(name, value) {
								return
							}
						}
					})
				}
				if result.Source != "" || result.SourcePresent {
					record["source"] = result.Source
				}
				out = record
			}
		case "checkAuth":
			check, err := runtime.CheckAuth(context.Background(), op.Provider)
			switch {
			case err != nil:
				out = map[string]any{"thrown": strings.ReplaceAll(err.Error(), dir, "<dir>")}
			case check == nil:
				out = nil
			default:
				record := map[string]any{"type": string(check.Type), "source": nil}
				if check.Source != "" {
					record["source"] = check.Source
				}
				out = record
			}
		case "rtHasAuth":
			out = runtime.HasConfiguredAuth(op.Provider)
		case "rtOAuth":
			out = runtime.IsUsingOAuth(op.Provider)
		case "subscription":
			out = runtime.IsUsingSubscription(op.Provider)
		case "setKey":
			out = nil
			if err := runtime.SetRuntimeApiKey(context.Background(), op.Provider, op.Key); err != nil {
				out = map[string]any{"thrown": err.Error()}
			}
		case "removeKey":
			out = nil
			if err := runtime.RemoveRuntimeApiKey(context.Background(), op.Provider); err != nil {
				out = map[string]any{"thrown": err.Error()}
			}
		case "credentials":
			list, err := runtime.ListCredentials(context.Background())
			entries := []string{}
			for _, info := range list {
				entries = append(entries, info.ProviderID+":"+string(info.Type))
			}
			slices.Sort(entries)
			out = entries
			if err != nil {
				out = map[string]any{"thrown": err.Error()}
			}
		case "logout":
			out = nil
			if err := runtime.Logout(context.Background(), op.Provider); err != nil {
				out = map[string]any{"thrown": err.Error()}
			}
		case "unregister":
			registry.UnregisterProvider(op.Name)
			out = nil
		case "registered":
			out = append([]string{}, runtime.GetRegisteredProviderIds()...)
		case "rewrite":
			if err := os.WriteFile(modelsPath, []byte(*op.Models), 0o644); err != nil {
				t.Fatal(err)
			}
			out = nil
		case "refresh":
			result := registry.Refresh(context.Background(), ai.ModelsRefreshOptions{})
			errs := [][2]string{}
			for _, name := range result.ErrorOrder {
				errs = append(errs, [2]string{name, strings.ReplaceAll(result.Errors[name].Error(), dir, "<dir>")})
			}
			out = map[string]any{"aborted": result.Aborted, "errors": errs}
		}
		outs = append(outs, out)
	}
	return outs
}

func anyModelRefs(models []ai.AnyModel) []string {
	refs := make([]string, 0, len(models))
	for _, model := range models {
		refs = append(refs, model.ProviderID()+"/"+model.ModelID())
	}
	return refs
}

func sortedPairs(n int, each func(yield func(string, any) bool)) [][2]any {
	pairs := make([][2]any, 0, n)
	each(func(name string, value any) bool {
		if pointer, ok := value.(*string); ok {
			if pointer == nil {
				value = nil
			} else {
				value = *pointer
			}
		}
		pairs = append(pairs, [2]any{name, value})
		return true
	})
	slices.SortFunc(pairs, func(a, b [2]any) int { return strings.Compare(a[0].(string), b[0].(string)) })
	return pairs
}

func registryOracleClearedEnv() map[string]struct{} {
	cleared := map[string]struct{}{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		for _, prefix := range []string{"AWS_", "GOOGLE_", "AZURE_", "PI_", "CLOUDFLARE", "ANTHROPIC", "OPENAI"} {
			if strings.HasPrefix(key, prefix) {
				cleared[key] = struct{}{}
			}
		}
		for _, suffix := range []string{"_API_KEY", "_TOKEN"} {
			if strings.HasSuffix(key, suffix) {
				cleared[key] = struct{}{}
			}
		}
	}
	return cleared
}

// ModelRegistry (model-registry.ts) over a real ModelRuntime against pinned Pi for seeded scenarios of temp auth.json and models.json files, environment keys, and operation sequences: the load error text,
// the catalog and its availability, a model's fields, configured-auth checks, resolved request headers, the provider auth status, display names, OAuth use, provider registration and removal, and refresh after
// a models.json rewrite. Every op's result must agree.
func TestModelRegistryMatchesPi(t *testing.T) {
	scenarios := registryOracleScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/model_registry.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]any
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, scenario := range scenarios {
		if only := os.Getenv("ORACLE_ONLY"); only != "" && only != fmt.Sprint(i) {
			continue
		}
		got := runRegistryOracleWithPig(t, scenario)
		gotJSON, _ := json.Marshal(got)
		var normalized []any
		_ = json.Unmarshal(gotJSON, &normalized)
		if reflect.DeepEqual(normalized, expected[i]) {
			continue
		}
		if failures++; failures > 5 && os.Getenv("ORACLE_ALL") == "" {
			continue
		}
		for k, op := range scenario.Ops {
			if k < len(normalized) && k < len(expected[i]) && !reflect.DeepEqual(normalized[k], expected[i][k]) {
				g, _ := json.Marshal(normalized[k])
				e, _ := json.Marshal(expected[i][k])
				opJSON, _ := json.Marshal(op)
				env, _ := json.Marshal(scenario.Env)
				models, auth := "<none>", "<none>"
				if scenario.Models != nil {
					models = *scenario.Models
				}
				if scenario.Auth != nil {
					auth = *scenario.Auth
				}
				allOps, _ := json.Marshal(scenario.Ops[:k+1])
				if gl, ok := normalized[k].([]any); ok {
					if el, ok := expected[i][k].([]any); ok {
						seen := map[string]int{}
						for _, v := range gl {
							seen[fmt.Sprint(v)]--
						}
						for _, v := range el {
							seen[fmt.Sprint(v)]++
						}
						diff := []string{}
						for key, n := range seen {
							if n != 0 {
								diff = append(diff, fmt.Sprintf("%s:%+d(Pi-Pig)", key, n))
							}
						}
						slices.Sort(diff)
						t.Logf("list diff: %.600s (Pig %d, Pi %d)", strings.Join(diff, " "), len(gl), len(el))
						for at := range min(len(gl), len(el)) {
							if fmt.Sprint(gl[at]) != fmt.Sprint(el[at]) {
								t.Logf("first order difference at %d: Pig %v, Pi %v", at, gl[at], el[at])
								break
							}
						}
					}
				}
				t.Errorf("scenario %d op %d %s\n  ops %.1500s\n  env %s\n  models.json %s\n  auth.json %s\n  Pig %.300s\n  Pi  %.300s", i, k, opJSON, allOps, env, models, auth, g, e)
				break
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}
