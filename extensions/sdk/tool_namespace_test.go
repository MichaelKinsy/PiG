package sdk

import (
	"bytes"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// toolNamespaceSliceShape is ToolNamespace as the aliases port first wrote it, with the aliases in a []string. Its encoding is the wire shape
// of upstream's ToolNamespace (types.ts:534-546) that ToolNamespaceAliases must keep.
type toolNamespaceSliceShape struct {
	Name         string   `json:"name"`
	Aliases      []string `json:"aliases,omitempty"`
	Description  string   `json:"description,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
}

// ToolNamespace is comparable, as it was in the v0.4.1 SDK (sdk-apidiff), and its aliases keep upstream's `aliases?: readonly string[]` wire
// shape: the same bytes as a []string member in every encoding mode, and the same values back when decoded.
func TestToolNamespaceAliasesKeepTheWireShapeAndComparability(t *testing.T) {
	cases := [][]string{nil, {}, {"docs"}, {"go", "go-helpers"}, {""}, {"", ""}, {"a<b>&c", "\u2028", "naïve", "bad\xffutf8", string(make([]byte, 200))}}
	for _, aliases := range cases {
		ns := ToolNamespace{Name: "mcp__docs", Aliases: NewToolNamespaceAliases(aliases...), Description: "Docs server", Instructions: "Search first."}
		shape := toolNamespaceSliceShape{Name: ns.Name, Aliases: aliases, Description: ns.Description, Instructions: ns.Instructions}
		for _, mode := range []struct {
			name    string
			marshal func(any) ([]byte, error)
		}{{"Marshal", json.Marshal}, {"MarshalJS", json.MarshalJS}, {"MarshalStringify", json.MarshalStringify}} {
			got, err := mode.marshal(ns)
			if err != nil {
				t.Fatal(err)
			}
			want, err := mode.marshal(shape)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s(%q) = %s, want %s", mode.name, aliases, got, want)
			}
		}
		if got := ns.Aliases.Names(); !slices.Equal(got, aliases) || (len(aliases) == 0) != (got == nil) {
			t.Errorf("Names() = %q, want %q (nil for none)", got, aliases)
		}
		wire, err := json.Marshal(shape)
		if err != nil {
			t.Fatal(err)
		}
		var decoded ToolNamespace
		var back toolNamespaceSliceShape
		if err := json.Unmarshal(wire, &decoded); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(wire, &back); err != nil {
			t.Fatal(err)
		}
		if want := (ToolNamespace{Name: back.Name, Aliases: NewToolNamespaceAliases(back.Aliases...), Description: back.Description, Instructions: back.Instructions}); decoded != want {
			t.Errorf("decoded %s = %+v, want %+v", wire, decoded, want)
		}
	}

	var decoded ToolNamespace
	if err := json.Unmarshal([]byte(`{"name":"n","aliases":null}`), &decoded); err != nil || decoded != (ToolNamespace{Name: "n"}) {
		t.Errorf("null aliases decoded to %+v, %v", decoded, err)
	}
	if err := json.Unmarshal([]byte(`{"name":"n","aliases":"docs"}`), &decoded); err == nil {
		t.Error("a string aliases member was accepted")
	}

	// == compares the aliases by value, in order, so a namespace is still a map key.
	byNamespace := map[ToolNamespace]int{{Name: "n", Aliases: NewToolNamespaceAliases("a", "b")}: 1}
	if byNamespace[ToolNamespace{Name: "n", Aliases: NewToolNamespaceAliases("a", "b")}] != 1 {
		t.Error("equal aliases built apart are not ==")
	}
	for _, other := range [][]string{{"b", "a"}, {"ab"}, {"a"}, {"a", "b", ""}} {
		if _, found := byNamespace[ToolNamespace{Name: "n", Aliases: NewToolNamespaceAliases(other...)}]; found {
			t.Errorf("aliases %q == [a b]", other)
		}
	}
}
