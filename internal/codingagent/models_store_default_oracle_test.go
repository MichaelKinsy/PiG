package codingagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/models-store.ts
// model-runtime.ts:226 gives a runtime that has a models.json path and no store of its own a FileModelsStore at models-store.json next to it
// (models-store.ts FileModelsStore, JSON.stringify(entries, null, 2)). Pi's ModelRuntime.create and PiG's registry persist the same catalog,
// byte for byte but for the checkedAt clock, into that file when a credential carries a legacy gateway config.
func TestDefaultModelsStoreNextToModelsJSONMatchesPi(t *testing.T) {
	gatewayConfig := radiusTestConfigJSON("https://radius.example.com/v1")
	var want struct {
		Files []string `json:"files"`
		Text  string   `json:"text"`
	}
	pioracle.Run(t, `
const { mkdtempSync, readdirSync, readFileSync } = await import("node:fs");
const { tmpdir } = await import("node:os");
const { join } = await import("node:path");
const { ModelRuntime } = await load("pi-coding-agent/core/model-runtime.js");
const { AuthStorage } = await load("pi-coding-agent/core/auth-storage.js");
const dir = mkdtempSync(join(tmpdir(), "pi-models-store-default-"));
await ModelRuntime.create({
	credentials: AuthStorage.inMemory({ radius: { type: "oauth", access: "access-token", refresh: "refresh-token", expires: Date.now() + 3600000, gatewayConfig: JSON.parse(input.gatewayConfig) } }),
	modelsPath: join(dir, "models.json"),
	allowModelNetwork: false,
});
emit({ files: readdirSync(dir), text: readFileSync(join(dir, "models-store.json"), "utf8") });`, map[string]any{"gatewayConfig": gatewayConfig}, &want)
	if !reflect.DeepEqual(want.Files, []string{"models-store.json"}) {
		t.Fatalf("Pi wrote %v", want.Files)
	}

	dir := t.TempDir()
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Set("radius", radiusOAuthCredential(gatewayConfig)); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistryWithModelsPath(filepath.Join(dir, "models.json"))
	registry.SetAuthStorage(auth)
	if result := registry.RefreshCatalogs(context.Background(), CatalogRefreshOptions{}); len(result.Errors) != 0 || result.Aborted {
		t.Fatalf("refresh = %+v", result)
	}
	got, err := os.ReadFile(filepath.Join(dir, "models-store.json"))
	if err != nil {
		t.Fatal(err)
	}
	// checkedAt is the clock of the write; the stored values must equal Pi's. Pi lists a model's api, provider and baseUrl after its
	// configured fields where PiG's model encoder lists them first, so the order of keys inside a model is not compared.
	checkedAt := regexp.MustCompile(`"checkedAt": ([0-9.e+]+)`)
	if checkedAt.FindString(string(got)) == "" || checkedAt.FindString(want.Text) == "" {
		t.Fatalf("checkedAt missing: PiG %s, Pi %s", got, want.Text)
	}
	decode := func(text string) any {
		var value any
		if err := json.Unmarshal([]byte(checkedAt.ReplaceAllString(text, `"checkedAt": 0`)), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if gotValue, wantValue := decode(string(got)), decode(want.Text); !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("models-store.json = %s\nPi wrote %s", got, want.Text)
	}
	// The layout is JSON.stringify(entries, null, 2): two-space indent, no trailing newline.
	layout := "{\n  \"radius\": {\n    \"models\": [\n      {\n        \"id\": \"auto\","
	if !strings.HasPrefix(string(got), layout) || !strings.HasPrefix(want.Text, layout) || strings.HasSuffix(string(got), "\n") || strings.HasSuffix(want.Text, "\n") {
		t.Fatalf("layout differs from JSON.stringify(entries, null, 2): PiG %q, Pi %q", got, want.Text)
	}

	// Without a models.json path the store is in memory: nothing is written anywhere.
	memory := NewModelRegistryWithModelsPath("")
	if _, ok := memory.catalogStore().(*ai.InMemoryModelsStore); !ok {
		t.Fatalf("store without a models path = %T, want the in-memory store", memory.catalogStore())
	}
}
