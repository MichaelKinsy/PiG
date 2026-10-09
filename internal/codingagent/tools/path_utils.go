// Path resolution utilities for all file tools.
//
// Mirrors upstream path-utils.ts: ~ expansion, @ prefix stripping,
// unicode space normalization, macOS filename variants (NFD, curly
// quotes, AM/PM screenshot paths).
//
// upstream: coding-agent/src/core/tools/path-utils.ts (94 LOC)
package tools

import (
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"golang.org/x/text/unicode/norm"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// unicodeSpacesRE matches non-standard spaces that LLMs sometimes emit
// when quoting file paths (NBSP, en-space, em-space, etc.).
//
// upstream: path-utils.ts UNICODE_SPACES regex
var unicodeSpacesRE = lazyregexp.New(
	`[\x{00A0}\x{2000}-\x{200A}\x{202F}\x{205F}\x{3000}]`,
)

// narrowNoBreakSpace is the character macOS uses before AM/PM in
// screenshot filenames (e.g. "Screenshot 2024-01-01 at 10\u202FAM.png").
const narrowNoBreakSpace = "\u202F"

// normalizeUnicodeSpaces replaces non-standard Unicode spaces with ASCII space.
func normalizeUnicodeSpaces(s string) string {
	return unicodeSpacesRE.ReplaceAllString(s, " ")
}

// normalizeAtPrefix strips a leading "@" prefix that some shells add
// for file arguments (e.g. `@path/to/file`).
func normalizeAtPrefix(s string) string {
	if strings.HasPrefix(s, "@") {
		return s[1:]
	}
	return s
}

// expandPath expands ~ to home directory and normalizes unicode spaces
// and @ prefix. On Windows a Git Bash, MSYS, Cygwin or WSL drive path such
// as /c/Users becomes C:\Users, and ~\ expands like ~/.
//
// upstream: path-utils.ts expandPath, utils/paths.ts normalizePath
func expandPath(filePath string) string {
	normalized := normalizeUnicodeSpaces(normalizeAtPrefix(filePath))
	// Pi's normalizePath throws for an invalid file URL; this string-only form keeps the path as written then.
	if expanded, err := resolvepath.Normalize(normalized); err == nil {
		return expanded
	}
	return normalized
}

// NormalizeWindowsShellPath converts a Git Bash, MSYS, Cygwin or WSL drive
// path to the form native Windows APIs accept.
//
// upstream: utils/paths.ts normalizeWindowsShellPath
func NormalizeWindowsShellPath(filePath string) string {
	return resolvepath.NormalizeWindowsShellPath(filePath)
}

// resolveToCwd resolves a file path relative to cwd with ~ expansion
// and unicode normalization. Like Node's path.resolve, the result is
// absolute and clean; on Windows a path rooted at '\' or '/' lands on the
// process's drive, not the cwd's, and keeps a trailing dot in its last segment.
//
// upstream: path-utils.ts resolveToCwd, utils/paths.ts resolvePath
func resolveToCwd(filePath, cwd string) (string, error) {
	// resolvePath(filePath, cwd, { normalizeUnicodeSpaces: true, stripAtPrefix: true }): those two options first, then the default
	// normalization (Windows shell paths, `~`, a file:// URL) and the resolution against cwd.
	return resolvepath.Resolve(normalizeUnicodeSpaces(normalizeAtPrefix(filePath)), cwd)
}

// resolveReadPath resolves a path for reading, trying macOS filename
// variants if the primary resolved path doesn't exist. Falls through
// to the resolved path (letting the caller surface the error).
//
// Variants tried in order:
//  1. AM/PM screenshot path (narrow no-break space before AM/PM)
//  2. NFD variant (macOS stores filenames in decomposed form)
//  3. Curly quote variant (macOS uses U+2019 in French screenshot names)
//  4. Combined NFD + curly quote
//
// upstream: path-utils.ts resolveReadPath
func resolveReadPath(filePath, cwd string) (string, error) {
	resolved, err := resolveToCwd(filePath, cwd)
	if err != nil {
		return "", err
	}

	if fileExists(resolved) {
		return resolved, nil
	}

	// Try macOS AM/PM variant (narrow no-break space before AM/PM)
	amPmVariant := tryMacOSScreenshotPath(resolved)
	if amPmVariant != resolved && fileExists(amPmVariant) {
		return amPmVariant, nil
	}

	// Try NFD variant (macOS stores filenames in NFD form)
	nfdVariant := tryNFDVariant(resolved)
	if nfdVariant != resolved && fileExists(nfdVariant) {
		return nfdVariant, nil
	}

	// Try curly quote variant (macOS uses U+2019 in screenshot names)
	curlyVariant := tryCurlyQuoteVariant(resolved)
	if curlyVariant != resolved && fileExists(curlyVariant) {
		return curlyVariant, nil
	}

	// Try combined NFD + curly quote (for French macOS screenshots)
	nfdCurlyVariant := tryCurlyQuoteVariant(nfdVariant)
	if nfdCurlyVariant != resolved && fileExists(nfdCurlyVariant) {
		return nfdCurlyVariant, nil
	}

	return resolved, nil
}

// fileExists returns true if the path exists (regular file or dir).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// amPmRE matches " AM." or " PM." (case-insensitive) in screenshot filenames.
var amPmRE = lazyregexp.New(`(?i) (AM|PM)\.`)

// tryMacOSScreenshotPath replaces the regular space before AM/PM with
// a narrow no-break space, matching macOS's screenshot naming convention.
func tryMacOSScreenshotPath(filePath string) string {
	return amPmRE.ReplaceAllStringFunc(filePath, func(m string) string {
		// m is like " AM." or " PM.": replace leading space with NNBSP
		return narrowNoBreakSpace + strings.TrimLeft(m, " ")
	})
}

// tryNFDVariant converts the path to NFD (decomposed) form, which is
// how macOS HFS+/APFS stores filenames internally.
func tryNFDVariant(filePath string) string {
	return norm.NFD.String(filePath)
}

// tryCurlyQuoteVariant replaces straight apostrophes with right single
// quotation marks (U+2019), matching macOS French screenshot names like
// "Capture d\u2019écran".
func tryCurlyQuoteVariant(filePath string) string {
	return strings.ReplaceAll(filePath, "'", "\u2019")
}

// ResolveToCwd is upstream path-utils.ts resolveToCwd for other packages.
func ResolveToCwd(filePath, cwd string) (string, error) { return resolveToCwd(filePath, cwd) }

// ResolveReadPath is upstream path-utils.ts resolveReadPath for other packages.
func ResolveReadPath(filePath, cwd string) (string, error) { return resolveReadPath(filePath, cwd) }
