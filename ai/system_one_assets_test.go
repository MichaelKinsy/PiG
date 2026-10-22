package ai

import (
	"reflect"
	"testing"
)

// Ports packages/ai/src/api/system-one-shared.ts SystemOneWireRequest.assets (Record<string, SystemOneImageAsset>): keyed lookup and
// own-key iteration in insertion order, which the wire body keeps.
func TestSystemOneImageAssetsKeepInsertionOrderAndLookupByName(t *testing.T) {
	assets := SystemOneImageAssets{
		Names:  []string{"<image-2>", "<image-1>"},
		Assets: []SystemOneImageAsset{{Format: "png", DataBase64: "Mg=="}, {Format: "jpeg", DataBase64: "MQ=="}},
	}
	var names []string
	for name, asset := range assets.All() {
		got, ok := assets.Get(name)
		if !ok || !reflect.DeepEqual(got, asset) {
			t.Fatalf("Get(%q) = %+v, %v; want %+v", name, got, ok, asset)
		}
		names = append(names, name)
	}
	if !reflect.DeepEqual(names, []string{"<image-2>", "<image-1>"}) {
		t.Fatalf("All names = %v", names)
	}
	if _, ok := assets.Get("<image-3>"); ok {
		t.Fatal("Get found a name that is not an asset")
	}
	for range assets.All() {
		break // a yield that returns false stops the iteration
	}
}
