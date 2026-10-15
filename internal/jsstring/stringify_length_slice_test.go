package jsstring

import (
	"math"
	"testing"
)

type pointerMarshaled struct{ Name string }

func (p *pointerMarshaled) MarshalJSON() ([]byte, error) {
	return []byte(`{"pointer":"` + p.Name + `"}`), nil
}

type valueMarshaled struct{ Name string }

func (v valueMarshaled) MarshalJSON() ([]byte, error) {
	return []byte(`["value","` + v.Name + `"]`), nil
}

type textMarshaled int

func (t textMarshaled) MarshalText() ([]byte, error) { return []byte("t<é>"), nil }

type selfMarshaledList []string

func (selfMarshaledList) MarshalJSON() ([]byte, error) { return []byte(`"list"`), nil }

type pointerMarshaledList []int

func (*pointerMarshaledList) MarshalJSON() ([]byte, error) { return []byte(`"pointer list"`), nil }

type plainStruct struct {
	Name  string         `json:"name"`
	Value float64        `json:"value"`
	Extra map[string]any `json:"extra,omitempty"`
}

// A slice is counted element by element; the count is the length of the text the whole slice encodes to, whatever the elements' methods,
// and a failing element fails the whole count.
func TestStringifyLengthOfSlicesMatchesTheWholeText(t *testing.T) {
	text := "a\u2028<é>😀\xff"
	cases := map[string]any{
		"structs":                []plainStruct{{Name: text, Value: math.Copysign(0, -1)}, {Name: "b", Value: 1e21, Extra: map[string]any{"k": []any{nil, 1.5}}}},
		"pointer methods":        []pointerMarshaled{{"a"}, {"b"}},
		"value methods":          []valueMarshaled{{"a"}, {"b"}},
		"pointer elements":       []*pointerMarshaled{{"a"}, nil, {"c"}},
		"text marshalers":        []textMarshaled{1, 2},
		"map keys of text":       []map[textMarshaled]int{{1: 1}, {2: 2}},
		"self-marshaled list":    selfMarshaledList{"a", "b"},
		"pointer-marshaled list": pointerMarshaledList{1, 2},
		"bytes":                  []byte("abc"),
		"slices of bytes":        [][]byte{[]byte("ab"), nil},
		"interfaces":             []any{text, nil, 1.5, []string{"x", "y"}, map[string]any{"z": math.Copysign(0, -1)}},
		"empty":                  []string{},
		"nil":                    []string(nil),
		"one":                    []string{text},
		"array":                  [3]string{"a", "b", "c"},
	}
	for name, value := range cases {
		want, wantErr := referenceStringifyLength(value)
		got, gotErr := StringifyLength(value)
		if wantErr != nil || gotErr != nil || got != want {
			t.Errorf("%s: got %d, %v; want %d, %v", name, got, gotErr, want, wantErr)
		}
	}
	for name, value := range map[string]any{
		"unencodable element": []any{"a", make(chan int)},
		"unsupported map key": []any{1, map[bool]int{true: 1}},
	} {
		_, wantErr := referenceStringifyLength(value)
		if _, gotErr := StringifyLength(value); wantErr == nil || gotErr == nil {
			t.Errorf("%s: got %v; want an error like %v", name, gotErr, wantErr)
		}
	}
}
