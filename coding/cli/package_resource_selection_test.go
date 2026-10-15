package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

func TestPackageResourceDeltaPreservesPatternInsertionOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	paths := []string{filepath.Join(root, "extensions/a.ts"), filepath.Join(root, "extensions/b.ts"), filepath.Join(root, "extensions/c.ts")}
	for _, path := range paths {
		writePackageResource(t, path, "export default function() {}")
	}
	cwd, agent := t.TempDir(), t.TempDir()
	settings := codingagent.NewSettingsManager(cwd, agent)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: root}}); err != nil {
		t.Fatal(err)
	}
	if err := settings.SetProjectPackages([]codingagent.PackageSource{{Source: root, Autoload: new(false), Extensions: []string{"+extensions/b.ts", "!extensions/a.ts", "-extensions/b.ts"}}}); err != nil {
		t.Fatal(err)
	}
	items, err := packagemanager.CollectResolvedPackageResourceItems(cwd, settings.AgentDir(), settings, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, item := range items {
		got = append(got, fmt.Sprintf("%s/%s/%t", filepath.Base(item.Path), item.Scope, item.Enabled))
	}
	// Pi applyAutoloadDisabledPatterns uses Map.set: changing b's state does not move it behind a, and c retains inherited metadata.
	want := []string{"b.ts/project/false", "a.ts/project/false", "c.ts/user/true"}
	if !slices.Equal(got, want) {
		t.Fatalf("delta resources=%v, want %v", got, want)
	}
}

func BenchmarkConfiguredPackageResources(b *testing.B) {
	cwd, agent, root := b.TempDir(), b.TempDir(), b.TempDir()
	b.Setenv("HOME", b.TempDir())
	b.Setenv("PIG_HOME", b.TempDir())
	const members = 32
	for i := range members {
		for _, kind := range []string{"extensions", "prompts", "skills"} {
			name, content := fmt.Sprintf("%s/%02d.md", kind, i), "prompt"
			switch kind {
			case "extensions":
				name, content = fmt.Sprintf("extensions/%02d.ts", i), "export default function() {}"
			case "skills":
				name, content = fmt.Sprintf("skills/%02d/SKILL.md", i), fmt.Sprintf("---\nname: skill-%02d\ndescription: benchmark\n---\n", i)
			}
			path := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	sm := codingagent.NewSettingsManager(cwd, agent)
	if err := sm.SetPackages([]codingagent.PackageSource{{Source: root}}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		items, err := collectConfigResourceItems(cwd, agent, sm)
		if err != nil {
			b.Fatal(err)
		}
		packageItems := 0
		for _, item := range items {
			if item.Origin == "package" {
				packageItems++
			}
		}
		if packageItems != members*3 {
			b.Fatalf("Package resources=%d, want %d", packageItems, members*3)
		}
	}
}
