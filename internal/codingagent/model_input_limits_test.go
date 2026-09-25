package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Mirrors upstream model-registry.test.ts: deeply merges inputLimits image overrides.
func TestModelRegistryInputLimitsDeepMerge(t *testing.T) {
	dir := t.TempDir()
	config := `{"providers":{"test":{"api":"openai-completions","baseUrl":"https://example.test","models":[{"id":"vision-model","inputLimits":{"maxRequestBytes":33554432,"images":{"maxPerRequest":100,"resize":{"maxWidth":2000,"maxHeight":2000,"maxBytes":4718592,"jpegQuality":80}}}}],"modelOverrides":{"vision-model":{"inputLimits":{"images":{"resize":{"maxWidth":1568,"maxBytes":524288,"jpegQuality":75}}}}}}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	if err := registry.LoadError(); err != "" {
		t.Fatal(err)
	}
	entry, ok := registry.Resolve("test", "vision-model")
	if !ok {
		t.Fatal("model not resolved")
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal([]byte(`{"maxRequestBytes":33554432,"images":{"maxPerRequest":100,"resize":{"maxWidth":1568,"maxHeight":2000,"maxBytes":524288,"jpegQuality":75}}}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fields["InputLimits"], want) {
		t.Fatalf("InputLimits = %v, want %v", fields["InputLimits"], want)
	}
}

func TestModelRegistryInputLimitsValidation(t *testing.T) {
	for _, limits := range []string{`null`, `[]`, `{"maxRequestBytes":0}`, `{"maxRequestBytes":null}`, `{"maxRequestBytes":1.5}`, `{"images":null}`, `{"images":{"maxPerMessage":-1}}`, `{"images":{"maxPerRequest":"100"}}`, `{"images":{"resize":null}}`, `{"images":{"resize":{"maxWidth":0}}}`, `{"images":{"resize":{"maxHeight":null}}}`, `{"images":{"resize":{"maxBytes":0}}}`, `{"images":{"resize":{"jpegQuality":101}}}`} {
		for _, target := range []string{"models", "modelOverrides"} {
			t.Run(target+"/"+limits, func(t *testing.T) {
				dir := t.TempDir()
				definition := fmt.Sprintf(`"models":[{"id":"m","inputLimits":%s}]`, limits)
				if target == "modelOverrides" {
					definition = fmt.Sprintf(`"modelOverrides":{"m":{"inputLimits":%s}}`, limits)
				}
				config := fmt.Sprintf(`{"providers":{"test":{"baseUrl":"https://example.test",%s}}}`, definition)
				if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0600); err != nil {
					t.Fatal(err)
				}
				if NewModelRegistry(dir).LoadError() == "" {
					t.Fatalf("accepted invalid inputLimits: %s", limits)
				}
			})
		}
	}
}

func TestModelRegistryInputLimitsOptionalObjectsAndIntegerSpellings(t *testing.T) {
	for _, limits := range []string{`{}`, `{"images":{}}`, `{"images":{"resize":{}}}`, `{"maxRequestBytes":1e3,"images":{"resize":{"jpegQuality":100.0}}}`} {
		t.Run(limits, func(t *testing.T) {
			var definition modelDefinition
			if err := json.Unmarshal([]byte(`{"id":"m","inputLimits":`+limits+`}`), &definition); err != nil {
				t.Fatal(err)
			}
			if definition.InputLimits == nil {
				t.Fatal("present object was dropped")
			}
		})
	}
}

func TestModelRegistryInputLimitsResolutionDoesNotAlias(t *testing.T) {
	dir := t.TempDir()
	config := `{"providers":{"test":{"baseUrl":"https://example.test","models":[{"id":"m","inputLimits":{"images":{"resize":{"maxWidth":1200}}}}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	first, ok := registry.Resolve("test", "m")
	if !ok {
		t.Fatal("missing model")
	}
	first.InputLimits.Images.Resize.MaxWidth = 1
	second, ok := registry.Resolve("test", "m")
	if !ok {
		t.Fatal("missing model")
	}
	if second.InputLimits.Images.Resize.MaxWidth != 1200 {
		t.Fatal("resolved model aliases configuration")
	}
}
