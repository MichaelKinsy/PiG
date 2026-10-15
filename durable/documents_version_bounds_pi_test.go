package durable

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// TestDocDefinitionVersionMustBeAPositiveSafeInteger ports documents.ts:96-97: `!Number.isSafeInteger(version) || version < 1` throws
// TypeError "Document <kind> version must be a positive integer" for singleton and family definitions alike; 1 and 2^53-1 are accepted.
func TestDocDefinitionVersionMustBeAPositiveSafeInteger(t *testing.T) {
	semantics := DocumentSemantics{Scope: ScopeSession}
	define := func(version int, family bool) (message string) {
		defer func() {
			if recovered := recover(); recovered != nil {
				message, _ = recovered.(string)
			}
		}()
		if family {
			DefineDocFamily(DocFamilyDefinition[JsonObject, JsonValue]{Kind: "v.test", Version: version, DocumentSemantics: semantics, Family: true,
				Initial: func(JsonValue) JsonObject { return delta.NewJsonObject(0) }})
			return ""
		}
		DefineDoc(DocDefinition[JsonObject]{CommonDocDefinition: CommonDocDefinition[JsonObject]{Kind: "v.test", Version: version,
			Initial: func() JsonObject { return delta.NewJsonObject(0) }}, DocumentSemantics: semantics})
		return ""
	}
	for _, family := range []bool{false, true} {
		for _, version := range []int{0, -1, maxSafeInteger + 1} {
			if got := define(version, family); !strings.Contains(got, "Document v.test version must be a positive integer") {
				t.Errorf("family=%v version %d: panic %q, want the positive-integer error", family, version, got)
			}
		}
		for _, version := range []int{1, maxSafeInteger} {
			if got := define(version, family); got != "" {
				t.Errorf("family=%v version %d rejected: %q", family, version, got)
			}
		}
	}
}
