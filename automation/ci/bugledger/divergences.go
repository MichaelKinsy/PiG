// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// divergence is one record of docs/parity/DIVERGENCES.md.
type divergence struct {
	ID       string
	Title    string
	Body     string
	PiBug    string
	PiIssue  string
	Remove   string
	What     string
	Why      string
	Evidence []string
	// Observable is the record's "Observable effect" paragraph.
	Observable string
}

// piRepo is the Pi repository whose issue numbers a divergence record cites as "Pi #N".
const piRepo = "https://github.com/earendil-works/pi"

var piIssueRef = regexp.MustCompile(`\b(?:Pi|pi-mono) #(\d+)`)

var divHeading = regexp.MustCompile(`(?m)^## (D\d+) (.+)$`)

var divergencePaths = []string{"docs/parity/DIVERGENCES.md", "DIVERGENCES.md"}

func divergenceFile(root string) (string, string, bool) {
	for _, p := range divergencePaths {
		if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p))); err == nil {
			return p, string(data), true
		}
	}
	return "", "", false
}

func parseDivergences(text string) map[string]divergence {
	out := map[string]divergence{}
	ms := divHeading.FindAllStringSubmatchIndex(text, -1)
	for i, m := range ms {
		end := len(text)
		if i+1 < len(ms) {
			end = ms[i+1][0]
		}
		d := divergence{ID: text[m[2]:m[3]], Title: strings.TrimSpace(text[m[4]:m[5]]), Body: text[m[1]:end]}
		d.PiBug = field(d.Body, "Pi bug")
		d.PiIssue = field(d.Body, "Pi issue")
		d.Remove = field(d.Body, "Remove when")
		d.What = field(d.Body, "What")
		d.Why = field(d.Body, "Why")
		d.Observable = field(d.Body, "Observable effect")
		if d.PiIssue == "" {
			// A record cites the Pi issue as "Pi #N" or "pi-mono #N" in its title or its reason.
			for _, text := range []string{d.Title, d.Why} {
				if m := piIssueRef.FindStringSubmatch(text); m != nil {
					d.PiIssue = piRepo + "/issues/" + m[1]
					break
				}
			}
		}
		d.Evidence = divergenceTests(d.Body)
		out[d.ID] = d
	}
	return out
}

// fixesPiDefect reports whether the record says Pi has a defect that PiG fixes: it has a "Pi bug:" paragraph or cites a Pi issue.
func (d divergence) fixesPiDefect() bool { return d.PiBug != "" || d.PiIssue != "" }

// symptom is what a user sees in Pi because of the defect.
func (d divergence) symptom() string {
	for _, s := range []string{d.PiBug, d.Observable, d.What} {
		if s != "" {
			return s
		}
	}
	return d.Title
}

// field returns the text of the "Name:" paragraph of a divergence record, up to its blank line.
func field(body, name string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `:\s*(.*(?:\n[^\n]+)*)`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(spaceRun.ReplaceAllString(m[1], " "))
}

var backtickTest = regexp.MustCompile("`((?:Test|Fuzz)[A-Z][A-Za-z0-9_]*)`")

func divergenceTests(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range backtickTest.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

var divRef = regexp.MustCompile(`\bD(\d+)\b`)

// citesOnly reports whether a release note cites divergence id and no other divergence.
func citesOnly(note, id string) bool {
	ms := divRef.FindAllStringSubmatch(note, -1)
	if len(ms) == 0 {
		return false
	}
	for _, m := range ms {
		if "D"+m[1] != id {
			return false
		}
	}
	return true
}

// divergenceCommits returns the commit that adds a divergence record and the commits that name it in their message and change shipped code, oldest first. A commit that names it only in documentation or tooling is not part of the fix.
func divergenceCommits(r repo, base, id, path string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(list string) {
		for h := range strings.SplitSeq(strings.TrimSpace(list), "\n") {
			if h != "" && !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	added, err := r.git("log", "-m", "--reverse", "--format=%H", "-S## "+id+" ", base+"..HEAD", "--", path)
	if err != nil {
		return nil, err
	}
	if first, _, _ := strings.Cut(strings.TrimSpace(added), "\n"); first != "" {
		add(first)
	}
	named, err := r.git("log", "--no-merges", "--reverse", "--name-only", "-E", "--grep=(^|[^0-9A-Za-z])"+id+"([^0-9]|$)", "--format=@@%H", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	for chunk := range strings.SplitSeq(named, "@@") {
		hash, files, _ := strings.Cut(chunk, "\n")
		if hash = strings.TrimSpace(hash); hash == "" {
			continue
		}
		for f := range strings.SplitSeq(files, "\n") {
			if f = strings.TrimSpace(f); f != "" && shipped(f) {
				add(hash)
				break
			}
		}
	}
	return out, nil
}
