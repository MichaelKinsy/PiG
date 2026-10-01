package ai

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// publishedCatalogScript imports the published pi-ai barrel (dist/models.generated.js) and lists every provider's chat,
// image and classifier models in the order JavaScript enumerates them: the export upstream tests and callers see.
const publishedCatalogScript = `import { pathToFileURL } from "node:url";
const mod = await import(pathToFileURL(process.argv[1]).href);
const list = (catalog) => Object.entries(catalog).flatMap(([provider, models]) => Object.values(models));
process.stdout.write(JSON.stringify({ chat: list(mod.MODELS), image: list(mod.IMAGE_MODELS), classifier: list(mod.CLASSIFIER_MODELS) }));`

type publishedCatalog map[string][]map[string]any

// loadPublishedCatalog is the independent denominator for the generated catalogs: the exact pinned pi-ai package that
// `make model-catalogs` reads, evaluated by Node. It skips when the package is not installed.
func loadPublishedCatalog(t *testing.T) publishedCatalog {
	t.Helper()
	dist, err := filepath.Abs(filepath.Join("..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "node_modules", "@earendil-works", "pi-ai", "dist"))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dist, "models.generated.js")
	if _, err := os.Stat(source); err != nil {
		t.Skipf("published pi-ai %s is not installed: %v", UpstreamVersionString(), err)
	}
	manifest, err := os.ReadFile(filepath.Join(dist, "..", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(manifest, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Version != UpstreamVersionString() {
		t.Fatalf("installed pi-ai is %s, want the pinned %s", pkg.Version, UpstreamVersionString())
	}
	command := exec.Command("node", "--input-type=module", "-e", publishedCatalogScript, source)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("import published catalog: %v\n%s", err, stderr.String())
	}
	var catalog publishedCatalog
	if err := json.Unmarshal(out, &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}

// canonicalJSON round-trips a value through encoding/json so maps compare by content and numbers by value.
func canonicalJSON(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func generatedModelJSON(model GeneratedModel) map[string]any {
	cost := map[string]any{"input": model.InputCostPerMTokens, "output": model.OutputCostPerMTokens, "cacheRead": model.CacheReadCost, "cacheWrite": model.CacheWriteCost}
	if len(model.Tiers) > 0 {
		cost["tiers"] = model.Tiers
	}
	out := map[string]any{"type": "chat", "id": model.ID, "name": model.DisplayName, "api": string(model.API), "provider": model.Provider, "baseUrl": model.BaseURL,
		"reasoning": model.Reasoning, "input": model.Capabilities, "cost": cost, "contextWindow": model.ContextWindow, "maxTokens": model.MaxOutputTokens}
	set := func(name string, present bool, value any) {
		if present {
			out[name] = value
		}
	}
	set("headers", len(model.Headers) > 0, model.Headers)
	set("compat", model.Compat != nil, model.Compat)
	set("thinkingLevelMap", len(model.ThinkingLevelMap) > 0, model.ThinkingLevelMap)
	set("samplingParams", len(model.SamplingParams) > 0, model.SamplingParams)
	set("promptCache", len(model.PromptCache) > 0, model.PromptCache)
	set("inputLimits", model.InputLimits != nil, model.InputLimits)
	set("enabled", model.Enabled != nil, model.Enabled)
	set("lab", model.Lab != "", model.Lab)
	set("providers", len(model.Providers) > 0, model.Providers)
	return out
}

// TestGeneratedCatalogsMatchPublishedPackage compares every generated chat, image and classifier model, in order and field
// by field, with the published pinned pi-ai catalog. It fails when the generator drops, reorders or alters catalog data.
func TestGeneratedCatalogsMatchPublishedPackage(t *testing.T) {
	published := loadPublishedCatalog(t)
	var chat, image, classifier []any
	for _, model := range GeneratedModels {
		chat = append(chat, generatedModelJSON(model))
	}
	for _, model := range GeneratedImageModels {
		entry := map[string]any{"type": "image", "id": model.ID, "name": model.Name, "api": string(model.API), "provider": model.Provider, "baseUrl": model.BaseURL, "input": model.Input, "output": model.Output, "cost": model.Cost}
		if len(model.Headers) > 0 {
			entry["headers"] = model.Headers
		}
		if model.InputLimits != nil {
			entry["inputLimits"] = model.InputLimits
		}
		image = append(image, entry)
	}
	for _, model := range GeneratedClassifierModels {
		entry := map[string]any{"type": "classifier", "id": model.ID, "name": model.Name, "api": string(model.API), "provider": model.Provider, "baseUrl": model.BaseURL, "input": model.Input, "cost": model.Cost, "contextWindow": model.ContextWindow}
		if len(model.Headers) > 0 {
			entry["headers"] = model.Headers
		}
		if model.InputLimits != nil {
			entry["inputLimits"] = model.InputLimits
		}
		classifier = append(classifier, entry)
	}
	for name, got := range map[string][]any{"chat": chat, "image": image, "classifier": classifier} {
		want := published[name]
		if len(got) != len(want) {
			t.Errorf("%s catalog has %d models, published %s has %d", name, len(got), UpstreamVersionString(), len(want))
			continue
		}
		for i := range want {
			if !reflect.DeepEqual(canonicalJSON(t, got[i]), canonicalJSON(t, want[i])) {
				t.Errorf("%s model %d (%v/%v) differs from the published catalog:\n got %v\nwant %v", name, i, want[i]["provider"], want[i]["id"], canonicalJSON(t, got[i]), canonicalJSON(t, want[i]))
				break
			}
		}
	}
	if len(chat) == 0 || len(image) == 0 || len(classifier) == 0 {
		t.Fatalf("the pinned catalog has chat, image and classifier models; generated %d, %d and %d", len(chat), len(image), len(classifier))
	}
}
