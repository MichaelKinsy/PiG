package extensionconformance

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// scopedModelsWireValue projects the reference's native pointers only when comparing subprocess JSON; native callback/list identity is tested separately.
func scopedModelsWireValue(models []extension.ScopedModel) []map[string]any {
	result := make([]map[string]any, len(models))
	for i, scoped := range models {
		result[i] = map[string]any{"model": extension.ModelInfo(scoped.Model)}
		if scoped.ThinkingLevel != "" {
			result[i]["thinkingLevel"] = string(scoped.ThinkingLevel)
		}
	}
	return result
}

// Based on the scope-only conformance in cdd29705f; runner.ts:841-848 returns live ordered values rather than a constant empty list.
func TestScopedModelsAcrossSDKs(t *testing.T) {
	cases := allHarnessCases()
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedScopeHarness(t, language) }})
	}
	for _, tc := range cases {
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
			var models []extension.ScopedModel
			getter := func() []extension.ScopedModel { return models }
			h.runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{GetScopedModels: getter}, nil)
			if h.bridge != nil {
				h.bridge.SetHostAction("getScopedModels", getter)
			}
			for _, value := range [][]extension.ScopedModel{
				{},
				{{Model: &ai.Model{ID: "second", ProviderMeta: ai.ProviderMetadata{ProviderID: "scope"}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000}}, ThinkingLevel: "high"}, {Model: &ai.Model{ID: "first", ProviderMeta: ai.ProviderMetadata{ProviderID: "scope"}}}},
				{},
			} {
				models = value
				for _, name := range []string{"scoped-models-probe", "scoped-models-probe-two"} {
					command, ok := findCommand(h.runner, name)
					if !ok {
						if name == "scoped-models-probe-two" {
							continue
						}
						t.Fatal("scope probe missing")
					}
					h.ui.ClearRecorded()
					cc := h.runner.CreateCommandContext()
					ctx := extension.WithCommandContext(extension.WithContext(t.Context(), cc.Context), cc)
					if err := command.Handler(ctx, ""); err != nil {
						t.Fatal(err)
					}
					waitFor(t, func() bool { return len(h.ui.Recorded()) > 0 })
					recorded := h.ui.Recorded()
					if len(recorded) != 1 {
						t.Fatalf("notifications=%v", recorded)
					}
					var got, want any
					if err := json.Unmarshal([]byte(strings.TrimSuffix(recorded[0], ":info")), &got); err != nil {
						t.Fatal(err)
					}
					encoded, err := json.Marshal(scopedModelsWireValue(value))
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(encoded, &want); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("scope=%v; want %v", got, want)
					}
				}
			}
		})
	}
}
