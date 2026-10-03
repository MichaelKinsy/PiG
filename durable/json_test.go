package durable

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// ToJsonValue is copyJson: non-finite numbers fail as strict JSON errors whether the value is a JSON kind or a typed
// Go value, and typed values copy through their JSON encoding.
func TestToJsonValueIsStrictCopyJson(t *testing.T) {
	type gauge struct {
		Level float64 `json:"level"`
	}
	for name, value := range map[string]any{
		"object": map[string]any{"level": math.NaN()},
		"typed":  gauge{Level: math.Inf(1)},
	} {
		_, err := ToJsonValue(value)
		if !errors.Is(err, delta.ErrNotStrictJSON) || !strings.Contains(err.Error(), "strict JSON") {
			t.Fatalf("%s: err = %v, want a strict JSON error", name, err)
		}
	}
	copied, err := ToJsonValue(gauge{Level: 2})
	if err != nil || jsonOf(t, copied) != `{"level":2}` {
		t.Fatalf("typed copy = %#v, %v", copied, err)
	}
	source := map[string]any{"list": []any{1.0}}
	detached, err := ToJsonValue(source)
	if err != nil {
		t.Fatal(err)
	}
	detached.(map[string]any)["list"].([]any)[0] = 2.0
	if source["list"].([]any)[0] != 1.0 {
		t.Fatalf("copy shares the source")
	}

	token := DefineDoc(DocDefinition[gauge]{
		CommonDocDefinition: CommonDocDefinition[gauge]{Kind: "test.gauge", Version: 1},
		DocumentSemantics:   DocumentSemantics{Scope: ScopeSession},
		Initial:             func() gauge { return gauge{Level: math.NaN()} },
	})
	if _, err := token.AnyDefinition().Initial(nil); !errors.Is(err, delta.ErrNotStrictJSON) {
		t.Fatalf("initial err = %v, want a strict JSON error", err)
	}
}

// A JSON-object document's checkpoint predicate receives the exact prepared revision, and family seeds and JSON-kind
// decodes are strict copyJson.
func TestDocumentConversionsKeepRevisionsAndStayStrict(t *testing.T) {
	var seen JsonObject
	token := DefineDoc(DocDefinition[JsonObject]{
		CommonDocDefinition: CommonDocDefinition[JsonObject]{
			Kind: "test.exact", Version: 1,
			CheckpointWhen: func(value JsonObject, _ []Op, _ CheckpointInfo) bool { seen = value; return false },
		},
		DocumentSemantics: DocumentSemantics{Scope: ScopeSession},
		Initial:           func() JsonObject { return JsonObject{} },
	})
	revision := JsonObject{"count": 1.0}
	if _, err := token.AnyDefinition().CheckpointWhen(revision, nil, CheckpointInfo{}); err != nil {
		t.Fatal(err)
	}
	seen["probe"] = true
	if revision["probe"] != true {
		t.Fatalf("checkpointWhen received a copy, want the exact revision")
	}

	family := DefineDocFamily(DocFamilyDefinition[JsonObject, JsonValue]{
		CommonDocDefinition: CommonDocDefinition[JsonObject]{Kind: "test.family", Version: 1},
		DocumentSemantics:   DocumentSemantics{Scope: ScopeSession},
		Initial:             func(seed JsonValue) JsonObject { return JsonObject{"seed": seed} },
	})
	if _, err := family.AnyDefinition().Initial(math.NaN()); !errors.Is(err, delta.ErrNotStrictJSON) {
		t.Fatalf("NaN seed err = %v, want a strict JSON error", err)
	}
	if _, err := FromJsonValue[JsonObject](JsonObject{"bad": math.Inf(-1)}); !errors.Is(err, delta.ErrNotStrictJSON) {
		t.Fatalf("FromJsonValue err = %v, want a strict JSON error", err)
	}
	if _, err := FromJsonValue[JsonValue](JsonObject{"bad": func() {}}); !errors.Is(err, delta.ErrNotStrictJSON) {
		t.Fatalf("non-JSON kind err = %v, want a strict JSON error", err)
	}
}
