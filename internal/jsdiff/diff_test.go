package jsdiff

import (
	"reflect"
	"testing"
)

// The cases word.js documents in dedupeWhitespaceInChangeObjects (jsdiff 8.0.4, libesm/diff/word.js): the keep, delete and insert
// values after the whitespace tidy. tui.TestRenderDiffMatchesPi compares the full algorithm with the installed package.
func TestDiffWordsWhitespaceCasesFromJsdiff(t *testing.T) {
	for _, tc := range []struct {
		name     string
		old, new string
		want     [][2]string
	}{
		{"delete between keeps", "foo bar baz", "foo baz", [][2]string{{"", "foo "}, {"-", "bar "}, {"", "baz"}}},
		{"replace between keeps", "foo bar baz", "foo qux baz", [][2]string{{"", "foo "}, {"-", "bar"}, {"+", "qux"}, {"", " baz"}}},
		{"identical", "same text", "same text", [][2]string{{"", "same text"}}},
		{"empty to text", "", "a b", [][2]string{{"+", "a b"}}},
		{"text to empty", "a b", "", [][2]string{{"-", "a b"}}},
		{"both empty", "", "", nil},
	} {
		var got [][2]string
		for _, change := range DiffWords(tc.old, tc.new) {
			kind := ""
			switch {
			case change.Added:
				kind = "+"
			case change.Removed:
				kind = "-"
			}
			got = append(got, [2]string{kind, change.Value})
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: DiffWords(%q, %q) = %q, want %q", tc.name, tc.old, tc.new, got, tc.want)
		}
	}
}
