package tui

import (
	"reflect"
	"testing"
)

// upstream: packages/coding-agent/src/modes/interactive/components/dynamic-border.ts render.
func TestDynamicBorderFuncColorsWholeRule(t *testing.T) {
	border := NewDynamicBorderFunc(func(text string) string { return "<" + text + ">" })
	if got := border.Render(3); !reflect.DeepEqual(got, []string{"<───>"}) {
		t.Fatalf("Render(3) = %q", got)
	}
	if got := border.Render(0); !reflect.DeepEqual(got, []string{"<─>"}) {
		t.Fatalf("Render(0) = %q", got)
	}
}

// upstream: packages/tui/src/components/loader.ts updateDisplay and getRenderedIndicator.
func TestLoaderColorFns(t *testing.T) {
	loader := NewStyledLoader("\x1b[31m", "\x1b[32m", "Working", []string{"a", "b"})
	loader.SpinnerColorFn = func(text string) string { return "<s:" + text + ">" }
	loader.MessageColorFn = func(text string) string { return "<m:" + text + ">" }
	want := append([]string{""}, NewPaddedText("<s:a> <m:Working>", loaderPaddingX, 0, nil).Render(40)...)
	if got := loader.Render(40); !reflect.DeepEqual(got, want) {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	loader.SetIndicator([]string{"*"}, true)
	want = append([]string{""}, NewPaddedText("* <m:Working>", loaderPaddingX, 0, nil).Render(40)...)
	if got := loader.Render(40); !reflect.DeepEqual(got, want) {
		t.Fatalf("verbatim Render = %q, want %q", got, want)
	}
	loader.SetIndicator([]string{}, false)
	want = append([]string{""}, NewPaddedText("<m:Working>", loaderPaddingX, 0, nil).Render(40)...)
	if got := loader.Render(40); !reflect.DeepEqual(got, want) {
		t.Fatalf("hidden indicator Render = %q, want %q", got, want)
	}
}
