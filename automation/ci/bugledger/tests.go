// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
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

// maxCommitFiles is the most files one commit may touch and still be a commit whose tests the ledger attributes to a fix. A commit above it is a squash or a merge of many changes: its tests are the tests of everything it carries, not of one fix, and crediting them to an entry made the 0.5.0 squash (13,725 files, about 11,000 tests) the "fix" of every release note that named no commit of its own.
//
// The threshold sits between the two populations of the repository's history. Counting every file a commit touches (git show --name-only --no-renames; the first parent for a merge), the largest fix-titled commit is 246 files (492afa2b, "Fix package skills directories") and the largest fix the ledger lists under another title is 237 (b341a82e, "go install works again", which adds 235 pinned ledger files); no other fix-titled commit is above 106. Every release or content squash above 500 is far larger: 799 (a8c5ae1b, Pi 1.0.3 content), 3,572 (0.2.0), 3,968 (0.4.0), 8,439 (0.3.0) and 13,725 (0.5.0). 500 is twice the largest legitimate fix and below the smallest of those squashes, so a legitimate fix does not trip it and a release squash always does. A feature pull request squash may sit between the two (f91b5a54, 400 files); it is not a fix, so it names no entry.
const maxCommitFiles = 500

// testIndex lists, for every Go test function in the tree under root, the files that declare it.
type testIndex map[string][]string

// movedTestsPath lists the tests a later commit renamed or moved away from the file a fix commit added them to, one per line.
const movedTestsPath = "automation/ci/bugledger/moved-tests.txt"

// movedTests maps a test reference a fix commit adds ("old-file::OldName") to the reference the tree declares it under.
type movedTests map[string]string

// movedLine is one line of moved-tests.txt.
type movedLine struct{ from, to string }

// readMovedTests reads moved-tests.txt: "<old-file>::<OldName> <new-file>::<NewName>" lines, with # comments and blank lines. A moved test that keeps its name needs no line when exactly one file in the tree declares it (see resolveTest); a renamed one, or a name the tree declares more than once, does. The file may be absent.
func readMovedTests(root string) ([]movedLine, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(movedTestsPath)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lines []movedLine
	for l := range strings.SplitSeq(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		fields := strings.Fields(l)
		if len(fields) != 2 || !strings.Contains(fields[0], "::") || !strings.Contains(fields[1], "::") {
			return nil, fmt.Errorf("%s: %q must be \"<old-file>::<OldName> <new-file>::<NewName>\"", movedTestsPath, l)
		}
		lines = append(lines, movedLine{from: fields[0], to: fields[1]})
	}
	return lines, nil
}

func loadMovedTests(root string) (movedTests, error) {
	lines, err := readMovedTests(root)
	if err != nil {
		return nil, err
	}
	moved := movedTests{}
	for _, l := range lines {
		moved[l.from] = l.to
	}
	return moved, nil
}

// checkMovedTests reports the lines of moved-tests.txt that no longer say anything true: a line whose new test the tree does not declare, whose old test the tree still declares (nothing moved), or whose old test an earlier line already maps.
func checkMovedTests(root string) []string {
	lines, err := readMovedTests(root)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	seen := map[string]bool{}
	for _, l := range lines {
		switch {
		case seen[l.from]:
			problems = append(problems, fmt.Sprintf("%s: %s is mapped twice", movedTestsPath, l.from))
		case !testExists(root, l.to):
			problems = append(problems, fmt.Sprintf("%s: %s names the test %s, which does not exist", movedTestsPath, l.from, l.to))
		case testExists(root, l.from):
			problems = append(problems, fmt.Sprintf("%s: %s still exists, so %s is a stale line", movedTestsPath, l.from, l.to))
		}
		seen[l.from] = true
	}
	return problems
}

// resolveTest names where the tree declares the test name that a commit declared or edited in file. A test file the tree still declares it in keeps it; a test that moved is the one moved-tests.txt maps, or else the one file that declares a test of that name.
func resolveTest(root string, idx testIndex, moved movedTests, file, name string) (string, bool) {
	if declares(idx, name, file) {
		return file + "::" + name, true
	}
	if to, ok := moved[file+"::"+name]; ok && testExists(root, to) {
		return to, true
	}
	if files := idx[name]; len(files) == 1 {
		return files[0] + "::" + name, true
	}
	return "", false
}

// recordedTests returns the tests a committed entry records that the tree still has: a test the tree no longer declares follows moved-tests.txt to the test it became, and is dropped when no line names it or its target is missing too. A port that retires or renames a test no longer names anything, and the entry keeps the guard only through a line the reviewer wrote.
func recordedTests(root string, moved movedTests, recorded []string) []string {
	out := make([]string, 0, len(recorded))
	followed := false
	for _, t := range recorded {
		if !testExists(root, t) {
			to, ok := moved[t]
			if !ok || !testExists(root, to) {
				continue
			}
			t, followed = to, true
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if followed {
		slices.Sort(out)
	}
	return out
}

// movedByName returns where moved-tests.txt sends a test a commit message names that no file declares any more.
func movedByName(root string, moved movedTests, name string) (string, bool) {
	for from, to := range moved {
		if strings.HasSuffix(from, "::"+name) && testExists(root, to) {
			return to, true
		}
	}
	return "", false
}

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
func testsOfCommit(r repo, idx testIndex, moved movedTests, hash, message string) ([]string, error) {
	var out []string
	add := func(s string) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	var file string
	touched := map[string]bool{}
	var addedFiles []string
	var newFile bool
	// hunkTest is the reference of the test function whose body a hunk of a test file edits: git names the nearest preceding function in the hunk header, and the first added line must be indented to count as inside it.
	hunkTest := ""
	var edited []string
	// The diff is read as git prints it: a commit that adds large generated files is a gigabyte of patch text.
	err := r.gitLines([]string{"show", "--format=", "-U0", "--no-renames", hash}, func(l string) {
		switch {
		case strings.HasPrefix(l, "@@ "):
			hunkTest = ""
			if _, ctx, ok := strings.Cut(strings.TrimPrefix(l, "@@ "), " @@"); ok && strings.HasSuffix(file, "_test.go") {
				if m := goTestDecl.FindStringSubmatch(strings.TrimSpace(ctx)); m != nil {
					hunkTest, _ = resolveTest(r.root, idx, moved, file, m[1])
				}
			}
		case hunkTest != "" && strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			if strings.HasPrefix(l, "+\t") && !slices.Contains(edited, hunkTest) {
				edited = append(edited, hunkTest)
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
				if ref, ok := resolveTest(r.root, idx, moved, file, m[1]); ok {
					add(ref)
				}
			}
		}
	})
	if err != nil {
		return nil, err
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
			if to, ok := movedByName(r.root, moved, m[1]); ok {
				add(to)
			}
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
