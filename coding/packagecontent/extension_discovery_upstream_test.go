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
	for _, marker := range []string{"go.mod", "go.work", "Cargo.toml"} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, filepath.Join(root, marker), "")
			if got := DiscoverAutomatic(root, Extensions); !slices.Equal(got, []string{root}) {
				t.Fatalf("native entries=%q, want root %q", got, root)
			}
		})
	}
}

// A go.work-only directory is one extension when it selects modules elsewhere, and a development workspace when its children are extensions. The workspace case yielded each child before go.work was a build marker and must keep doing so.
func TestGoWorkspaceDirectoryDiscovery(t *testing.T) {
	t.Run("workspace over child modules", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "go.work"), "go 1.26\n\nuse (\n\t./a\n\t./b\n)\n")
		for _, name := range []string{"a", "b"} {
			writeTestFile(t, filepath.Join(root, name, "go.mod"), "module example.com/"+name+"\n\ngo 1.26\n")
		}
		want := []string{filepath.Join(root, "a"), filepath.Join(root, "b")}
		if got := DiscoverAutomatic(root, Extensions); !slices.Equal(got, want) {
			t.Fatalf("entries=%q, want each child module %q", got, want)
		}
	})
	t.Run("workspace beside Node entries", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "go.work"), "go 1.26\n")
		writeTestFile(t, filepath.Join(root, "tool.ts"), "export default function() {}")
		if got, want := DiscoverAutomatic(root, Extensions), []string{filepath.Join(root, "tool.ts")}; !slices.Equal(got, want) {
			t.Fatalf("entries=%q, want %q", got, want)
		}
	})
	t.Run("child selector workspace", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "named", "go.work"), "go 1.26\n\nuse ../../elsewhere\n")
		if got, want := DiscoverAutomatic(root, Extensions), []string{filepath.Join(root, "named")}; !slices.Equal(got, want) {
			t.Fatalf("entries=%q, want %q", got, want)
		}
	})
	t.Run("child development workspace", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, filepath.Join(root, "group", "go.work"), "go 1.26\n\nuse ./a\n")
		writeTestFile(t, filepath.Join(root, "group", "a", "go.mod"), "module example.com/a\n\ngo 1.26\n")
		if got := DiscoverAutomatic(root, Extensions); len(got) != 0 {
			t.Fatalf("entries=%q, want none: discovery reads one level, as for any directory without an entry marker", got)
		}
	})
}
