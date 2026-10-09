package packagecontent

// Ports packages/coding-agent/src/core/package-manager.ts
// expandPackageGlob uses Node's globstar traversal: wildcard directory steps
// do not follow symlinks, while literal steps can select symlinked trees.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/minimatch"
)

func expandPackageGlob(root, pattern string) []string {
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	// Adjacent globstars describe the same set of paths.
	parts = slices.CompactFunc(parts, func(a, b string) bool { return a == "**" && b == "**" })
	type state struct {
		dir   string
		index int
	}
	pending := []state{{root, 0}}
	seen := make(map[state]bool)
	matches := make(map[string]bool)
	add := func(file string) {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return
		}
		if rel == "." {
			rel = ""
		}
		for segment := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
			if segment != ".." && strings.HasPrefix(segment, ".") {
				return
			}
		}
		matches[file] = true
	}
	for len(pending) > 0 {
		s := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[s] || s.index >= len(parts) {
			continue
		}
		seen[s] = true
		current := parts[s.index]
		last := s.index == len(parts)-1
		if current != "**" && !strings.ContainsAny(current, "*?[") {
			target := filepath.Join(s.dir, filepath.FromSlash(current))
			info, err := os.Lstat(target)
			if err != nil {
				continue
			}
			if last {
				if current != "" || info.IsDir() {
					add(target)
				}
			} else {
				pending = append(pending, state{target, s.index + 1})
			}
			continue
		}
		if current == "**" && (last || s.index+1 == len(parts)-1 && parts[s.index+1] == "") {
			if info, err := os.Lstat(s.dir); err == nil && (last || info.IsDir()) {
				add(s.dir)
			}
		}
		entries, err := os.ReadDir(s.dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			target := filepath.Join(s.dir, entry.Name())
			if current == "**" {
				if entry.IsDir() {
					pending = append(pending, state{target, s.index})
				} else if last {
					add(target)
				}
				if last {
					continue
				}
				next := parts[s.index+1]
				nextMatches := matchGlobSegment(entry.Name(), next)
				if nextMatches && s.index+1 == len(parts)-1 {
					add(target)
				}
				if nextMatches && entry.IsDir() {
					pending = append(pending, state{target, s.index + 2})
				}
				if (nextMatches || parts[0] == ".") && (entry.IsDir() || entry.Type()&os.ModeSymlink != 0) {
					pending = append(pending, state{target, s.index + 1})
				}
				if next == ".." && entry.IsDir() {
					for _, dir := range []string{s.dir, filepath.Dir(s.dir)} {
						if s.index+1 == len(parts)-1 {
							add(dir)
						} else {
							pending = append(pending, state{dir, s.index + 2})
						}
					}
				}
			} else if matchGlobSegment(entry.Name(), current) {
				if last {
					add(target)
				} else if entry.IsDir() {
					pending = append(pending, state{target, s.index + 1})
				}
			}
		}
	}
	result := make([]string, 0, len(matches))
	for match := range matches {
		result = append(result, match)
	}
	slices.Sort(result)
	return result
}

// matchGlobSegment is minimatch(name, pattern) for one path segment: the test node-glob applies to each directory entry.
func matchGlobSegment(name, pattern string) bool {
	return minimatch.Match(name, pattern, minimatch.Options{})
}

// matchGlob is minimatch(name, pattern).
func matchGlob(name, pattern string) bool {
	return minimatch.Match(name, pattern, minimatch.Options{})
}
