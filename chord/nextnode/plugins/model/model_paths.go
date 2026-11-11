package model

import (
	"path/filepath"
	"strings"
)

// ResolvePath is Node's `path.resolve(dir, path)` for an absolute dir.
func ResolvePath(dir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(dir, path)
}

// Inside reports whether path lies inside directory dir (both absolute): not dir itself. Upstream's test is `relative(dir,
// path)` not empty, not starting with `..` (a file named `..x` is outside) and not absolute.
func Inside(dir, path string) bool {
	rest, err := Relative(dir, path)
	return err == nil && rest != "" && !HasDotDotPrefix(rest) && !filepath.IsAbs(rest)
}

// Relative is Node's `path.relative(from, to)`: both resolved against the working directory first.
func Relative(from, to string) (string, error) {
	absoluteFrom, err := filepath.Abs(from)
	if err != nil {
		return "", err
	}
	absoluteTo, err := filepath.Abs(to)
	if err != nil {
		return "", err
	}
	rest, err := filepath.Rel(absoluteFrom, absoluteTo)
	if rest == "." {
		// Node's is empty for the same path.
		rest = ""
	}
	return rest, err
}

func HasDotDotPrefix(path string) bool { return strings.HasPrefix(path, "..") }

// Extname is Node's `path.extname`: the last `.` of the base name on, empty when it is the name's first character.
func Extname(path string) string {
	base := filepath.Base(path)
	i := strings.LastIndex(base, ".")
	if i <= 0 {
		return ""
	}
	return base[i:]
}
