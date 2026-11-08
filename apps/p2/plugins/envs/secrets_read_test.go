package envs

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// The read side of "sandbox secrets" in apps/p2/test/p2/sandboxes.test.ts, on the file Pi's saveSecret writes.
func TestSandboxSecretsReadMetaAndValuesInLocaleOrder(t *testing.T) {
	directory := t.TempDir()
	meta, values, err := SandboxSecrets(t.Context(), directory, "box1")
	if err != nil || len(meta) != 0 || values.Len() != 0 {
		t.Fatalf("no file: %v %v %v", meta, values, err)
	}
	file := `{
  "box1": {
    "GITHUB_TOKEN": { "hosts": ["*.github.com"], "preset": "github", "value": "ghp_secret", "version": "v1", "setAt": 1 },
    "A1": { "hosts": ["a.example"], "value": "one", "version": "v2", "setAt": 2 },
    "_Z": { "hosts": ["z.example"], "preset": "custom", "value": "z", "version": "v3", "setAt": 3 }
  },
  "other": { "NPM_TOKEN": { "hosts": ["registry.npmjs.org"], "value": "n", "version": "v4", "setAt": 4 } }
}
`
	if err := os.WriteFile(SecretsPath(directory), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	meta, values, err = SandboxSecrets(t.Context(), directory, "box1")
	if err != nil {
		t.Fatal(err)
	}
	// localeCompare: `_` before digits before letters.
	want := []SecretMeta{
		{Name: "_Z", Hosts: []string{"z.example"}, Version: "v3"},
		{Name: "A1", Hosts: []string{"a.example"}, Version: "v2"},
		{Name: "GITHUB_TOKEN", Hosts: []string{"*.github.com"}, Version: "v1", Aliases: []string{"GH_TOKEN"}, GitHosts: []string{"github.com"}},
	}
	if !reflect.DeepEqual(meta, want) {
		t.Fatalf("meta = %+v", meta)
	}
	if !reflect.DeepEqual(values.Keys(), []string{"_Z", "A1", "GITHUB_TOKEN"}) || values.Value("GITHUB_TOKEN") != "ghp_secret" {
		t.Fatalf("values = %v", values.Keys())
	}
	argument := decodeArgument(t, SandboxArgument(object(t, boxJSON), meta[2:]))
	if !strings.Contains(argument, "GITHUB_TOKEN") || strings.Contains(argument, "ghp_secret") {
		t.Fatalf("argument = %s", argument)
	}
	if meta, _, err = SandboxSecrets(t.Context(), directory, "missing"); err != nil || len(meta) != 0 {
		t.Fatalf("missing sandbox: %v %v", meta, err)
	}
	for _, bad := range []string{"[]", "null", "{not json"} {
		if err := os.WriteFile(SecretsPath(directory), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := SandboxSecrets(t.Context(), directory, "box1"); err == nil || err.Error() != "Invalid sandbox-secrets.json" {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	if err := os.WriteFile(SecretsPath(directory), []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if meta, _, err = SandboxSecrets(t.Context(), directory, "box1"); err != nil || len(meta) != 0 {
		t.Fatalf("blank file: %v %v", meta, err)
	}
}
