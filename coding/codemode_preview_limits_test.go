//go:build !pig_strip_codemode

package coding

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

// execute.ts:46-66 and :231: a nested call's args preview keeps ARGS_PREVIEW_CHARS (200) UTF-16 units and its error
// ERROR_PREVIEW_CHARS (500), each cut to `slice(0, max - 3) + "..."` only when longer; script output stays whole up to
// DEFAULT_MAX_OUTPUT_TOKENS (10,000) tokens of CHARS_PER_TOKEN (4) characters. Pi's tests do not assert these limits.
func TestCodemodeNestedCallPreviewsAndTheDefaultOutputBudget(t *testing.T) {
	h := newCodemodeHarness(t, codemodeHarnessOptions{fixtures: []string{"nested-tools"}, activeTools: []string{"codemode"}})
	units := func(text string) int { return len(utf16.Encode([]rune(text))) }

	// `{"text":"` and `"}` take 11 units, so 189 characters make a 200-unit preview and 190 a 201-unit one.
	result := codemodeRun(t, h, `await tools.echo({ text: "a".repeat(189) }); await tools.echo({ text: "b".repeat(190) }); try { await tools.echo({ pad: "c".repeat(600) }); } catch {}`)
	calls := codemodeDetailsOf(t, result).Calls
	if len(calls) != 3 {
		t.Fatalf("calls = %+v, want 3", calls)
	}
	if want := `{"text":"` + strings.Repeat("a", 189) + `"}`; calls[0].Args != want {
		t.Errorf("200-unit args preview = %q, want it whole", calls[0].Args)
	}
	if want := `{"text":"` + strings.Repeat("b", 188) + "..."; calls[1].Args != want {
		t.Errorf("201-unit args preview = %q (%d units), want the first 197 units and ...", calls[1].Args, units(calls[1].Args))
	}
	if calls[2].Status != "error" || units(calls[2].Error) != 500 || !strings.HasSuffix(calls[2].Error, "...") {
		t.Errorf("error preview = %d units (%q...), status %s, want 500 units ending in ...", units(calls[2].Error), calls[2].Error[:min(len(calls[2].Error), 40)], calls[2].Status)
	}

	for _, tc := range []struct {
		chars     int
		truncated bool
	}{{40_000, false}, {40_001, true}} {
		result := codemodeRun(t, h, "text(\"x\".repeat("+strconv.Itoa(tc.chars)+"));")
		text := codemodeResultText(t, result)
		path := codemodeDetailsOf(t, result).FullOutputPath
		if path != "" {
			t.Cleanup(func() { _ = os.Remove(path) })
		}
		if tc.truncated != strings.HasPrefix(text, "Warning: truncated output (original token count: 10001)") || tc.truncated != (path != "") {
			t.Errorf("%d characters: text = %.70q, full output path %q, want truncated %t", tc.chars, text, path, tc.truncated)
		}
	}
}
