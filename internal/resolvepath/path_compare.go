package resolvepath

import (
	"runtime"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// platform is the operating system the comparison helpers behave as; nil is the host's. Pi's helpers read process.platform at call time, so its tests
// run Windows paths on any host; OverridePlatform is the same seam.
var platform atomic.Pointer[string]

// OverridePlatform makes ComparisonKey, PathsEqual and IsPathWithin behave as on goos ("windows", or any other name for POSIX rules) until the returned function
// runs. It is for tests: production code never calls it.
func OverridePlatform(goos string) (restore func()) {
	previous := platform.Swap(&goos)
	return func() { platform.Store(previous) }
}

func currentPlatform() string {
	if override := platform.Load(); override != nil {
		return *override
	}
	return runtime.GOOS
}

func onWindows() bool { return currentPlatform() == "windows" }

// FoldsCase reports whether paths compare case-insensitively on the current platform (Windows). Callers on a hot path use it to skip a comparison that ComparisonKey would only repeat.
func FoldsCase() bool { return onWindows() }

// pathSeparatorOfPlatform is `sep` of the node:path flavor for the current platform.
func pathSeparatorOfPlatform() string {
	if onWindows() {
		return `\`
	}
	return "/"
}

// ComparisonKey is Pi's getPathComparisonKey: the resolved absolute path, case-folded on Windows because Windows paths are case-insensitive (`c:\Users` and
// `C:\Users` are the same directory). It does not touch the filesystem; call CanonicalizePath first when symlinks must compare equal. Use it only for
// comparison, never for display or filesystem access. A relative path that cannot be resolved because the working directory is gone keeps its spelling,
// where Pi's path.resolve throws.
// pig divergence (D217): Pi 1be312b4 (#10488) compares Windows paths case-insensitively; Pi 1.4.0+b3a29f11 compared them as exact strings.
func ComparisonKey(path string) string {
	var resolved string
	var err error
	switch {
	case onWindows():
		resolved, err = nodepath.Win32Resolve(nodepath.Process(), path)
	case nodepath.PosixIsAbsolute(path):
		// posix.resolve reads process.cwd() only for a relative path; skip the getcwd(3) it costs.
		resolved, err = nodepath.PosixResolve(nodepath.Env{}, path)
	default:
		resolved, err = nodepath.PosixResolve(nodepath.Process(), path)
	}
	if err != nil {
		resolved = path
	}
	if onWindows() {
		return strings.ToLower(resolved)
	}
	return resolved
}

// PathsEqual is Pi's pathsEqual: both paths name the same location (see ComparisonKey).
func PathsEqual(left, right string) bool {
	return ComparisonKey(left) == ComparisonKey(right)
}

// IsPathWithin is Pi's isPathWithin: child is parent or inside it, matching whole path segments (see ComparisonKey).
func IsPathWithin(parent, child string) bool {
	parentKey, childKey := ComparisonKey(parent), ComparisonKey(child)
	if childKey == parentKey {
		return true
	}
	// Filesystem roots (`/`, `C:\`) already end with a separator.
	separator := pathSeparatorOfPlatform()
	if !strings.HasSuffix(parentKey, separator) {
		parentKey += separator
	}
	return strings.HasPrefix(childKey, parentKey)
}
