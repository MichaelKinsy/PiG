package runtimecell

import "testing"

// Go links Android executables externally on every architecture except arm64 (internal/platform.MustLinkExternal), and
// external linking needs cgo: with CGO_ENABLED=0, `go build` on android/amd64 stops with "android/amd64 requires external
// (cgo) linking, but cgo is not enabled". Every other platform keeps the cgo-free build.
func TestGoBuildCgoFollowsGoExternalLinkRule(t *testing.T) {
	for _, tc := range []struct{ goos, goarch, want string }{
		{"android", "amd64", "CGO_ENABLED=1"},
		{"android", "386", "CGO_ENABLED=1"},
		{"android", "arm", "CGO_ENABLED=1"},
		{"android", "arm64", "CGO_ENABLED=0"},
		{"linux", "amd64", "CGO_ENABLED=0"},
		{"linux", "arm64", "CGO_ENABLED=0"},
		{"darwin", "arm64", "CGO_ENABLED=0"},
		{"windows", "amd64", "CGO_ENABLED=0"},
	} {
		if got := GoBuildCgo(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("GoBuildCgo(%q, %q) = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}
}
