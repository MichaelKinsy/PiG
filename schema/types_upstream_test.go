package schema_test

// Ports the two function types of packages/schema/src/types.ts: FormatCheck (types.ts:10) and Check (types.ts:36).

import (
	"testing"

	"github.com/MichaelKinsy/PiG/schema"
)

// types.ts:10 `FormatCheck = (value: string) => boolean`: compile options map a format name to one, and `format` asserts it.
func TestFormatCheckIsTheCheckOfAFormatName(t *testing.T) {
	even := schema.FormatCheck(func(value string) bool { return len(value)%2 == 0 })
	v := mustCompile(t, map[string]any{"type": "string", "format": "even"}, schema.CompileOptions{Formats: map[string]schema.FormatCheck{"even": even}})
	if !v.Check("ab") {
		t.Error("a string the format accepts failed")
	}
	if v.Check("abc") {
		t.Error("a string the format refuses passed")
	}
}

// types.ts:36 `Check = (value: unknown) => boolean`: a compiled check answers for one value.
func TestCheckIsABooleanFunctionOfAValue(t *testing.T) {
	var isString schema.Check = func(value any) bool {
		_, ok := value.(string)
		return ok
	}
	if !isString("a") || isString(1.0) || isString(nil) {
		t.Error("a Check must answer true for exactly the values it accepts")
	}
}
