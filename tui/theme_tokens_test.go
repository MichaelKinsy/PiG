package tui

import (
	"slices"
	"strings"
	"testing"
)

// The ThemeColor and ThemeBg constants are the theme schema's tokens (upstream ThemeJsonSchema.colors, theme.ts:21-56): every schema token is exactly one of the two,
// split by the Bg suffix upstream uses for backgrounds.
func TestThemeTokenConstantsMatchTheThemeSchema(t *testing.T) {
	var wantColors, wantBgs []string
	for _, token := range themeColorTokens {
		if strings.HasSuffix(token.name, "Bg") {
			wantBgs = append(wantBgs, token.name)
		} else {
			wantColors = append(wantColors, token.name)
		}
	}
	gotColors := allThemeColors()
	gotBgs := allThemeBgs()
	slices.Sort(wantColors)
	slices.Sort(wantBgs)
	slices.Sort(gotColors)
	slices.Sort(gotBgs)
	if !slices.Equal(gotColors, wantColors) {
		t.Errorf("ThemeColor constants differ from the schema:\n got %v\nwant %v", gotColors, wantColors)
	}
	if !slices.Equal(gotBgs, wantBgs) {
		t.Errorf("ThemeBg constants differ from the schema:\n got %v\nwant %v", gotBgs, wantBgs)
	}
}

func allThemeColors() []string {
	var out []string
	for _, c := range []ThemeColor{
		ThemeColorAccent,
		ThemeColorBorder,
		ThemeColorBorderAccent,
		ThemeColorBorderMuted,
		ThemeColorSuccess,
		ThemeColorError,
		ThemeColorWarning,
		ThemeColorMuted,
		ThemeColorDim,
		ThemeColorText,
		ThemeColorThinkingText,
		ThemeColorScrollbarTrack,
		ThemeColorScrollbarThumb,
		ThemeColorSearchMatchText,
		ThemeColorUserMessageText,
		ThemeColorCustomMessageText,
		ThemeColorCustomMessageLabel,
		ThemeColorToolTitle,
		ThemeColorToolOutput,
		ThemeColorMdHeading,
		ThemeColorMdLink,
		ThemeColorMdLinkUrl,
		ThemeColorMdCode,
		ThemeColorMdCodeBlock,
		ThemeColorMdCodeBlockBorder,
		ThemeColorMdQuote,
		ThemeColorMdQuoteBorder,
		ThemeColorMdHr,
		ThemeColorMdListBullet,
		ThemeColorToolDiffAdded,
		ThemeColorToolDiffRemoved,
		ThemeColorToolDiffContext,
		ThemeColorSyntaxComment,
		ThemeColorSyntaxKeyword,
		ThemeColorSyntaxFunction,
		ThemeColorSyntaxVariable,
		ThemeColorSyntaxString,
		ThemeColorSyntaxNumber,
		ThemeColorSyntaxType,
		ThemeColorSyntaxOperator,
		ThemeColorSyntaxPunctuation,
		ThemeColorThinkingOff,
		ThemeColorThinkingMinimal,
		ThemeColorThinkingLow,
		ThemeColorThinkingMedium,
		ThemeColorThinkingHigh,
		ThemeColorThinkingXhigh,
		ThemeColorThinkingMax,
		ThemeColorBashMode,
	} {
		out = append(out, string(c))
	}
	return out
}

func allThemeBgs() []string {
	var out []string
	for _, c := range []ThemeBg{
		ThemeBgSelectedBg,
		ThemeBgSearchMatchBg,
		ThemeBgUserMessageBg,
		ThemeBgCustomMessageBg,
		ThemeBgToolPendingBg,
		ThemeBgToolSuccessBg,
		ThemeBgToolErrorBg,
	} {
		out = append(out, string(c))
	}
	return out
}

// theme.ts:117 ThemeToken = ThemeColor | ThemeBg, and Theme.colors is a Record<ThemeToken, Color> (theme.ts:322): a theme resolves a concrete color for every
// token of both slots and for nothing else.
func TestThemeColorsAreKeyedByEveryThemeToken(t *testing.T) {
	var tokens []ThemeToken
	for _, name := range allThemeColors() {
		tokens = append(tokens, ThemeToken(ThemeColor(name)))
	}
	for _, name := range allThemeBgs() {
		tokens = append(tokens, ThemeToken(ThemeBg(name)))
	}
	colors := builtinDarkTheme().Colors()
	for _, token := range tokens {
		if _, ok := colors[string(token)]; !ok {
			t.Errorf("Theme.colors has no color for token %q", token)
		}
	}
	if len(colors) != len(tokens) {
		t.Errorf("Theme.colors has %d entries, want one per ThemeToken (%d)", len(colors), len(tokens))
	}
}
