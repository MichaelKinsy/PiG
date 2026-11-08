package surfacehost

import (
	"encoding/json"
	"os"
	"testing"
)

// TestBrandThemesAreTheWebTokens: the native themes carry exactly the web's tokens (apps/p2/web/brand/tokens.json).
func TestBrandThemesAreTheWebTokens(t *testing.T) {
	raw, err := os.ReadFile("../../../p2/web/brand/tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	var tokens map[string]json.RawMessage
	if err := json.Unmarshal(raw, &tokens); err != nil {
		t.Fatal(err)
	}
	rename := map[string]string{"text": "fg", "bad": "error", "surface": RoleSurface, "soft": RoleSoft, "line": RoleLine}
	for _, theme := range BrandThemes() {
		var set map[string]string
		if err := json.Unmarshal(tokens[theme.Mode], &set); err != nil {
			t.Fatal(err)
		}
		for name, want := range set {
			role := name
			if r, ok := rename[name]; ok {
				role = r
			}
			if got := theme.Colors[role]; got != want {
				t.Errorf("%s %s: native %q, web %q", theme.Mode, name, got, want)
			}
		}
	}
}
