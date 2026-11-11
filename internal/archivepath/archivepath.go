// Package archivepath decides which names and symbolic-link targets of a tar archive may be written into one extraction tree.
//
// Archive names are slash paths by the tar format, not host paths, so this package uses package path throughout.
package archivepath

import (
	"fmt"
	"path"
	"strings"
)

// Member returns name as a clean relative slash path inside the archive root, or an error when it is absolute or escapes the root.
func Member(name string) (string, error) {
	//portlint:allow pathseparators tar member and link names are slash paths by the tar format, not host paths
	clean := path.Clean(name)
	//portlint:allow pathseparators tar member and link names are slash paths by the tar format, not host paths
	if name == "" || path.IsAbs(name) || strings.Contains(name, `\`) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("archive path %q is not inside the archive", name)
	}
	return clean, nil
}

// CheckLink reports whether a symbolic link at slash path name of an archive may carry target. The target is relative, resolves
// inside the archive root, and has no ".." after a name.
//
// The last rule is what keeps the text of a target equal to where it resolves. A name in the target may itself be a link, and
// "z/.." then reaches a different directory than its text says: with z a link to ".", each "z/.." climbs one directory more than
// it reads, so a target that passes the lexical check can resolve above the root. A target that climbs only before it names
// anything resolves the same however the rest of the tree's links point, provided no entry's location passes through a link
// (the extraction in coding/extension/host/cellpack checks the tree on disk for that). A target holds no ":": on Windows "C:foo"
// is relative to the current folder of drive C, an absolute path that filepath.IsAbs does not report on other systems.
func CheckLink(name, target string) error {
	//portlint:allow pathseparators tar member and link names are slash paths by the tar format, not host paths
	if path.IsAbs(target) || strings.ContainsAny(target, `\:`) {
		return fmt.Errorf("archive link %s has a non-relative target %q", name, target)
	}
	named := false
	for part := range strings.SplitSeq(target, "/") {
		switch part {
		case "", ".":
		case "..":
			if named {
				return fmt.Errorf("archive link %s target %q climbs after naming a directory, which a link can make resolve elsewhere; rewrite the target with its \"..\" steps first", name, target)
			}
		default:
			named = true
		}
	}
	//portlint:allow pathseparators tar member and link names are slash paths by the tar format, not host paths
	if _, err := Member(path.Join(path.Dir(name), target)); err != nil {
		return fmt.Errorf("archive link %s points outside its sources: %w", name, err)
	}
	return nil
}
