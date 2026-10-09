package subprocess_test

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Runs packages/tui/test/native-module-path.test.ts (:8 and :26) unchanged, apart from the module locations, against the vendored native-module-path.js that the Node extension runtime loads. It needs only node, so it also runs where TestNodeVendoredTuiUpstreamTests cannot qualify the Xvfb-backed native clipboard case.
func TestNativeModulePathUpstreamCases(t *testing.T) {
	dist, err := filepath.Abs("runtime-node/shims/pi-dist/pi-tui")
	if err != nil {
		t.Fatal(err)
	}
	moduleURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(dist) + "/"}).String()
	data, err := os.ReadFile(filepath.Join("../../../..", ".upstream/current/packages/tui/test/native-module-path.test.ts"))
	if err != nil {
		t.Fatal(err)
	}
	source := regexp.MustCompile(`\.\./src/([^"`+"`"+`]+)\.ts`).ReplaceAllString(string(data), moduleURL+"${1}.js")
	testFile := filepath.Join(t.TempDir(), "native-module-path.test.mts")
	if err := os.WriteFile(testFile, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "node", "--test", "--test-reporter=tap", testFile).CombinedOutput()
	if err != nil {
		t.Fatalf("upstream native-module-path: %v\n%s", err, out)
	}
	for _, summary := range []string{"# tests 2\n", "# pass 2\n", "# fail 0\n", "# cancelled 0\n", "# skipped 0\n"} {
		if !strings.Contains(string(out), summary) {
			t.Fatalf("native module path cases did not all execute (%q missing):\n%s", summary, out)
		}
	}
}
