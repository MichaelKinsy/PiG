//go:build !pig_strip_extension_sdk_go && !pig_strip_extension_sdk_python && !pig_strip_extension_sdk_rust

package cli

import (
	"bytes"
	"strings"
	"testing"
)

// Startup warns that extensions may build against a stale SDK and points the
// user at this report, so the report has to carry that detail. It pointed at
// "pig doctor", which does not exist, and the section it promised was missing.
func TestDiagnoseReportsExtensionSDKStaging(t *testing.T) {
	var buf bytes.Buffer
	runDiagnose(&buf, "/usr/local/bin/pig")
	out := buf.String()

	if !strings.Contains(out, "extension SDKs") {
		t.Fatal("diagnose has no extension SDK section, so the startup warning points at nothing")
	}
	for _, lang := range []string{"go:", "python:", "rust:"} {
		if !strings.Contains(out, lang) {
			t.Errorf("diagnose omits the %s SDK", strings.TrimSuffix(lang, ":"))
		}
	}
}
