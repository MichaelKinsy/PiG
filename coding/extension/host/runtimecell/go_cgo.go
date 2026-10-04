package runtimecell

// GoBuildCgo returns the CGO_ENABLED entry for building a Go extension for goos/goarch. Extensions build without cgo,
// except where Go must link externally without it: Android on every architecture but arm64
// (internal/platform.MustLinkExternal). There `go build` with CGO_ENABLED=0 fails with "requires external (cgo) linking".
func GoBuildCgo(goos, goarch string) string {
	if goos == "android" && goarch != "arm64" {
		return "CGO_ENABLED=1"
	}
	return "CGO_ENABLED=0"
}
