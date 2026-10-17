package packagemanager

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// writeThrowingGlobPackage writes a Package whose prompts entry "prompts/[[:alpha:]]*,md" makes Node's globSync throw: the POSIX class gives minimatch's RegExp the u flag, which rejects the escaped ",". Skills come before prompts in Pi's RESOURCE_TYPES and themes after.
func writeThrowingGlobPackage(t *testing.T, root string) {
	t.Helper()
	writeResolveFile(t, filepath.Join(root, "package.json"), `{"name":"globprobe","version":"1.0.0","pi":{"skills":["skills"],"prompts":["prompts/good.md","prompts/[[:alpha:]]*,md"],"themes":["themes/*.json"]}}`)
	writeResolveFile(t, filepath.Join(root, "skills", "probe-skill", "SKILL.md"), "---\nname: probe-skill\ndescription: d\n---\nBody\n")
	writeResolveFile(t, filepath.Join(root, "prompts", "good.md"), "Good\n")
	writeResolveFile(t, filepath.Join(root, "prompts", "a,md"), "A\n")
	writeResolveFile(t, filepath.Join(root, "themes", "probe.json"), "{}\n")
}

// throwingGlobMessage is the SyntaxError Node 24 gives for that entry; globSync ignores case on Windows and macOS only.
func throwingGlobMessage() string {
	flags := "u"
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		flags = "iu"
	}
	return `Invalid regular expression: /^(?!\.)[\p{L}\p{Nl}][^/]*?\,md$/` + flags + `: Invalid escape`
}

// A manifest glob that Node's globSync throws for stops Pi's collectPackageResources. For a local Package, resolveLocalExtensionSource catches the throw (package-manager.ts:1372-1389), so the kinds collected before it stay and that kind and the later ones are lost; an npm Package's resources are collected outside any catch (:1315-1327), so resolve() rejects with the SyntaxError. Probed with Pi 1.1.0 (get_commands over RPC and print mode): the local Package keeps its skill and loses its prompts and theme, and the npm Package stops startup with that message.
func TestManifestGlobThrowFollowsPiCallers(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	t.Run("local", func(t *testing.T) {
		cwd, agentDir, sm, pm := resolveFixture(t)
		writeThrowingGlobPackage(t, filepath.Join(agentDir, "localpkg"))
		if err := sm.SetPackages([]codingagent.PackageSource{{Source: "./localpkg"}}); err != nil {
			t.Fatal(err)
		}
		if err := ValidateConfiguredPackagePatterns(cwd, agentDir, sm); err != nil {
			t.Fatalf("ValidateConfiguredPackagePatterns = %v, want nil for a local Package", err)
		}
		resolved, err := pm.Resolve(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(resolved.Skills) != 1 || !strings.Contains(filepath.ToSlash(resolved.Skills[0].Path), "skills/probe-skill") || len(resolved.Prompts) != 0 || len(resolved.Themes) != 0 {
			t.Fatalf("skills %+v, prompts %+v, themes %+v; want the skill only", resolved.Skills, resolved.Prompts, resolved.Themes)
		}
	})
	t.Run("npm", func(t *testing.T) {
		cwd, agentDir, sm, pm := resolveFixture(t)
		writeThrowingGlobPackage(t, filepath.Join(codingagent.NPMInstallRoot(cwd, agentDir, false), "node_modules", "globprobe"))
		if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:globprobe"}}); err != nil {
			t.Fatal(err)
		}
		err := ValidateConfiguredPackagePatterns(cwd, agentDir, sm)
		thrown, ok := errors.AsType[*packagecontent.PackagePatternError](err)
		if !ok || err.Error() != throwingGlobMessage() || thrown.Kind != packagecontent.Prompts {
			t.Fatalf("ValidateConfiguredPackagePatterns = %v, want the prompts entry's %q", err, throwingGlobMessage())
		}
		if _, err := pm.Resolve(nil); err == nil || !strings.HasSuffix(err.Error(), throwingGlobMessage()) {
			t.Fatalf("Resolve = %v, want %q", err, throwingGlobMessage())
		}
	})
	// An autoload-disabled Project delta collects only the kinds its patterns name (applyPackageDeltaFilter, package-manager.ts:2316-2333), so a delta naming skills never runs the prompts glob.
	t.Run("delta", func(t *testing.T) {
		cwd, agentDir, sm, _ := resolveFixture(t)
		writeThrowingGlobPackage(t, filepath.Join(codingagent.NPMInstallRoot(cwd, agentDir, false), "node_modules", "globprobe"))
		disabled := false
		if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:globprobe", Skills: []string{}}}); err != nil {
			t.Fatal(err)
		}
		if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:globprobe", Autoload: &disabled, Skills: []string{"-skills/probe-skill/SKILL.md"}}}); err != nil {
			t.Fatal(err)
		}
		pkgs := ResolvedConfiguredPackageSources(cwd, agentDir, sm, true)
		if len(pkgs) != 2 || !IsProjectPackageDelta(pkgs[0].Source) {
			t.Fatalf("packages = %+v, want the Project delta first", pkgs)
		}
		if _, err := discoverConfiguredPackage(pkgs[0].InstalledPath, pkgs[0]); err != nil {
			t.Fatalf("delta discovery = %v, want nil", err)
		}
		if _, err := discoverConfiguredPackage(pkgs[1].InstalledPath, pkgs[1]); err == nil {
			t.Fatal("user Package discovery succeeded, want the prompts glob's error")
		}
	})
}

// Pi resolves Project Packages before user ones (resolve, package-manager.ts:925-936), so a Project npm Package's throw is the one resolve() rejects with, and a throw in an npm Package's "pig" block, which Pi never reads, rejects nothing.
func TestManifestGlobThrowOrderAndKinds(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	t.Setenv("PIG_OFFLINE", "1")
	t.Run("project first", func(t *testing.T) {
		cwd, agentDir, sm, _ := resolveFixture(t)
		for _, pkg := range []struct {
			project bool
			name    string
			glob    string
		}{{false, "userpkg", "prompts/[[:alpha:]]*,md"}, {true, "projectpkg", "prompts/[[:alpha:]]*#md"}} {
			root := filepath.Join(codingagent.NPMInstallRoot(cwd, agentDir, pkg.project), "node_modules", pkg.name)
			writeResolveFile(t, filepath.Join(root, "package.json"), `{"name":"`+pkg.name+`","version":"1.0.0","pi":{"prompts":["`+pkg.glob+`"]}}`)
		}
		if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:userpkg"}}); err != nil {
			t.Fatal(err)
		}
		if err := sm.SetProjectPackages([]codingagent.PackageSource{{Source: "npm:projectpkg"}}); err != nil {
			t.Fatal(err)
		}
		if err := ValidateConfiguredPackagePatterns(cwd, agentDir, sm); err == nil || !strings.Contains(err.Error(), `\#md$/`) {
			t.Fatalf("ValidateConfiguredPackagePatterns = %v, want the Project Package's SyntaxError", err)
		}
	})
	t.Run("pig block", func(t *testing.T) {
		cwd, agentDir, sm, pm := resolveFixture(t)
		root := filepath.Join(codingagent.NPMInstallRoot(cwd, agentDir, false), "node_modules", "pigprobe")
		writeResolveFile(t, filepath.Join(root, "package.json"), `{"name":"pigprobe","version":"1.0.0","pi":{"prompts":["prompts/*.md"]},"pig":{"hooks":["hooks/[[:alpha:]]*,json"]}}`)
		writeResolveFile(t, filepath.Join(root, "prompts", "good.md"), "Good\n")
		if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:pigprobe"}}); err != nil {
			t.Fatal(err)
		}
		resolved, err := pm.Resolve(nil)
		if err != nil || len(resolved.Prompts) != 1 {
			t.Fatalf("Resolve = %+v, %v; want the prompt and no error", resolved.Prompts, err)
		}
	})
}
