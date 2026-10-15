package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// migrateAuthToAuthJson (migrations.ts:33-58) lists providers in Object.entries order of oauth.json, then of the
// settings apiKeys not already migrated: array-index names ascending first, then first-insertion order. The interactive
// "Migrated credentials to auth.json" warning (interactive-mode.ts:1199-1201) prints that list as is.
func TestMigrateAuthToAuthJSONListsProvidersInEntriesOrder(t *testing.T) {
	agentDir := t.TempDir()
	oauth := `{"zeta":{"refresh":"r"},"7":{},"alpha":{},"2":{},"alpha":{"x":1}}`
	settings := `{"apiKeys":{"mid":"k","zeta":"dup","beta":"k","10":"n"}}`
	if err := os.WriteFile(filepath.Join(agentDir, "oauth.json"), []byte(oauth), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	// Map iteration would pass a single run by chance; repeat on fresh copies.
	want := []string{"2", "7", "zeta", "alpha", "10", "mid", "beta"}
	for range 20 {
		dir := t.TempDir()
		for name, content := range map[string]string{"oauth.json": oauth, "settings.json": settings} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got, err := migrateAuthToAuthJSON(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("migrated providers = %q, want %q", got, want)
		}
	}
}

// migrations.ts:60 and :69 write JSON.stringify(settings, null, 2) and JSON.stringify(migrated, null, 2): both keep the
// parsed key order (array-index names first), leave <, > and & unescaped, and the credential spread puts `type` first
// while a type in oauth.json keeps that position and overrides the value.
func TestMigrateAuthToAuthJSONWritesFilesInPiKeyOrder(t *testing.T) {
	dir := t.TempDir()
	oauth := `{"zeta":{"refresh":"r<1>","type":"custom","access":"a"},"alpha":{"b":1,"a":2}}`
	settings := `{"theme":"dark","apiKeys":{"mid":"k&1","7":"n"},"defaultModel":"m","a":{"z":1,"y":[]}}`
	for name, content := range map[string]string{"oauth.json": oauth, "settings.json": settings} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := migrateAuthToAuthJSON(dir); err != nil {
		t.Fatal(err)
	}
	gotSettings, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	const wantSettings = "{\n  \"theme\": \"dark\",\n  \"defaultModel\": \"m\",\n  \"a\": {\n    \"z\": 1,\n    \"y\": []\n  }\n}"
	if string(gotSettings) != wantSettings {
		t.Fatalf("settings.json = %q\nwant %q", gotSettings, wantSettings)
	}
	gotAuth, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	const wantAuth = "{\n  \"7\": {\n    \"type\": \"api_key\",\n    \"key\": \"n\"\n  },\n  \"zeta\": {\n    \"type\": \"custom\",\n    \"refresh\": \"r<1>\",\n    \"access\": \"a\"\n  },\n  \"alpha\": {\n    \"type\": \"oauth\",\n    \"b\": 1,\n    \"a\": 2\n  },\n  \"mid\": {\n    \"type\": \"api_key\",\n    \"key\": \"k&1\"\n  }\n}"
	if string(gotAuth) != wantAuth {
		t.Fatalf("auth.json = %q\nwant %q", gotAuth, wantAuth)
	}
}

// migrations.ts:36 and :51 parse JSON.parse(stripBom(...)), so a BOM-prefixed oauth.json or settings.json still migrates.
func TestMigrateAuthToAuthJSONStripsTheBOM(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"oauth.json":    "\ufeff" + `{"one":{"access":"a"}}`,
		"settings.json": "\ufeff" + `{"apiKeys":{"two":"k"},"theme":"dark"}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	providers, err := migrateAuthToAuthJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(providers, []string{"one", "two"}) {
		t.Fatalf("providers = %v, want [one two]", providers)
	}
	gotSettings, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if want := "{\n  \"theme\": \"dark\"\n}"; string(gotSettings) != want {
		t.Fatalf("settings.json = %q, want %q", gotSettings, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "oauth.json.migrated")); err != nil {
		t.Fatalf("oauth.json was not renamed: %v", err)
	}
}
