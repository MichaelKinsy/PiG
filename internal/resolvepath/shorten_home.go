package resolvepath

import "github.com/MichaelKinsy/PiG/internal/nodepath"

// ShortenHomePath replaces a leading home directory with "~" for display. A path outside home, a relative path and an empty home come back unchanged. Only whole path segments match: "/home/me2/x" is not under "/home/me". On Windows the match ignores case and the rest is written with backslashes.
// Ports packages/coding-agent/src/utils/paths.ts shortenHomePath (Pi 1be312b4, #10488).
// pig divergence (D217): Pi 1be312b4 (#10488) anchors the home prefix at a path segment; Pi 1.4.0+b3a29f11 shortens any path that starts with the home string (`/home/me2/x` became `~2/x`).
func ShortenHomePath(path, home string) string {
	if home == "" || !isAbsoluteOnPlatform(path) || !IsPathWithin(home, path) {
		return path
	}
	rel, err := relativeOnPlatform(home, path)
	if err != nil {
		return path
	}
	if rel == "" {
		return "~"
	}
	return "~" + pathSeparatorOfPlatform() + rel
}

func isAbsoluteOnPlatform(path string) bool {
	if onWindows() {
		return nodepath.Win32IsAbsolute(path)
	}
	return nodepath.PosixIsAbsolute(path)
}

func relativeOnPlatform(from, to string) (string, error) {
	if onWindows() {
		return nodepath.Win32Relative(nodepath.Process(), from, to)
	}
	return nodepath.PosixRelative(nodepath.Process(), from, to)
}
