package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/utils/paths.ts

// isLocalPath, getCwdRelativePath, formatPathRelativeToCwdOrAbsolute and canonicalizePath against pinned Pi, over regular files, symlinks
// (also dangling), dot-prefixed names, parent traversals, absolute and tilde paths, file URLs and package-source prefixes.
func TestPathsMatchPi(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	cwd := filepath.Join(root, "work")
	must(os.MkdirAll(filepath.Join(cwd, "sub", "deep"), 0o755))
	must(os.MkdirAll(filepath.Join(cwd, "..config"), 0o755))
	must(os.WriteFile(filepath.Join(cwd, "a.txt"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(cwd, "..config", "AGENTS.md"), []byte("x"), 0o644))
	must(os.WriteFile(filepath.Join(root, "outside.txt"), []byte("x"), 0o644))
	must(os.Symlink(filepath.Join(cwd, "a.txt"), filepath.Join(cwd, "link.txt")))
	must(os.Symlink(filepath.Join(cwd, "sub"), filepath.Join(cwd, "dirlink")))
	must(os.Symlink(filepath.Join(cwd, "missing"), filepath.Join(cwd, "dangling")))
	must(os.Symlink(filepath.Join(root, "outside.txt"), filepath.Join(cwd, "escape")))
	values := []string{
		"", ".", "..", "a.txt", "./a.txt", "sub/../a.txt", "sub/deep", "sub/deep/../..", "../outside.txt", "../work/a.txt", "..config/AGENTS.md",
		"link.txt", "dirlink/deep", "dangling", "escape", "missing/file", "~", "~/x", "~draft.md", "file://" + filepath.Join(cwd, "a.txt"),
		"file://" + filepath.Join(cwd, "sub with space"), "file:///%E0%A4%A", cwd, filepath.Join(cwd, "a.txt"), filepath.Join(cwd, "..", "work", "sub"),
		filepath.Join(root, "outside.txt"), "/", "/etc", "  a.txt  ", "npm:pkg", "git:github.com/a/b", "github:a/b", "http://x", "https://x", "ssh://x", "builtin:x",
		" npm:pkg", "NPM:pkg", "./npm:pkg", "\u00a0npm:pkg", "file:foo", "relative/dir/",
	}
	cwds := []string{cwd, filepath.Join(cwd, "sub"), root, "/"}
	var cases [][2]string
	for _, c := range cwds {
		for _, v := range values {
			cases = append(cases, [2]string{v, c})
		}
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/paths_cwd.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	type result struct {
		Ok  *string `json:"ok"`
		Err *string `json:"err"`
	}
	var expected []struct {
		Local bool   `json:"local"`
		Rel   result `json:"rel"`
		Fmt   result `json:"fmt"`
		Canon string `json:"canon"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	report := func(format string, args ...any) {
		if failures++; failures <= 8 {
			t.Errorf(format, args...)
		}
	}
	for i, c := range cases {
		value, dir, want := c[0], c[1], expected[i]
		if got := IsLocalPath(value); got != want.Local {
			report("IsLocalPath(%q) = %v, Pi %v", value, got, want.Local)
		}
		if got := GetCwdRelativePath(value, dir); want.Rel.Err == nil {
			wantRel := ""
			if want.Rel.Ok != nil {
				wantRel = *want.Rel.Ok
			}
			if got != wantRel {
				report("GetCwdRelativePath(%q, %q) = %q, Pi %q (undefined = empty)", value, dir, got, wantRel)
			}
		}
		if want.Fmt.Err == nil && want.Fmt.Ok != nil {
			if got := FormatPathRelativeToCwdOrAbsolute(value, dir); got != *want.Fmt.Ok {
				report("FormatPathRelativeToCwdOrAbsolute(%q, %q) = %q, Pi %q", value, dir, got, *want.Fmt.Ok)
			}
		}
		// Node's realpathSync("") returns the working directory while CanonicalizePath("") keeps the empty input (canonical_path_relative_test.go pins
		// that choice); empty is excluded here and reported to the lead.
		if dir == cwd && value != "" {
			if got := CanonicalizePath(value); got != want.Canon {
				report("CanonicalizePath(%q) = %q, Pi %q", value, got, want.Canon)
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d differences from Pi over %d cases", failures, len(cases))
	}
}
