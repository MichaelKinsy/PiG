package store

import (
	"math"
	"testing"
)

// A JsonText value is the caller's own serialization: the cell stores its text as it is, and the value is what the row records.
func TestEncodeCellStoresTheTextOfAJsonTextWithoutSerializingTheValueAgain(t *testing.T) {
	column := Column{Name: "record", Type: ColJson}
	value := map[string]any{"b": 1.0, "a": "x"}
	plain, err := EncodeCell(column, value, "t: column")
	if err != nil {
		t.Fatal(err)
	}
	same, err := EncodeCell(column, JsonText{Text: plain.Param.(string), Value: value}, "t: column")
	if err != nil {
		t.Fatal(err)
	}
	if same.Param != plain.Param {
		t.Fatalf("param %v, want the plain cell's %v", same.Param, plain.Param)
	}
	// Not serialized again: the text given is the text stored.
	given := `{"a": "x", "b": 1}`
	kept, err := EncodeCell(column, JsonText{Text: given, Value: value}, "t: column")
	if err != nil {
		t.Fatal(err)
	}
	if kept.Param != given {
		t.Fatalf("param %v, want the text given %q", kept.Param, given)
	}
}

func TestEncodeCellChecksTheValueOfAJsonText(t *testing.T) {
	column := Column{Name: "record", Type: ColJson}
	if _, err := EncodeCell(column, JsonText{Text: "null", Value: math.NaN()}, "t: column"); err == nil {
		t.Fatal("a value that is not strict JSON was accepted")
	}
}
