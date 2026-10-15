package tui

import (
	"reflect"
	"testing"
)

// upstream: packages/coding-agent/src/modes/interactive/components/dynamic-border.ts render.
func TestDynamicBorderFuncColorsWholeRule(t *testing.T) {
	border := NewDynamicBorder(func(text string) string { return "<" + text + ">" })
	if got := border.Render(3); !reflect.DeepEqual(got, []string{"<───>"}) {
		t.Fatalf("Render(3) = %q", got)
	}
	if got := border.Render(0); !reflect.DeepEqual(got, []string{"<─>"}) {
		t.Fatalf("Render(0) = %q", got)
	}
}

// upstream: packages/tui/src/components/loader.ts updateDisplay and getRenderedIndicator.
func TestLoaderColorFns(t *testing.T) {
	loader := NewLoader(nil, func(text string) string { return "<s:" + text + ">" }, func(text string) string { return "<m:" + text + ">" }, "Working", nil)
	want := append([]string{""}, NewPaddedText("<s:"+DefaultSpinnerFrames[0]+"> <m:Working>", loaderPaddingX, 0, nil).Render(40)...)
	if got := loader.Render(40); !reflect.DeepEqual(got, want) {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	loader.SetIndicator(&LoaderIndicatorOptions{Frames: []string{"*"}})
	want = append([]string{""}, NewPaddedText("* <m:Working>", loaderPaddingX, 0, nil).Render(40)...)
	if got := loader.Render(40); !reflect.DeepEqual(got, want) {
		t.Fatalf("verbatim Render = %q, want %q", got, want)
	}
	loader.SetIndicator(&LoaderIndicatorOptions{Frames: []string{}})
	want = append([]string{""}, NewPaddedText("<m:Working>", loaderPaddingX, 0, nil).Render(40)...)
	if got := loader.Render(40); !reflect.DeepEqual(got, want) {
		t.Fatalf("hidden indicator Render = %q, want %q", got, want)
	}
}
