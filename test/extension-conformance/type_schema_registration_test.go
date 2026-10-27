package extensionconformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// typeSchemaToolParameters is the schema Pi's Type returned for the parameters the SDK fixtures build ("conformance tool parameters" in test/parity/sdk-vectors/pi-ai/Type.json). The
// in-process Go reference registers a tool of that name with it, so every transport lists the same tools (TestConformance_TransportsMatch); the SDK cells register the same tool built
// with their own Type, and TestConformance_TypeSchemaToolParametersAgreeAcrossSDKs compares what the host holds.
func typeSchemaToolParameters() json.RawMessage {
	raw, err := os.ReadFile(filepath.Join("..", "parity", "sdk-vectors", "pi-ai", "Type.json"))
	if err != nil {
		panic(err)
	}
	var file struct {
		Cases []struct {
			Name   string
			Result json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		panic(err)
	}
	for _, c := range file.Cases {
		if c.Name == "conformance tool parameters" {
			var result struct{ JSON string }
			if err := json.Unmarshal(c.Result, &result); err != nil {
				panic(err)
			}
			return json.RawMessage(result.JSON)
		}
	}
	panic("the vector \"conformance tool parameters\" is missing")
}

// TestConformance_TypeSchemaToolParametersAgreeAcrossSDKs: every SDK registers the tool "type-schema-tool" with parameters built by pi-ai's Type (Go sdk.Type, Python Type, Rust Type; the
// Node runtime runs Pi's own), and the host holds the schema Pi's Type returns for the same calls: the vector "conformance tool parameters" of test/parity/sdk-vectors/pi-ai/Type.json.
// An SDK whose Type returned another schema (an Optional property listed as required, a Record without its pattern, a changed key order, the root's included) fails here.
func TestConformance_TypeSchemaToolParametersAgreeAcrossSDKs(t *testing.T) {
	var want string
	for _, c := range piVectorCases(t, "pi-ai", "Type") {
		if c.Name != "conformance tool parameters" {
			continue
		}
		var result struct {
			JSON string `json:"json"`
		}
		if err := json.Unmarshal(c.Result, &result); err != nil || result.JSON == "" {
			t.Fatalf("vector result %s: %v", c.Result, err)
		}
		want = result.JSON
	}
	if want == "" {
		t.Fatal("the vector \"conformance tool parameters\" is missing")
	}
	for _, tc := range allHarnessCases() {
		if tc.name == "inproc-go" {
			continue // the in-process Go reference registers through the host API, with no SDK (and so no sdk.Type) in the loop
		}
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
			for _, tool := range h.runner.Tools() {
				if tool.Definition.Name != "type-schema-tool" {
					continue
				}
				// Compared in order, root included: the host and the provider see the member order Pi's Type wrote (the host keeps the order the registration carried).
				if got, expected := piAiCanonicalJSON(t, tool.Definition.Parameters, true), piAiCanonicalJSON(t, []byte(want), true); got != expected {
					t.Fatalf("parameters %s, Pi's Type returns %s", got, expected)
				}
				return
			}
			t.Fatal("type-schema-tool is not registered")
		})
	}
}
