package codingagent

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// PR-5-5: Pi's literal-space editor command parsing does not justify skipping the caller tests when the Go executable's directory or basename contains spaces.
func TestExternalEditorFixtureRunsFromSpacedExecutablePath(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, dir, executable string }{
		{"directory", "editor directory", "editor-probe.test"},
		{"basename", "editor-directory", "editor probe.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), tc.dir)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			name := tc.executable
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			spaced := filepath.Join(dir, name)
			if err := os.Link(binary, spaced); err != nil {
				copyEditorHelper(t, binary, spaced)
			}
			cmd := exec.CommandContext(t.Context(), spaced, "-test.run=^TestOpenExternalEditor(RoundTrip|VISUALWinsOverEDITOR|ConfiguredCommandWinsOverEnvironment|AcceptsArguments)$", "-test.count=1", "-test.v")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("editor regression subprocess: %v\n%s", err, output)
			}
			if bytes.Contains(output, []byte("SKIP")) {
				t.Fatalf("editor callers skipped for a spaced test-binary path:\n%s", output)
			}
			for _, test := range []string{"RoundTrip", "VISUALWinsOverEDITOR", "ConfiguredCommandWinsOverEnvironment", "AcceptsArguments"} {
				if !bytes.Contains(output, []byte("--- PASS: TestOpenExternalEditor"+test)) {
					t.Fatalf("editor caller %s did not execute:\n%s", test, output)
				}
			}
		})
	}
}
