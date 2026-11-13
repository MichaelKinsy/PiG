//go:build !pig_strip_syntax_highlight

package tui

import (
	"slices"
	"testing"
)

// heldWarmUnits is how many resume states hold the UTF-16 units of the text a warm-up left.
func heldWarmUnits() (n int) {
	hlMu.Lock()
	defer hlMu.Unlock()
	for _, r := range hlResumes {
		if r.text != nil {
			n++
		}
	}
	return n
}

// A warm-up keeps the units of its text for the next one. The draw of the whole text that ends a stream takes them and keeps none.
func TestWarmHighlightUnitsGoWithTheDrawOfTheWholeText(t *testing.T) {
	orig := ActiveTheme().Name
	defer SetTheme(orig)
	SetTheme("dark")
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlMu.Unlock()
	code := warmedSource(400)
	for end := 4096; end < len(code); end += 4096 {
		WarmHighlight(code[:end], "typescript")
	}
	if heldWarmUnits() == 0 {
		t.Fatal("the warm-ups kept no units")
	}
	HighlightCode(code, "typescript")
	if n := heldWarmUnits(); n != 0 {
		t.Errorf("%d resume states hold units after the whole text was drawn", n)
	}
}

// A warm-up that is still running when the call completes ends after the draw of the whole text, and leaves the units of its text in
// a state nothing continues; ReleaseWarmHighlight drops them without changing the lines a later draw returns.
func TestReleaseWarmHighlightDropsTheUnitsOfALateWarmUp(t *testing.T) {
	orig := ActiveTheme().Name
	defer SetTheme(orig)
	SetTheme("dark")
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlMu.Unlock()
	code := warmedSource(400)
	late := code[:len(code)*3/4]
	WarmHighlight(code[:len(code)/2], "typescript")
	HighlightCode(code, "typescript")
	WarmHighlight(late, "typescript")
	if heldWarmUnits() != 1 {
		t.Fatalf("a warm-up after the draw of the whole text holds %d states with units, want 1", heldWarmUnits())
	}
	ReleaseWarmHighlight(late, "typescript")
	if n := heldWarmUnits(); n != 0 {
		t.Errorf("%d resume states hold units after the release", n)
	}
	want := highlightCodeUncached(highlightRegistry(), late, "typescript", ActiveTheme())
	if got := HighlightCode(late, "typescript"); !slices.Equal(got, want) {
		t.Errorf("the lines after the release differ from a whole run (%d lines, want %d)", len(got), len(want))
	}
}

// A release of a text and language that no state holds, and of one a longer warm-up replaced, leaves the other states' units alone.
func TestReleaseWarmHighlightKeepsTheUnitsOfOtherTexts(t *testing.T) {
	orig := ActiveTheme().Name
	defer SetTheme(orig)
	SetTheme("dark")
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlMu.Unlock()
	code := warmedSource(200)
	WarmHighlight(code[:2000], "typescript")
	ReleaseWarmHighlight(code[:1000], "typescript")
	ReleaseWarmHighlight(code[:2000], "javascript")
	if heldWarmUnits() != 1 {
		t.Fatal("a release of another text or language dropped the units of the warm-up")
	}
	WarmHighlight(code[:4000], "typescript")
	ReleaseWarmHighlight(code[:2000], "typescript")
	if heldWarmUnits() != 1 {
		t.Fatal("a release of a text that a longer warm-up replaced dropped the units of the longer one")
	}
	ReleaseWarmHighlight(code[:4000], "typescript")
	if heldWarmUnits() != 0 {
		t.Fatal("the release of the warmed text kept its units")
	}
}

// Files that begin alike (the files a model writes in a row, or those of one directory, share their first lines) leave states with
// the same resume point, and a warm-up of a later file continues from the longest of them, another file's, so a state of its own
// earlier warm-up keeps its units, which no run takes. The release of the text of the file's last warm-up drops them all.
func TestReleaseWarmHighlightDropsTheUnitsOfEveryWarmUpOfFilesThatBeginAlike(t *testing.T) {
	orig := ActiveTheme().Name
	defer SetTheme(orig)
	SetTheme("dark")
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlMu.Unlock()
	for file := range 5 {
		code := warmedSource(300 + file)
		last := ""
		for end := 4096; end < len(code); end += 4096 {
			WarmHighlight(code[:end], "typescript")
			last = code[:end]
		}
		HighlightCode(code, "typescript")
		ReleaseWarmHighlight(last, "typescript")
		if n := heldWarmUnits(); n != 0 {
			t.Fatalf("file %d: %d resume states hold units after the release", file, n)
		}
	}
}
