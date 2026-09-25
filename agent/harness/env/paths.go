package env

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// resolvePath expands "~", "~/" (and "~\" on Windows), and file:// URLs, then
// resolves the result against cwd. A malformed file URL stays an ordinary
// path so filesystem methods keep their non-throwing contract.
func resolvePath(cwd, path string) string {
	normalized := path
	switch {
	case normalized == "~":
		normalized = homeDir()
	case strings.HasPrefix(normalized, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(normalized, `~\`)):
		normalized = filepath.Join(homeDir(), normalized[2:])
	case strings.HasPrefix(normalized, "file://"):
		if converted, err := nodeurl.FileURLToPath(normalized, runtime.GOOS == "windows"); err == nil {
			normalized = converted
		}
	}
	if runtime.GOOS == "windows" {
		return resolveWindowsPath(cwd, normalized)
	}
	if filepath.IsAbs(normalized) {
		return filepath.Clean(normalized)
	}
	return filepath.Join(cwd, normalized)
}

// resolveWindowsPath applies the path.win32 rules upstream's
// isAbsolute(p) ? resolve(p) : resolve(cwd, p) follows where filepath
// differs: a path rooted at \ or / without a drive is absolute and lands on
// the drive of the process's working directory; a drive-relative path such
// as C:rel resolves against cwd only when cwd is on that drive; and a UNC
// share root keeps its trailing separator.
func resolveWindowsPath(cwd, path string) string {
	if filepath.IsAbs(path) {
		clean := filepath.Clean(path)
		if strings.HasPrefix(clean, `\\`) && clean == filepath.VolumeName(clean) {
			return clean + `\`
		}
		return clean
	}
	if strings.HasPrefix(path, `\`) || strings.HasPrefix(path, "/") {
		processDir, err := os.Getwd()
		if err != nil {
			processDir = cwd
		}
		return filepath.Clean(filepath.VolumeName(processDir) + path)
	}
	if volume := filepath.VolumeName(path); volume != "" {
		if strings.EqualFold(volume, filepath.VolumeName(cwd)) {
			return filepath.Join(cwd, path[len(volume):])
		}
		if abs, err := filepath.Abs(path); err == nil {
			return abs
		}
	}
	return filepath.Join(cwd, path)
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
