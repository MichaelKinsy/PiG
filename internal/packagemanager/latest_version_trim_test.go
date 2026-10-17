package packagemanager

import "testing"

// getLatestNpmVersion (package-manager.ts) reads `stdout.trim()` and fails an empty result with "Empty response from npm view": String.prototype.trim removes
// U+FEFF and U+00A0 and keeps U+0085.
func TestLatestNpmVersionFromJSONTrimsOutputLikeJS(t *testing.T) {
	for _, output := range []string{"\ufeff", "\u00a0\n", " \t\n"} {
		_, err := LatestNpmVersionFromJSON([]byte(output), "")
		if err == nil || err.Error() != "Empty response from npm view" {
			t.Errorf("%q: error %v, want the empty-response error", output, err)
		}
	}
	if _, err := LatestNpmVersionFromJSON([]byte("\u0085"), ""); err == nil || err.Error() == "Empty response from npm view" {
		t.Errorf("a lone U+0085 is not empty after a JS trim, got %v", err)
	}
}
