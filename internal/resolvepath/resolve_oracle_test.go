package resolvepath

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/utils/paths.ts

// normalizePath, resolvePath and normalizeWindowsShellPath against pinned Pi: tilde forms, file URLs (valid, invalid, percent sequences),
// absolute and relative paths against a plain or file-URL base, trimming, and the shell drive-path forms.
func TestResolveMatchesPi(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	base := filepath.Join(t.TempDir(), "base")
	inputs := []string{
		"", "~", "~/", "~//", "~/.", "~/./", "~/dir/", "~/file.txt", "~draft.md", "~user/x", "subdir/file.txt", "./a/../b", "/abs/path/x", "/abs/report%2026.md", "/abs/foo%2Fbar",
		"file:///abs/file%20with%20spaces.txt", "file:///%E0%A4%A", "file:///abs/x%2Fy", "file://host/share/x", "file:///c:/dir/x", "  spaced  ",
		"\uFEFF rel \uFEFF", "/c/Users/example/project", "/a/\u2028b", "/a/\u2029b", "/a/b\nc", "/\u017f/x", "/\u212a/x", "/MNT/c/x", "/cygdrive/d/work", "/mnt/e/source", "/c", "//server/share/file", `/c/Users\example`, "relative/file",
		"C:/Users/example", `C:\Users\example`, "..", "../x", "a//b///c",
	}
	// Strings assembled from the tokens that make these functions branch: tildes, schemes, percent escapes, drive and mount prefixes, separators, dots, Unicode spaces, BOM.
	pieces := []string{"~", "~", "/", "/", "\\", "//", ".", "..", "file:", "file://", "file:///", "%", "%20", "%2F", "%5C", "%E0%A4%A", "%zz", "%C3%A9", "c", "C:", "c:", "/c/", "/mnt/", "/cygdrive/", "d", "Users", "x y", " ", "  ", "\u00a0", "\u2003", "\u202f", "\u3000", "\ufeff", "\t", "\n", "é", "😀", "host", "localhost", "?", "#", "a b", "~user", "$HOME"}
	rng := rand.New(rand.NewSource(83))
	for range 2500 {
		var b strings.Builder
		for range 1 + rng.Intn(7) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		inputs = append(inputs, b.String())
	}
	type call struct {
		Input string  `json:"0"`
		Base  *string `json:"1"`
		Trim  bool    `json:"2"`
	}
	var rows [][3]any
	var cases []call
	for _, in := range inputs {
		for _, b := range []*string{nil, &base, new("file://" + base)} {
			for _, trim := range []bool{false, true} {
				var bv any
				if b != nil {
					bv = *b
				}
				rows = append(rows, [3]any{in, bv, trim})
				cases = append(cases, call{in, b, trim})
			}
		}
	}
	input, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/resolve_path.mjs", pigversion.UpstreamPackageVersion)
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
		Normalize result `json:"normalize"`
		Resolve   result `json:"resolve"`
		Shell     string `json:"shell"`
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
		want := expected[i]
		if got, err := Normalize(c.Input); (err != nil) != (want.Normalize.Err != nil) || (err == nil && (want.Normalize.Ok == nil || got != *want.Normalize.Ok)) {
			report("Normalize(%q) = %q, %v; Pi %s", c.Input, got, err, showResult(want.Normalize.Ok, want.Normalize.Err))
		}
		baseDir := ""
		if c.Base != nil {
			baseDir = *c.Base
		}
		var got string
		var err error
		if c.Trim {
			got, err = ResolveTrimmed(c.Input, baseDir)
		} else {
			got, err = Resolve(c.Input, baseDir)
		}
		if (err != nil) != (want.Resolve.Err != nil) || (err == nil && (want.Resolve.Ok == nil || got != *want.Resolve.Ok)) {
			report("Resolve(%q, %q, trim=%v) = %q, %v; Pi %s", c.Input, baseDir, c.Trim, got, err, showResult(want.Resolve.Ok, want.Resolve.Err))
		}
		if got := NormalizeWindowsShellPath(c.Input); got != want.Shell {
			report("NormalizeWindowsShellPath(%q) = %q, Pi %q", c.Input, got, want.Shell)
		}
	}
	if failures > 8 {
		t.Errorf("%d differences from Pi", failures)
	}
}

//go:fix inline

func showResult(ok, err *string) string {
	if err != nil {
		return "error " + *err
	}
	if ok == nil {
		return "<none>"
	}
	return fmt.Sprintf("%q", *ok)
}
