package packagemanager

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func resolveExtensionsForSettings(t *testing.T, userSettings, projectSettings string, files ...string) (string, []string) {
	t.Helper()
	t.Setenv("PI_OFFLINE", "1")
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", filepath.Join(dir, "home"))
	cwd, agentDir := filepath.Join(dir, "work", "proj"), filepath.Join(dir, "agent")
	write := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		write(filepath.Join(dir, f), "x")
	}
	write(filepath.Join(agentDir, "settings.json"), userSettings)
	write(filepath.Join(cwd, codingagent.ConfigDirName(), "settings.json"), projectSettings)
	manager := NewPackageManager(PackageManagerOptions{CWD: cwd, AgentDir: agentDir, SettingsManager: codingagent.NewSettingsManager(cwd, agentDir)})
	got, err := manager.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, r := range got.Extensions {
		if r.Enabled {
			rel, _ := filepath.Rel(dir, r.Path)
			paths = append(paths, filepath.ToSlash(rel))
		}
	}
	return dir, paths
}

// upstream: package-manager.ts:1360-1377 resolveLocalExtensionSource: a local source that is a file is added enabled before any filter is read, so autoload: false does not drop it.
func TestAutoloadFalseKeepsALocalFileSource(t *testing.T) {
	_, got := resolveExtensionsForSettings(t, `{}`, `{"packages":[{"source":"../../solo.ts","autoload":false,"extensions":["!other.ts"]}]}`, "work/solo.ts")
	if !slices.Equal(got, []string{"work/solo.ts"}) {
		t.Fatalf("enabled extensions = %q, want the file source", got)
	}
}

// upstream: package-manager.ts:1742-1762 dedupePackages keeps the seen index on a project entry with autoload: false, so every user entry for that Package is kept, the repeated one included.
func TestProjectAutoloadFalseKeepsRepeatedUserEntries(t *testing.T) {
	files := []string{"work/pkg/extensions/e1.ts", "work/pkg/extensions/e2.ts"}
	user := `{"packages":[{"source":"../work/pkg","autoload":false,"extensions":["!nothing.ts"]},"../work/pkg"]}`
	_, got := resolveExtensionsForSettings(t, user, `{"packages":[{"source":"../../pkg","autoload":false,"extensions":[]}]}`, files...)
	if !slices.Equal(got, []string{"work/pkg/extensions/e1.ts", "work/pkg/extensions/e2.ts"}) {
		t.Fatalf("enabled extensions = %q, want the repeated user entry's resources", got)
	}
	// Without the project entry the repeated user entry is ignored: the first, autoload: false, touches nothing.
	_, got = resolveExtensionsForSettings(t, user, `{}`, files...)
	if len(got) != 0 {
		t.Fatalf("enabled extensions = %q, want none", got)
	}
}
