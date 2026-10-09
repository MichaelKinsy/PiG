package resolvepath

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
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
		"", "~", "~/file.txt", "~draft.md", "~user/x", "subdir/file.txt", "./a/../b", "/abs/path/x", "/abs/report%2026.md", "/abs/foo%2Fbar",
		"file:///abs/file%20with%20spaces.txt", "file:///%E0%A4%A", "file:///abs/x%2Fy", "file://host/share/x", "file:///c:/dir/x", "  spaced  ",
		"\uFEFF rel \uFEFF", "/c/Users/example/project", "/cygdrive/d/work", "/mnt/e/source", "/c", "//server/share/file", `/c/Users\example`, "relative/file",
		"C:/Users/example", `C:\Users\example`, "..", "../x", "a//b///c",
	}
	type call struct {
		Input string  `json:"0"`
		Base  *string `json:"1"`
		Trim  bool    `json:"2"`
	}
	var rows [][3]any
	var cases []call
	for _, in := range inputs {
		for _, b := range []*string{nil, &base, ptr("file://" + base)} {
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
	cmd := exec.CommandContext(t.Context(), "node", "testdata/resolve_path.mjs", pigversion.UpstreamVersion)
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
			report("Normalize(%q) = %q, %v; Pi %+v", c.Input, got, err, want.Normalize)
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
			report("Resolve(%q, %q, trim=%v) = %q, %v; Pi %+v", c.Input, baseDir, c.Trim, got, err, want.Resolve)
		}
		if got := NormalizeWindowsShellPath(c.Input); got != want.Shell {
			report("NormalizeWindowsShellPath(%q) = %q, Pi %q", c.Input, got, want.Shell)
		}
	}
	if failures > 8 {
		t.Errorf("%d differences from Pi", failures)
	}
}

func ptr(s string) *string { return &s }
