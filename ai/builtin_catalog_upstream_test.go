package ai

// pi: packages/ai/src/models.generated.ts
// pi: packages/ai/src/providers/amazon-bedrock.models.ts
// pi: packages/ai/src/providers/ant-ling.models.ts
// pi: packages/ai/src/providers/anthropic.models.ts
// pi: packages/ai/src/providers/azure.models.ts
// pi: packages/ai/src/providers/cerebras.models.ts
// pi: packages/ai/src/providers/cloudflare-ai-gateway.models.ts
// pi: packages/ai/src/providers/cloudflare-workers-ai.models.ts
// pi: packages/ai/src/providers/deepseek.models.ts
// pi: packages/ai/src/providers/fireworks.models.ts
// pi: packages/ai/src/providers/github-copilot.models.ts
// pi: packages/ai/src/providers/google-vertex.models.ts
// pi: packages/ai/src/providers/google.models.ts
// pi: packages/ai/src/providers/groq.models.ts
// pi: packages/ai/src/providers/huggingface.models.ts
// pi: packages/ai/src/providers/kimi-coding.models.ts
// pi: packages/ai/src/providers/meta.models.ts
// pi: packages/ai/src/providers/minimax-cn.models.ts
// pi: packages/ai/src/providers/minimax.models.ts
// pi: packages/ai/src/providers/mistral.models.ts
// pi: packages/ai/src/providers/moonshotai-cn.models.ts
// pi: packages/ai/src/providers/moonshotai.models.ts
// pi: packages/ai/src/providers/nvidia.models.ts
// pi: packages/ai/src/providers/openai-codex.models.ts
// pi: packages/ai/src/providers/openai.models.ts
// pi: packages/ai/src/providers/opencode-go.models.ts
// pi: packages/ai/src/providers/opencode.models.ts
// pi: packages/ai/src/providers/openrouter.models.ts
// pi: packages/ai/src/providers/qwen-token-plan-cn.models.ts
// pi: packages/ai/src/providers/qwen-token-plan-individual.models.ts
// pi: packages/ai/src/providers/qwen-token-plan.models.ts
// pi: packages/ai/src/providers/radius.models.ts
// pi: packages/ai/src/providers/together.models.ts
// pi: packages/ai/src/providers/typesafe.models.ts
// pi: packages/ai/src/providers/vercel-ai-gateway.models.ts
// pi: packages/ai/src/providers/xai.models.ts
// pi: packages/ai/src/providers/xiaomi-token-plan-ams.models.ts
// pi: packages/ai/src/providers/xiaomi-token-plan-cn.models.ts
// pi: packages/ai/src/providers/xiaomi-token-plan-sgp.models.ts
// pi: packages/ai/src/providers/xiaomi.models.ts
// pi: packages/ai/src/providers/zai-coding-cn.models.ts
// pi: packages/ai/src/providers/zai.models.ts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The pinned 1.0.4 provider catalogs are `providers/<id>.models.ts` modules that flatten `providers/data/<id>.json` into
// a chat, an image and a classifier model record per provider. The Go catalogs are generated from the same data: every
// provider module must list the same model keys with the same identity, endpoint, capability and price fields.
func TestBuiltinCatalogsMatchPinnedProviderDataModules(t *testing.T) {
	modules, err := filepath.Glob("../.upstream/current/packages/ai/src/providers/*.models.ts")
	if err != nil || len(modules) == 0 {
		t.Fatalf("upstream provider model modules: %v", err)
	}
	flatten := regexp.MustCompile(`flattenChatModelCatalog\("([^"]+)", values\)`)
	dataImport := regexp.MustCompile(`from "\./data/([^"]+)\.json"`)
	seen := map[string]bool{}
	for _, module := range modules {
		source, err := os.ReadFile(module)
		if err != nil {
			t.Fatal(err)
		}
		provider := flatten.FindStringSubmatch(string(source))
		data := dataImport.FindStringSubmatch(string(source))
		if provider == nil || data == nil {
			t.Fatalf("%s: no catalog flatten call or data import", module)
		}
		seen[provider[1]] = true
		t.Run(provider[1], func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("../.upstream/current/packages/ai/src/providers/data", data[1]+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var groups map[string]map[string]map[string]any
			if err := json.Unmarshal(raw, &groups); err != nil {
				t.Fatal(err)
			}
			wantChat, wantImage, wantClassifier := map[string]map[string]any{}, map[string]map[string]any{}, map[string]map[string]any{}
			for _, models := range groups {
				for key, model := range models {
					kind, id, _ := strings.Cut(key, ":")
					if model["id"] != id || model["type"] != kind || model["provider"] != provider[1] {
						t.Fatalf("%s: key %q does not match its entry", data[1], key)
					}
					switch kind {
					case "chat":
						wantChat[id] = model
					case "image":
						wantImage[id] = model
					case "classifier":
						wantClassifier[id] = model
					default:
						t.Fatalf("%s: unknown model type in key %q", data[1], key)
					}
				}
			}
			checkChatCatalog(t, provider[1], wantChat)
			checkImageCatalog(t, provider[1], wantImage)
			checkClassifierCatalog(t, provider[1], wantClassifier)
		})
	}
	if len(seen) == 0 {
		t.Fatal("no provider catalog compared")
	}
}

func catalogCost(t *testing.T, want map[string]any) ModelCost {
	t.Helper()
	encoded, err := json.Marshal(want["cost"])
	if err != nil {
		t.Fatal(err)
	}
	var cost ModelCost
	if err := json.Unmarshal(encoded, &cost); err != nil {
		t.Fatal(err)
	}
	return cost
}

func catalogStrings(value any) []string {
	var out []string
	items, _ := value.([]any)
	for _, item := range items {
		out = append(out, item.(string))
	}
	return out
}

// sameJSON reports whether a Go value marshals to the same JSON document as the upstream data value.
func sameJSON(t *testing.T, got any, want any) bool {
	t.Helper()
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(decoded, want)
}

func checkChatCatalog(t *testing.T, provider string, want map[string]map[string]any) {
	t.Helper()
	// OpenRouter's :nitro and :floor variants are PiG's addition (D199); the upstream data has none.
	got := piListedModels(ListModels(provider))
	if len(got) != len(want) {
		t.Errorf("%s chat models: %d, upstream %d", provider, len(got), len(want))
	}
	for _, model := range got {
		upstream, ok := want[model.ID]
		if !ok {
			t.Errorf("%s: Go chat model %q is not in the upstream data", provider, model.ID)
			continue
		}
		cost := catalogCost(t, upstream)
		if model.DisplayName != upstream["name"] || string(model.API) != upstream["api"] || model.BaseURL != upstream["baseUrl"] ||
			model.Reasoning != upstream["reasoning"] || float64(model.ContextWindow) != upstream["contextWindow"] || float64(model.MaxOutputTokens) != upstream["maxTokens"] {
			t.Errorf("%s/%s: identity or capability differs: %+v vs %v", provider, model.ID, model, upstream)
		}
		if model.InputCostPerMTokens != cost.Input || model.OutputCostPerMTokens != cost.Output || model.CacheReadCost != cost.CacheRead || model.CacheWriteCost != cost.CacheWrite || !reflect.DeepEqual(model.Tiers, cost.Tiers) && (len(model.Tiers) != 0 || len(cost.Tiers) != 0) {
			t.Errorf("%s/%s: cost differs: %+v vs %+v", provider, model.ID, model, cost)
		}
		if !slices.Equal(model.Capabilities, catalogStrings(upstream["input"])) {
			t.Errorf("%s/%s: input %v vs %v", provider, model.ID, model.Capabilities, upstream["input"])
		}
		if upstreamLimits, has := upstream["inputLimits"]; has != (model.InputLimits != nil) || has && !sameJSON(t, model.InputLimits, upstreamLimits) {
			t.Errorf("%s/%s: inputLimits %+v vs %v", provider, model.ID, model.InputLimits, upstreamLimits)
		}
		if upstreamHeaders, has := upstream["headers"]; has != (len(model.Headers) != 0) || has && !sameJSON(t, model.Headers, upstreamHeaders) {
			t.Errorf("%s/%s: headers %v vs %v", provider, model.ID, model.Headers, upstreamHeaders)
		}
		// Every other member Pi's chat catalog entry carries: the compat bag, prompt-cache lifetimes, the thinking-level map and
		// the gateway fields (enabled, lab, providers). Absent upstream means absent here, not an empty value.
		if upstreamCompat, has := upstream["compat"]; has != (model.Compat != nil) || has && !sameJSON(t, model.Compat, upstreamCompat) {
			t.Errorf("%s/%s: compat %+v vs %v", provider, model.ID, model.Compat, upstreamCompat)
		}
		if upstreamCache, has := upstream["promptCache"]; has != (model.PromptCache != nil) || has && !sameJSON(t, model.PromptCache, upstreamCache) {
			t.Errorf("%s/%s: promptCache %v vs %v", provider, model.ID, model.PromptCache, upstreamCache)
		}
		if upstreamLevels, has := upstream["thinkingLevelMap"]; has != (model.ThinkingLevelMap != nil) || has && !sameJSON(t, model.ThinkingLevelMap, upstreamLevels) {
			t.Errorf("%s/%s: thinkingLevelMap %v vs %v", provider, model.ID, model.ThinkingLevelMap, upstreamLevels)
		}
		if upstreamEnabled, has := upstream["enabled"]; has != (model.Enabled != nil) || has && !sameJSON(t, model.Enabled, upstreamEnabled) {
			t.Errorf("%s/%s: enabled %v vs %v", provider, model.ID, model.Enabled, upstreamEnabled)
		}
		if upstreamLab, has := upstream["lab"]; has != (model.Lab != "") || has && model.Lab != upstreamLab {
			t.Errorf("%s/%s: lab %q vs %v", provider, model.ID, model.Lab, upstreamLab)
		}
		if upstreamProviders, has := upstream["providers"]; has != (len(model.Providers) != 0) || has && !sameJSON(t, model.Providers, upstreamProviders) {
			t.Errorf("%s/%s: providers %v vs %v", provider, model.ID, model.Providers, upstreamProviders)
		}
	}
}

func checkImageCatalog(t *testing.T, provider string, want map[string]map[string]any) {
	t.Helper()
	got := GetImageModels(BuiltinImageProvider(provider))
	if len(got) != len(want) {
		t.Errorf("%s image models: %d, upstream %d", provider, len(got), len(want))
	}
	for _, model := range got {
		upstream, ok := want[model.ID]
		if !ok {
			t.Errorf("%s: Go image model %q is not in the upstream data", provider, model.ID)
			continue
		}
		if model.Name != upstream["name"] || string(model.API) != upstream["api"] || model.BaseURL != upstream["baseUrl"] ||
			!slices.Equal(model.Input, catalogStrings(upstream["input"])) || !slices.Equal(model.Output, catalogStrings(upstream["output"])) {
			t.Errorf("%s/%s: image identity differs: %+v vs %v", provider, model.ID, model, upstream)
		}
		cost := catalogCost(t, upstream)
		if model.Cost.Input != cost.Input || model.Cost.Output != cost.Output || model.Cost.CacheRead != cost.CacheRead || model.Cost.CacheWrite != cost.CacheWrite {
			t.Errorf("%s/%s: image cost %+v vs %+v", provider, model.ID, model.Cost, cost)
		}
		if upstreamLimits, has := upstream["inputLimits"]; has != (model.InputLimits != nil) || has && !sameJSON(t, model.InputLimits, upstreamLimits) {
			t.Errorf("%s/%s: image inputLimits %+v vs %v", provider, model.ID, model.InputLimits, upstreamLimits)
		}
	}
}

func checkClassifierCatalog(t *testing.T, provider string, want map[string]map[string]any) {
	t.Helper()
	got := GetBuiltinClassifierModels(provider)
	if len(got) != len(want) {
		t.Errorf("%s classifier models: %d, upstream %d", provider, len(got), len(want))
	}
	for _, model := range got {
		upstream, ok := want[model.ID]
		if !ok {
			t.Errorf("%s: Go classifier model %q is not in the upstream data", provider, model.ID)
			continue
		}
		cost := catalogCost(t, upstream)
		if model.Name != upstream["name"] || string(model.API) != upstream["api"] || model.BaseURL != upstream["baseUrl"] || float64(model.ContextWindow) != upstream["contextWindow"] ||
			!slices.Equal(model.Input, catalogStrings(upstream["input"])) || model.Cost.Input != cost.Input || model.Cost.Output != cost.Output || model.Cost.CacheRead != cost.CacheRead || model.Cost.CacheWrite != cost.CacheWrite {
			t.Errorf("%s/%s: classifier differs: %+v vs %v", provider, model.ID, model, upstream)
		}
	}
}

// barrelRecordKeys returns the provider keys, in source order, of the `export const <name>` record of the pinned catalog barrel
// (models.generated.ts), with the shard identifier each key is bound to.
func barrelRecordKeys(t *testing.T, source, name string) (keys, shards []string) {
	t.Helper()
	_, rest, ok := strings.Cut(source, "export const "+name+": {")
	if !ok {
		t.Fatalf("models.generated.ts: no export %s", name)
	}
	_, rest, ok = strings.Cut(rest, "} = {\n")
	if !ok {
		t.Fatalf("models.generated.ts: %s has no value", name)
	}
	body, _, ok := strings.Cut(rest, "\n};")
	if !ok {
		t.Fatalf("models.generated.ts: %s value is unterminated", name)
	}
	for _, match := range regexp.MustCompile(`(?m)^\t"([^"]+)": ([A-Z0-9_]+),$`).FindAllStringSubmatch(body, -1) {
		keys, shards = append(keys, match[1]), append(shards, match[2])
	}
	return keys, shards
}

// The pinned models.generated.ts is the catalog barrel: MODELS, IMAGE_MODELS and CLASSIFIER_MODELS each bind every provider id to that
// provider's `<ID>_MODELS`, `<ID>_IMAGE_MODELS` or `<ID>_CLASSIFIER_MODELS` shard in one shared key order. The Go catalog must list the
// same providers and keep each type's records grouped by provider in that order.
func TestBuiltinCatalogRecordsFollowTheBarrelProviderOrder(t *testing.T) {
	raw, err := os.ReadFile("../.upstream/current/packages/ai/src/models.generated.ts")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	chatKeys, chatShards := barrelRecordKeys(t, source, "MODELS")
	imageKeys, imageShards := barrelRecordKeys(t, source, "IMAGE_MODELS")
	classifierKeys, classifierShards := barrelRecordKeys(t, source, "CLASSIFIER_MODELS")
	if len(chatKeys) == 0 || !slices.Equal(chatKeys, imageKeys) || !slices.Equal(chatKeys, classifierKeys) {
		t.Fatalf("barrel records disagree: MODELS %v, IMAGE_MODELS %v, CLASSIFIER_MODELS %v", chatKeys, imageKeys, classifierKeys)
	}
	for i, provider := range chatKeys {
		id := strings.ToUpper(strings.ReplaceAll(provider, "-", "_"))
		if chatShards[i] != id+"_MODELS" || imageShards[i] != id+"_IMAGE_MODELS" || classifierShards[i] != id+"_CLASSIFIER_MODELS" {
			t.Errorf("%s is bound to %s, %s, %s", provider, chatShards[i], imageShards[i], classifierShards[i])
		}
	}
	if got := ListProviders(); !slices.Equal(got, chatKeys) {
		t.Fatalf("providers = %v, barrel %v", got, chatKeys)
	}
	position := map[string]int{}
	for i, provider := range chatKeys {
		position[provider] = i
	}
	// grouped checks that the records of each provider are contiguous and the providers appear in barrel order.
	grouped := func(kind string, providers []string) {
		t.Helper()
		last := -1
		for i, provider := range providers {
			at, ok := position[provider]
			if !ok {
				t.Fatalf("%s: record of %q, a provider the barrel does not list", kind, provider)
			}
			if i > 0 && provider == providers[i-1] {
				continue
			}
			if at <= last {
				t.Errorf("%s: provider %q appears out of barrel order or again after its group", kind, provider)
			}
			last = at
		}
	}
	var chat, image, classifier []string
	for _, model := range piListedModels(GeneratedModels) {
		chat = append(chat, model.Provider)
	}
	for _, model := range GeneratedImageModels {
		image = append(image, model.Provider)
	}
	for _, model := range GeneratedClassifierModels {
		classifier = append(classifier, model.Provider)
	}
	grouped("MODELS", chat)
	grouped("IMAGE_MODELS", image)
	grouped("CLASSIFIER_MODELS", classifier)
}
