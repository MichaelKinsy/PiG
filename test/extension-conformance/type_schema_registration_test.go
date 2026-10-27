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
	return typeVectorParameters("conformance tool parameters")
}

// typeEngineToolParameters is the schema Pi's Type returned for the parameters of the tool "type-engine-tool" ("conformance engine tool parameters"): one property per member of Type
// that the host's type engine serves.
func typeEngineToolParameters() json.RawMessage {
	return typeVectorParameters("conformance engine tool parameters")
}

func typeVectorParameters(caseName string) json.RawMessage {
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
		if c.Name == caseName {
			var result struct{ JSON string }
			if err := json.Unmarshal(c.Result, &result); err != nil {
				panic(err)
			}
			return json.RawMessage(result.JSON)
		}
	}
	panic("the vector \"" + caseName + "\" is missing")
}

// TestConformance_TypeSchemaToolParametersAgreeAcrossSDKs: every SDK registers the tool "type-schema-tool" with parameters built by pi-ai's Type (Go sdk.Type, Python Type, Rust Type; the
// Node runtime runs Pi's own), and the host holds the schema Pi's Type returns for the same calls: the vector "conformance tool parameters" of test/parity/sdk-vectors/pi-ai/Type.json.
// An SDK whose Type returned another schema (an Optional property listed as required, a Record without its pattern, a changed key order, the root's included) fails here.
func TestConformance_TypeSchemaToolParametersAgreeAcrossSDKs(t *testing.T) {
	typeToolParametersAgree(t, "type-schema-tool", "conformance tool parameters")
}

// TestConformance_TypeEngineMembersAgreeAcrossSDKs: every SDK registers the tool "type-engine-tool" whose parameters have one property per member of Type that the host's type engine
// serves (Pick, Omit, Partial, Required, KeyOf, Index, Evaluate, Exclude, Extract, the intrinsics, Function, Tuple, TemplateLiteral, Conditional, Mapped, Cyclic, Module, Interface,
// Instantiate, Call, Script, ...), and the host holds the schema Pi's Type returns for the same calls (the vector "conformance engine tool parameters"). Each SDK reaches the engine
// through its own blocking host call while the extension's factory runs, isolated and fused; the Node cells run Pi's own Type. An SDK that returned a constant, or another
// schema for any member, fails here.
func TestConformance_TypeEngineMembersAgreeAcrossSDKs(t *testing.T) {
	typeToolParametersAgree(t, "type-engine-tool", "conformance engine tool parameters")
}

func typeToolParametersAgree(t *testing.T, toolName, caseName string) {
	var want string
	for _, c := range piVectorCases(t, "pi-ai", "Type") {
		if c.Name != caseName {
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
		t.Fatalf("the vector %q is missing", caseName)
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
				if tool.Definition.Name != toolName {
					continue
				}
				// Compared in order, root included: the host and the provider see the member order Pi's Type wrote (the host keeps the order the registration carried).
				if got, expected := piAiCanonicalJSON(t, tool.Definition.Parameters, true), piAiCanonicalJSON(t, []byte(want), true); got != expected {
					t.Fatalf("parameters %s, Pi's Type returns %s", got, expected)
				}
				return
			}
			t.Fatalf("%s is not registered", toolName)
		})
	}
}
