package packagecontent

// Ports packages/coding-agent/src/core/package-manager.ts
// expandPackageGlob lists the matches of Node's globSync, walked as the sync
// walk of lib/internal/fs/glob.js as of the qualified Node (.node-version, 24.22.1;
// Node 26.12.1 walks the same): wildcard directory steps do not follow
// symlinks, while literal steps can select symlinked trees. Node 24.22 stopped
// ending a listing at an entry whose path the walk already holds (24.19 and 22.19
// returned from the loop) and stopped a globstar from queueing the next segment
// below a symlink.

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodefs"
)

// expandPackageGlob is Pi's expandPackageGlob (package-manager.ts:294-303): the matches of globSync(pattern, { cwd: root }), each resolved against root, without those that have a dot segment below root, sorted as JavaScript sorts strings. Two matches that resolve to one path both stay, as in Pi. It fails, before it reads the file system, where the Glob constructor's matcher throws: for a pattern minimatch rejects, a brace sequence brace-expansion cannot hold, or a segment whose RegExp V8 rejects.
func expandPackageGlob(root, pattern string) ([]string, error) {
	patterns, err := compilePackageGlob(pattern)
	if err != nil {
		return nil, err
	}
	walk := packageGlobWalk{root: root, seen: make(map[packageGlobKey]bool), subpatternAt: make(map[string]int), results: make(map[string]bool), stats: make(map[string]*globDirent), listings: make(map[string][]*globDirent)}
	queue := []packageGlobQueued{{".", patterns}}
	for len(queue) > 0 {
		item := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, pattern := range item.patterns {
			walk.addSubpatterns(item.path, pattern)
		}
		queue = append(queue, walk.subpatterns...)
		walk.subpatterns = nil
		clear(walk.subpatternAt)
	}
	matches := make([]string, 0, len(walk.results))
	for result := range walk.results {
		resolved := walk.resolve(result)
		rel := resolved
		if r, err := filepath.Rel(root, resolved); err == nil {
			rel = r
		}
		if rel == "." {
			rel = ""
		}
		if !slices.ContainsFunc(strings.Split(rel, string(filepath.Separator)), func(segment string) bool {
			return segment != ".." && strings.HasPrefix(segment, ".")
		}) {
			matches = append(matches, resolved)
		}
	}
	slices.SortFunc(matches, compareUTF16)
	return matches, nil
}

// compilePackageGlob is the matcher the Glob constructor builds from pattern before it reads a directory: minimatch's set, one walk pattern per set member. It fails where that constructor throws.
func compilePackageGlob(pattern string) ([]packageGlobPattern, error) {
	globParts, err := globPatternParts(pattern)
	if err != nil {
		return nil, err
	}
	patterns := make([]packageGlobPattern, 0, len(globParts))
	for _, parts := range globParts {
		parsed := &packageGlobParts{keys: make([]string, len(parts)+1)}
		if parsed.segments, err = globPatternSegments(parts, globIgnoresCase); err != nil {
			return nil, err
		}
		for i := range parts {
			parsed.keys[i] = strings.Join(parts[i:], "/")
		}
		patterns = append(patterns, packageGlobPattern{parts: parsed, indexes: []int{0}})
	}
	return patterns, nil
}

// packageGlobParts is one pattern of minimatch's set: its segments, and for each index the rest of its glob strings, which keys the walk's cache (Pattern.cacheKey).
type packageGlobParts struct {
	segments []packageGlobSegment
	keys     []string
}

// packageGlobPattern is a Pattern: the indexes of the segments that the children of its path may match, and the indexes of the globstars that reached the path through a symlink.
type packageGlobPattern struct {
	parts    *packageGlobParts
	indexes  []int
	symlinks []int
}

// child is Pattern.child: the same pattern at other indexes.
func (p packageGlobPattern) child(indexes, symlinks []int) packageGlobPattern {
	return packageGlobPattern{parts: p.parts, indexes: indexes, symlinks: symlinks}
}

func (p packageGlobPattern) last() int { return len(p.parts.segments) - 1 }

func (p packageGlobPattern) has(index int) bool { return slices.Contains(p.indexes, index) }

func (p packageGlobPattern) segment(index int) (packageGlobSegment, bool) {
	if index < 0 || index >= len(p.parts.segments) {
		return packageGlobSegment{}, false
	}
	return p.parts.segments[index], true
}

func (p packageGlobPattern) isGlobstar(index int) bool {
	s, ok := p.segment(index)
	return ok && s.globstar
}

func (p packageGlobPattern) literalIs(index int, text string) bool {
	s, ok := p.segment(index)
	return ok && s.isLiteral && s.literal == text
}

// test is Pattern.test: false past the last segment.
func (p packageGlobPattern) test(index int, name string) bool {
	s, ok := p.segment(index)
	return ok && s.matches(name)
}

func (p packageGlobPattern) key(index int) string {
	return p.parts.keys[min(index, len(p.parts.keys)-1)]
}

// hasSeenSymlinks is Pattern.hasSeenSymlinks: some index of the pattern was not reached through a symlink.
func (p packageGlobPattern) hasSeenSymlinks() bool {
	return slices.ContainsFunc(p.indexes, func(index int) bool { return !slices.Contains(p.symlinks, index) })
}

type packageGlobQueued struct {
	path     string
	patterns []packageGlobPattern
}

type packageGlobKey struct{ path, rest string }

// packageGlobWalk is the state of one Glob#globSync walk. Paths are Node's: relative to root unless absolute, joined with path.join, so a path reached through ".." differs from the same directory reached directly.
type packageGlobWalk struct {
	root string
	// seen is the Cache: each path with each rest of a pattern walked there.
	seen map[packageGlobKey]bool
	// subpatterns is the paths and patterns that the current queue item adds, in insertion order, and subpatternAt indexes it by path.
	subpatterns  []packageGlobQueued
	subpatternAt map[string]int
	results      map[string]bool
	// stats and listings are the Cache's statsCache and readdirCache, keyed by full path. A listed entry is also the stat of its path (addToStatCache), so the walk shares one Dirent between them, and a later single-literal step renames it, as Node's does.
	stats    map[string]*globDirent
	listings map[string][]*globDirent
}

// globDirent is a Dirent: the name, which a single-literal step may change, and the type bits of an lstat.
type globDirent struct {
	name string
	mode os.FileMode
}

// stat is Cache.statSync: the cached Dirent of full, or a new one from lstat, or nil when lstat fails, which is not cached.
func (w *packageGlobWalk) stat(full string) *globDirent {
	if d := w.stats[full]; d != nil {
		return d
	}
	info, err := os.Lstat(full)
	if err != nil {
		return nil
	}
	d := &globDirent{name: filepath.Base(full), mode: info.Mode()}
	w.stats[full] = d
	return d
}

// readdir is Cache.readdirSync: the cached listing of full in Node's readdir order, empty when it cannot be read.
func (w *packageGlobWalk) readdir(full string) []*globDirent {
	if listing, ok := w.listings[full]; ok {
		return listing
	}
	entries, _ := nodefs.ReadDir(full)
	listing := make([]*globDirent, 0, len(entries))
	for _, entry := range entries {
		listing = append(listing, &globDirent{name: entry.Name(), mode: entry.Type()})
	}
	w.listings[full] = listing
	return listing
}

// resolve is path.resolve(root, p).
func (w *packageGlobWalk) resolve(p string) string {
	switch {
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	case strings.HasPrefix(p, "/") || runtime.GOOS == "windows" && strings.HasPrefix(p, `\`):
		return filepath.Join(filepath.VolumeName(w.root), p)
	}
	return filepath.Join(w.root, p)
}

// join is path.join for a walk path.
func join(base, name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(base, name)
	}
	//portlint:allow pathseparators glob walk paths are Node path.join, which is posix.join outside Windows; the Windows branch above uses filepath.Join as win32.join
	return path.Join(base, name)
}

func (w *packageGlobWalk) isSeen(path string, pattern packageGlobPattern, index int) bool {
	return w.seen[packageGlobKey{path, pattern.key(index)}]
}

// cacheAdd is Cache.add: it records every index of pattern at path and reports whether any was recorded already.
func (w *packageGlobWalk) cacheAdd(path string, pattern packageGlobPattern) bool {
	added := 0
	for _, index := range pattern.indexes {
		if key := (packageGlobKey{path, pattern.key(index)}); !w.seen[key] {
			w.seen[key] = true
			added++
		}
	}
	return added != len(pattern.indexes)
}

func (w *packageGlobWalk) addSubpattern(path string, pattern packageGlobPattern) {
	if i, ok := w.subpatternAt[path]; ok {
		w.subpatterns[i].patterns = append(w.subpatterns[i].patterns, pattern)
		return
	}
	w.subpatternAt[path] = len(w.subpatterns)
	w.subpatterns = append(w.subpatterns, packageGlobQueued{path, []packageGlobPattern{pattern}})
}

// addSubpatterns is Glob#addSubpatterns for path and pattern. Without followSymlinks, Node treats a symlink that the walk reaches as a directory and lists it, and never adds a globstar's own index or the segment after it for a symlink it lists.
func (w *packageGlobWalk) addSubpatterns(path string, pattern packageGlobPattern) {
	if w.cacheAdd(path, pattern) {
		return
	}
	fullpath := w.resolve(path)
	stat := w.stat(fullpath)
	isDirectory := stat != nil && (stat.mode.IsDir() || stat.mode&os.ModeSymlink != 0 && pattern.hasSeenSymlinks())
	last := pattern.last()
	isLast := pattern.has(last) || pattern.literalIs(last, "") && isDirectory && pattern.has(last-1) && pattern.isGlobstar(last-1)
	if pattern.has(0) {
		first, _ := pattern.segment(0)
		switch {
		case first.isLiteral && runtime.GOOS == "windows" && strings.HasSuffix(first.literal, ":"):
			w.addSubpattern(first.literal+`\`, pattern.child([]int{1}, nil))
			return
		case first.isLiteral && first.literal == "":
			w.addSubpattern("/", pattern.child([]int{1}, nil))
			return
		case first.isLiteral && first.literal == "..":
			w.addSubpattern("../", pattern.child([]int{1}, nil))
			return
		case first.isLiteral && first.literal == ".":
			w.addSubpattern(".", pattern.child([]int{1}, nil))
			return
		}
	}
	if final, _ := pattern.segment(last); isLast && final.isLiteral {
		if w.stat(filepath.Join(fullpath, final.literal)) != nil && (final.literal != "" || isDirectory) {
			w.results[join(path, final.literal)] = true
		}
		if len(pattern.indexes) == 1 && pattern.has(last) {
			return
		}
	} else if isLast && final.globstar && (path != "." || pattern.literalIs(0, ".") || last == 0 && stat != nil) {
		w.results[path] = true
	}
	if !isDirectory {
		return
	}
	var children []*globDirent
	if only, ok := pattern.segment(pattern.indexes[0]); len(pattern.indexes) == 1 && ok && only.isLiteral {
		// A single literal pending: the child is the path the literal names, spelled as the pattern spells it. Its Dirent may be one that a listing holds, which then shows the new name.
		child := w.stat(filepath.Join(fullpath, only.literal))
		if child == nil {
			return
		}
		child.name = only.literal
		children = []*globDirent{child}
	} else {
		children = w.readdir(fullpath)
	}
	for _, entry := range children {
		entryPath := join(path, entry.name)
		w.stats[filepath.Join(fullpath, entry.name)] = entry
		entryIsDirectory := entry.mode.IsDir()
		isSymlink := entry.mode&os.ModeSymlink != 0
		var subPatterns, symlinks []int
		for _, index := range pattern.indexes {
			next := index + 1
			fromSymlink := slices.Contains(pattern.symlinks, index)
			if pattern.isGlobstar(index) {
				isDot := strings.HasPrefix(entry.name, ".")
				nextMatches := pattern.test(next, entry.name)
				nextNonGlob := next
				for pattern.isGlobstar(nextNonGlob) {
					nextNonGlob++
				}
				if isDot && !pattern.test(nextNonGlob, entry.name) {
					continue
				}
				if !fromSymlink && entryIsDirectory {
					subPatterns = addGlobIndex(subPatterns, index)
				} else if !fromSymlink && index == last {
					w.results[entryPath] = true
				}
				if nextMatches && next == last && !isLast {
					w.results[entryPath] = true
				} else if nextMatches && entryIsDirectory {
					subPatterns = addGlobIndex(subPatterns, index+2)
				}
				if (nextMatches || pattern.literalIs(0, ".")) && entryIsDirectory && !fromSymlink {
					subPatterns = addGlobIndex(subPatterns, next)
				}
				if isSymlink {
					symlinks = addGlobIndex(symlinks, index)
				}
				if pattern.literalIs(next, "..") && entryIsDirectory {
					w.addGlobstarParent(path, pattern, next)
				}
			}
			if current, _ := pattern.segment(index); current.isLiteral {
				if pattern.test(index, entry.name) && index != last {
					subPatterns = addGlobIndex(subPatterns, next)
				} else if current.literal == "." && pattern.test(next, entry.name) {
					if next == last {
						w.results[entryPath] = true
					} else {
						subPatterns = addGlobIndex(subPatterns, next+1)
					}
				}
			} else if !current.globstar && pattern.test(index, entry.name) {
				if index == last {
					w.results[entryPath] = true
				} else if entryIsDirectory {
					subPatterns = addGlobIndex(subPatterns, next)
				}
			}
		}
		if len(subPatterns) > 0 {
			w.addSubpattern(entryPath, pattern.child(subPatterns, symlinks))
		}
	}
}

// addGlobstarParent is the "**/.." step of Glob#addSubpatterns, where index is the index of the "..": both path and its parent go on with the segment after it, or are matches when it is the last.
func (w *packageGlobWalk) addGlobstarParent(path string, pattern packageGlobPattern, index int) {
	parent := join(path, "..")
	if index < pattern.last() {
		for _, dir := range []string{path, parent} {
			if _, pending := w.subpatternAt[dir]; !pending && !w.isSeen(dir, pattern, index+1) {
				w.addSubpattern(dir, pattern.child([]int{index + 1}, nil))
			}
		}
		return
	}
	if !w.isSeen(path, pattern, index) {
		w.cacheAdd(path, pattern.child([]int{index}, nil))
		w.results[path] = true
	}
	if !w.isSeen(path, pattern, index) || !w.isSeen(parent, pattern, index) {
		w.cacheAdd(parent, pattern.child([]int{index}, nil))
		w.results[parent] = true
	}
}

// addGlobIndex adds index to an insertion-ordered set.
func addGlobIndex(set []int, index int) []int {
	if slices.Contains(set, index) {
		return set
	}
	return append(set, index)
}
