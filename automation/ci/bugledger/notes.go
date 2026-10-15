// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// noteSections are the changelog sections a release note reads, in the order it prints them. Upgrading sections are matched by prefix.
var noteSections = []string{"Added", "Changed", "Fixed", "Deprecated", "Removed", "Security"}

func noteSectionSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, s := range noteSections {
		set[s] = true
	}
	for l := range strings.SplitSeq(text, "\n") {
		if m := sectionHeading.FindStringSubmatch(l); m != nil && strings.HasPrefix(m[1], "Upgrading") {
			set[m[1]] = true
		}
	}
	return set
}

// releaseNotes writes the draft release notes for target from the changelog sections after since, the changelog fragments, the bug fix ledger, and the divergence records. It is a draft: an owner edits and posts it.
func releaseNotes(root, since, target string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		return "", err
	}
	set := noteSectionSet(string(data))
	bySection := map[string][]string{}
	seen := map[string]bool{}
	add := func(bs []bullet) {
		for _, b := range bs {
			if b.Text == "" || seen[b.Text] {
				continue
			}
			seen[b.Text] = true
			bySection[b.Section] = append(bySection[b.Section], b.Text)
		}
	}
	add(sectionBullets(string(data), since, false, set))
	frags, err := filepath.Glob(filepath.Join(root, "changelog.d", "*.md"))
	if err != nil {
		return "", err
	}
	for _, f := range frags {
		fd, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		add(sectionBullets(string(fd), since, true, noteSectionSet(string(fd))))
	}
	ledger, err := build(root, since)
	if err != nil {
		return "", err
	}
	r := repo{root: root}
	base, err := r.releaseBaseline(since)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# PiG %s release notes (draft)\n\n", target)
	fmt.Fprintf(&b, "> **Draft. Not published.** `make release-notes-draft` generated this file from CHANGELOG.md, `changelog.d`, the [bug fix ledger](../site/docs/bug-ledger.md) and the divergence records. The owner edits and posts it.\n\n")
	b.WriteString("## Summary\n\n")
	counts := map[string]int{}
	for _, e := range ledger.Entries {
		counts[e.Class]++
	}
	var upgrading []string
	for sec := range bySection {
		if strings.HasPrefix(sec, "Upgrading") {
			upgrading = append(upgrading, sec)
		}
	}
	slices.Sort(upgrading)
	fmt.Fprintf(&b, "- %d additions, %d changes and %d recorded fixes since PiG %s.\n", len(bySection["Added"]), len(bySection["Changed"]), len(bySection["Fixed"]), since)
	fmt.Fprintf(&b, "- The bug fix ledger lists %d bugs: %d PiG defects, %d Pi defects PiG fixed first, %d Pi fixes ported.\n", len(ledger.Entries), counts[ClassOwn], counts[ClassAheadOfPi], counts[ClassPiPort])
	if pin, err := os.ReadFile(filepath.Join(root, "internal", "coding", "pigversion", "pigversion.go")); err == nil {
		if m := pinConstRE.FindStringSubmatch(string(pin)); m != nil {
			fmt.Fprintf(&b, "- PiG %s follows Pi %s.\n", target, m[1])
		}
	}
	b.WriteString("\n")

	for _, sec := range upgrading {
		fmt.Fprintf(&b, "## %s\n\n", sec)
		writeBullets(&b, bySection[sec])
	}
	for _, sec := range []string{"Added", "Changed"} {
		if len(bySection[sec]) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", sec)
		writeBullets(&b, bySection[sec])
	}
	writeFixed(&b, ledger)
	for _, sec := range []string{"Deprecated", "Removed", "Security"} {
		if len(bySection[sec]) > 0 {
			fmt.Fprintf(&b, "## %s\n\n", sec)
			writeBullets(&b, bySection[sec])
		}
	}
	if err := writeDivergences(&b, root, r, base); err != nil {
		return "", err
	}
	if err := writeContributors(&b, r, base); err != nil {
		return "", err
	}
	return b.String(), nil
}

func writeBullets(b *strings.Builder, items []string) {
	for _, it := range items {
		fmt.Fprintf(b, "- %s\n", it)
	}
	b.WriteString("\n")
}

// writeFixed lists the fixes that matter most in full and the rest by count, and leaves the details to the ledger.
func writeFixed(b *strings.Builder, l *Ledger) {
	b.WriteString("## Fixed\n\n")
	fmt.Fprintf(b, "PiG fixed %d bugs since %s. The [bug fix ledger](../site/docs/bug-ledger.md) gives each one's symptom, root cause, platform, fix commits and guarding tests. Counts by user impact:\n\n", len(l.Entries), l.Since)
	byImpact := map[string][]Entry{}
	for _, e := range l.Entries {
		byImpact[e.Impact] = append(byImpact[e.Impact], e)
	}
	var impacts []string
	for k := range byImpact {
		impacts = append(impacts, k)
	}
	sort.Slice(impacts, func(i, j int) bool {
		if len(byImpact[impacts[i]]) != len(byImpact[impacts[j]]) {
			return len(byImpact[impacts[i]]) > len(byImpact[impacts[j]])
		}
		return impacts[i] < impacts[j]
	})
	for _, k := range impacts {
		fmt.Fprintf(b, "- %s: %d\n", k, len(byImpact[k]))
	}
	b.WriteString("\n")
	for _, k := range []string{"Credential or permission exposure", "Crash, hang or runaway process", "Lost or altered data"} {
		if len(byImpact[k]) == 0 {
			continue
		}
		fmt.Fprintf(b, "### %s\n\n", k)
		for _, e := range byImpact[k] {
			text := e.Note
			if text == "" {
				text = e.Symptom
			}
			fmt.Fprintf(b, "- %s (%s)\n", text, e.ID)
		}
		b.WriteString("\n")
	}
	var ahead, port []Entry
	for _, e := range l.Entries {
		switch e.Class {
		case ClassAheadOfPi:
			ahead = append(ahead, e)
		case ClassPiPort:
			port = append(port, e)
		}
	}
	if len(ahead) > 0 {
		b.WriteString("### Pi defects PiG fixed first\n\nEach is a numbered divergence that ends when Pi fixes the defect.\n\n")
		for _, e := range ahead {
			issue := e.PiIssue
			if issue == "" {
				issue = "no Pi issue recorded"
			}
			fmt.Fprintf(b, "- %s (%s; %s). Removed when: %s\n", e.Title, e.Divergence, issue, e.RemoveWhen)
		}
		b.WriteString("\n")
	}
	if len(port) > 0 {
		b.WriteString("### Pi fixes PiG ported\n\n")
		for _, e := range port {
			fmt.Fprintf(b, "- %s (%s)\n", e.Title, e.ID)
		}
		b.WriteString("\n")
	}
}

// writeDivergences lists the divergence records added since the baseline release.
func writeDivergences(b *strings.Builder, root string, r repo, base string) error {
	path, text, ok := divergenceFile(root)
	if !ok {
		return nil
	}
	old, err := r.git("show", base+":"+path)
	if err != nil {
		old = ""
	}
	var ids []string
	cur := parseDivergences(text)
	for id := range cur {
		if !strings.Contains(old, "## "+id+" ") {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, cmpID)
	if len(ids) == 0 {
		return nil
	}
	b.WriteString("## New divergences from Pi\n\nEach is recorded in the divergence ledger with its call sites, evidence and the condition that removes it.\n\n")
	for _, id := range ids {
		fmt.Fprintf(b, "- %s: %s\n", id, cur[id].Title)
	}
	b.WriteString("\n")
	return nil
}

// cmpID orders divergence IDs by number: D9 before D10.
func cmpID(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}

// agentEmails are the addresses coding agents sign Co-authored-by trailers with; an agent is not a contributor to thank.
var agentEmails = map[string]bool{"cursoragent@cursor.com": true, "noreply@anthropic.com": true}

// writeContributors thanks the people whose work is in the release: the commit authors and the co-authors a maintainer
// credits with a Co-authored-by trailer when a change lands inside a larger commit (.github/CONTRIBUTING.md), other than
// the maintainer and automation. One person is thanked once: identities that share an email, then those that share a name,
// are the same person, named as the first commit since base credits them.
func writeContributors(b *strings.Builder, r repo, base string) error {
	out, err := r.git("log", "--reverse", "--no-merges", "--format=%aN%x00%aE%x00%(trailers:key=Co-authored-by,valueonly,separator=%x00)%x1e", base+"..HEAD")
	if err != nil {
		return err
	}
	var names []string
	seenEmail, seenName := map[string]bool{}, map[string]bool{}
	credit := func(name, email string) {
		name, email = strings.TrimSpace(name), strings.ToLower(strings.TrimSpace(email))
		if name == "" || email == "" || name == "Michael Kinsy" || name == "pig-ci" || strings.Contains(name, "[bot]") || agentEmails[email] {
			return
		}
		known := seenEmail[email] || seenName[name]
		seenEmail[email], seenName[name] = true, true
		if !known {
			names = append(names, name)
		}
	}
	for record := range strings.SplitSeq(out, "\x1e") {
		fields := strings.Split(strings.TrimSpace(record), "\x00")
		if len(fields) < 2 {
			continue
		}
		credit(fields[0], fields[1])
		for _, trailer := range fields[2:] {
			// A trailer names "Name <email>"; one without an email names no one the notes can thank.
			name, rest, ok := strings.Cut(trailer, "<")
			if email, closed := strings.CutSuffix(strings.TrimSpace(rest), ">"); ok && closed {
				credit(name, email)
			}
		}
	}
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)
	b.WriteString("## Contributors\n\nThanks to the contributors whose commits are in this release: " + strings.Join(names, ", ") + ".\n\n")
	return nil
}
