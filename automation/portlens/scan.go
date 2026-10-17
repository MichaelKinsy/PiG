// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Site is one source location found in the tree.
type Site struct {
	File string
	Line int
}

// treeScan is the result of one walk over the repository's source files.
type treeScan struct {
	// Markers maps "D12" to the lines that carry a `pig divergence (D<N>)` or `pig additive (D<N>)` marker.
	Markers map[string][]Site
	// Tests maps a Go test, fuzz or benchmark function name to its declarations.
	Tests map[string][]Site
	// Scenarios is the set of parity scenario ids ("family/name") that exist.
	Scenarios map[string]string
}

var (
	markerRe   = regexp.MustCompile(`pig (?:divergence|additive)\s*\(([^)]*)\)`)
	testDeclRe = regexp.MustCompile(`^func ((?:Test|Fuzz|Benchmark|Example)\w*)\(`)
	skipDirs   = map[string]bool{".git": true, "node_modules": true, ".upstream": true, "third_party": true, "vendor": true, "build": true, "tmp": true, "bin": true, "dist": true, "target": true, "__pycache__": true}
)

// scanTree walks the repository once for divergence markers, test declarations and scenario ids.
func scanTree(root string) (*treeScan, error) {
	ts := &treeScan{Markers: map[string][]Site{}, Tests: map[string][]Site{}, Scenarios: map[string]string{}}
	scen := filepath.Join(root, "test", "parity", "scenarios")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if skipDirs[d.Name()] || rel == "automation/portlens" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(p, scen+string(filepath.Separator)) && strings.HasSuffix(p, ".toml") {
			id := strings.TrimSuffix(strings.TrimPrefix(filepath.ToSlash(p[len(scen)+1:]), "/"), ".toml")
			ts.Scenarios[id] = rel
			return nil
		}
		switch filepath.Ext(p) {
		case ".go", ".rs", ".py", ".mjs", ".ts":
		default:
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		hasMarker := bytes.Contains(b, []byte("pig divergence")) || bytes.Contains(b, []byte("pig additive"))
		isTest := strings.HasSuffix(p, "_test.go")
		if !hasMarker && !isTest {
			return nil
		}
		for i, ln := range strings.Split(string(b), "\n") {
			if hasMarker {
				for _, m := range markerRe.FindAllStringSubmatch(ln, -1) {
					for _, d := range dRef.FindAllString(m[1], -1) {
						ts.Markers[d] = append(ts.Markers[d], Site{rel, i + 1})
					}
				}
			}
			if isTest {
				if m := testDeclRe.FindStringSubmatch(ln); m != nil {
					ts.Tests[m[1]] = append(ts.Tests[m[1]], Site{rel, i + 1})
				}
			}
		}
		return nil
	})
	for k := range ts.Markers {
		sort.Slice(ts.Markers[k], func(i, j int) bool {
			a, b := ts.Markers[k][i], ts.Markers[k][j]
			if a.File != b.File {
				return a.File < b.File
			}
			return a.Line < b.Line
		})
	}
	return ts, err
}

// piSource finds the cited files of the pinned Pi tree.
type piSource struct {
	root string
	// byBase maps a file's base name to its slash paths under the Pi root.
	byBase map[string][]string
	lines  map[string][]string
}

func newPiSource(root string) *piSource {
	ps := &piSource{root: root, byBase: map[string][]string{}, lines: map[string][]string{}}
	if root == "" {
		return ps
	}
	pk := filepath.Join(root, "packages")
	_ = filepath.WalkDir(pk, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == "dist" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(p) {
		case ".ts", ".tsx", ".mts", ".js", ".mjs", ".rs":
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			ps.byBase[d.Name()] = append(ps.byBase[d.Name()], rel)
		}
		return nil
	})
	for _, v := range ps.byBase {
		sort.Strings(v)
	}
	return ps
}

func (ps *piSource) available() bool { return len(ps.byBase) > 0 }

func (ps *piSource) file(rel string) []string {
	if l, ok := ps.lines[rel]; ok {
		return l
	}
	b, err := os.ReadFile(filepath.Join(ps.root, filepath.FromSlash(rel)))
	var l []string
	if err == nil {
		l = strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	}
	ps.lines[rel] = l
	return l
}

// resolve picks the file a citation names: an exact or suffix path match, preferring a file long enough to hold the cited range.
func (ps *piSource) resolve(cite string, end int) (string, bool) {
	base := cite
	if i := strings.LastIndex(cite, "/"); i >= 0 {
		base = cite[i+1:]
	}
	var fits, any []string
	for _, c := range ps.byBase[base] {
		if c != cite && !strings.HasSuffix(c, "/"+cite) {
			continue
		}
		any = append(any, c)
		if len(ps.file(c)) >= end {
			fits = append(fits, c)
		}
	}
	if len(fits) > 0 {
		return fits[0], true
	}
	if len(any) > 0 {
		return any[0], true
	}
	return "", false
}

// Cite is one Pi file:line range named in a note.
type Cite struct {
	File       string
	Start, End int
}

var citeRe = regexp.MustCompile("`?([A-Za-z0-9_][A-Za-z0-9_./-]*\\.(?:tsx?|mts|mjs|js|rs)):(\\d+)(?:[-\u2013](\\d+))?`?")

// findCites lists the distinct Pi citations of a note in order of appearance. A citation is a TypeScript or JavaScript file name with a line or range.
func findCites(text string) []Cite {
	var out []Cite
	seen := map[Cite]bool{}
	for _, m := range citeRe.FindAllStringSubmatch(text, -1) {
		if strings.HasPrefix(m[1], "node_modules/") {
			continue
		}
		var s, e int
		atoi(m[2], &s)
		e = s
		if m[3] != "" {
			atoi(m[3], &e)
		}
		if s < 1 || e < s || e-s > 400 {
			continue
		}
		c := Cite{m[1], s, e}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func atoi(s string, v *int) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	*v = n
}
