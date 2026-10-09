//go:build !pig_strip_word_dictionaries

package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// Pi 0.87.1 Intl.Segmenter splits 学生/です with ICU's CJK dictionary; cursor columns are UTF-16 units. A Piglet that
// strips word-dictionaries moves over the whole run, as a Binary built without the dictionaries does.
func TestEditor_WordBoundaryDictionary(t *testing.T) {
	e := NewEditor()
	if got := e.prevWordStart("学生です", 4); got != 2 {
		t.Fatalf("prevWordStart(学生です, 4) = %d, want 2", got)
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.WordDictionaries))
	if got := e.prevWordStart("学生です", 4); got != 0 {
		t.Fatalf("stripped prevWordStart(学生です, 4) = %d, want 0", got)
	}
	if got := e.nextWordEnd("学生です", 0); got != 4 {
		t.Fatalf("stripped nextWordEnd(学生です, 0) = %d, want 4", got)
	}
}
