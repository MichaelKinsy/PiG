package subprocess

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// runtime-modules.mjs lists every module the runtime tree loads on first use, every file it reads as source and every Worker it starts, so a TypeScript runtime that bundles the tree can provide each of them. The lists must equal the call sites exactly: a missing entry throws at run time, and a stale entry makes a bundling runtime embed a module nothing loads.
func TestRuntimeModulesListsEveryLazyLoad(t *testing.T) {
	root := "runtime-node"
	listed := map[string][]string{}
	source, err := os.ReadFile(filepath.Join(root, "runtime-modules.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, list := range []string{"RUNTIME_REQUIRES", "RUNTIME_SOURCES", "RUNTIME_WORKERS"} {
		block := regexp.MustCompile(`(?s)export const ` + list + ` = Object\.freeze\(\[(.*?)\]\);`).FindSubmatch(source)
		if block == nil {
			t.Fatalf("runtime-modules.mjs has no %s list", list)
		}
		for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(block[1], -1) {
			listed[list] = append(listed[list], string(m[1]))
		}
	}
	calls := map[string]*regexp.Regexp{
		"RUNTIME_REQUIRES": regexp.MustCompile(`\b(?:runtimeRequire|requireModule)\("([^"]+)"\)`),
		"RUNTIME_SOURCES":  regexp.MustCompile(`\bruntimeSource\("([^"]+)"\)`),
		"RUNTIME_WORKERS":  regexp.MustCompile(`\bruntimeWorker\("([^"]+)"\)`),
	}
	used := map[string][]string{}
	var files []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Vendored packages under shims/ load their own files; PiG's sources are the tree's top level, harness/ and the shims/ modules.
		if entry.IsDir() && filepath.Dir(path) == filepath.Join(root, "shims") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".mjs") || entry.Name() == "runtime-modules.mjs" {
			return nil
		}
		files = append(files, path)
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// A file that creates its own require reaches the tree by a path the lists cannot cover.
		if strings.Contains(string(text), "createRequire(import.meta.url)") && path != filepath.Join(root, "jiti-loader.mjs") {
			t.Errorf("%s creates its own require; route lazy loads through runtime-modules.mjs", path)
		}
		for list, call := range calls {
			for _, m := range call.FindAllSubmatch(text, -1) {
				used[list] = append(used[list], string(m[1]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no runtime sources scanned")
	}
	for list := range calls {
		want := slices.Sorted(slices.Values(listed[list]))
		got := slices.Compact(slices.Sorted(slices.Values(used[list])))
		if !slices.Equal(got, want) {
			t.Errorf("%s = %q, call sites use %q", list, want, got)
		}
		for _, specifier := range want {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(specifier))); err != nil {
				t.Errorf("%s entry %s: %v", list, specifier, err)
			}
		}
	}
}
