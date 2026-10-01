package sdk

import (
	"encoding/json"
	"testing"
)

// Pi 0.99.2 theme.ts:363 closes the foreground of a faint token with SGR 22;39, and theme.ts:399-402 opens it with
// SGR 2 after the color, which is how the host sends a faint token's foreground. Fg and the border colorizers
// that draw through it must end the faint run at the text (the Node runtime's ctx.ui.theme and the other SDKs do).
func TestUIThemeFgClosesFaintTokensLikePi(t *testing.T) {
	theme, ok := decodeUITheme(json.RawMessage(`{"name":"faint","foregrounds":{"accent":"\u001b[38;5;5m","muted":"\u001b[39m\u001b[2m","thinkingXhigh":"\u001b[38;5;13m\u001b[2m","bashMode":"\u001b[38;5;2m"},"backgrounds":{},"modifiers":true,"mode":"256color"}`))
	if !ok {
		t.Fatal("palette rejected")
	}
	for _, tc := range []struct{ name, got, want string }{
		{"fg faint token", theme.Fg("muted", "x"), "\x1b[39m\x1b[2mx\x1b[22;39m"},
		{"fg color token", theme.Fg("accent", "x"), "\x1b[38;5;5mx\x1b[39m"},
		{"thinking border (faint token)", theme.GetThinkingBorderColor("xhigh")("x"), "\x1b[38;5;13m\x1b[2mx\x1b[22;39m"},
		{"bash border", theme.GetBashModeBorderColor()("x"), "\x1b[38;5;2mx\x1b[39m"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}
