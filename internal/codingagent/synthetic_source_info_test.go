package codingagent

import "testing"

// source-info.ts:41-57 createSyntheticSourceInfo: scope defaults to "temporary", origin to "top-level", baseDir stays undefined.
func TestCreateSyntheticSourceInfoAppliesUpstreamDefaults(t *testing.T) {
	got := CreateSyntheticSourceInfo("<sdk:tool>", SyntheticSourceInfoOptions{Source: "sdk"})
	want := PiSourceInfo{Path: "<sdk:tool>", Source: "sdk", Scope: "temporary", Origin: "top-level"}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
	got = CreateSyntheticSourceInfo("/p/SKILL.md", SyntheticSourceInfoOptions{Source: "git:x", Scope: "project", Origin: "package", BaseDir: "/p"})
	want = PiSourceInfo{Path: "/p/SKILL.md", Source: "git:x", Scope: "project", Origin: "package", BaseDir: "/p"}
	if got != want {
		t.Fatalf("explicit = %+v, want %+v", got, want)
	}
}

func TestCLISourceInfoIsASyntheticSourceInfo(t *testing.T) {
	if got, want := CLISourceInfo("/x.ts"), CreateSyntheticSourceInfo("/x.ts", SyntheticSourceInfoOptions{Source: CLISourceName}); got != want {
		t.Fatalf("CLISourceInfo = %+v, want %+v", got, want)
	}
}
