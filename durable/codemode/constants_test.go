package codemode

import (
	"encoding/json"
	"testing"
)

// packages/durable/src/codemode/codemode.ts:86 CODEMODE_MODULE_VERSION, :88 CODEMODE_TOOL_NAME, describe.ts:23
// DEFAULT_CODEMODE_INLINE_BUDGET, codemode.ts:159 CodemodeStoreDoc (kind, version, semantics), :208 CodemodeToolInput.
func TestExportedConstantsAndDocumentMatchPi(t *testing.T) {
	if ModuleVersion != 2 || ToolName != "codemode" || DefaultInlineBudget != 3000 {
		t.Errorf("ModuleVersion %d, ToolName %q, DefaultInlineBudget %d", ModuleVersion, ToolName, DefaultInlineBudget)
	}
	definition := StoreDoc.AnyDefinition()
	if definition.Kind != "pi.codemode.store" || definition.Version != 1 || definition.Family {
		t.Errorf("store document %q v%d family %v", definition.Kind, definition.Version, definition.Family)
	}
	initial, err := definition.Initial(nil)
	if err != nil || initial == nil {
		t.Fatalf("initial %v, %v", initial, err)
	}
	if values, ok := initial.Get("values"); !ok || values == nil {
		t.Errorf("the initial store has no values object: %v", initial)
	}
	var input ToolInput
	if err := json.Unmarshal([]byte(`{"code":"return 1"}`), &input); err != nil || input.Code != "return 1" {
		t.Errorf("tool input %+v, %v", input, err)
	}
	if encoded, err := json.Marshal(ToolInput{Code: "x"}); err != nil || string(encoded) != `{"code":"x"}` {
		t.Errorf("tool input encodes as %s, %v", encoded, err)
	}
}
