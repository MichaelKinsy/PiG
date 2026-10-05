package runtimecell

// GoBuildCgoEnv returns the CGO_ENABLED entry for building a Go extension for goos/goarch. Extensions build without cgo, except on Android for every architecture but arm64, where Go links executables externally even without cgo (internal/platform.MustLinkExternal) and `go build` with CGO_ENABLED=0 fails with "requires external (cgo) linking".
func GoBuildCgoEnv(goos, goarch string) string {
	if goos == "android" && goarch != "arm64" {
		return "CGO_ENABLED=1"
	}
	return "CGO_ENABLED=0"
}
