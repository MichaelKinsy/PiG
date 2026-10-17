package ordered

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

type inner struct {
	Value any `json:"value"`
}

type Embedded struct {
	Extra any `json:"extra,omitempty"`
}

type node struct {
	Data any `json:"data"`
	//portlint:allow emptydrop a Restore fixture: omitempty only shapes the fixture text, nothing is sent to Pi
	Children []node `json:"children,omitempty"`
}

type custom struct{ text string }

func (c *custom) UnmarshalJSON(data []byte) error { c.text = string(data); return nil }

type record struct {
	Embedded
	Data    any    `json:"data"`
	Pointer *inner `json:"pointer,omitempty"`
	//portlint:allow emptydrop a Restore fixture: omitempty only shapes the fixture text, nothing is sent to Pi
	Items []inner `json:"items,omitempty"`
	//portlint:allow emptydrop a Restore fixture: omitempty only shapes the fixture text, nothing is sent to Pi
	Memos map[string]any `json:"memos,omitempty"`
	//portlint:allow emptydrop a Restore fixture: omitempty only shapes the fixture text, nothing is sent to Pi
	ByNumber map[int]inner `json:"byNumber,omitempty"`
	Fixed    [1]any        `json:"fixed"`
	Hidden   any           `json:"-"`
	Folded   any           `json:"folded"`
	Custom   *custom       `json:"custom,omitempty"`
	Tree     *node         `json:"tree,omitempty"`
	//portlint:allow emptydrop a Restore fixture: omitempty only shapes the fixture text, nothing is sent to Pi
	Typed map[string]int64 `json:"typed,omitempty"`
}

func decodeRecord(t *testing.T, text string) record {
	t.Helper()
	var decoded record
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatal(err)
	}
	tree, err := delta.DecodeJson([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	Restore(&decoded, tree)
	return decoded
}

func encode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// Pi's JSON.parse keeps an object's key order, so every dynamic member of a decoded record reads back in the text's order.
func TestRestoreGivesDynamicMembersTheTextsKeyOrder(t *testing.T) {
	text := `{"data":{"z":1,"a":{"y":2,"b":3}},"pointer":{"value":{"q":1,"c":2}},"items":[{"value":{"n":1,"m":2}}],` +
		`"memos":{"k":{"t":1,"s":2}},"byNumber":{"7":{"value":{"v":1,"u":2}}},"fixed":[{"r":1,"d":2}],"FOLDED":{"x":1,"e":2},` +
		`"extra":{"w":1,"f":2},"tree":{"data":{"p":1,"g":2},"children":[{"data":{"o":1,"h":2}}]}}`
	decoded := decodeRecord(t, text)
	for name, got := range map[string]any{
		"data":     decoded.Data,
		"pointer":  decoded.Pointer.Value,
		"items":    decoded.Items[0].Value,
		"memos":    decoded.Memos["k"],
		"byNumber": decoded.ByNumber[7].Value,
		"fixed":    decoded.Fixed[0],
		"folded":   decoded.Folded,
		"embedded": decoded.Extra,
		"tree":     decoded.Tree.Data,
		"child":    decoded.Tree.Children[0].Data,
	} {
		if _, ok := got.(*delta.JsonObject); !ok {
			t.Errorf("%s = %T, want *delta.JsonObject", name, got)
		}
	}
	want := map[string]string{
		"data": `{"z":1,"a":{"y":2,"b":3}}`, "pointer": `{"q":1,"c":2}`, "items": `{"n":1,"m":2}`, "memos": `{"t":1,"s":2}`,
		"byNumber": `{"v":1,"u":2}`, "fixed": `{"r":1,"d":2}`, "folded": `{"x":1,"e":2}`, "embedded": `{"w":1,"f":2}`,
		"tree": `{"p":1,"g":2}`, "child": `{"o":1,"h":2}`,
	}
	got := map[string]string{
		"data": encode(t, decoded.Data), "pointer": encode(t, decoded.Pointer.Value), "items": encode(t, decoded.Items[0].Value),
		"memos": encode(t, decoded.Memos["k"]), "byNumber": encode(t, decoded.ByNumber[7].Value), "fixed": encode(t, decoded.Fixed[0]),
		"folded": encode(t, decoded.Folded), "embedded": encode(t, decoded.Extra), "tree": encode(t, decoded.Tree.Data),
		"child": encode(t, decoded.Tree.Children[0].Data),
	}
	for name, text := range want {
		if got[name] != text {
			t.Errorf("%s = %s, want %s", name, got[name], text)
		}
	}
}

// Members the text does not reach keep what encoding/json gave them: absent and null members stay nil, a custom decoder keeps its own
// representation, and a typed map keeps its typed values.
func TestRestoreLeavesOtherMembers(t *testing.T) {
	decoded := decodeRecord(t, `{"data":null,"fixed":[null],"custom":{"b":1,"a":2},"typed":{"b":1,"a":2},"Hidden":{"b":1}}`)
	if decoded.Data != nil || decoded.Fixed[0] != nil || decoded.Folded != nil || decoded.Hidden != nil || decoded.Pointer != nil {
		t.Fatalf("absent and null members are nil, got %+v", decoded)
	}
	if decoded.Custom.text != `{"b":1,"a":2}` {
		t.Fatalf("custom decoder text = %q", decoded.Custom.text)
	}
	if decoded.Typed["a"] != 2 || decoded.Typed["b"] != 1 {
		t.Fatalf("typed map = %v", decoded.Typed)
	}
}

func TestHasDynamicFollowsRecursiveTypes(t *testing.T) {
	type plain struct {
		Name  string
		Items []plain
	}
	if HasDynamic(reflectType[plain]()) {
		t.Fatal("a recursive type without a dynamic member has none")
	}
	if !HasDynamic(reflectType[[]node]()) || !HasDynamic(reflectType[record]()) {
		t.Fatal("a recursive type with a dynamic member has one")
	}
	if HasDynamic(reflectType[custom]()) || HasDynamic(reflectType[map[string]int]()) {
		t.Fatal("a custom decoder or a typed map has no dynamic member")
	}
	if !strings.Contains(encode(t, decodeRecord(t, `{"data":[{"b":1,"a":2}],"fixed":[1]}`).Data), `[{"b":1,"a":2}]`) {
		t.Fatal("an array member keeps its objects' order")
	}
}

func reflectType[T any]() reflect.Type { return reflect.TypeFor[T]() }

// selfDecoding fills its dynamic member in its own UnmarshalJSON, from a member of another name.
type selfDecoding struct {
	Value any `json:"value"`
}

func (s *selfDecoding) UnmarshalJSON(data []byte) error {
	var wire struct {
		Other any `json:"other"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	s.Value = wire.Other
	return nil
}

// A member that decodes itself keeps the value its decoder produced: Restore matches JSON names to fields as encoding/json
// does, which a custom decoder need not follow, so walking into it would overwrite its dynamic member with another member's text.
func TestRestoreLeavesSelfDecodingMembersAlone(t *testing.T) {
	const text = `{"self":{"value":{"b":1,"a":2},"other":"kept"}}`
	var target struct {
		Self selfDecoding `json:"self"`
	}
	if err := json.Unmarshal([]byte(text), &target); err != nil {
		t.Fatal(err)
	}
	tree, err := delta.DecodeJson([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	Restore(&target, tree)
	if target.Self.Value != "kept" {
		t.Fatalf("self-decoding member = %#v, want the value its decoder produced", target.Self.Value)
	}
}
