package codingagent

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Pi 1be312b4 (#11588) session-selector.ts getSessionPathKey: the session tree finds a parent whose path differs from the child's parentSessionPath in casing only,
// on Windows. The same two spellings are two sessions on a case-sensitive platform.
func TestBuildSessionTreeFindsTheParentAcrossWindowsPathCasing(t *testing.T) {
	t.Cleanup(resolvepath.OverridePlatform("linux")) // the first half pins POSIX rules, whatever the host
	now := time.Now()
	root := SessionInfo{Path: `C:\Sessions\Root.jsonl`, Modified: now, Name: "root"}
	child := SessionInfo{Path: `C:\Sessions\child.jsonl`, ParentSessionPath: `c:\sessions\root.jsonl`, Modified: now.Add(-time.Hour), Name: "child"}
	roots := func() int { return len(buildSessionTree([]SessionInfo{root, child})) }
	if got := roots(); got != 2 {
		t.Fatalf("case-sensitive platform: %d roots, want the child to stay a root of its own", got)
	}
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	tree := buildSessionTree([]SessionInfo{root, child})
	if len(tree) != 1 || len(tree[0].Children) != 1 || tree[0].Children[0].Session.Path != child.Path {
		t.Fatalf("Windows: tree = %+v, want the child under the root", tree)
	}
}

// Pi 1be312b4 (#11588) session-selector.ts isCurrentSessionPath: the active session is found whatever the casing of its path on Windows, so the list marks it
// and Ctrl+D refuses to delete it.
func TestSessionSelectorFindsTheCurrentSessionAcrossWindowsPathCasing(t *testing.T) {
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	current := SessionInfo{Path: `C:\Sessions\Current.jsonl`, Name: "Current", Modified: time.Now()}
	loader := func() ([]SessionInfo, error) { return []SessionInfo{current}, nil }
	sel := newLoadedSessionSelector(loader, loader, nil, nil, `c:\sessions\current.jsonl`, sessionSelectorInputBindings(t))
	if !sel.isCurrentSession(current.Path) {
		t.Fatal("the active session is not marked when its path differs in casing")
	}
	sel.HandleInput("\x04")
	if sel.confirmDelete != "" || sel.statusState.message != "Cannot delete the currently active session" {
		t.Fatalf("Ctrl+D: confirmation=%q status=%q", sel.confirmDelete, sel.statusState.message)
	}
}

// Pi 1be312b4 (#11588) package-manager.ts: collectAncestorAgentsSkillDirs(cwd) drops the user's ~/.agents/skills with pathsEqual, so a home spelled in other casing
// than the working directory does not list the user's skills as the project's on Windows.
func TestAncestorAgentsSkillScanExcludesTheUserDirectoryAcrossWindowsPathCasing(t *testing.T) {
	t.Cleanup(resolvepath.OverridePlatform("linux")) // the first half pins POSIX rules, whatever the host
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	t.Setenv("HOME", strings.ToUpper(home))
	userSkills := filepath.Join(home, ".agents", "skills")
	dirs := func() []string {
		var listed []string
		for _, dir := range NewAgentsSkillScan(cwd).Dirs() {
			listed = append(listed, dir.Dir)
		}
		return listed
	}
	if !slices.Contains(dirs(), userSkills) {
		t.Fatalf("case-sensitive platform: the ancestor scan %q omits %q, a different directory from the home's", dirs(), userSkills)
	}
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	if listed := dirs(); slices.Contains(listed, userSkills) {
		t.Fatalf("Windows: the ancestor scan lists the user's %q as the project's: %q", userSkills, listed)
	}
}
