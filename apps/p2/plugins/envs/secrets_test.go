package envs

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
)

// Ports sandboxes.test.ts "sandbox secrets: stored 0600, listed without values, sent only to allowed hosts, part of the
// revision".

func str(value string) *string { return &value }

func TestSandboxSecretsAreStoredPrivateListedWithoutValuesAndPartOfTheRevision(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	box := object(t, boxJSON)
	allow := []string{"registry.npmjs.org", "*.github.com"}

	wantError(t, SaveSecret(ctx, directory, "box1", "GITHUB_TOKEN", SecretChange{Value: str("ghp_x"), Hosts: []string{"api.github.com"}}, allow), "not an allowed host")
	if err := SaveSecret(ctx, directory, "box1", "bad-name", SecretChange{Value: str("x"), Hosts: []string{"*.github.com"}}, allow); err == nil {
		t.Error("a secret named bad-name was accepted")
	}
	wantError(t, SaveSecret(ctx, directory, "box1", "A", SecretChange{Value: str("x\ny"), Hosts: []string{"*.github.com"}}, allow), "one line")
	wantError(t, SaveSecret(ctx, directory, "box1", "A", SecretChange{Value: str("x")}, allow), "at least one host")
	wantError(t, SaveSecret(ctx, directory, "box1", "A", SecretChange{Value: str(""), Hosts: []string{"*.github.com"}}, allow), "1 to 16384")
	wantError(t, SaveSecret(ctx, directory, "box1", "A", SecretChange{Hosts: []string{"*.github.com"}}, allow), "needs a value")
	if _, err := os.Stat(SecretsPath(directory)); err == nil {
		if listed, err := ListSecrets(directory, "box1"); err != nil || len(listed) != 0 {
			t.Errorf("a refused save left %v, %v", listed, err)
		}
	}

	if err := SaveSecret(ctx, directory, "box1", "GITHUB_TOKEN", SecretChange{Value: str("ghp_secret"), Hosts: []string{"*.github.com"}, Preset: "github"}, allow); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(SecretsPath(directory))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("sandbox-secrets.json = %v, %v; want 0600", info, err)
	}
	listed, err := ListSecrets(directory, "box1")
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListSecrets = %v, %v", listed, err)
	}
	if got := listed[0]; got.Name != "GITHUB_TOKEN" || !slices.Equal(got.Hosts, []string{"*.github.com"}) || got.Preset != "github" || got.SetAt == 0 {
		t.Errorf("listed %+v", got)
	}
	if text := mustMarshal(t, listed[0].JSON()); strings.Contains(text, "ghp_secret") {
		t.Errorf("a listed secret carries its value: %s", text)
	}

	meta, values, err := SandboxSecrets(ctx, directory, "box1")
	if err != nil {
		t.Fatal(err)
	}
	if mustMarshal(t, values) != `{"GITHUB_TOKEN":"ghp_secret"}` {
		t.Errorf("values = %s", mustMarshal(t, values))
	}
	if len(meta) != 1 || !slices.Equal(meta[0].Aliases, []string{"GH_TOKEN"}) {
		t.Errorf("meta = %+v", meta)
	}
	// The configuration names the secret, never its value; a new value changes the revision.
	argument := SandboxArgument(box, meta)
	if strings.Contains(argument, "ghp_secret") {
		t.Error("the argument carries the value")
	}
	first := SandboxRevision(box, meta)

	// No value: the stored one is kept (and its age).
	if err := SaveSecret(ctx, directory, "box1", "GITHUB_TOKEN", SecretChange{Hosts: []string{"*.github.com"}}, allow); err != nil {
		t.Fatal(err)
	}
	_, values, _ = SandboxSecrets(ctx, directory, "box1")
	if mustMarshal(t, values) != `{"GITHUB_TOKEN":"ghp_secret"}` {
		t.Errorf("the value was not kept: %s", mustMarshal(t, values))
	}
	kept, _ := ListSecrets(directory, "box1")
	if kept[0].SetAt != listed[0].SetAt || kept[0].Preset != "github" {
		t.Errorf("a save without a value changed %+v to %+v", listed[0], kept[0])
	}

	if err := SaveSecret(ctx, directory, "box1", "GITHUB_TOKEN", SecretChange{Value: str("ghp_new"), Hosts: []string{"*.github.com"}}, allow); err != nil {
		t.Fatal(err)
	}
	secondMeta, _, _ := SandboxSecrets(ctx, directory, "box1")
	if SandboxRevision(box, secondMeta) == first || SandboxRevision(box, secondMeta) == SandboxRevision(box, nil) {
		t.Error("a new secret value does not change the sandbox's revision")
	}
	all, err := AllSecretValues(directory)
	if err != nil || !slices.Equal(all, []string{"ghp_new"}) {
		t.Errorf("AllSecretValues = %v, %v", all, err)
	}

	name := "GITHUB_TOKEN"
	if err := DeleteSecret(ctx, directory, "box1", &name); err != nil {
		t.Fatal(err)
	}
	if listed, _ := ListSecrets(directory, "box1"); len(listed) != 0 {
		t.Errorf("after delete: %v", listed)
	}
	// Deleting a sandbox's secrets: all of them go.
	_ = SaveSecret(ctx, directory, "box1", "A", SecretChange{Value: str("1"), Hosts: []string{"registry.npmjs.org"}}, allow)
	_ = SaveSecret(ctx, directory, "box1", "B", SecretChange{Value: str("2"), Hosts: []string{"registry.npmjs.org"}}, allow)
	if err := DeleteSecret(ctx, directory, "box1", nil); err != nil {
		t.Fatal(err)
	}
	if listed, _ := ListSecrets(directory, "box1"); len(listed) != 0 {
		t.Errorf("after deleting the sandbox's secrets: %v", listed)
	}
}

func TestAnInvalidSecretsFileIsRefused(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(SecretsPath(directory), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListSecrets(directory, "box1"); err == nil || !strings.Contains(err.Error(), "Invalid sandbox-secrets.json") {
		t.Errorf("ListSecrets on an array = %v", err)
	}
	wantError(t, SaveSecret(context.Background(), directory, "box1", "A", SecretChange{Value: str("1"), Hosts: []string{"x.com"}}, []string{"x.com"}), "Invalid sandbox-secrets.json")
	if data, _ := os.ReadFile(SecretsPath(directory)); string(data) != "[]" {
		t.Errorf("an invalid file was overwritten: %q", data)
	}
}

func TestSecretPresetsAreThePresetsOfTheLauncher(t *testing.T) {
	ids := []string{}
	for _, preset := range SecretPresets {
		ids = append(ids, preset.ID)
		if !SecretName.MatchString(preset.Name) {
			t.Errorf("preset %s names %q", preset.ID, preset.Name)
		}
	}
	if !slices.Equal(ids, []string{"github", "radius", "npm", "custom"}) {
		t.Errorf("presets = %v", ids)
	}
}
