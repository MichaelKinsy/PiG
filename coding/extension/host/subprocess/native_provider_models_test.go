package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: model-runtime.ts keeps the model objects a provider object returns as they are, so their members stay in the order the extension
// wrote them. A record without a provider id takes this provider's, after the members it has.
func TestNativeProviderModelsKeepTheirMemberOrder(t *testing.T) {
	proxy := &nativeProviderProxy{declaration: NativeProviderDeclaration{ID: "p", Name: "P"}}
	const members = `"name":"Zed","id":"zed","api":"openai-completions","baseUrl":"https://example.test","reasoning":false,"input":["text"],"cost":{"output":2,"input":1,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100`
	records := []json.RawMessage{
		json.RawMessage(`{"provider":"p",` + members + `}`),
		json.RawMessage(`{` + members + `}`),
	}
	models, err := proxy.decodeModels(records)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := ai.EncodeModelsCatalog(models)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`{"provider":"p",` + members + `}`, `{` + members + `,"provider":"p"}`}
	if len(encoded) != len(want) {
		t.Fatalf("decoded %d models, want %d", len(encoded), len(want))
	}
	for i := range want {
		if string(encoded[i]) != want[i] {
			t.Errorf("model %d = %s\nwant      %s", i, encoded[i], want[i])
		}
	}
}
