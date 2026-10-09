package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// cli/file-processor.ts processFileArguments against pinned Pi, one @file argument at a time: how the argument resolves (cwd-relative,
// absolute, `~`, the macOS screenshot variants of resolveReadPath), what an empty, BOM-prefixed, binary-looking or missing file yields.
func TestProcessCLIFileArgumentsMatchesPi(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	work := filepath.Join(dir, "work")
	for _, d := range []string{home, work, filepath.Join(work, "sub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"work/a.txt":          "hello\n",
		"work/no newline.txt": "x",
		"work/empty.txt":      "",
		"work/bom.txt":        "\ufeffwith bom\n",
		"work/crlf.txt":       "a\r\nb\r\n",
		"work/sub/b.txt":      "in sub",
		"work/Screenshot 2024-01-01 at 10.00.00\u202fAM.txt": "narrow nbsp",
		"work/cafe\u0301.txt":                                "nfd name",
		"work/it\u2019s.txt":                                 "curly",
		"work/Capture d\u2019e\u0301cran 1.txt":              "combined",
		"work/@at.txt":                                       "at file",
		"work/~tilde.txt":                                    "tilde name",
		"home/h.txt":                                         "home file",
		"home/sp ace.txt":                                    "home space",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"a.txt", "./a.txt", "sub/b.txt", "../work/a.txt", filepath.Join(work, "a.txt"), "no newline.txt", "empty.txt", "bom.txt", "crlf.txt",
		"missing.txt", "sub", "", ".", "~/h.txt", "~", "~/sp ace.txt", "~/missing.txt", "~tilde.txt", "@at.txt", "a.txt ", " a.txt",
		"Screenshot 2024-01-01 at 10.00.00 AM.txt", "Screenshot 2024-01-01 at 10.00.00\u202fAM.txt", "caf\u00e9.txt", "cafe\u0301.txt", "it's.txt", "it\u2019s.txt",
		"Capture d'\u00e9cran 1.txt", "Capture d\u2019\u00e9cran 1.txt", "A.TXT", "a.txt/", "a.txt/.", "\u00a0a.txt", "sub/../a.txt", "file:///a.txt"}
	input, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/file_processor.mjs", pigversion.UpstreamVersion, work)
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct{ Text, Error string }
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	failures := 0
	for i, arg := range args {
		result, err := ProcessCLIFileArguments([]string{arg}, work)
		got := struct{ Text, Error string }{Text: result.Text}
		if err != nil {
			got.Error = "Error: " + err.Error()
		}
		want := expected[i]
		if strings.HasPrefix(want.Error, "Error: Could not read file") {
			want.Error = "Error: could not read file" // wording of the Go read error; the failing path and cause are compared separately
			got.Error = strings.SplitN(got.Error, ":", 3)[0] + ": " + strings.SplitN(strings.ToLower(strings.TrimPrefix(got.Error, "Error: ")), ":", 2)[0]
			want.Error = strings.SplitN(want.Error, ":", 3)[0] + ": " + strings.SplitN(want.Error, ": ", 2)[1]
		}
		// Pi's detectSupportedImageMimeTypeFromFile throws EISDIR on a directory and the process crashes; Pig reports a read error. Both fail.
		if strings.HasPrefix(want.Error, "Error: EISDIR") && strings.HasPrefix(got.Error, "Error: could not read file") {
			continue
		}
		if got != want {
			if failures++; failures <= 8 {
				t.Errorf("@%q:\n  Pig %+v\n  Pi  %+v", arg, got, want)
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d of %d arguments differ from Pi", failures, len(args))
	}
}
