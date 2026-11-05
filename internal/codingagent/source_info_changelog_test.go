package codingagent

import "testing"

// Pi b4b72a35 source-info.ts: createSourceInfo and createSyntheticSourceInfo carry the changelogPath of the package, only when set.
func TestSourceInfoCarriesTheChangelogPath(t *testing.T) {
	metadata := PathMetadata{Source: "npm:pkg", Scope: "user", Origin: "package", BaseDir: "/pkg", ChangelogPath: "/pkg/CHANGELOG.md"}
	if got := metadata.SourceInfo("/pkg/index.ts"); got.ChangelogPath != "/pkg/CHANGELOG.md" {
		t.Errorf("SourceInfo.ChangelogPath = %q", got.ChangelogPath)
	}
	if got := (PathMetadata{Source: "npm:pkg"}).SourceInfo("/pkg/index.ts"); got.ChangelogPath != "" {
		t.Errorf("SourceInfo.ChangelogPath without a changelog = %q", got.ChangelogPath)
	}
	synthetic := CreateSyntheticSourceInfo("<inline:x>", SyntheticSourceInfoOptions{Source: "inline", ChangelogPath: "/x/CHANGELOG.md"})
	if synthetic.ChangelogPath != "/x/CHANGELOG.md" {
		t.Errorf("synthetic ChangelogPath = %q", synthetic.ChangelogPath)
	}
}

// Pi b4b72a35 settings-manager.ts getExtensionChangelogVersion/setExtensionChangelogVersion: a global map keyed by package, written one key at a time.
func TestExtensionChangelogVersionsRoundTripThroughSettings(t *testing.T) {
	sm := NewInMemorySettingsManager(Settings{})
	if _, ok := sm.GetExtensionChangelogVersion("a"); ok {
		t.Fatal("an unseen package reports a version")
	}
	if err := sm.SetExtensionChangelogVersion("a", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetExtensionChangelogVersion("b", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetExtensionChangelogVersion("a", "1.1.0"); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"a": "1.1.0", "b": "2.0.0"} {
		if got, ok := sm.GetExtensionChangelogVersion(key); !ok || got != want {
			t.Errorf("version of %q = %q, %v; want %q", key, got, ok, want)
		}
	}
	if got := sm.GetGlobalSettings().ExtensionChangelogVersions; len(got) != 2 {
		t.Errorf("global settings hold %v", got)
	}
}
