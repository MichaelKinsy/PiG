package extensionconformance

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pythonTextWrite matches an open() call whose mode writes text: "w", "a" or "x" (with an optional "+"), not a binary mode. The mode is the
// second positional argument or the mode= keyword, in single or double quotes.
var pythonTextWrite = regexp.MustCompile(`\bopen\(.*(?:,\s*|\bmode\s*=\s*)["'][wax]\+?["']`)

// pythonNoTranslation matches the newline="" (or newline=”) argument that turns the translation off.
var pythonNoTranslation = regexp.MustCompile(`newline\s*=\s*(?:""|'')`)

func TestPythonTextWriteRuleRecognizesEveryModeForm(t *testing.T) {
	for code, want := range map[string]bool{
		`open(path, "w")`:                  true,
		`open(path, 'w')`:                  true,
		`open(path, mode="w")`:             true,
		`open(path, mode='a+')`:            true,
		`open(path, "w", newline="")`:      false,
		`open(path, 'w', newline='')`:      false,
		`open(path, mode="w", newline="")`: false,
		`open(path, "wb")`:                 false,
		`open(path, mode="rb")`:            false,
		`open(path)`:                       false,
		`with open(path, "r") as f:`:       false,
	} {
		if got := pythonTextWrite.MatchString(code) && !pythonNoTranslation.MatchString(code); got != want {
			t.Errorf("%s: flagged = %v, want %v", code, got, want)
		}
	}
}

// A Python fixture that writes a file the test reads back must write the bytes it means. Python's text mode turns "\n" into os.linesep, which is
// "\r\n" on Windows, so a marker file the test splits on "\n" (as it does for the Go, Rust and Node fixtures' markers) ended every line in "\r"
// there (TestConformance_HeadlessModesNeverRunUIClosures: "python:footer\r"). Windows is the only place the translation shows, so this pins the
// rule where it can be checked on every platform: each text-mode write in a fixture passes newline="" (or writes bytes).
func TestPythonFixturesWriteFilesWithoutNewlineTranslation(t *testing.T) {
	const root = "testdata"
	found := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(path) != ".py" {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found++
		for number, line := range strings.Split(string(source), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "#") {
				continue
			}
			switch {
			case pythonTextWrite.MatchString(code) && !pythonNoTranslation.MatchString(code):
				t.Errorf("%s:%d opens a file for text writing without newline=\"\" (Windows would write CRLF): %s", path, number+1, code)
			case strings.Contains(code, ".write_text("):
				t.Errorf("%s:%d write_text translates \"\\n\" on Windows; write the encoded bytes: %s", path, number+1, code)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatalf("no Python fixture under %s: the walk matched nothing", root)
	}
}
