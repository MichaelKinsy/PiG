package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// Review CLIEXT-016: Pi package-manager.ts:2036-2050 keeps one global npm root per package manager, keyed by the npmCommand argv joined with NUL. An unchanged command reuses the root, a changed command runs `root -g` again, and a command change back to an earlier argv runs it once more because only the latest key is kept.
func TestGlobalNpmRootCachedPerCommand(t *testing.T) {
	f := newPackageProcessFixture(t, `if(command!=='mise'||args.slice(-2).join(' ')!=='root -g')throw new Error('unexpected command'); console.log(path.join(process.env.PIG_TEST_PACKAGE_CWD,args[1]==='node@20'?'node20':'node22','lib','node_modules'));`)
	root20 := filepath.Join(f.cwd, "node20", "lib", "node_modules")
	installed := filepath.Join(root20, "@scope", "pkg")
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	node20 := []string{"mise", "exec", "node@20", "--", "npm"}
	node22 := []string{"mise", "exec", "node@22", "--", "npm"}
	lookup := func(sm *codingagent.SettingsManager, command []string, want string) {
		t.Helper()
		if err := sm.SetNpmCommand(command); err != nil {
			t.Fatal(err)
		}
		if got := packagemanager.InstalledPathForSource(f.cwd, sm.AgentDir(), sm, "npm:@scope/pkg", false); got != want {
			t.Fatalf("npmCommand %q: installed=%q want=%q", command, got, want)
		}
	}
	lookup(f.settings, node20, installed)
	lookup(f.settings, node20, installed)
	lookup(f.settings, node22, "")
	lookup(f.settings, node22, "")
	lookup(f.settings, node20, installed)
	// A separate SettingsManager stands for a separate Pi package manager, which starts without a cached root.
	lookup(codingagent.NewSettingsManager(f.cwd, f.agent), node20, installed)
	var got [][]string
	for _, call := range f.calls(t) {
		if call.Command != "mise" {
			t.Fatalf("unexpected call %+v", call)
		}
		got = append(got, call.Args)
	}
	want := [][]string{
		{"exec", "node@20", "--", "npm", "root", "-g"},
		{"exec", "node@22", "--", "npm", "root", "-g"},
		{"exec", "node@20", "--", "npm", "root", "-g"},
		{"exec", "node@20", "--", "npm", "root", "-g"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("root lookups=%q\nwant %q", got, want)
	}
}

// Pi package-manager.ts:2685-2698 reads a synchronous npm command's stdout, falling back to stderr only when stdout is empty, so a warning on stderr does not corrupt the global root.
func TestGlobalNpmRootIgnoresStderrWarnings(t *testing.T) {
	f := newPackageProcessFixture(t, `if(args.join(' ')!=='root -g')throw new Error('unexpected command'); console.error('npm warn config deprecated setting'); console.log(path.join(process.env.PIG_TEST_PACKAGE_CWD,'legacy','node_modules'));`)
	installed := filepath.Join(f.cwd, "legacy", "node_modules", "legacy-pkg")
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := packagemanager.InstalledPathForSource(f.cwd, f.settings.AgentDir(), f.settings, "npm:legacy-pkg", false); got != installed {
		t.Fatalf("installed=%q want=%q", got, installed)
	}
}
