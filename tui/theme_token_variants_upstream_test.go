package tui

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// Pi theme-tokens.ts:103 ThemeToken = keyof typeof THEME_TOKENS: the variants are the descriptor keys, in source order. The foreground/background
// split (the ThemeBg half of the union) is covered by TestThemeTokenConstantsMatchTheThemeSchema.
func TestThemeTokenVariantsMatchPiThemeTokens(t *testing.T) {
	raw, err := os.ReadFile("../.upstream/current/packages/coding-agent/src/modes/interactive/theme/theme-tokens.ts")
	if err != nil {
		t.Fatal(err)
	}
	table := regexp.MustCompile(`(?s)export const THEME_TOKENS = defineThemeTokens\(\{\n(.*?)\n\}\)`).FindSubmatch(raw)
	if table == nil {
		t.Fatal("THEME_TOKENS table not found")
	}
	var want []ThemeToken
	for _, m := range regexp.MustCompile(`(?m)^\t(\w+): \{`).FindAllSubmatch(table[1], -1) {
		want = append(want, ThemeToken(m[1]))
	}
	var got []ThemeToken
	for _, token := range ThemeColorTokens() {
		got = append(got, ThemeToken(token.Name))
	}
	if len(want) == 0 || !slices.Equal(got, want) {
		t.Fatalf("ThemeToken variants\n got  %v\n Pi   %v", got, want)
	}
}
