package evals

import (
	"os"
	"path/filepath"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's readStoredCredential parses stripBom(readFileSync(auth.json)) and returns undefined for a missing, malformed or absent entry (auth-storage.ts:496-506); harness.ts:314-326 treats a falsy entry as no credential.
func TestStoredCredentialReadsProviderEntry(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	const entry = `{"type":"api_key","key":"k"}`
	for _, tc := range []struct {
		name, path, provider, want string
	}{
		{"plain", write("plain.json", `{"p":`+entry+`}`), "p", entry},
		{"byte order mark", write("bom.json", "\ufeff"+`{"p":`+entry+`}`), "p", entry},
		{"absent provider", write("other.json", `{"q":`+entry+`}`), "p", ""},
		{"malformed", write("bad.json", `{`), "p", ""},
		{"null entry", write("null.json", `{"p":null}`), "p", ""},
		{"false entry", write("false.json", `{"p":false}`), "p", ""},
		{"empty string entry", write("empty.json", `{"p":""}`), "p", ""},
		{"zero entry", write("zero.json", `{"p":-0.0e3}`), "p", ""},
		{"missing file", filepath.Join(dir, "none.json"), "p", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(icodingagent.ReadStoredCredentialEntry(tc.provider, tc.path)); got != tc.want {
				t.Fatalf("credential=%q want %q", got, tc.want)
			}
		})
	}
}
