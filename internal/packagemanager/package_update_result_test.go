package packagemanager

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// pi: packages/coding-agent/test/git-update.test.ts, 342514bdf (#11707): a Git update reports the commit it moved from and to as a
// "result" event, and an up-to-date checkout reports nothing.
func TestGitUpdateReportsTheChangedCommitAsAResultEvent(t *testing.T) {
	cwd, sm := trustedSettings(t)
	testenv.IsolateGit(t)
	origin := testenv.NewSubdirectoryOrigin(t, "origin")
	source := "git:" + origin.URL
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: source}}); err != nil {
		t.Fatal(err)
	}
	checkout, err := GitCheckoutPath(cwd, sm.AgentDir(), source, false)
	if err != nil {
		t.Fatal(err)
	}
	origin.CloneAt(t, checkout, origin.Commits[0])
	latest := origin.Commits[len(origin.Commits)-1]

	var results []ProgressEvent
	manager := NewPackageManager(PackageManagerOptions{CWD: cwd, AgentDir: sm.AgentDir(), SettingsManager: sm})
	manager.SetProgressCallback(func(event ProgressEvent) {
		if event.Type == "result" {
			results = append(results, event)
		}
	})
	if err := manager.Update(""); err != nil {
		t.Fatal(err)
	}
	want := []ProgressEvent{{Type: "result", Action: "update", Source: source, Message: new(source + " " + origin.Commits[0][:7] + " -> " + latest[:7])}}
	if !slices.EqualFunc(results, want, func(a, b ProgressEvent) bool {
		return a.Type == b.Type && a.Action == b.Action && a.Source == b.Source && *a.Message == *b.Message
	}) {
		t.Fatalf("result events = %+v, want %+v", results, want)
	}

	results = nil
	if err := manager.Update(""); err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("an up-to-date checkout reported %+v", results)
	}
}

func TestUpdatedPackageMessage(t *testing.T) {
	for _, tc := range []struct {
		pkg  updatedPackage
		want string
	}{
		{updatedPackage{kind: "npm", scope: "user", source: "npm:foo", from: "1.2.0", to: "1.3.0"}, "npm:foo 1.2.0 -> 1.3.0"},
		{updatedPackage{kind: "npm", scope: "project", source: "npm:example@^1.0.0", from: "1.0.0", to: "1.2.0"}, "npm:example@^1.0.0 (project) 1.0.0 -> 1.2.0"},
		{updatedPackage{kind: "npm", scope: "user", source: "npm:foo", to: "1.0.0"}, "npm:foo (none) -> 1.0.0"},
		{updatedPackage{kind: "git", scope: "project", source: "git:github.com/user/bar", from: "abc1234567", to: "def5678901"}, "git:github.com/user/bar (project) abc1234 -> def5678"},
	} {
		if got := tc.pkg.message(); got != tc.want {
			t.Errorf("message() = %q, want %q", got, tc.want)
		}
	}
}
