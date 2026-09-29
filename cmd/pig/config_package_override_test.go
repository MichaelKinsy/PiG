package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// upstream: packages/coding-agent/src/core/settings-manager.ts:99-113
func TestPackageAutoloadJSONPresence(t *testing.T) {
	t.Parallel()
	for _, input := range []string{`"npm:pi-tools"`, `{"source":"npm:pi-tools"}`, `{"source":"npm:pi-tools","autoload":false}`, `{"source":"npm:pi-tools","autoload":true}`} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			var pkg codingagent.PackageSource
			require.NoError(t, json.Unmarshal([]byte(input), &pkg))
			encoded, err := json.Marshal(pkg)
			require.NoError(t, err)
			assert.JSONEq(t, input, string(encoded))
		})
	}
}

func BenchmarkProjectPackageOverridePersistence(b *testing.B) {
	root := b.TempDir()
	settings := codingagent.NewSettingsManager(filepath.Join(root, "project"), filepath.Join(root, "agent"))
	packages := make([]codingagent.PackageSource, 64)
	for i := range packages {
		packages[i] = codingagent.PackageSource{Source: fmt.Sprintf("npm:package-%d", i)}
	}
	if err := settings.SetProjectPackages(packages); err != nil {
		b.Fatal(err)
	}
	item := &tui.ResourceItem{Path: filepath.Join(root, "package", "prompts", "review.md"), ResourceType: tui.ResourcePrompts, Scope: "user", Origin: "package", Source: "npm:pi-tools", BaseDir: filepath.Join(root, "package")}
	b.ReportAllocs()
	for b.Loop() {
		for _, state := range []string{"unload", "load", "inherit"} {
			if err := applyProjectConfigOverride(settings.CWD(), settings, item, state); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// upstream: packages/coding-agent/src/modes/interactive/components/config-selector.ts:696-727,803-808
func TestProjectPackageOverridePreservesInstallAndSiblingFilters(t *testing.T) {
	for _, kind := range []tui.ResourceType{tui.ResourceExtensions, tui.ResourceSkills, tui.ResourcePrompts, tui.ResourceThemes} {
		for _, tc := range []struct {
			name, initial, afterInherit string
		}{
			{"new override", `[]`, `[]`},
			{"explicit install", `["npm:pi-tools"]`, `["npm:pi-tools"]`},
			{"explicit autoload", `[{"source":"npm:pi-tools","autoload":true}]`, `["npm:pi-tools"]`},
			{"sibling empty filter", `[{"source":"npm:pi-tools","autoload":false,"themes":[]}]`, `[{"source":"npm:pi-tools","autoload":false,"themes":[]}]`},
		} {
			// A sibling filter must use a different resource kind.
			if kind == tui.ResourceThemes && tc.name == "sibling empty filter" {
				tc.initial = `[{"source":"npm:pi-tools","autoload":false,"prompts":[]}]`
				tc.afterInherit = tc.initial
			}
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				f := newPackageCommandPathsFixture(t)
				writeStartupFixtureFile(t, filepath.Join(f.projectDir, ".pi", "settings.json"), `{"packages":`+tc.initial+`}`)
				settings := codingagent.NewSettingsManagerWithProjectTrust(f.projectDir, f.agentDir, true)
				root := filepath.Join(f.root, "package")
				item := &tui.ResourceItem{Path: filepath.Join(root, string(kind), "selected"), ResourceType: kind, Scope: "user", Origin: "package", Source: "npm:pi-tools", BaseDir: root}
				for _, state := range []string{"unload", "load", "inherit"} {
					require.NoError(t, applyProjectConfigOverride(f.projectDir, settings, item, state))
					settings.Reload()
					if state != "inherit" && tc.name == "new override" {
						pkg := settings.GetProjectSettings().Packages[0]
						assert.Equal(t, new(false), pkg.Autoload)
					}
				}
				encoded, err := json.Marshal(settings.GetProjectSettings().Packages)
				require.NoError(t, err)
				assert.JSONEq(t, tc.afterInherit, string(encoded))
			})
		}
	}
}
