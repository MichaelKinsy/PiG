package codingagent

import (
	"regexp"
	"testing"
)

// pi: packages/coding-agent/src/utils/pi-user-agent.ts
//
// Pi's test (test/pi-user-agent.test.ts) expects `pi/<version> (<platform>; <runtime>; <arch>)` and the shape /^pi\/[^\s()]+ \([^;()]+;\s*[^;()]+;\s*[^()]+\)$/.
// PiG sends the owner-approved D65 identity `<AppName>/<version> (<platform> <release>; <arch>)` instead, with Node's platform and arch names (process.platform
// "linux", process.arch "x64" for amd64): the version segment stays one token without spaces or parentheses, the parentheses hold the platform and arch.
func TestPiCodingAgentSrcUtilsPiUserAgentShape(t *testing.T) {
	got := codingAgentUserAgent("1.2.3", nodePlatform("linux"), "6.1.0", nodeArch("amd64"))
	if want := AppName + "/1.2.3 (linux 6.1.0; x64)"; got != want {
		t.Fatalf("user agent = %q, want %q", got, want)
	}
	shape := regexp.MustCompile(`^` + regexp.QuoteMeta(AppName) + `/[^\s()]+ \([^;()]+;\s*[^()]+\)$`)
	if !shape.MatchString(got) {
		t.Fatalf("user agent %q does not have Pi's shape <product>/<version> (<platform>...; <arch>)", got)
	}
	for goos, want := range map[string]string{"linux": "linux", "darwin": "darwin", "windows": "win32"} {
		if got := nodePlatform(goos); got != want {
			t.Fatalf("nodePlatform(%q) = %q, want %q (process.platform)", goos, got, want)
		}
	}
	for goarch, want := range map[string]string{"amd64": "x64", "386": "ia32", "arm64": "arm64"} {
		if got := nodeArch(goarch); got != want {
			t.Fatalf("nodeArch(%q) = %q, want %q (process.arch)", goarch, got, want)
		}
	}
}
