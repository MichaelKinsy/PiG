package coding_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// D645, the stored credential row. Pi's readStoredCredential returns `data[providerId]` of the plain object parsed from auth.json
// (packages/coding-agent/src/core/auth-storage.ts:504), so for a provider named like a member of Object.prototype it returns the inherited function or
// object where a credential or undefined is promised. PiG reads the file's own entries: such a provider has no stored credential. `openai` (stored) and
// `nosuchprovider` are the ordinary names and must be equal on both sides.
func TestAProviderNamedLikeAnObjectPrototypeMemberHasNoStoredCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"openai":{"type":"api_key","key":"k"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ provider, pi string }{
		{"constructor", "function Object"}, {"toString", "function toString"}, {"hasOwnProperty", "function hasOwnProperty"}, {"__proto__", "object {}"},
		{"openai", `object {"type":"api_key","key":"k"}`}, {"nosuchprovider", "undefined"},
	}
	providers := make([]string, 0, len(cases))
	for _, c := range cases {
		providers = append(providers, c.provider)
	}
	var pi []string
	pioracle.Run(t, `
const { readStoredCredential } = await load("pi-coding-agent/core/auth-storage.js");
emit(input.providers.map((provider) => {
	const value = readStoredCredential(provider, input.path);
	if (typeof value === "function") return "function " + value.name;
	return value === undefined ? "undefined" : typeof value + " " + JSON.stringify(value);
}));`, map[string]any{"providers": providers, "path": path}, &pi)
	if len(pi) != len(cases) {
		t.Fatalf("Pi answered %d of %d probes", len(pi), len(cases))
	}
	for i, c := range cases {
		credential := coding.ReadStoredCredential(c.provider, path)
		t.Logf("readStoredCredential(%q): Pi %s, PiG stored %v", c.provider, pi[i], credential != nil)
		if pi[i] != c.pi {
			t.Errorf("readStoredCredential(%q): Pi answers %s, the record says %s", c.provider, pi[i], c.pi)
		}
		if stored := credential != nil; stored != (c.provider == "openai") {
			t.Errorf("ReadStoredCredential(%q): stored = %v, want %v", c.provider, stored, c.provider == "openai")
		}
	}
}
