// Package resolvepath resolves user-supplied resource paths the way Pi's
// resolvePath does. It sits below internal/codingagent and coding/packagecontent
// so both share one implementation.
// Ports packages/coding-agent/src/utils/paths.ts:75-106.
package resolvepath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodepath"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// windowsShellDrivePath matches /c, /c/rest, /mnt/c/rest and /cygdrive/c/rest.
var windowsShellDrivePath = lazyregexp.New(`(?i)^/(?:mnt/|cygdrive/)?([a-z])(?:/(.*))?$`)

// NormalizeWindowsShellPath converts a Git Bash, MSYS, Cygwin or WSL drive
// path to the form native Windows APIs accept.
//
// upstream: utils/paths.ts normalizeWindowsShellPath
func NormalizeWindowsShellPath(filePath string) string {
	if !strings.HasPrefix(filePath, "/") || strings.HasPrefix(filePath, "//") || strings.Contains(filePath, `\`) {
		return filePath
	}
	match := windowsShellDrivePath.FindStringSubmatch(filePath)
	if match == nil {
		return filePath
	}
	return strings.ToUpper(match[1]) + `:\` + strings.ReplaceAll(match[2], "/", `\`)
}

// Normalize applies Pi's default path normalization: a leading "~" or "~/"
// expands to the home directory and a file:// URL becomes its path through
// Node's fileURLToPath, whose errors it returns. An empty path stays empty.
func Normalize(path string) (string, error) {
	return normalize(path, "")
}

func normalize(path, home string) (string, error) {
	if path == "" {
		return "", nil
	}
	windows := runtime.GOOS == "windows"
	if windows {
		path = NormalizeWindowsShellPath(path)
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home != "" {
		if path == "~" {
			return home, nil
		}
		if after, ok := strings.CutPrefix(path, "~/"); ok {
			return filepath.Join(home, after), nil
		}
		if after, ok := strings.CutPrefix(path, `~\`); ok && windows {
			return filepath.Join(home, after), nil
		}
	}
	if strings.HasPrefix(path, "file://") {
		return nodeurl.FileURLToPath(path, windows)
	}
	return path, nil
}

// Resolve normalizes input and baseDir, then resolves the input to an absolute
// path. An empty baseDir uses the process working directory.
func Resolve(input, baseDir string) (string, error) {
	return resolve(input, baseDir, "")
}

func resolve(input, baseDir, home string) (string, error) {
	normalized, err := normalize(input, home)
	if err != nil {
		return "", err
	}
	normalizedBaseDir, err := Normalize(baseDir)
	if err != nil {
		return "", err
	}
	// Node's isAbsolute counts a rooted path without a drive (`\x`), which then resolves on the process's drive, not the base directory's.
	if nodepath.IsAbsolute(normalized) {
		return nodepath.Resolve(normalized)
	}
	return nodepath.Resolve(normalizedBaseDir, normalized)
}

// ResolveTrimmed is Resolve after String.prototype.trim, the { trim: true }
// option Pi's resource loader and package manager pass.
func ResolveTrimmed(input, baseDir string) (string, error) {
	return Resolve(Trim(input), baseDir)
}

// ResolvePackagePath uses the package manager's HOME || homedir() tilde expansion and trims the input, without changing normalization of baseDir.
// Ports packages/coding-agent/src/core/package-manager.ts:221-223,2145-2151.
func ResolvePackagePath(input, baseDir string) (string, error) {
	return resolve(Trim(input), baseDir, os.Getenv("HOME"))
}

// Trim applies String.prototype.trim, including BOM but excluding NEL.
func Trim(input string) string {
	return strings.TrimFunc(input, isJSWhitespace)
}

func isJSWhitespace(r rune) bool {
	switch r {
	case 0xFEFF:
		return true
	case 0x85:
		return false
	}
	return unicode.IsSpace(r)
}
