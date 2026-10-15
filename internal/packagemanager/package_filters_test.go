package packagemanager

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// A Package's configured filters go through minimatch as Pi's applyPackageFilter applies them (package-manager.ts:2280-2306, 661-685), and a filter minimatch cannot compile throws in collectPackageResources: a local Package keeps the kinds before it (resolveLocalExtensionSource's catch, :1372-1389), and an npm Package's throw rejects resolve() (:1315-1327). Probed with Pi 1.1.0 over RPC get_commands: braces keep a and b, "+(a|b)" keeps ab, a skills filter that throws drops the skills and the later prompts, and an npm Package's skills filter error comes before its prompts glob error.
func TestPackageFiltersFollowMinimatch(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	enabledPrompts := func(t *testing.T, resolved ResolvedPaths) []string {
		t.Helper()
		var names []string
		for _, r := range resolved.Prompts {
			if r.Enabled {
				names = append(names, strings.TrimSuffix(filepath.Base(r.Path), ".md"))
			}
		}
		slices.Sort(names)
		return names
	}
	for _, tc := range []struct {
		name    string
		prompts []string
		filter  codingagent.PackageSource
		want    []string
	}{
		{"braces", []string{"a", "b", "c"}, codingagent.PackageSource{Source: "./pkg", Prompts: []string{"prompts/{a,b}.md"}}, []string{"a", "b"}},
		{"extglob", []string{"ab", "x"}, codingagent.PackageSource{Source: "./pkg", Prompts: []string{"prompts/+(a|b).md"}}, []string{"ab"}},
		{"local throw", []string{"p"}, codingagent.PackageSource{Source: "./pkg", Skills: []string{"[[:alpha:]]-x"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, agentDir, sm, pm := resolveFixture(t)
			root := filepath.Join(agentDir, "pkg")
			for _, name := range tc.prompts {
				writeResolveFile(t, filepath.Join(root, "prompts", name+".md"), "x\n")
			}
			writeResolveFile(t, filepath.Join(root, "skills", "s1", "SKILL.md"), "---\nname: s1\ndescription: d\n---\nBody\n")
			if err := sm.SetPackages([]codingagent.PackageSource{tc.filter}); err != nil {
				t.Fatal(err)
			}
			resolved, err := pm.Resolve(nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := enabledPrompts(t, resolved); !slices.Equal(got, tc.want) || (tc.name == "local throw" && len(resolved.Skills) != 0) {
				t.Fatalf("enabled prompts %q, skills %+v; want prompts %q and no skills after a throw", got, resolved.Skills, tc.want)
			}
		})
	}
	t.Run("npm throw", func(t *testing.T) {
		cwd, agentDir, sm, pm := resolveFixture(t)
		root := filepath.Join(codingagent.NPMInstallRoot(cwd, agentDir, false), "node_modules", "filterprobe")
		writeResolveFile(t, filepath.Join(root, "package.json"), `{"name":"filterprobe","version":"1.0.0"}`)
		writeResolveFile(t, filepath.Join(root, "prompts", "p.md"), "x\n")
		if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:filterprobe", Prompts: []string{"[[:alpha:]]-x"}}}); err != nil {
			t.Fatal(err)
		}
		_, err := pm.Resolve(nil)
		if _, ok := errors.AsType[*packagecontent.PackagePatternError](err); !ok || !strings.HasSuffix(err.Error(), `[\p{L}\p{Nl}]\-x$/u: Invalid escape`) {
			t.Fatalf("Resolve = %v, want the prompts filter's SyntaxError", err)
		}
	})
	// Pi applies a kind's filter before it collects the next kind, so the skills filter throws before the prompts manifest glob does.
	t.Run("npm throw order", func(t *testing.T) {
		cwd, agentDir, sm, pm := resolveFixture(t)
		root := filepath.Join(codingagent.NPMInstallRoot(cwd, agentDir, false), "node_modules", "orderprobe")
		writeResolveFile(t, filepath.Join(root, "package.json"), `{"name":"orderprobe","version":"1.0.0","pi":{"skills":["skills"],"prompts":["prompts/[[:alpha:]]*,md"]}}`)
		writeResolveFile(t, filepath.Join(root, "skills", "s1", "SKILL.md"), "---\nname: s1\ndescription: d\n---\nBody\n")
		writeResolveFile(t, filepath.Join(root, "prompts", "a.md"), "x\n")
		if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:orderprobe", Skills: []string{"[[:alpha:]]-*"}}}); err != nil {
			t.Fatal(err)
		}
		_, err := pm.Resolve(nil)
		if err == nil || !strings.HasSuffix(err.Error(), `[\p{L}\p{Nl}]\-[^/]*?$/u: Invalid escape`) {
			t.Fatalf("Resolve = %v, want the skills filter's SyntaxError", err)
		}
	})
}
