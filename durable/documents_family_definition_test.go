package durable

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// types.ts:84-88: a family definition written in Pi's shape carries `family: true` (documents.ts:31-34 requires it at compile time, so DefineDocFamily panics
// without it); the erased definition is a family (read at documents.ts:123 and transaction.ts:525, 609); a singleton definition is not.
func TestDocFamilyDefinitionCarriesPisFamilyDiscriminator(t *testing.T) {
	semantics := DocumentSemantics{Scope: ScopeSession}
	common := CommonDocDefinition[JsonObject]{Kind: "family.test", Version: 1, CheckpointWhen: func(JsonObject, []Op, CheckpointInfo) bool { return false }}
	initial := func(JsonValue) JsonObject { return delta.NewJsonObject(0) }
	family := DefineDocFamily(DocFamilyDefinition[JsonObject, JsonValue]{Kind: common.Kind, Version: common.Version, DocumentSemantics: semantics, Family: true, Initial: initial, CheckpointWhen: func(JsonObject, []Op, CheckpointInfo) bool { return false }})
	if !family.AnyDefinition().Family {
		t.Error("the erased definition is not a family")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("a family definition without family: true was accepted")
			}
		}()
		DefineDocFamily(DocFamilyDefinition[JsonObject, JsonValue]{Kind: common.Kind, Version: common.Version, DocumentSemantics: semantics, Initial: initial, CheckpointWhen: func(JsonObject, []Op, CheckpointInfo) bool { return false }})
	}()
	common.Initial = func() JsonObject { return delta.NewJsonObject(0) }
	single := DefineDoc(DocDefinition[JsonObject]{CommonDocDefinition: common, DocumentSemantics: semantics})
	if single.AnyDefinition().Family {
		t.Error("a singleton definition is a family")
	}
}
