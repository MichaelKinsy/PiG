package tui

import (
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The extension SDK module (extensions/sdk) cannot import this package: the root module requires the SDK, and an extension author must
// not pull the coding agent in to measure a string. The SDK's pi-tui helpers (extensions/sdk/tui) therefore carry copies of the
// Pi-faithful engines that live here. This file is the one place that defines the copies: sdkTuiCopies lists them, sdkTuiTransform
// derives each from its source, and TestSDKTuiCopiesAreCurrent fails when a copy differs from what the source produces.
//
//	PIG_UPDATE_SDK_TUI=1 go test ./tui -run TestSDKTuiCopiesAreCurrent
//
// rewrites the copies. Nothing in extensions/sdk/tui/internal/engine, widthx or jsstring is edited by hand; the vectors in
// extensions/sdk-vectors (generated from Pi's own functions) judge every copy through the SDK's own tests.
type sdkTuiCopy struct{ src, dst string }

func sdkTuiCopies(t *testing.T) []sdkTuiCopy {
	t.Helper()
	var copies []sdkTuiCopy
	for _, name := range []string{"key_helper", "key_match", "key_parse", "keys_decode", "keybindings", "alt_screen_keybindings", "fuzzy", "colors", "oklab"} {
		copies = append(copies, sdkTuiCopy{"tui/" + name + ".go", "extensions/sdk/tui/internal/engine/" + name + ".go"})
	}
	entries, err := os.ReadDir("widthx")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		copies = append(copies, sdkTuiCopy{"tui/widthx/" + name, "extensions/sdk/tui/internal/widthx/" + name})
	}
	for _, name := range []string{"case", "codepoint", "slice", "utf16"} {
		copies = append(copies, sdkTuiCopy{"internal/jsstring/" + name + ".go", "extensions/sdk/tui/internal/jsstring/" + name + ".go"})
	}
	return copies
}

const sdkTuiModule = "github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/"

var (
	sdkTuiShouldDeliver = regexp.MustCompile(`(?s)// ShouldDeliverKey reports.*?\n}\n\n`)
	sdkTuiD185          = regexp.MustCompile(`(?s) else if codepoint >= 'a' && codepoint <= 'z' \{\n\t\t\t// pig divergence \(D185\).*?\n\t\t\}\n`)
	sdkTuiImportLine    = regexp.MustCompile(`(?m)^\t"golang.org/x/text/[a-z]+"\n`)
)

func sdkTuiTransform(src string, text string) string {
	text = strings.NewReplacer(
		"github.com/MichaelKinsy/PiG/internal/jsstring", sdkTuiModule+"jsstring",
		"github.com/MichaelKinsy/PiG/internal/lazyregexp", sdkTuiModule+"lazyregexp",
		"github.com/MichaelKinsy/PiG/internal/jsnumber", sdkTuiModule+"jsnumber",
		"github.com/MichaelKinsy/PiG/tui/widthx", sdkTuiModule+"widthx",
		"cases.Lower(language.Und).String(", "jsstring.ToLower(",
		"JSNumberString(", "jsnumber.String(",
	).Replace(text)
	text = sdkTuiImportLine.ReplaceAllString(text, "")
	if strings.HasPrefix(src, "tui/") && !strings.HasPrefix(src, "tui/widthx/") {
		text = strings.Replace(text, "package tui\n", "package engine\n", 1)
	}
	if src == "tui/keys_decode.go" {
		text = sdkTuiShouldDeliver.ReplaceAllString(text, "")
		// The SDK helpers are Pi's, and Node extensions get Pi's: D185 (a base-only Shift+letter decodes as the capital) is the
		// application editor's behavior, not the helper's.
		if !sdkTuiD185.MatchString(text) {
			panic("keys_decode.go: the D185 branch changed; update sdkTuiD185")
		}
		text = sdkTuiD185.ReplaceAllString(text, "\n")
	}
	if src == "tui/fuzzy.go" || src == "tui/key_match.go" {
		if !strings.Contains(text, sdkTuiModule+"jsstring") {
			text = strings.Replace(text, "import (\n", "import (\n\t\""+sdkTuiModule+"jsstring\"\n", 1)
		}
	}
	formatted, err := format.Source([]byte(text))
	if err != nil {
		panic(src + ": " + err.Error())
	}
	return "// Code generated from " + src + " by tui/sdk_tui_copy_test.go; DO NOT EDIT.\n\n" + string(formatted)
}

func TestSDKTuiCopiesAreCurrent(t *testing.T) {
	root := ".."
	update := os.Getenv("PIG_UPDATE_SDK_TUI") != ""
	for _, c := range sdkTuiCopies(t) {
		source, err := os.ReadFile(filepath.Join(root, c.src))
		if err != nil {
			t.Fatal(err)
		}
		want := sdkTuiTransform(c.src, string(source))
		dst := filepath.Join(root, c.dst)
		got, err := os.ReadFile(dst)
		if update {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatal(err)
			}
			if err != nil || string(got) != want {
				if err := os.WriteFile(dst, []byte(want), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			continue
		}
		if err != nil || string(got) != want {
			t.Errorf("%s is stale: run PIG_UPDATE_SDK_TUI=1 go test ./tui -run TestSDKTuiCopiesAreCurrent", c.dst)
		}
	}
}
