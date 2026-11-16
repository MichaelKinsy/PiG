package codingagent

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The frame's model array is converted by several goroutines; at the caller's boundary it is the bytes one goroutine writes over the registry's whole
// catalog, OpenRouter's routing variants (D199) included.
func TestExtensionCatalogFrameModelsEqualTheSerialConversion(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	catalog := registry.GetAllModelData()
	want := []byte{'['}
	for _, model := range catalog {
		object, err := extension.ModelObject(model)
		if err != nil {
			t.Fatal(err)
		}
		if object == nil {
			continue
		}
		if len(want) > 1 {
			want = append(want, ',')
		}
		want = append(want, object...)
	}
	want = append(want, ']')
	state := (&extensionCatalogEncoding{}).state(registry, catalog)
	models, ok := state["models"].(json.RawMessage)
	if !ok {
		t.Fatalf("the state's models are %T, want the encoded array", state["models"])
	}
	if !bytes.Equal(models, want) {
		t.Fatalf("the frame's %d model bytes differ from the %d a single goroutine writes over %d models (%d of ai.GeneratedModels)", len(models), len(want), len(catalog), len(ai.GeneratedModels))
	}
}
