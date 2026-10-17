// SPDX-License-Identifier: MIT

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	goTestDecl    = regexp.MustCompile(`(?m)^func ((?:Test|Fuzz)[A-Za-z0-9_]*)\(`)
	goTestMention = regexp.MustCompile(`\b((?:Test|Fuzz)[A-Z][A-Za-z0-9_]*)\b`)
	scenarioRef   = regexp.MustCompile(`\b([a-z][a-z0-9-]*)/(\d\d+-[a-z0-9-]+)\b`)
	scenarioFile  = regexp.MustCompile(`^test/parity/scenarios/[^/]+/[^/]+\.toml$`)
	otherTest     = regexp.MustCompile(`(\.test\.(mjs|ts|js)|_test\.py|(^|/)test_[^/]+\.py)$`)
)

// maxEditedTests is the most tests a commit may edit in place before the edits stop counting as the fix's guards.
const maxEditedTests = 5

// testIndex lists, for every Go test function in the tree under root, the files that declare it.
type testIndex map[string][]string

func buildTestIndex(root string) (testIndex, error) {
	idx := testIndex{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "target", ".upstream":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for _, m := range goTestDecl.FindAllSubmatch(data, -1) {
			name := string(m[1])
			idx[name] = append(idx[name], rel)
		}
		return nil
	})
	for _, files := range idx {
		slices.Sort(files)
	}
	return idx, err
}

// testsOfCommit returns the tests a fix commit adds or names, resolved against the current tree: Go tests the diff adds, Go tests and parity scenarios the message names, and test files the commit adds.
func testsOfCommit(r repo, idx testIndex, hash, message string) ([]string, error) {
	var out []string
	add := func(s string) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	diff, err := r.git("show", "--format=", "-U0", "--no-renames", hash)
	if err != nil {
		return nil, err
	}
	var file string
	touched := map[string]bool{}
	var addedFiles []string
	var newFile bool
	// hunkTest is the test function whose body a hunk of a test file edits: git names the nearest preceding function in the hunk header, and the first added line must be indented to count as inside it.
	hunkTest := ""
	var edited []string
	for l := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "@@ "):
			hunkTest = ""
			if _, ctx, ok := strings.Cut(strings.TrimPrefix(l, "@@ "), " @@"); ok && strings.HasSuffix(file, "_test.go") {
				if m := goTestDecl.FindStringSubmatch(strings.TrimSpace(ctx)); m != nil && declares(idx, m[1], file) {
					hunkTest = m[1]
				}
			}
		case hunkTest != "" && strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			if strings.HasPrefix(l, "+\t") && !slices.Contains(edited, file+"::"+hunkTest) {
				edited = append(edited, file+"::"+hunkTest)
			}
			hunkTest = ""
		case strings.HasPrefix(l, "diff --git "):
			newFile = false
		case strings.HasPrefix(l, "new file mode"):
			newFile = true
		case strings.HasPrefix(l, "+++ b/"):
			file = strings.TrimPrefix(l, "+++ b/")
			touched[file] = true
			if newFile {
				addedFiles = append(addedFiles, file)
			}
		case strings.HasPrefix(l, "+func ") && strings.HasSuffix(file, "_test.go"):
			if m := goTestDecl.FindStringSubmatch(l[1:]); m != nil {
				if declares(idx, m[1], file) {
					add(file + "::" + m[1])
				}
			}
		}
	}
	// A commit that edits many tests in place is a sweep, not a guard for one bug.
	if len(edited) <= maxEditedTests {
		for _, e := range edited {
			add(e)
		}
	}
	for _, f := range addedFiles {
		if (scenarioFile.MatchString(f) || otherTest.MatchString(f)) && exists(r.root, f) {
			add(f)
		}
	}
	for _, m := range goTestMention.FindAllStringSubmatch(message, -1) {
		files := idx[m[1]]
		if len(files) == 0 {
			continue
		}
		pick := files[0]
		for _, f := range files {
			if touched[f] {
				pick = f
				break
			}
		}
		add(pick + "::" + m[1])
	}
	for _, m := range scenarioRef.FindAllStringSubmatch(message, -1) {
		p := "test/parity/scenarios/" + m[1] + "/" + m[2] + ".toml"
		if exists(r.root, p) {
			add(p)
		}
	}
	slices.Sort(out)
	return out, nil
}

func declares(idx testIndex, name, file string) bool { return slices.Contains(idx[name], file) }

func exists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// testExists reports whether a recorded test reference still resolves in the tree under root.
func testExists(root string, ref string) bool {
	path, name, hasName := strings.Cut(ref, "::")
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return false
	}
	if !hasName {
		return true
	}
	for _, m := range goTestDecl.FindAllSubmatch(data, -1) {
		if string(m[1]) == name {
			return true
		}
	}
	return false
}
