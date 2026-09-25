package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeThemeFile writes a minimal theme JSON and returns its path.
func writeThemeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "theme.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const fullscreenThemeBaseColors = `"selectedBg":"#3a3a4a","muted":"#808080","text":"#d4d4d4"`

func loadThemeBody(t *testing.T, body string) *Theme {
	t.Helper()
	var fields struct {
		Name   string          `json:"name"`
		Colors json.RawMessage `json:"colors"`
	}
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatal(err)
	}
	colors := strings.TrimSuffix(strings.TrimPrefix(string(fields.Colors), "{"), "}")
	theme, err := LoadThemeFile(writeThemeFile(t, completeThemeJSON(t, fields.Name, colors)))
	if err != nil {
		t.Fatal(err)
	}
	return theme
}

// TestFullscreenThemeTokensFallBackWhenOmitted mirrors
// withThemeColorFallbacks for the foreground tokens: scrollbarTrack (muted),
// scrollbarThumb (text), and thinkingMax (thinkingXhigh).
func TestFullscreenThemeTokensFallBackWhenOmitted(t *testing.T) {
	theme := loadThemeBody(t, `{"name":"missing-scrollbar-theme","colors":{`+fullscreenThemeBaseColors+`}}`)
	for token, fallback := range map[string]string{
		"scrollbarTrack": "muted",
		"scrollbarThumb": "text",
		"thinkingMax":    "thinkingXhigh",
	} {
		if got, want := theme.Fg(token), theme.Fg(fallback); got != want || got == "" {
			t.Fatalf("%s fg = %q, want %s fallback %q", token, got, fallback, want)
		}
	}
}

// TestFullscreenThemeUsesExplicitScrollbarColors mirrors upstream "uses
// explicitly configured scrollbar colors".
func TestFullscreenThemeUsesExplicitScrollbarColors(t *testing.T) {
	theme := loadThemeBody(t, `{"name":"custom-scrollbar-theme","colors":{`+fullscreenThemeBaseColors+`,"scrollbarTrack":"#654321","scrollbarThumb":"#123456"}}`)
	if got := theme.Fg("scrollbarTrack"); got != "\x1b[38;2;101;67;33m" {
		t.Fatalf("scrollbarTrack fg = %q", got)
	}
	if got := theme.Fg("scrollbarThumb"); got != "\x1b[38;2;18;52;86m" {
		t.Fatalf("scrollbarThumb fg = %q", got)
	}
}

// TestFullscreenThemeSearchHighlightsFallBack mirrors upstream "falls back to
// existing selection and text colors for search highlights".
func TestFullscreenThemeSearchHighlightsFallBack(t *testing.T) {
	theme := loadThemeBody(t, `{"name":"legacy-search-theme","colors":{`+fullscreenThemeBaseColors+`}}`)
	if got, want := theme.Bg("searchMatchBg"), theme.Bg("selectedBg"); got != want || got == "" {
		t.Fatalf("searchMatchBg bg = %q, want selectedBg %q", got, want)
	}
	if got, want := theme.Fg("searchMatchText"), theme.Fg("text"); got != want || got == "" {
		t.Fatalf("searchMatchText fg = %q, want text %q", got, want)
	}
}

// TestFullscreenThemeUsesExplicitSearchHighlightColors mirrors upstream "uses
// explicitly configured search highlight colors".
func TestFullscreenThemeUsesExplicitSearchHighlightColors(t *testing.T) {
	theme := loadThemeBody(t, `{"name":"custom-search-theme","colors":{`+fullscreenThemeBaseColors+`,"searchMatchBg":"#112233","searchMatchText":"#223344"}}`)
	if got := theme.Bg("searchMatchBg"); got != "\x1b[48;2;17;34;51m" {
		t.Fatalf("searchMatchBg bg = %q", got)
	}
	if got := theme.Fg("searchMatchText"); got != "\x1b[38;2;34;51;68m" {
		t.Fatalf("searchMatchText fg = %q", got)
	}
}

// TestFullscreenThemeFallbacksReachANSIPaletteAndColorKeys guards that each
// fallback reaches the surfaces that iterate colorKeys, not only Fg()/Bg(): the
// extension ANSIPalette and (via ColorKeys) the HTML export.
func TestFullscreenThemeFallbacksReachANSIPaletteAndColorKeys(t *testing.T) {
	theme := loadThemeBody(t, `{"name":"legacy-scrollbar-theme","colors":{`+fullscreenThemeBaseColors+`}}`)
	fg, bg := theme.ANSIPalette()
	for _, fallback := range themeColorFallbacks {
		if !slices.Contains(theme.ColorKeys(), fallback.token) {
			t.Fatalf("ColorKeys omits %s: %v", fallback.token, theme.ColorKeys())
		}
		if got, want := theme.Colors()[fallback.token], theme.Colors()[fallback.from]; got != want {
			t.Fatalf("Colors[%s] = %q, want %s fallback %q", fallback.token, got, fallback.from, want)
		}
		if fg[fallback.token] != fg[fallback.from] || bg[fallback.token] != bg[fallback.from] {
			t.Fatalf("ANSIPalette %s differs from its %s fallback", fallback.token, fallback.from)
		}
	}
}

// TestScrollbarThumbExplicitKeepsColorKeyPosition guards that an explicitly
// configured scrollbarThumb retains its original JSON key position (the fallback
// only appends when the token is absent), mirroring upstream's spread semantics.
func TestScrollbarThumbExplicitKeepsColorKeyPosition(t *testing.T) {
	theme := loadThemeBody(t, `{"name":"explicit-scrollbar-theme","colors":{"selectedBg":"#3a3a4a","scrollbarThumb":"#123456","text":"#ffffff"}}`)
	keys := theme.ColorKeys()
	if got := slices.Index(keys, "scrollbarThumb"); got != 1 {
		t.Fatalf("explicit scrollbarThumb at ColorKeys index %d, want 1 (original position): %v", got, keys)
	}
}

// TestScrollbarThumbExplicitEmptyIsRetained guards the nullish-fallback
// semantics (`colors.scrollbarThumb ?? colors.text`): an explicitly configured
// empty scrollbarThumb is retained, only omission falls back.
func TestScrollbarThumbExplicitEmptyIsRetained(t *testing.T) {
	theme := loadThemeBody(t, `{"name":"empty-scrollbar-theme","colors":{`+fullscreenThemeBaseColors+`,"scrollbarThumb":""}}`)
	if got := theme.Fg("scrollbarThumb"); got != SGRFgReset {
		t.Fatalf("explicit empty scrollbarThumb fg = %q, want terminal-default reset %q (not the text fallback)", got, SGRFgReset)
	}
}
