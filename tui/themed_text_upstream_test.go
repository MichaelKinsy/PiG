package tui

// pi: packages/coding-agent/src/modes/interactive/components/themed-text.ts

import (
	"strings"
	"testing"
)

// Ports packages/coding-agent/test/themed-text.test.ts (upstream 0.99.1).

// themed-text.test.ts:9
func TestThemedTextUpstream(t *testing.T) {
	t.Run("builds lazily and rebuilds with the current theme after invalidation", func(t *testing.T) {
		previous := ActiveTheme()
		t.Cleanup(func() { activeTheme.Store(previous); SetTheme("dark") })
		SetTheme("dark")
		builds := 0
		text := NewThemedText(func() string {
			builds++
			return ActiveTheme().Fg("accent", "hello")
		}, 1, 1)
		if builds != 0 {
			t.Fatalf("builds = %d before the first render, want 0", builds)
		}
		dark := strings.Join(text.Render(20), "")

		SetTheme("light")
		if got := strings.Join(text.Render(20), ""); got != dark {
			t.Errorf("render after a theme change without invalidation = %q, want the cached %q", got, dark)
		}
		text.Invalidate()
		if got := strings.Join(text.Render(20), ""); !strings.Contains(got, ActiveTheme().GetFgAnsi("accent")) {
			t.Errorf("render after invalidation = %q, want the light accent %q", got, ActiveTheme().GetFgAnsi("accent"))
		}
		if builds != 2 {
			t.Errorf("builds = %d, want 2", builds)
		}
	})
}
