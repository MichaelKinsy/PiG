package abi

import (
	"errors"
	"testing"
)

// TestRecipeExpandsToItsText checks that a recipe stands for its text with each range segment replaced by the text its
// loader derives, in place, with the loader called once per segment and the arguments the core wrote.
func TestRecipeExpandsToItsText(t *testing.T) {
	r := NewRecipe(nil)
	r.SetText(append(r.Text(), `{"context":{"messages":[`...))
	r.SetText(append(r.Text(), `x`...)) // text segments merge
	r.Range(7, 0, 40)
	r.SetText(append(r.Text(), `,`...))
	r.Range(8, 12, 90)
	r.SetText(append(r.Text(), `]}}`...))
	recipe := r.Bytes()
	if n := int(recipe[0]) | int(recipe[1])<<8; n != 5 {
		t.Fatalf("%d segments, want 5 (adjacent text merged)", n)
	}
	var calls [][3]uint32
	rng := func(conv, head, tail uint32) ([]byte, error) {
		calls = append(calls, [3]uint32{conv, head, tail})
		return []byte(`{"c":` + string(rune('0'+conv)) + `}`), nil
	}
	got, err := Expand(recipe, rng)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"context":{"messages":[x{"c":7},{"c":8}]}}`; string(got) != want {
		t.Fatalf("expansion = %q, want %q", got, want)
	}
	if len(calls) != 2 || calls[0] != [3]uint32{7, 0, 40} || calls[1] != [3]uint32{8, 12, 90} {
		t.Fatalf("range loader calls = %v", calls)
	}
}

func TestRecipeErrors(t *testing.T) {
	r := NewRecipe(nil)
	r.SetText(append(r.Text(), `[`...))
	r.Range(1, 0, 5)
	good := append([]byte(nil), r.Bytes()...)
	rng := func(uint32, uint32, uint32) ([]byte, error) { return []byte("m"), nil }
	for name, recipe := range map[string][]byte{
		"empty":     nil,
		"truncated": good[:len(good)-1],
		"trailing":  append(append([]byte(nil), good...), 0),
		"kind":      {1, 0, 0, 0, 7},
		"text":      {1, 0, 0, 0, 0, 9, 0, 0, 0, 'a'},
	} {
		if _, err := Expand(recipe, rng); !errors.Is(err, ErrRecipe) {
			t.Errorf("%s: error %v, want ErrRecipe", name, err)
		}
	}
	if _, err := Expand(good, nil); !errors.Is(err, ErrRecipe) {
		t.Errorf("a range without a loader: error %v, want ErrRecipe", err)
	}
	failing := func(uint32, uint32, uint32) ([]byte, error) { return nil, ErrRecipeRecord }
	if _, err := Expand(good, failing); !errors.Is(err, ErrRecipeRecord) {
		t.Errorf("a failing range loader: error %v, want ErrRecipeRecord", err)
	}
	if got, err := Expand(good, rng); err != nil || string(got) != "[m" {
		t.Fatalf("got %q, %v", got, err)
	}
}
