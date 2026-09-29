package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestPackageSettingsSourceNormalizationUpstream(t *testing.T) {
	for _, local := range []bool{false, true} {
		// packages/coding-agent/test/package-manager.test.ts:1305,1319.
		name := "should store global local packages relative to agent settings base"
		if local {
			name = "should store project local packages relative to .pi settings base"
		}
		t.Run(name, func(t *testing.T) {
			cwd := t.TempDir()
			agentDir := filepath.Join(cwd, "agent")
			pkgRel := "packages/local-global-pkg"
			if local {
				pkgRel = "project-local-pkg"
			}
			pkg := filepath.Join(cwd, filepath.FromSlash(pkgRel))
			writePackageResource(t, filepath.Join(pkg, "extensions", "index.ts"), "export default function() {}")
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			if added, err := addSourceToSettings(cwd, sm, "./"+pkgRel, local); err != nil || !added {
				t.Fatalf("added=%t error=%v", added, err)
			}
			base, settings := agentDir, sm.GetGlobalSettings()
			if local {
				base, settings = codingagent.ProjectConfigDir(cwd), sm.GetProjectSettings()
			}
			expected, err := filepath.Rel(base, pkg)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(expected, ".") {
				expected = "./" + expected
			}
			if !reflect.DeepEqual(settings.Packages, []codingagent.PackageSource{{Source: expected}}) {
				t.Fatalf("packages=%+v, want %s", settings.Packages, expected)
			}
		})
	}
	// packages/coding-agent/test/package-manager.test.ts:1333.
	t.Run("should remove local package entries using equivalent path forms", func(t *testing.T) {
		cwd := t.TempDir()
		sm := codingagent.NewSettingsManager(cwd, filepath.Join(cwd, "agent"))
		pkg := filepath.Join(cwd, "remove-local-pkg")
		writePackageResource(t, filepath.Join(pkg, "extensions", "index.ts"), "export default function() {}")
		if _, err := addSourceToSettings(cwd, sm, "./remove-local-pkg", false); err != nil {
			t.Fatal(err)
		}
		removed, err := removeSourceFromSettings(cwd, sm, pkg+"/", false)
		if err != nil || !removed || len(sm.GetGlobalSettings().Packages) != 0 {
			t.Fatalf("removed=%t err=%v packages=%+v", removed, err, sm.GetGlobalSettings().Packages)
		}
	})
	for _, tc := range []struct {
		name, source string
		changed      bool
	}{
		// packages/coding-agent/test/package-manager.test.ts:1344.
		{"should return false when adding the same git source with the same ref", "git:github.com/user/repo@v1", false},
		// packages/coding-agent/test/package-manager.test.ts:1353.
		{"should update the ref when adding the same git source with a different ref", "git:github.com/user/repo@v2", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			sm := codingagent.NewSettingsManager(cwd, t.TempDir())
			if added, err := addSourceToSettings(cwd, sm, "git:github.com/user/repo@v1", false); err != nil || !added {
				t.Fatalf("first add=%t error=%v", added, err)
			}
			if changed, err := addSourceToSettings(cwd, sm, tc.source, false); err != nil || changed != tc.changed {
				t.Fatalf("changed=%t error=%v, want %t", changed, err, tc.changed)
			}
			want := []codingagent.PackageSource{{Source: tc.source}}
			if got := sm.GetGlobalSettings().Packages; !reflect.DeepEqual(got, want) {
				t.Fatalf("packages=%+v, want %+v", got, want)
			}
		})
	}
	// packages/coding-agent/test/package-manager.test.ts:1361.
	t.Run("should preserve package filters when replacing a package source ref", func(t *testing.T) {
		cwd := t.TempDir()
		sm := codingagent.NewSettingsManager(cwd, t.TempDir())
		var initial []codingagent.PackageSource
		if err := json.Unmarshal([]byte(`[{"source":"git:github.com/user/repo@v1","extensions":["extensions/main.ts"],"skills":[],"prompts":["prompts/review.md"],"themes":["themes/dark.json"]}]`), &initial); err != nil {
			t.Fatal(err)
		}
		if err := sm.SetPackages(initial); err != nil {
			t.Fatal(err)
		}
		if updated, err := addSourceToSettings(cwd, sm, "git:github.com/user/repo@v2", false); err != nil || !updated {
			t.Fatalf("updated=%t error=%v", updated, err)
		}
		want := []codingagent.PackageSource{{Source: "git:github.com/user/repo@v2", Extensions: []string{"extensions/main.ts"}, Skills: []string{}, Prompts: []string{"prompts/review.md"}, Themes: []string{"themes/dark.json"}, WasObject: true}}
		if got := sm.GetGlobalSettings().Packages; !reflect.DeepEqual(got, want) {
			t.Fatalf("packages=%+v, want %+v", got, want)
		}
	})
}
