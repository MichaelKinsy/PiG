// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// bullet is one "### Fixed" list item.
type bullet struct {
	// Fix lists the commits a "<!-- fix: <commit>, ... -->" marker at the end of the bullet names as its fix. The marker is how a note that was written after the fact points at the commit that fixed the bug.
	Fix []string
	// Cause is the root cause a "<!-- cause: <text> -->" marker states. It is the entry's root cause as written, whatever the fix commits say.
	Cause string
	// Tests are the regression tests a "<!-- tests: <test>[, <test>...] -->" marker names. They are the entry's tests, whatever the fix commits add or name.
	Tests []string
	// File is the file that holds the bullet, relative to the repository root: CHANGELOG.md or a fragment of changelog.d.
	File string
	// Section is the changelog heading the bullet sits under.
	Section string
	// Added reports whether the bullet's first line was added by the diff being scanned.
	Added bool
	Text  string
	// Version is the changelog heading that holds it; "Unreleased" for a fragment in changelog.d.
	Version string
}

var (
	versionHeading = regexp.MustCompile(`^## \[([^\]]+)\]`)
	sectionHeading = regexp.MustCompile(`^### (.+?)\s*$`)
	spaceRun       = regexp.MustCompile(`\s+`)
)

var pinConstRE = regexp.MustCompile(`(?m)^const UpstreamVersion = "([^"]+)"`)

// The markers a bullet may end with. They are HTML comments, so the rendered changelog does not show them, and the bullet's text is the same with and without them.
var (
	fixMarker   = regexp.MustCompile(`\s*<!--\s*fix:\s*([0-9a-f][0-9a-f ,]*?)\s*-->`)
	causeMarker = regexp.MustCompile(`\s*<!--\s*cause:\s*(.+?)\s*-->`)
	testsMarker = regexp.MustCompile(`\s*<!--\s*tests:\s*(.+?)\s*-->`)
)

// replaceOutsideCodeSpans applies fn to each segment of s outside Markdown code spans, keeping code spans verbatim.
func replaceOutsideCodeSpans(s string, fn func(string) string) string {
	if !strings.Contains(s, "`") {
		return fn(s)
	}
	var b strings.Builder
	b.Grow(len(s))
	plain := 0
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		start := i
		for i < len(s) && s[i] == '`' {
			i++
		}
		delim := i - start
		for j := i; j < len(s); {
			if s[j] != '`' {
				j++
				continue
			}
			k := j
			for k < len(s) && s[k] == '`' {
				k++
			}
			if k-j == delim {
				b.WriteString(fn(s[plain:start]))
				b.WriteString(s[start:k])
				i, plain = k, k
				break
			}
			j = k
		}
	}
	b.WriteString(fn(s[plain:]))
	return b.String()
}

// splitFixMarker removes every fix marker from a bullet outside Markdown code spans and returns the commits they name.
func splitFixMarker(s string) (string, []string) {
	var fix []string
	out := replaceOutsideCodeSpans(s, func(seg string) string {
		for _, m := range fixMarker.FindAllStringSubmatch(seg, -1) {
			for h := range strings.FieldsFuncSeq(m[1], func(r rune) bool { return r == ',' || r == ' ' }) {
				fix = append(fix, h)
			}
		}
		return fixMarker.ReplaceAllString(seg, "")
	})
	return out, fix
}

// splitStated removes the cause and tests markers from a bullet outside Markdown code spans and returns what they state. Of several cause markers the first counts.
func splitStated(s string) (text, cause string, tests []string) {
	out := replaceOutsideCodeSpans(s, func(seg string) string {
		if cause == "" {
			if m := causeMarker.FindStringSubmatch(seg); m != nil {
				cause = normalizeBullet(m[1])
			}
		}
		for _, m := range testsMarker.FindAllStringSubmatch(seg, -1) {
			tests = append(tests, testList(m[1])...)
		}
		return testsMarker.ReplaceAllString(causeMarker.ReplaceAllString(seg, ""), "")
	})
	return out, cause, tests
}

func normalizeBullet(s string) string { return strings.TrimSpace(spaceRun.ReplaceAllString(s, " ")) }

// versionAfter reports whether changelog heading v names a release newer than since, or the unreleased section.
func versionAfter(v, since string) bool {
	if v == "Unreleased" {
		return true
	}
	a, b := semver(v), semver(since)
	if a == nil || b == nil {
		return false
	}
	for i := range 3 {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func semver(v string) []int {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// scanner walks a changelog file line by line and yields the "### Fixed" bullets of the versions after since. A bullet that continues on indented lines is joined.
type scanner struct {
	// sections names the "###" sections to read; nil reads only Fixed.
	sections map[string]bool
	since    string
	version  string
	section  string
	cur      *strings.Builder
	out      []bullet
	curVer   string
	curAdd   bool
	curSec   string
}

func (s *scanner) wants(section string) bool {
	if s.sections == nil {
		return section == "Fixed"
	}
	return s.sections[section]
}

func (s *scanner) flush() {
	if s.cur != nil {
		text, fix := splitFixMarker(s.cur.String())
		text, cause, tests := splitStated(text)
		text = stripHTMLComments(text)
		s.out = append(s.out, bullet{Text: normalizeBullet(text), Fix: fix, Cause: cause, Tests: tests, Version: s.curVer, Added: s.curAdd, Section: s.curSec})
		s.cur = nil
	}
}

func (s *scanner) line(l string, fragment, added bool) {
	if m := versionHeading.FindStringSubmatch(l); m != nil && !fragment {
		s.flush()
		s.version, s.section = m[1], ""
		return
	}
	if m := sectionHeading.FindStringSubmatch(l); m != nil {
		s.flush()
		s.section = m[1]
		return
	}
	if strings.HasPrefix(l, "## ") || strings.HasPrefix(l, "# ") {
		s.flush()
		s.section = ""
		return
	}
	if !s.wants(s.section) || (!fragment && !versionAfter(s.version, s.since)) {
		s.flush()
		return
	}
	switch {
	case strings.HasPrefix(l, "- "):
		s.flush()
		s.cur = &strings.Builder{}
		s.cur.WriteString(strings.TrimPrefix(l, "- "))
		s.curVer, s.curAdd, s.curSec = s.version, added, s.section
		if fragment {
			s.curVer = "Unreleased"
		}
	case s.cur != nil && strings.TrimSpace(l) != "" && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")):
		s.cur.WriteString(" " + strings.TrimSpace(l))
	default:
		s.flush()
	}
}

func fixedBullets(text, since string, fragment bool) []bullet {
	return sectionBullets(text, since, fragment, nil)
}

// sectionBullets reads the bullets of the named changelog sections; nil names only Fixed.
func sectionBullets(text, since string, fragment bool, sections map[string]bool) []bullet {
	s := &scanner{since: since, sections: sections}
	for l := range strings.SplitSeq(text, "\n") {
		s.line(l, fragment, false)
	}
	s.flush()
	return s.out
}

// currentFixed reads the "### Fixed" bullets of the working tree: the releases after since in CHANGELOG.md, and every fragment in changelog.d.
func currentFixed(root, since string) ([]bullet, error) {
	var out []bullet
	read := func(rel string, fragment bool) error {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		for _, b := range fixedBullets(string(data), since, fragment) {
			b.File = rel
			out = append(out, b)
		}
		return nil
	}
	if err := read("CHANGELOG.md", false); err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(root, "changelog.d", "*.md"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := read("changelog.d/"+filepath.Base(f), true); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// intro is the commit that first added a bullet and whether it added it to a changelog.d fragment.
type intro struct {
	Hash     string
	Fragment bool
}

// introductions maps each "### Fixed" bullet ever added to the changelog files after base to the first commit that added it. A bullet that the fold of a fragment moves into CHANGELOG.md keeps its first commit. The slice lists the bullets in the order of their first commits.
func introductions(r repo, base, since string) (map[string]intro, []string, error) {
	scan := &introScan{since: since, first: map[string]intro{}}
	// Bullets a merge brings in, such as the changelog of a pull request merged from the public repository, are found in a second pass; a bullet a commit added keeps that commit.
	for _, mode := range [][]string{{"--no-merges"}, {"--merges", "-m", "--first-parent"}} {
		args := append([]string{"log", "--reverse", "-U100000", "--format=@@%H", "-p"}, mode...)
		if err := r.gitLines(append(args, base+"..HEAD", "--", "CHANGELOG.md", "changelog.d"), scan.line); err != nil {
			return nil, nil, err
		}
		scan.record()
	}
	return scan.first, scan.order, nil
}

// introScan reads the patch text of git log -p one line at a time and records the first commit that added each bullet.
type introScan struct {
	since    string
	first    map[string]intro
	order    []string
	hash     string
	file     *scanner
	fragment bool
}

// record ends the file being read and records the bullets it added that no earlier commit did.
func (s *introScan) record() {
	if s.file == nil {
		return
	}
	s.file.flush()
	for _, b := range s.file.out {
		if _, seen := s.first[b.Text]; b.Added && !seen {
			s.first[b.Text] = intro{Hash: s.hash, Fragment: s.fragment}
			s.order = append(s.order, b.Text)
		}
	}
	s.file = nil
}

func (s *introScan) line(l string) {
	switch {
	case strings.HasPrefix(l, "@@") && len(l) == 42:
		s.record()
		s.hash = l[2:]
	case strings.HasPrefix(l, "diff --git "):
		s.record()
	case strings.HasPrefix(l, "+++ "):
		path := strings.TrimPrefix(l, "+++ b/")
		if path != "/dev/null" && !strings.HasPrefix(l, "+++ /dev") {
			s.file = &scanner{since: s.since}
			s.fragment = strings.HasPrefix(path, "changelog.d/")
		}
	case s.file != nil && l != "" && (l[0] == ' ' || l[0] == '+') && !strings.HasPrefix(l, "+++ "):
		s.file.line(l[1:], s.fragment, l[0] == '+')
	}
}
