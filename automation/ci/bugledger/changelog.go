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

var fixMarker = regexp.MustCompile(`\s*<!--\s*fix:\s*([0-9a-f][0-9a-f ,]*?)\s*-->`)

// splitFixMarker removes every fix marker from a bullet and returns the commits they name.
func splitFixMarker(s string) (string, []string) {
	var fix []string
	for _, m := range fixMarker.FindAllStringSubmatch(s, -1) {
		for h := range strings.FieldsFuncSeq(m[1], func(r rune) bool { return r == ',' || r == ' ' }) {
			fix = append(fix, h)
		}
	}
	return fixMarker.ReplaceAllString(s, ""), fix
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
		s.out = append(s.out, bullet{Text: normalizeBullet(text), Fix: fix, Version: s.curVer, Added: s.curAdd, Section: s.curSec})
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
	data, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		return nil, err
	}
	out = append(out, fixedBullets(string(data), since, false)...)
	files, err := filepath.Glob(filepath.Join(root, "changelog.d", "*.md"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		out = append(out, fixedBullets(string(data), since, true)...)
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
	first := map[string]intro{}
	var order []string
	// Bullets a merge brings in, such as the changelog of a pull request merged from the public repository, are found in a second pass; a bullet a commit added keeps that commit.
	for _, mode := range [][]string{{"--no-merges"}, {"--merges", "-m", "--first-parent"}} {
		args := append([]string{"log", "--reverse", "-U100000", "--format=@@%H", "-p"}, mode...)
		out, err := r.git(append(args, base+"..HEAD", "--", "CHANGELOG.md", "changelog.d")...)
		if err != nil {
			return nil, nil, err
		}
		order = scanIntroductions(out, since, first, order)
	}
	return first, order, nil
}

func scanIntroductions(out, since string, first map[string]intro, order []string) []string {
	var hash string
	var file *scanner
	fragment := false
	record := func() {
		if file == nil {
			return
		}
		file.flush()
		for _, b := range file.out {
			if _, seen := first[b.Text]; b.Added && !seen {
				first[b.Text] = intro{Hash: hash, Fragment: fragment}
				order = append(order, b.Text)
			}
		}
		file = nil
	}
	for l := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(l, "@@") && len(l) == 42:
			record()
			hash = l[2:]
		case strings.HasPrefix(l, "diff --git "):
			record()
		case strings.HasPrefix(l, "+++ "):
			path := strings.TrimPrefix(l, "+++ b/")
			if path != "/dev/null" && !strings.HasPrefix(l, "+++ /dev") {
				file = &scanner{since: since}
				fragment = strings.HasPrefix(path, "changelog.d/")
			}
		case file != nil && l != "" && (l[0] == ' ' || l[0] == '+') && !strings.HasPrefix(l, "+++ "):
			file.line(l[1:], fragment, l[0] == '+')
		}
	}
	record()
	return order
}
