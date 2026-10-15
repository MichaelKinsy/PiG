// Package resolvepath resolves user-supplied resource paths the way Pi's
// resolvePath does. It sits below internal/codingagent and coding/packagecontent
// so both share one implementation.
// Ports packages/coding-agent/src/utils/paths.ts:75-106.
package resolvepath

import (
	"os"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodepath"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// windowsShellDrivePath matches /c, /c/rest, /mnt/c/rest and /cygdrive/c/rest. The JavaScript source is /^\/(?:mnt\/|cygdrive\/)?([a-z])(?:\/(.*))?$/i: its "." does not
// match a line terminator (\n, \r, U+2028, U+2029), and its /i flag folds ASCII letters only, where Go's (?i) also folds U+017F and U+212A.
var windowsShellDrivePath = lazyregexp.New(`^/(?:[mM][nN][tT]/|[cC][yY][gG][dD][rR][iI][vV][eE]/)?([a-zA-Z])(?:/([^\n\r\x{2028}\x{2029}]*))?$`)

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

// isUnicodeSpace reports whether r is one of the non-standard spaces Pi's normalizeUnicodeSpaces option replaces.
//
// upstream: utils/paths.ts UNICODE_SPACES, /[\u00A0\u2000-\u200A\u202F\u205F\u3000]/g
func isUnicodeSpace(r rune) bool {
	return r == 0x00A0 || r >= 0x2000 && r <= 0x200A || r == 0x202F || r == 0x205F || r == 0x3000
}

// NormalizeUnicodeSpaces replaces each non-standard Unicode space with an ASCII space, Pi's resolvePath option normalizeUnicodeSpaces.
// It scans the path instead of running a regexp: a regexp match takes its matcher from a sync.Pool, whose per-P array the first use
// after a collection reallocates (GOMAXPROCS*128 bytes), and every read and write tool call resolves a path. Bytes that are not part of
// such a space, ill-formed UTF-8 included, are copied as they are, as ReplaceAllString copies them.
func NormalizeUnicodeSpaces(s string) string {
	var out strings.Builder
	copied := 0
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if isUnicodeSpace(r) {
			if out.Cap() == 0 {
				out.Grow(len(s))
			}
			out.WriteString(s[copied:i])
			out.WriteByte(' ')
			copied = i + size
		}
		i += size
	}
	if out.Cap() == 0 {
		return s
	}
	out.WriteString(s[copied:])
	return out.String()
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
			return nodepath.Join(home, after), nil
		}
		if after, ok := strings.CutPrefix(path, `~\`); ok && windows {
			return nodepath.Join(home, after), nil
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
	return strings.TrimFunc(input, jsstring.IsSpace)
}
