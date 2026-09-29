package packagecontent

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi package-manager.ts:288-297 uses node:fs globSync, sorts matches and
// removes dot paths. Globstar matches zero or more directories, not one.
func TestPackageManifestGlobstar(t *testing.T) {
	root := t.TempDir()
	files := []string{"root.md", "a/one.md", "a/deep/two.md", ".hidden/no.md", "a/.dot.md", "node_modules/dep/dep.md"}
	for _, file := range files {
		writeTestFile(t, filepath.Join(root, file), "prompt\n")
	}
	tests := []struct {
		pattern string
		want    []string
	}{
		{"**", []string{".", "a", "a/deep", "a/deep/two.md", "a/one.md", "node_modules", "node_modules/dep", "node_modules/dep/dep.md", "root.md"}},
		{"**/*.md", []string{"a/deep/two.md", "a/one.md", "node_modules/dep/dep.md", "root.md"}},
		{"a/**/two.md", []string{"a/deep/two.md"}},
		{"a/**/one.md", []string{"a/one.md"}},
		{"a/**/**/two.md", []string{"a/deep/two.md"}},
		{"a/**two.md", nil},
		{"missing/**/*.md", nil},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got := expandPackageGlob(root, tt.pattern)
			var want []string
			for _, file := range tt.want {
				want = append(want, filepath.Join(root, file))
			}
			if !slices.Equal(got, want) {
				t.Fatalf("glob = %v; want %v", got, want)
			}
		})
	}
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"glob-package","pi":{"prompts":["a/**/**/two.md"]}}`)
	resources, err := ValidatePackage(root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(resources.PromptFiles, []string{filepath.Join(root, "a/deep/two.md")}) {
		t.Fatal(resources.PromptFiles)
	}
	_, missing, err := InspectConfigured(root, nil)
	if err != nil || len(missing) != 0 {
		t.Fatalf("inspection: %v, %v", missing, err)
	}
}

func TestPackageGlobstarSymlinks(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "real/deep/prompt.md"), "prompt\n")
	testenv.RequireDirectoryLink(t, filepath.Join(root, "real"), filepath.Join(root, "link"))
	testenv.RequireDirectoryLink(t, root, filepath.Join(root, "real/cycle"))
	for _, tt := range []struct {
		pattern string
		want    []string
	}{
		{"**/*.md", []string{"real/deep/prompt.md"}},
		{"link/**/*.md", []string{"link/deep/prompt.md"}},
		{"*/**/*.md", []string{"real/deep/prompt.md"}},
	} {
		t.Run(tt.pattern, func(t *testing.T) {
			got := expandPackageGlob(root, tt.pattern)
			var want []string
			for _, file := range tt.want {
				want = append(want, filepath.Join(root, file))
			}
			if !slices.Equal(got, want) {
				t.Fatalf("glob = %v; want %v", got, want)
			}
		})
	}
}

func TestPackageGlobstarOverrides(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"glob-package","pi":{"prompts":["**/*.md","!**/skip*.md","+a/deep/skip-keep.md"]}}`)
	for _, file := range []string{"root.md", "a/skip.md", "a/deep/skip.md", "a/deep/skip-keep.md"} {
		writeTestFile(t, filepath.Join(root, file), "prompt\n")
	}
	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "root.md"), filepath.Join(root, "a/deep/skip-keep.md")}
	if !slices.Equal(resources.PromptFiles, want) {
		t.Fatalf("prompts = %v; want %v", resources.PromptFiles, want)
	}
}

func TestSkillManifestOverridesUseSkillFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"skills","pi":{"skills":["suite","!**/excluded/SKILL.md","!suite"]}}`)
	writeTestFile(t, filepath.Join(root, "suite", "listed.md"), "---\nname: listed\ndescription: file skill\n---\n")
	writeTestFile(t, filepath.Join(root, "suite", "excluded", "SKILL.md"), "---\nname: excluded\ndescription: directory skill\n---\n")
	resources, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "suite", "listed.md")}
	if !slices.Equal(resources.SkillDirs, want) {
		t.Fatalf("skill overrides = %v; want %v", resources.SkillDirs, want)
	}
	listed := filepath.Join(root, "suite", "listed.md")
	excluded := filepath.Join(root, "suite", "excluded")
	for _, tt := range []struct {
		patterns []string
		want     []string
	}{
		{[]string{"-suite"}, []string{listed, excluded}},
		{[]string{"-suite/excluded/SKILL.md"}, []string{listed}},
		{[]string{"!**", "+suite/excluded/SKILL.md"}, []string{excluded}},
	} {
		got := ApplyPatterns([]string{listed, excluded}, tt.patterns, root, Skills)
		if !slices.Equal(got, tt.want) {
			t.Fatalf("exact overrides %v = %v; want %v", tt.patterns, got, tt.want)
		}
	}
}

func BenchmarkPackageGlobstarStartup(b *testing.B) {
	for _, count := range []int{0, 8, 512} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := b.TempDir()
			if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"benchmark","pi":{"prompts":["prompts/**/*.md"]}}`), 0o600); err != nil {
				b.Fatal(err)
			}
			for i := range count {
				file := filepath.Join(root, "prompts", fmt.Sprint(i%8), "nested", fmt.Sprintf("prompt-%d.md", i))
				if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("A prompt.\n"), 0o600); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				resources, _, _, err := ValidateConfiguredForStartup(root, nil)
				if err != nil || len(resources.PromptFiles) != count {
					b.Fatalf("discovered %d/%d prompts: %v", len(resources.PromptFiles), count, err)
				}
			}
		})
	}
}

// Pi skills.ts:436-453 keeps both paths until loadSkills selects the first
// name and emits a collision diagnostic for the second.
func TestConfiguredPackageAllowsDuplicateSkillNames(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{"name":"skills","pi":{"skills":["second/SKILL.md","first/SKILL.md"]}}`)
	for _, dir := range []string{"first", "second"} {
		writeTestFile(t, filepath.Join(root, dir, "SKILL.md"), "---\nname: same\ndescription: "+dir+"\n---\n")
	}
	resources, _, _, err := ValidateConfiguredForStartup(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "second/SKILL.md"), filepath.Join(root, "first/SKILL.md")}
	if !slices.Equal(resources.SkillDirs, want) {
		t.Fatalf("skill inputs = %v; want %v", resources.SkillDirs, want)
	}
}
