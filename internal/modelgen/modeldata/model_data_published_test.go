package modeldata

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// The schema version is a constant of the pinned scripts (model-data.ts:5).
func TestModelDataSchemaVersionIsPinned(t *testing.T) {
	if ModelDataSchemaVersion != 6 {
		t.Fatalf("ModelDataSchemaVersion = %d, want 6 (packages/ai/scripts/model-data.ts at %s)", ModelDataSchemaVersion, pigversion.UpstreamVersion)
	}
}

// model-data.ts:279 builds the identity with String(model.type) and String(model.id). The expectations are Node 24's
// String(JSON.parse(raw)).
func TestModelDataJSStringMatchesNode(t *testing.T) {
	for raw, want := range map[string]string{
		`-0`: "0", `1e21`: "1e+21", `1e-7`: "1e-7", `1.5`: "1.5", `100`: "100", `1e400`: "Infinity",
		`true`: "true", `false`: "false", `null`: "null", `[1,[2,null],"a"]`: "1,2,,a", `{}`: "[object Object]", `"x"`: "x", ``: "undefined",
	} {
		if got := modelDataJSString([]byte(raw)); got != want {
			t.Errorf("String(%s) = %q, want %q", raw, got, want)
		}
	}
}
