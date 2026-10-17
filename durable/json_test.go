package durable

import (
	"encoding/json"
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
	source := delta.JsonObjectOf("list", []any{1.0})
	detached, err := ToJsonValue(source)
	if err != nil {
		t.Fatal(err)
	}
	detached.(*delta.JsonObject).Value("list").([]any)[0] = 2.0
	if source.Value("list").([]any)[0] != 1.0 {
		t.Fatalf("copy shares the source")
	}

	token := DefineDoc(DocDefinition[gauge]{
		CommonDocDefinition: CommonDocDefinition[gauge]{Kind: "test.gauge", Version: 1, Initial: func() gauge { return gauge{Level: math.NaN()} }},
		DocumentSemantics:   DocumentSemantics{Scope: ScopeSession},
	})
	if _, err := token.AnyDefinition().Initial(nil); !errors.Is(err, delta.ErrNotStrictJSON) {
		t.Fatalf("initial err = %v, want a strict JSON error", err)
	}
}

// A JSON-object document's checkpoint predicate receives the exact prepared revision, and family seeds and JSON-kind
// decodes are strict copyJson.
// Pi source: packages/durable/src/documents.ts
// mutation-checked: zeroing the results of DocFamilyToken.AnyDefinition fails it
func TestDocumentConversionsKeepRevisionsAndStayStrict(t *testing.T) {
	var seen JsonObject
	token := DefineDoc(DocDefinition[JsonObject]{
		CommonDocDefinition: CommonDocDefinition[JsonObject]{
			Kind: "test.exact", Version: 1,
			CheckpointWhen: func(value JsonObject, _ []Op, _ CheckpointInfo) bool { seen = value; return false },

			Initial: func() JsonObject { return delta.NewJsonObject(0) },
		},
		DocumentSemantics: DocumentSemantics{Scope: ScopeSession},
	})
	revision := delta.JsonObjectOf("count", 1.0)
	if _, err := token.AnyDefinition().CheckpointWhen(revision, nil, CheckpointInfo{}); err != nil {
		t.Fatal(err)
	}
	seen.Set("probe", true)
	if revision.Value("probe") != true {
		t.Fatalf("checkpointWhen received a copy, want the exact revision")
	}

	family := DefineDocFamily(DocFamilyDefinition[JsonObject, JsonValue]{
		Family: true,
		Kind:   "test.family", Version: 1,
		DocumentSemantics: DocumentSemantics{Scope: ScopeSession},
		Initial:           func(seed JsonValue) JsonObject { return delta.JsonObjectOf("seed", seed) },
	})
	if _, err := family.AnyDefinition().Initial(math.NaN()); !errors.Is(err, delta.ErrNotStrictJSON) {
		t.Fatalf("NaN seed err = %v, want a strict JSON error", err)
	}
	if _, err := FromJsonValue[JsonObject](delta.JsonObjectOf("bad", math.Inf(-1))); !errors.Is(err, delta.ErrNotStrictJSON) {
		t.Fatalf("FromJsonValue err = %v, want a strict JSON error", err)
	}
	if _, err := FromJsonValue[JsonValue](delta.JsonObjectOf("bad", func() {})); !errors.Is(err, delta.ErrNotStrictJSON) {
		t.Fatalf("non-JSON kind err = %v, want a strict JSON error", err)
	}
}

// A T that is a Go map receives the value: chord.CopyJSON copies an object to a *delta.JsonObject, which is not T, so the
// value decodes through its JSON encoding instead of returning a nil map. The map's own members are JsonValues, so a nested
// object keeps its key order as JSON.parse gives Pi. An entry kind whose data is a Go map reads the data storage returns
// (packages/durable/src/entries.ts readEntry returns entry.data as the declared type).
func TestFromJsonValueDecodesIntoAGoMap(t *testing.T) {
	source := map[string]any{"b": 1.0, "a": delta.JsonObjectOf("z", 2.0, "c", []any{true})}
	got, err := FromJsonValue[map[string]any](source)
	if err != nil || jsonOf(t, got) != `{"a":{"z":2,"c":[true]},"b":1}` {
		t.Fatalf("FromJsonValue[map[string]any] = %#v, %v", got, err)
	}
	got["a"].(*delta.JsonObject).Value("c").([]any)[0] = false
	if source["a"].(*delta.JsonObject).Value("c").([]any)[0] != true {
		t.Fatalf("the decoded map shares the source")
	}
	if _, err := FromJsonValue[map[string]any](map[string]any{"bad": math.NaN()}); !errors.Is(err, delta.ErrNotStrictJSON) {
		t.Fatalf("NaN err = %v, want a strict JSON error", err)
	}

	token := DefineEntry[map[string]any]("test.map")
	typed, err := token.As(&EntryRecord{Id: 1, Kind: "test.map", Data: map[string]any{"text": "hello"}})
	if err != nil || typed == nil || typed.TypedData["text"] != "hello" {
		t.Fatalf("Entry[map[string]any].As = %#v, %v", typed, err)
	}
}

// FromJsonValue decodes a typed value whose JsonValue members hold objects, which keep their key order as JSON.parse gives Pi.
func TestFromJsonValueKeepsDynamicMemberKeyOrder(t *testing.T) {
	type state struct {
		Phase      string    `json:"phase"`
		Checkpoint JsonValue `json:"checkpoint"`
	}
	value := delta.JsonObjectOf("phase", "apply", "checkpoint", delta.JsonObjectOf("zeta", 1.0, "alpha", 2.0))
	decoded, err := FromJsonValue[state](value)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(decoded.Checkpoint)
	if err != nil || string(encoded) != `{"zeta":1,"alpha":2}` {
		t.Fatalf("checkpoint = %s, %v, want {\"zeta\":1,\"alpha\":2}", encoded, err)
	}
}
