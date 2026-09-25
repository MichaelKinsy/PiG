package tui

import (
	"strings"
	"testing"
)

func TestShowImagesSelector_Render(t *testing.T) {
	sel := NewShowImagesSelector(true, nil, nil)
	joined := stripANSI(strings.Join(sel.Render(80), "\n"))
	if strings.Contains(joined, "filter:") {
		t.Fatalf("non-searchable selector rendered a filter input:\n%s", joined)
	}
	if !strings.Contains(joined, "Yes         Show images inline in terminal") {
		t.Fatalf("render missing yes option and description column:\n%s", joined)
	}
	if !strings.Contains(joined, "No          Show text placeholder instead") {
		t.Fatalf("render missing no option and muted description column:\n%s", joined)
	}
}

func TestShowImagesSelector_SelectedShowImages(t *testing.T) {
	sel := NewShowImagesSelector(false, nil, nil)
	if _, ok := sel.SelectedShowImages(); ok {
		t.Fatal("selection should be unavailable before confirmation")
	}
	sel.HandleInput("\r")
	show, ok := sel.SelectedShowImages()
	if !ok {
		t.Fatal("selection should be available after confirmation")
	}
	if show {
		t.Fatal("expected false when 'No' option is preselected")
	}
}

func TestShowImagesSelector_CancelledHasNoSelection(t *testing.T) {
	sel := NewShowImagesSelector(true, nil, nil)
	sel.HandleInput("\x1b")
	if show, ok := sel.SelectedShowImages(); ok || show {
		t.Fatalf("cancelled selector should have no selection, got (%v,%v)", show, ok)
	}
}
