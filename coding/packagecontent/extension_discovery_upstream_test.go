package packagecontent

import (
	"path/filepath"
	"slices"
	"testing"
)

// Pi package-manager.ts:557-638 recognizes only manifest entries and index.ts/index.js as Node directory entries. Other files remain independent extensions.
func TestNodeConventionalExtensionDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, metadata string
		files, want    []string
	}{
		{"main TypeScript keeps siblings", "", []string{"main.ts", "other.ts"}, []string{"main.ts", "other.ts"}},
		{"main JavaScript keeps siblings", "", []string{"main.js", "other.js"}, []string{"main.js", "other.js"}},
		{"extension names keep siblings", "", []string{"extension.ts", "extension.js", "other.ts"}, []string{"extension.js", "extension.ts", "other.ts"}},
		{"plain package metadata keeps siblings", `{"name":"extensions"}`, []string{"first.ts", "other.ts"}, []string{"first.ts", "other.ts"}},
		{"subdirectory main is not an entry", "", []string{"nested/main.ts", "other.ts"}, []string{"other.ts"}},
		{"index still selects one entry", "", []string{"index.ts", "other.ts"}, []string{"index.ts"}},
		{"manifest still selects explicit entries", `{"pi":{"extensions":["other.ts"]}}`, []string{"main.ts", "other.ts"}, []string{"other.ts"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.metadata != "" {
				writeTestFile(t, filepath.Join(root, "package.json"), tc.metadata)
			}
			for _, file := range tc.files {
				writeTestFile(t, filepath.Join(root, file), "export default function() {}")
			}
			want := make([]string, len(tc.want))
			for i, file := range tc.want {
				want[i] = filepath.Join(root, file)
			}
			if got := DiscoverAutomatic(root, Extensions); !slices.Equal(got, want) {
				t.Fatalf("entries=%q, want %q", got, want)
			}
		})
	}
}

func TestNativeBuildDirectoriesRemainExtensionEntries(t *testing.T) {
	for _, marker := range []string{"go.mod", "Cargo.toml"} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, marker), "")
			if got := DiscoverAutomatic(root, Extensions); !slices.Equal(got, []string{root}) {
				t.Fatalf("native entries=%q, want root %q", got, root)
			}
		})
	}
}
