package coding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// auth-storage.ts:496-506 readStoredCredential: parse stripBom(readFileSync(authPath)), return data[providerId], and undefined for any failure.
func TestReadStoredCredentialReadsOneProviderWithoutAStore(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	const file = `{"openai":{"type":"api_key","key":"sk-one","env":{"A":"b"},"note":"kept"},"anthropic":{"type":"oauth","access":"a","refresh":"r","expires":123}}`
	t.Run("api key with env and provider metadata", func(t *testing.T) {
		got := ReadStoredCredential("openai", write("auth.json", file))
		if got == nil || got.Type != ai.CredentialAPIKey || got.Key != "sk-one" || got.Env["A"] != "b" {
			t.Fatalf("credential = %+v", got)
		}
		if string(got.Extra["note"]) != `"kept"` {
			t.Fatalf("provider metadata dropped: %v", got.Extra)
		}
	})
	t.Run("oauth", func(t *testing.T) {
		got := ReadStoredCredential("anthropic", filepath.Join(dir, "auth.json"))
		if got == nil || got.Type != ai.CredentialOAuth || got.Access != "a" || got.Refresh != "r" {
			t.Fatalf("credential = %+v", got)
		}
	})
	t.Run("a byte order mark is stripped", func(t *testing.T) {
		if got := ReadStoredCredential("openai", write("bom.json", "\ufeff"+file)); got == nil || got.Key != "sk-one" {
			t.Fatalf("credential = %+v", got)
		}
	})
	t.Run("command-valued keys stay unresolved", func(t *testing.T) {
		got := ReadStoredCredential("p", write("cmd.json", `{"p":{"type":"api_key","key":"!echo secret"}}`))
		if got == nil || got.Key != "!echo secret" {
			t.Fatalf("credential = %+v", got)
		}
	})
	for name, path := range map[string]string{
		"absent provider": write("other.json", `{"q":{"type":"api_key","key":"k"}}`),
		"malformed file":  write("bad.json", `{`),
		"missing file":    filepath.Join(dir, "none.json"),
		"null entry":      write("null.json", `{"p":null}`),
		"non-object":      write("string.json", `{"p":"text"}`),
	} {
		t.Run(name+" is undefined", func(t *testing.T) {
			if got := ReadStoredCredential("p", path); got != nil {
				t.Fatalf("credential = %+v, want nil", got)
			}
		})
	}
	t.Run("the default path is the agent directory's auth.json", func(t *testing.T) {
		agentDir := t.TempDir()
		t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
		t.Setenv("PI_CODING_AGENT_DIR", agentDir)
		if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ReadStoredCredential("openai"); got == nil || got.Key != "sk-one" {
			t.Fatalf("credential = %+v", got)
		}
	})
}
