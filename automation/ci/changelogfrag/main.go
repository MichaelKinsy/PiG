// SPDX-License-Identifier: MIT

// Command changelogfrag checks the user-facing changelog fragments in changelog.d, prints the Unreleased changelog
// they make up, and folds them into a release section of CHANGELOG.md.
//
// A lane writes one fragment per commit range, named <lane>-<sha>.md after the lane and the commit it describes, so two
// lanes never edit the same file. The fragment holds "### Added", "### Changed", "### Fixed" and similar sections of
// bullets in the style of CHANGELOG.md. The integrator folds the fragments into the release with -fold=<version>: each
// bullet is appended to its section of "## [<version>]", and the folded fragments and their legacy.txt lines are
// deleted, so a later fold takes only the fragments written since.
//
// -check also resolves commits against the history of HEAD (history.go): the commit in a fragment's name and in each
// fix marker must be reachable from HEAD, or named by a "Fix-Commit: <full sha>" trailer of a commit HEAD reaches (a
// squash commit carries one for each commit it replaced), and every bullet of a section the bug ledger reads ("### Fixed") needs a
// "<!-- fix: <sha> -->" marker, so the ledger never has to guess which commit fixed it. Fragments that predate these
// rules, whose commits a squash replaced, are listed in legacy.txt and are exempt from the name rule and from the
// reachability of their markers, not from the marker: the check and the fold both refuse an unmarked Fixed bullet.
package main

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/zstdblob"
)

//go:embed legacy.txt
var legacyList string

var (
	nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*-[0-9a-f]{7,40}\.md$`)

	// sections are the headings a fragment may use, in the order the assembly prints them. "Upgrading" holds what a user
	// of the previous release must change; the release names it "Upgrading from <previous>".
	sections = []string{"Upgrading", "Added", "Changed", "Deprecated", "Removed", "Fixed", "Security", "Known issues"}
)

type fragment struct {
	name  string
	items map[string][]string
	// legacy marks a fragment listed in legacy.txt; the history checks skip it.
	legacy bool
	// unsorted is the text of a legacy fragment that has no "### <section>" structure; the assembly prints it under
	// "### Unsorted" so the integrator sorts it by hand instead of losing it.
	unsorted string
}

func legacy() map[string]bool {
	set := map[string]bool{}
	for line := range strings.SplitSeq(legacyList, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	return set
}

// parse reads one fragment: section headings, each followed by bullets whose wrapped lines are indented.
func parse(name, text string) (fragment, error) {
	f := fragment{name: name, items: map[string][]string{}}
	known := map[string]bool{}
	for _, s := range sections {
		known[s] = true
	}
	section, bullets := "", 0
	for n, line := range strings.Split(text, "\n") {
		switch {
		case strings.TrimSpace(line) == "":
		case strings.HasPrefix(line, "### "):
			section = strings.TrimSpace(strings.TrimPrefix(line, "### "))
			if !known[section] {
				return f, fmt.Errorf("%s:%d: section %q is not one of %s", name, n+1, section, strings.Join(sections, ", "))
			}
		case section == "":
			return f, fmt.Errorf("%s:%d: text before the first \"### <section>\" heading", name, n+1)
		case strings.HasPrefix(line, "- "):
			f.items[section] = append(f.items[section], strings.TrimPrefix(line, "- "))
			bullets++
		case line[0] == ' ' || line[0] == '\t':
			items := f.items[section]
			if len(items) == 0 {
				return f, fmt.Errorf("%s:%d: indented text outside a bullet", name, n+1)
			}
			items[len(items)-1] += "\n" + line
		default:
			return f, fmt.Errorf("%s:%d: %q is neither a bullet (\"- ...\") nor a heading", name, n+1, abbreviate(line))
		}
	}
	if bullets == 0 {
		return f, fmt.Errorf("%s: no bullet under any section", name)
	}
	return f, nil
}

func abbreviate(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

// load reads every fragment in dir. Fragments in the legacy list are exempt from the name and format rules; one that
// does not parse is assembled under "### Unsorted". A legacy entry whose file is gone is a problem: the list only shrinks.
// A missing dir holds no fragments: Git does not keep the directory a fold empties.
func load(dir string) ([]fragment, []error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, []error{err}
	}
	exempt := legacy()
	seen := map[string]bool{}
	var out []fragment
	var problems []error
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			problems = append(problems, fmt.Errorf("%s: only .md fragments belong in %s", name, dir))
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			problems = append(problems, err)
			continue
		}
		f, err := parse(name, string(data))
		if exempt[name] {
			seen[name] = true
			if err != nil {
				f = fragment{name: name, unsorted: strings.TrimSpace(string(data))}
			}
			f.legacy = true
			out = append(out, f)
			continue
		}
		if !nameRE.MatchString(name) {
			problems = append(problems, fmt.Errorf("%s: a fragment is named <lane>-<sha>.md (lowercase lane, 7 to 40 hex digits of the commit it describes)", name))
		}
		if err != nil {
			problems = append(problems, err)
			continue
		}
		out = append(out, f)
	}
	var stale []string
	for name := range exempt {
		if !seen[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		problems = append(problems, fmt.Errorf("legacy.txt lists %s, which %s no longer holds; delete the line", name, dir))
	}
	return out, problems
}

// render prints the Unreleased changelog: each section once, the bullets of every fragment in file name order.
func render(fragments []fragment) string {
	sort.Slice(fragments, func(i, j int) bool { return fragments[i].name < fragments[j].name })
	var b strings.Builder
	b.WriteString("## [Unreleased]\n")
	for _, section := range sections {
		var items []string
		for _, f := range fragments {
			for _, item := range f.items[section] {
				items = append(items, "- "+item)
			}
		}
		if len(items) > 0 {
			fmt.Fprintf(&b, "\n### %s\n\n%s\n", section, strings.Join(items, "\n"))
		}
	}
	var unsorted []string
	for _, f := range fragments {
		if f.unsorted != "" {
			unsorted = append(unsorted, "<!-- changelog.d/"+f.name+" -->\n"+f.unsorted)
		}
	}
	if len(unsorted) > 0 {
		fmt.Fprintf(&b, "\n### Unsorted\n\n%s\n", strings.Join(unsorted, "\n\n"))
	}
	return b.String()
}

// unreleasedEntries is the text under "## [Unreleased]" in changelog, empty when the section is empty or absent.
func unreleasedEntries(changelog string) string {
	const heading = "## [Unreleased]\n"
	start := strings.Index(changelog, heading)
	if start < 0 {
		return ""
	}
	body := changelog[start+len(heading):]
	if next := strings.Index(body, "\n## ["); next >= 0 {
		body = body[:next]
	}
	return strings.TrimSpace(body)
}

// fold appends the bullets of fragments to the sections of the "## [<version>]" release in changelog, in the order of
// sections; a release heading "Upgrading from <previous>" is the Upgrading section. A section the release lacks is added
// after the sections that precede it in that order; other headings keep their place. A fragment without sections cannot
// be placed and stops the fold. So does an entry under "## [Unreleased]": the fold moves fragments only, and an entry
// left there is missing from the release (`make set-version ... SET_VERSION_ARGS=--move-unreleased` moves it). Unreleased is
// not a fold target: its fragments are assembled by `make changelog-unreleased` and are never written to CHANGELOG.md.
func fold(changelog string, fragments []fragment, version string) (string, error) {
	if version == "Unreleased" {
		return "", errors.New("\"Unreleased\" is not a release: changelog-fold appends fragments to a release heading (make set-version VERSION=x.y.z creates it); make changelog-unreleased prints the fragments' assembly")
	}
	var unsorted []string
	for _, f := range fragments {
		if f.unsorted != "" {
			unsorted = append(unsorted, f.name)
		}
	}
	if len(unsorted) > 0 {
		return "", fmt.Errorf("give these fragments \"### <section>\" headings before the fold: %s", strings.Join(unsorted, ", "))
	}
	// Whatever the fragments' history says, a bullet the ledger reads must not reach CHANGELOG.md without its marker.
	if problems := unmarked(fragments); len(problems) > 0 {
		var msg []string
		for _, p := range problems {
			msg = append(msg, p.Error())
		}
		return "", fmt.Errorf("the fold would carry bullets without a fix marker into CHANGELOG.md:\n%s", strings.Join(msg, "\n"))
	}
	heading := regexp.MustCompile(`(?m)^## \[` + regexp.QuoteMeta(version) + `\]( - .*)?\n`).FindStringIndex(changelog)
	if heading == nil {
		return "", fmt.Errorf("CHANGELOG.md has no \"## [%s]\" heading", version)
	}
	if entries := unreleasedEntries(changelog); entries != "" {
		return "", fmt.Errorf("CHANGELOG.md still has entries under \"## [Unreleased]\"; the release would not carry them. Run make set-version VERSION=%s SET_VERSION_ARGS=--move-unreleased (add --release-modules on a release branch) first:\n%s", version, entries)
	}
	start := heading[1]
	end := len(changelog)
	if next := strings.Index(changelog[start:], "\n## ["); next >= 0 {
		end = start + next + 1
	}

	type block struct{ heading, body string }
	var blocks []block
	preamble := changelog[start:end]
	if first := strings.Index("\n"+preamble, "\n### "); first >= 0 {
		rest := preamble[first:]
		preamble = preamble[:first]
		for _, part := range strings.Split("\n"+rest, "\n### ")[1:] {
			title, body, _ := strings.Cut(part, "\n")
			blocks = append(blocks, block{title, strings.TrimSpace(body)})
		}
	}
	rank := map[string]int{}
	for i, s := range sections {
		rank[s] = i + 1
	}
	sectionOf := func(heading string) string {
		if strings.HasPrefix(heading, "Upgrading from ") {
			return "Upgrading"
		}
		return heading
	}
	sort.Slice(fragments, func(i, j int) bool { return fragments[i].name < fragments[j].name })
	added := false
	for _, section := range sections {
		var items []string
		for _, f := range fragments {
			for _, item := range f.items[section] {
				items = append(items, "- "+item)
			}
		}
		if len(items) == 0 {
			continue
		}
		added = true
		text := strings.Join(items, "\n")
		at := -1
		for i, b := range blocks {
			if sectionOf(b.heading) == section {
				at = i
				break
			}
		}
		if at >= 0 {
			if blocks[at].body != "" {
				text = blocks[at].body + "\n" + text
			}
			blocks[at].body = text
			continue
		}
		at = len(blocks)
		for i, b := range blocks {
			if rank[sectionOf(b.heading)] > rank[section] {
				at = i
				break
			}
		}
		blocks = append(blocks[:at], append([]block{{section, text}}, blocks[at:]...)...)
	}
	if !added {
		return changelog, nil
	}
	var b strings.Builder
	b.WriteString(changelog[:start])
	b.WriteString(preamble)
	if preamble == "" {
		b.WriteString("\n")
	}
	for i, blk := range blocks {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "### %s\n\n%s\n", blk.heading, blk.body)
	}
	if end < len(changelog) {
		b.WriteString("\n")
	}
	b.WriteString(changelog[end:])
	return b.String(), nil
}

// foldInto folds the fragments of dir into the version section of changelogPath, then deletes the folded fragments and
// their lines in legacyPath. When that leaves dir empty it also drops dir's entry from the version records at recordsPath
// (records.go).
func foldInto(dir, changelogPath, legacyPath, recordsPath, version string, fragments []fragment) error {
	data, err := os.ReadFile(changelogPath)
	if err != nil {
		return err
	}
	out, err := fold(string(data), fragments, version)
	if err != nil {
		return err
	}
	if err := os.WriteFile(changelogPath, []byte(out), 0o644); err != nil {
		return err
	}
	if err := writeChangelogFrame(changelogPath, []byte(out)); err != nil {
		return err
	}
	folded := map[string]bool{}
	for _, f := range fragments {
		folded[f.name] = true
		if err := os.Remove(filepath.Join(dir, f.name)); err != nil {
			return err
		}
	}
	data, err = os.ReadFile(legacyPath)
	if err != nil {
		return err
	}
	var keep []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" && !folded[line] {
			keep = append(keep, line)
		}
	}
	text := strings.Join(keep, "\n")
	if text != "" {
		text += "\n"
	}
	if err := os.WriteFile(legacyPath, []byte(text), 0o644); err != nil {
		return err
	}
	if left, err := os.ReadDir(dir); err != nil && !errors.Is(err, fs.ErrNotExist) || len(left) > 0 {
		return err
	}
	return dropRecordEntryFile(recordsPath, filepath.ToSlash(dir)+"/")
}

func run(args []string, stdout *os.File) error {
	dir, check, version := "changelog.d", false, ""
	for _, arg := range args {
		switch {
		case arg == "-check":
			check = true
		case strings.HasPrefix(arg, "-dir="):
			dir = strings.TrimPrefix(arg, "-dir=")
		case strings.HasPrefix(arg, "-fold=") && arg != "-fold=":
			version = strings.TrimPrefix(arg, "-fold=")
		default:
			return fmt.Errorf("usage: changelogfrag [-check | -fold=<version>] [-dir=changelog.d]")
		}
	}
	fragments, problems := load(dir)
	if check {
		problems = append(problems, checkHistory(dir, fragments)...)
	}
	if len(problems) > 0 {
		var msg []string
		for _, p := range problems {
			msg = append(msg, p.Error())
		}
		return fmt.Errorf("%s", strings.Join(msg, "\n"))
	}
	if check {
		if err := checkChangelogFrame("CHANGELOG.md"); err != nil {
			return err
		}
	}
	switch {
	case version != "":
		return foldInto(dir, "CHANGELOG.md", filepath.Join("automation", "ci", "changelogfrag", "legacy.txt"), filepath.Join("automation", "ci", "version-records.toml"), version, fragments)
	case !check:
		_, err := fmt.Fprint(stdout, render(fragments))
		return err
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "changelogfrag:", err)
		os.Exit(1)
	}
}

// writeChangelogFrame writes the embedded form of CHANGELOG.md (embed.go), one zstd frame, beside path. A frame that already decodes to text stays as it is.
func writeChangelogFrame(path string, text []byte) error {
	existing, err := os.ReadFile(path + ".zst")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(path+".zst", zstdblob.Reuse(existing, text), 0o644)
}

// checkChangelogFrame fails when path+".zst", which the binary embeds, does not decode to exactly path.
func checkChangelogFrame(path string) error {
	fix := "run: go run ./automation/gen/zstdfile " + path + " " + path + ".zst"
	want, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path + ".zst")
	if err != nil {
		return fmt.Errorf("%s.zst: %w; %s", path, err, fix)
	}
	got, err := zstdblob.Decode(data)
	if err != nil {
		return fmt.Errorf("%s.zst is not a zstd frame: %w; %s", path, err, fix)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%s.zst does not decode to exactly %s; %s", path, path, fix)
	}
	return nil
}
