package tui

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// getLanguageFromPath (theme.ts) against pinned Pi over every extension in Pig's table (plus its case variants and the ones Pi might map but Pig does not) and seeded paths: extensions after the last dot of the whole path, mixed case, no dot, a trailing dot, dotfiles, directories with dots, wide and special-casing characters (U+0130, U+212A).
func TestLanguageFromPathMatchesPi(t *testing.T) {
	extensions := []string{"", "x", "TS", "Py", "jsX", "d.ts", "tar.gz", "min.js", "ɪ", "İ", "K", "\u212a", "tsx\u0130", "日本語", " ", "ts ", "MAKEFILE", "Dockerfile", "dockerfile", "cmake", "graphql", "proto", "vue", "svelte", "astro", "zig", "nim", "dart", "jl", "tf", "hcl", "ini", "conf", "env", "lock", "csv", "txt", "log", "diff", "patch", "gradle", "groovy", "kts", "mm", "m", "vb", "fs", "fsx", "pas", "asm", "s", "wasm", "wat", "nix", "bzl", "bazel", "sol", "v", "sv", "vhd", "tex", "bib", "rst", "adoc", "org"}
	for ext := range extToLang() {
		extensions = append(extensions, ext, strings.ToUpper(ext))
	}
	rng := rand.New(rand.NewPCG(5, 41))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	prefixes := []string{"", "file", "src/main", "a.b/c", "/abs/path/to/file", ".hidden", "dir.with.dots/name", "README", "Makefile", "x/y/z.", "..", ".", "日本語/ファイル", "name with spaces", "a\\b\\c"}
	var paths []string
	for _, ext := range extensions {
		paths = append(paths, "file."+ext, ext, "."+ext)
	}
	for range 2500 {
		path := pick(prefixes)
		if rng.IntN(5) > 0 {
			path += "." + pick(extensions)
		}
		if rng.IntN(6) == 0 {
			path += "." + pick(extensions)
		}
		paths = append(paths, path)
	}
	paths = slices.Compact(paths)
	input, err := json.Marshal(paths)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/language_from_path.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []*string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, path := range paths {
		got := LanguageFromPath(path)
		var want string
		if expected[i] != nil {
			want = *expected[i]
		}
		if got == want {
			continue
		}
		if failures++; failures <= 8 {
			t.Errorf("LanguageFromPath(%q) = %q, Pi %q", path, got, want)
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d paths differ from Pi", failures, len(paths))
	}
	_ = reflect.DeepEqual
}
