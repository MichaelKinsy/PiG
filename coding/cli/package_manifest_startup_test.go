package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's pi-manifest.ts:16-33 silently drops invalid fields and falls back to
// conventional directories for invalid JSON. package-manager.ts:2153-2202
// keeps valid siblings and the other Packages; vendor manifests are not read.
func TestRPCStartupMalformedPackageManifests(t *testing.T) {
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	fixtures := "../../test/parity/scenarios/extensions-runtime/testdata/package-manifests/agent"
	entries, err := os.ReadDir(filepath.Join(fixtures, "packages"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			home := t.TempDir()
			agentDir := filepath.Join(home, "agent")
			if err := os.CopyFS(agentDir, os.DirFS(fixtures)); err != nil {
				t.Fatal(err)
			}
			cwd := filepath.Join(home, "cwd")
			if err := os.Mkdir(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			settings := map[string]any{
				"packages":            []string{"./packages/" + entry.Name(), "./packages/context-mode"},
				"extensions":          []string{"./loose.ts"},
				"defaultProjectTrust": "always",
			}
			if entry.Name() == "context-mode" {
				settings["packages"] = []string{"./packages/context-mode"}
			}
			data, err := json.Marshal(settings)
			if err != nil {
				t.Fatal(err)
			}
			writeStartupFixtureFile(t, filepath.Join(agentDir, "settings.json"), string(data))
			result := runRPCStartup(t, binary, home, agentDir, cwd)
			if result.err != nil || result.stderr != "" {
				t.Fatalf("startup = %v, diagnostics = %q; Pi starts without a manifest diagnostic", result.err, result.stderr)
			}
			commands := []string{"loose-probe", "context-mode-probe"}
			if entry.Name() == "context-mode" || entry.Name() == "bad-prompts-type" {
				commands = append(commands, "skill:"+entry.Name()+"-skill")
			} else {
				commands = append(commands, entry.Name()+"-prompt")
			}
			for _, command := range commands {
				if !slices.Contains(result.commands, command) {
					t.Errorf("missing %s: %v", command, result.commands)
				}
			}
		})
	}
}

// Ports packages/coding-agent/test/suite/regressions/7187-malformed-package-manifest.test.ts.
func TestMalformedPackageManifestUpstream7187(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	packageDir := filepath.Join(agentDir, "npm", "node_modules", "bad-package")
	skillPath := filepath.Join(packageDir, "skills", "bad", "SKILL.md")
	promptPath := filepath.Join(packageDir, "prompts", "valid.md")
	writeStartupFixtureFile(t, skillPath, "---\nname: bad\ndescription: Must not load\n---\n")
	writeStartupFixtureFile(t, promptPath, "Valid prompt\n")
	writeStartupFixtureFile(t, filepath.Join(packageDir, "package.json"), `{"name":"bad-package","version":"1.0.0","pi":{"skills":"./skills","prompts":["./prompts"]}}`)
	sm := codingagent.NewSettingsManager(root, agentDir)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: "npm:bad-package"}}); err != nil {
		t.Fatal(err)
	}
	if err := validateConfiguredPackagesForStartup(root, sm, allScopesLoadExtensions); err != nil {
		t.Fatal(err)
	}
	for _, skill := range collectPackageSkillPaths(root, sm, nil) {
		if packagecontent.SkillFile(skill) == skillPath {
			t.Fatal("invalid skills field loaded its conventional skill")
		}
	}
	if prompts := collectPackagePromptPaths(root, sm); !slices.Contains(prompts, promptPath) {
		t.Fatalf("valid prompt lost: %v", prompts)
	}
}

func TestConfiguredPackageManifestDiscoveryMatchesStartup(t *testing.T) {
	fixtures := "../../test/parity/scenarios/extensions-runtime/testdata/package-manifests/agent"
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.CopyFS(agentDir, os.DirFS(fixtures)); err != nil {
		t.Fatal(err)
	}
	sm := codingagent.NewSettingsManager(home, agentDir)
	if err := validateConfiguredPackagesForStartup(home, sm, allScopesLoadExtensions); err != nil {
		t.Fatalf("startup preflight disagrees with discovery: %v", err)
	}
	prompts := collectPackagePromptPaths(home, sm)
	for _, name := range []string{"bad-json", "bad-extensions-type", "bad-skills-type", "bad-themes-type", "bad-missing", "vendor-claude", "vendor-cursor", "vendor-plugin", "context-mode-no-pi"} {
		want := filepath.Join(agentDir, "packages", name, "prompts", name+"-prompt.md")
		if !slices.Contains(prompts, want) {
			t.Errorf("missing valid prompt %s: %v", want, prompts)
		}
	}
	suppressed := filepath.Join(agentDir, "packages", "bad-prompts-type", "prompts", "bad-prompts-type-prompt.md")
	if slices.Contains(prompts, suppressed) {
		t.Errorf("malformed field must not enable its conventional directory: %v", prompts)
	}
}
