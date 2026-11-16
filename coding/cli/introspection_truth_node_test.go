//go:build !pig_strip_node_extensions

package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallValidateOnlyReportsWhatDiscoveryWouldLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping extension load in short mode")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(t.TempDir(), "agent"))
	dir := t.TempDir()
	const body = "export default function (pi) { pi.registerCommand('hello', { description: 'hi', handler: async () => {} }); }\n"
	mjs := filepath.Join(dir, "hello.mjs")
	js := filepath.Join(dir, "hello.js")
	writeResourceFixture(t, mjs, body)
	writeResourceFixture(t, js, body)
	validate := func(path string) (report struct {
		Valid          bool     `json:"valid"`
		AutoDiscovered *bool    `json:"autoDiscovered"`
		Warnings       []string `json:"warnings"`
	}, stderr string) {
		t.Helper()
		stdout, stderr, code := captureStdoutStderr(t, func() int {
			return runPackageCommand([]string{"install", path, "--validate-only", "--json"})
		})
		if code != 0 {
			t.Fatalf("validate %s: code=%d stdout=%s stderr=%s", path, code, stdout, stderr)
		}
		if err := json.Unmarshal([]byte(stdout), &report); err != nil {
			t.Fatalf("parse %s: %v", stdout, err)
		}
		return report, stderr
	}
	report, _ := validate(mjs)
	if !report.Valid || report.AutoDiscovered == nil || *report.AutoDiscovered || len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "loads only .ts and .js files") {
		t.Errorf(".mjs report = %#v", report)
	}
	report, _ = validate(js)
	if !report.Valid || report.AutoDiscovered == nil || !*report.AutoDiscovered || len(report.Warnings) != 0 {
		t.Errorf(".js report = %#v", report)
	}
	_, stderr := func() (struct{}, string) {
		_, stderr, _ := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", mjs, "--validate-only"}) })
		return struct{}{}, stderr
	}()
	if !strings.Contains(stderr, "Warning: auto-discovery does not load hello.mjs") {
		t.Errorf("human output has no warning: %q", stderr)
	}
}
