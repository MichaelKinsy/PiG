package main

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

func TestUnavailableNpmPackagesUseInheritedVersionForDeltas(t *testing.T) {
	// package-manager.ts:1259-1262 resolves project autoload deltas against the inherited user's source and install path, while retaining project metadata.
	for _, tc := range []struct {
		name, user, project string
		available           bool
	}{
		{"user-version-satisfied", "npm:example@1.0.0", "npm:example@2.0.0", true},
		{"user-version-unsatisfied", "npm:example@2.0.0", "npm:example@1.0.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, settings, _ := installedVersionFixture(t, tc.user, `{"name":"example","version":"1.0.0"}`, false)
			path := filepath.Join(settings.AgentDir(), "npm", "node_modules", "example", "prompts", "example.md")
			writeNpmTrustFile(t, path, []byte("prompt"))
			if err := settings.SetProjectPackages([]codingagent.PackageSource{{Source: tc.project, Autoload: new(false), Prompts: []string{"+prompts/example.md"}}}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PI_OFFLINE", "1")
			packagemanager.EnsureConfiguredPackagesInstalled(cwd, settings.AgentDir(), settings)
			items, err := collectConfigResourceItems(cwd, settings.AgentDir(), settings)
			if err != nil {
				t.Fatal(err)
			}
			if tc.available {
				if len(items) != 1 || items[0].Path != path || items[0].Scope != "project" || items[0].Source != tc.project || !items[0].Enabled {
					t.Fatalf("inherited resource identity = %+v", items)
				}
			} else if len(items) != 0 {
				t.Fatalf("unsatisfied inherited source exposed resources: %+v", items)
			}
		})
	}
}

func TestUnavailableNpmPackagesDoNotProvideResources(t *testing.T) {
	// packages/coding-agent/src/core/package-manager.ts:1286-1296: failed/skipped installation continues before collectPackageResources, even if the old directory still exists.
	for _, local := range []bool{true, false} {
		for _, tc := range []struct {
			name, source, manifest string
			offline, available     bool
		}{
			{"matching-offline", "npm:example@1.0.0", `{"name":"example","version":"1.0.0"}`, true, true},
			{"pin-mismatch-offline", "npm:example@2.0.0", `{"name":"example","version":"1.0.0"}`, true, false},
			{"range-mismatch-offline", "npm:example@^2.0.0", `{"name":"example","version":"1.0.0"}`, true, false},
			{"missing-manifest-offline", "npm:example", "", true, false},
			{"pin-mismatch-install-failed", "npm:example@2.0.0", `{"name":"example","version":"1.0.0"}`, false, false},
		} {
			scope := "user/"
			if local {
				scope = "project/"
			}
			t.Run(scope+tc.name, func(t *testing.T) {
				cwd, settings, _ := installedVersionFixture(t, tc.source, tc.manifest, local)
				base := settings.AgentDir()
				if local {
					base = filepath.Join(cwd, codingagent.CONFIG_DIR_NAME)
				}
				root := filepath.Join(base, "npm", "node_modules", "example")
				writeNpmTrustFile(t, filepath.Join(root, "prompts", "example.md"), []byte("prompt"))
				writeNpmTrustFile(t, filepath.Join(root, "themes", "example.json"), []byte(`{"name":"example"}`))
				writeNpmTrustFile(t, filepath.Join(root, "skills", "example", "SKILL.md"), []byte("---\nname: example\ndescription: Example\n---\nskill"))
				writeNpmTrustFile(t, filepath.Join(root, "extensions", "example.ts"), []byte("export default function() {}"))
				if tc.offline {
					t.Setenv("PI_OFFLINE", "1")
				} else {
					t.Setenv("PIG_NPM_TEST_FAIL", "1")
				}
				_, missing := packagemanager.EnsureConfiguredPackagesInstalled(cwd, settings.AgentDir(), settings)
				if (len(missing) == 0) != tc.available {
					t.Fatalf("missing=%v, available=%v", missing, tc.available)
				}
				// Physical install reporting remains separate from resolution eligibility.
				packages := listConfiguredPackages(cwd, settings)
				if len(packages) != 1 || packages[0].InstalledPath != root {
					t.Fatalf("physical installation hidden: %+v", packages)
				}
				counts := map[string]int{
					"prompts":    len(collectPackagePromptPaths(cwd, settings)),
					"themes":     len(collectPackageThemePaths(cwd, settings)),
					"skills":     len(collectPackageSkillPaths(cwd, settings, nil)),
					"extensions": len(collectPackageExtensionConfigs(cwd, settings, nil)),
				}
				if !local {
					counts["startup-themes"] = len(collectStartupThemePaths(cwd, settings.AgentDir(), settings))
				}
				want := 0
				if tc.available {
					want = 1 // The fixture declares one resource of each kind.
				}
				for kind, count := range counts {
					if count != want {
						t.Errorf("resolved %s=%d, want %d", kind, count, want)
					}
				}
				if !tc.available {
					configs, diagnostics, err := authExtensionConfigs(cwd, settings.AgentDir(), settings)
					if err != nil || len(configs) != 0 || len(diagnostics) != 0 {
						t.Errorf("unavailable auth resources: configs=%v diagnostics=%v error=%v", configs, diagnostics, err)
					}
					if err := validateConfiguredPackagesForStartup(cwd, settings, func(string) bool { return true }); err != nil {
						t.Errorf("unavailable package was validated: %v", err)
					}
					if infos := resourceSourceInfoProvider(cwd, settings.AgentDir(), settings, CLIFlags{})(); len(infos) != 0 {
						t.Errorf("unavailable resource metadata: %v", infos)
					}
				}
			})
		}
	}
}
