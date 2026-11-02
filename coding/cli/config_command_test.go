package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestProjectPackageDeltaDrivesStartupStatusAndResourceCollectors(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir, packageRoot := filepath.Join(root, "work"), filepath.Join(root, "agent"), filepath.Join(root, "pkg")
	for _, dir := range []string{cwd, agentDir, packageRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	files := map[string]string{
		"package.json":     `{"name":"pkg","pi":{"extensions":["extensions/*"],"skills":["skills/*"],"prompts":["prompts/*.md"],"themes":["themes/*.json"]}}`,
		"prompts/alpha.md": "alpha", "prompts/beta.md": "beta",
		"themes/alpha.json": `{}`, "themes/beta.json": `{}`,
		"skills/alpha/SKILL.md": "---\nname: alpha\ndescription: alpha\n---\n", "skills/beta/SKILL.md": "---\nname: beta\ndescription: beta\n---\n",
	}
	for _, name := range []string{"alpha", "beta"} {
		files["extensions/"+name+"/go.mod"] = "module example.com/" + name + "\n\ngo 1.26\n"
		files["extensions/"+name+"/extension.go"] = "package " + name + "\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return nil }\n"
	}
	for name, content := range files {
		path := filepath.Join(packageRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: packageRoot}}); err != nil {
		t.Fatal(err)
	}
	for kind, target := range map[tui.ResourceType]string{
		tui.ResourceExtensions: filepath.Join(packageRoot, "extensions", "alpha"),
		tui.ResourceSkills:     filepath.Join(packageRoot, "skills", "alpha"),
		tui.ResourcePrompts:    filepath.Join(packageRoot, "prompts", "alpha.md"),
		tui.ResourceThemes:     filepath.Join(packageRoot, "themes", "alpha.json"),
	} {
		item := &tui.ResourceItem{Path: target, ResourceType: kind, Scope: "user", Origin: "package", Source: packageRoot, BaseDir: packageRoot, Inherited: true, InheritedEnabled: true}
		if err := applyProjectConfigOverride(cwd, settings, item, "unload"); err != nil {
			t.Fatal(err)
		}
	}
	settings.Reload()
	if err := startupPackageValidationError(cwd, settings); err != nil {
		t.Fatalf("effective startup validation failed: %v", err)
	}
	assertOneBeta := func(name string, paths []string) {
		t.Helper()
		if len(paths) != 1 || !strings.Contains(filepath.ToSlash(paths[0]), "/beta") {
			t.Fatalf("%s paths = %v, want one beta", name, paths)
		}
	}
	assertOneBeta("prompts", collectPackagePromptPaths(cwd, settings))
	assertOneBeta("themes", collectPackageThemePaths(cwd, settings))
	assertOneBeta("skills", collectPackageSkillPaths(cwd, settings, nil))
	exts := collectPackageExtensionConfigs(cwd, settings, nil)
	if len(exts) != 1 || exts[0].Name != "beta" {
		t.Fatalf("extension configs = %#v, want beta only", exts)
	}
	items, err := collectConfigResourceItems(cwd, agentDir, settings)
	if err != nil {
		t.Fatal(err)
	}
	betaEnabled := make(map[tui.ResourceType]int)
	for _, item := range items {
		if item.Origin != "package" || !item.Enabled {
			continue
		}
		name := filepath.Base(item.Path)
		if strings.HasPrefix(name, "alpha") {
			t.Errorf("%s alpha remained enabled: %#v", item.ResourceType, item)
		}
		if strings.HasPrefix(name, "beta") {
			betaEnabled[item.ResourceType]++
		}
	}
	for _, kind := range []tui.ResourceType{tui.ResourceExtensions, tui.ResourceSkills, tui.ResourcePrompts, tui.ResourceThemes} {
		if betaEnabled[kind] != 1 {
			t.Errorf("%s enabled beta count = %d, want 1", kind, betaEnabled[kind])
		}
	}
	// Status applies the project's settings only when the project is trusted, as a print run does.
	if err := codingagent.NewProjectTrustStore(agentDir).Set(cwd, new(true)); err != nil {
		t.Fatal(err)
	}
	status := collectStatus()
	if status.Packages.Total != 1 || status.Packages.User != 1 || status.Packages.Project != 0 || len(status.Errors) != 0 {
		t.Fatalf("status Package delta projection = %#v", status)
	}
	for _, item := range status.Resources.Items {
		if item.Origin != "package" {
			continue
		}
		if strings.HasPrefix(item.Name, "alpha") && item.Enabled {
			t.Errorf("status reports disabled delta resource enabled: %#v", item)
		}
	}
}

func TestProjectPackageDeltaPreservesStrictManifestValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		prepare func(*testing.T, string) string
		// want is the validator's rejection for this escape, not merely any error.
		want []string
	}{
		"lexical traversal": {func(t *testing.T, root string) string { return `{"name":"pkg","pi":{"prompts":["../outside.md"]}}` }, []string{`prompts manifest entry "../outside.md": package resource `, " escapes package root "}},
		"absolute path":     {func(t *testing.T, root string) string { return `{"name":"pkg","pi":{"prompts":["/tmp/outside.md"]}}` }, []string{`prompts manifest entry "/tmp/outside.md" is not a package-relative path`}},
		"symlink escape": {func(t *testing.T, root string) string {
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "escape.md"), []byte("escape"), 0o644); err != nil {
				t.Fatal(err)
			}
			// A directory link: on Windows a junction, which escapes the Package root as a symbolic link does.
			testenv.RequireDirectoryLink(t, outside, filepath.Join(root, "outside"))
			return `{"name":"pkg","pi":{"prompts":["outside/escape.md"]}}`
		}, []string{`prompts manifest entry "outside/escape.md": package resource `, " resolves outside package root "}},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			cwd, agentDir, packageRoot := filepath.Join(root, "work"), filepath.Join(root, "agent"), filepath.Join(root, "pkg")
			for _, dir := range []string{cwd, agentDir, packageRoot} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(packageRoot, "package.json"), []byte(tc.prepare(t, packageRoot)), 0o644); err != nil {
				t.Fatal(err)
			}
			settings := codingagent.NewSettingsManager(cwd, agentDir)
			if err := settings.SetPackages([]codingagent.PackageSource{{Source: packageRoot}}); err != nil {
				t.Fatal(err)
			}
			projectSource, err := filepath.Rel(filepath.Join(cwd, ".pig"), packageRoot)
			if err != nil {
				t.Fatal(err)
			}
			if err := settings.SetProjectPackages([]codingagent.PackageSource{{Source: filepath.ToSlash(projectSource), Autoload: new(false), Prompts: []string{"-prompts/unused.md"}}}); err != nil {
				t.Fatal(err)
			}
			settings.Reload()
			err = startupPackageValidationError(cwd, settings)
			if err == nil {
				t.Fatal("project delta weakened strict Package validation")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("validation error=%v; want the rejection containing %q", err, want)
				}
			}
		})
	}
}

func TestRunConfigCommandLocalRequiresTrustedProject(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Chdir(cwd)
	_, stderr, code := captureStdoutStderr(t, func() int {
		return runConfigCommand([]string{"--local", "--no-approve"})
	})
	if code != 1 || !strings.Contains(stderr, "Project is not trusted. Use --approve to modify local resource config.") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

// TestRunConfigCommandFlagsMatchPi pins the `config` flags to Pi's handleConfigCommand (package-manager-cli.ts:790-870): --help prints the
// usage and exits 0 before any other argument is read, --approve lets --local proceed past the project-trust check that --no-approve fails,
// and an unknown option is named in the error.
func TestRunConfigCommandFlagsMatchPi(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Chdir(cwd)
	stdout, _, code := captureStdoutStderr(t, func() int { return runConfigCommand([]string{"--bogus", "--help"}) })
	if code != 0 || !strings.Contains(stdout, "-na, --no-approve") {
		t.Fatalf("--help: code=%d stdout=%q", code, stdout)
	}
	_, stderr, code := captureStdoutStderr(t, func() int { return runConfigCommand([]string{"--bogus"}) })
	if code != 1 || !strings.Contains(stderr, "Unknown option --bogus") {
		t.Fatalf("unknown option: code=%d stderr=%q", code, stderr)
	}
	_, denied, _ := captureStdoutStderr(t, func() int { return runConfigCommand([]string{"--local", "--no-approve"}) })
	_, approved, _ := captureStdoutStderr(t, func() int { return runConfigCommand([]string{"--local", "--approve"}) })
	if !strings.Contains(denied, "Project is not trusted. Use --approve to modify local resource config.") || strings.Contains(approved, "Project is not trusted") {
		t.Fatalf("--no-approve stderr=%q, --approve stderr=%q", denied, approved)
	}
}
