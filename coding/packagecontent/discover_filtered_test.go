package packagecontent

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi collectPackageResources (package-manager.ts:2214-2358): a bare source string reads a "pi" block's declared kinds only; a settings object reads a kind with patterns from the manifest entries or, when there are none, from the conventional directory, and a kind without patterns from the manifest entries or, when the kind is not declared, from the conventional directory. A manifest field that is not an array of strings is as absent (pi-manifest.ts:16-33).
func TestDiscoverFilteredFallsBackToConventionalDirectories(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		filters  map[Kind][]string
		filtered bool
		want     int
	}{
		{"string source loads only declared kinds", `{"pi":{"skills":["skills"]}}`, nil, false, 0},
		{"object without patterns falls back for an undeclared kind", `{"pi":{"skills":["skills"]}}`, map[Kind][]string{}, true, 1},
		{"object without patterns keeps an empty declaration empty", `{"pi":{"prompts":[]}}`, map[Kind][]string{}, true, 0},
		{"object with patterns falls back for an empty declaration", `{"pi":{"prompts":[]}}`, map[Kind][]string{Prompts: {"!x"}}, true, 1},
		{"object with patterns falls back for an undeclared kind", `{"pi":{"skills":["skills"]}}`, map[Kind][]string{Prompts: {"!x"}}, true, 1},
		{"malformed field is absent", `{"pi":{"prompts":"p.md"}}`, map[Kind][]string{}, true, 1},
		{"declared entries win over the directory", `{"pi":{"prompts":["other"]}}`, map[Kind][]string{}, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range map[string]string{"package.json": tc.manifest, "prompts/p.md": "---\ndescription: d\n---\nbody"} {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var got Resources
			var err error
			if tc.filtered {
				got, err = DiscoverFiltered(root, tc.filters)
			} else {
				got, err = Discover(root)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got.PromptFiles) != tc.want {
				t.Fatalf("prompt files = %v, want %d", got.PromptFiles, tc.want)
			}
		})
	}
}
