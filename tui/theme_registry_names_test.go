package tui

import (
	"slices"
	"testing"
)

// theme.ts getAvailableThemesWithPaths: the system theme first, then localeCompare order, whatever the registration order.
func TestThemeRegistryNamesSortsLikeUpstream(t *testing.T) {
	registry := NewThemeRegistry()
	for _, name := range []string{"zeta", "Aurora", "beta", "aurora"} {
		theme, err := LoadBuiltinTheme("dark")
		if err != nil {
			t.Fatal(err)
		}
		theme.Name = name
		registry.Add(theme)
	}
	// Node: ["system","dark","light","zeta","Aurora","beta","aurora"].sort((a, b) => a === "system" ? -1 : b === "system" ? 1 : a.localeCompare(b))
	want := []string{"system", "aurora", "Aurora", "beta", "dark", "light", "zeta"}
	if got := registry.Names(); !slices.Equal(got, want) {
		t.Fatalf("Names = %q, want %q", got, want)
	}
}
