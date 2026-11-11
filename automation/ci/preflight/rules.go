// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Results of a check.
const (
	resultPass  = "pass"
	resultFail  = "fail"
	resultScan  = "scan"
	resultError = "error"
)

// outcome is what a rule decides: a result, the reason a reader acts on, and warnings that do not fail the check but
// are shown to the reviewer.
type outcome struct {
	result   string
	reason   string
	warnings []warning
}

func pass(reason string) outcome { return outcome{result: resultPass, reason: reason} }

func fail(format string, args ...any) outcome {
	return outcome{result: resultFail, reason: fmt.Sprintf(format, args...)}
}

func scan(reason string) outcome { return outcome{result: resultScan, reason: reason} }

// rules maps a check name of config.yaml to its rule. A check without a rule here is run by a scan step.
var rules = map[string]func(*data) outcome{
	"template":          ruleTemplate,
	"signed":            ruleSigned,
	"signoff":           ruleSignoff,
	"depends-on":        ruleDependsOn,
	"up-to-date":        ruleUpToDate,
	"bug-ledger":        ruleBugLedger,
	"changelog":         ruleChangelog,
	"test-expectation":  ruleTestExpectation,
	"dependency-review": ruleDependencyReview,
	"reuse":             ruleReuse,
	"gitleaks":          ruleGitleaks,
}

// templateField reads "Name: value" from the PR body; ok is false when the line is missing or empty.
func templateField(body, name string) (string, bool) {
	re := regexp.MustCompile(`(?mi)^[ \t]*` + regexp.QuoteMeta(name) + `:[ \t]*(\S.*?)[ \t]*$`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func yesNo(body, name string) (yes, ok bool) {
	v, found := templateField(body, name)
	switch strings.ToLower(v) {
	case "yes":
		return true, found
	case "no":
		return false, found
	}
	return false, false
}

func ruleTemplate(d *data) outcome {
	body := d.Pull.Body
	if strings.TrimSpace(body) == "" {
		return fail("the PR body is empty: fill in .github/pull_request_template.md")
	}
	var missing []string
	if v, ok := templateField(body, "Tracking"); !ok || strings.HasPrefix(v, "<") {
		missing = append(missing, "`Tracking: <issue URL>` or `Tracking: No issue: <reason>`")
	}
	if _, ok := parseDependsOn(body); !ok {
		missing = append(missing, "`Depends on: None` or `Depends on: #<n>`")
	}
	if _, ok := yesNo(body, "Fix"); !ok {
		missing = append(missing, "`Fix: yes` or `Fix: no`")
	}
	if _, ok := yesNo(body, "User-facing"); !ok {
		missing = append(missing, "`User-facing: yes` or `User-facing: no`")
	}
	if len(missing) > 0 {
		return fail("the PR body misses %s", strings.Join(missing, ", "))
	}
	return pass("the template is filled in")
}

func ruleSigned(d *data) outcome {
	var bad []string
	for _, c := range d.Commits {
		if v := c.Commit.Verification; !v.Verified {
			bad = append(bad, fmt.Sprintf("%s (%s)", short(c.SHA), v.Reason))
		}
	}
	if len(bad) > 0 {
		return fail("commit(s) without a signature GitHub marks Verified: %s; sign with `git commit -S` (see .github/CONTRIBUTING.md)", strings.Join(bad, ", "))
	}
	return pass(fmt.Sprintf("%d commit(s) Verified", len(d.Commits)))
}

var signedOffRE = regexp.MustCompile(`(?m)^Signed-off-by: .+ <[^>]+>\s*$`)

func ruleSignoff(d *data) outcome {
	var bad []string
	for _, c := range d.Commits {
		if !signedOffRE.MatchString(c.Commit.Message) {
			bad = append(bad, short(c.SHA))
		}
	}
	if len(bad) > 0 {
		return fail("commit(s) without a `Signed-off-by:` line: %s; add it with `git commit --signoff`", strings.Join(bad, ", "))
	}
	return pass(fmt.Sprintf("%d commit(s) signed off", len(d.Commits)))
}

func ruleDependsOn(d *data) outcome {
	n, ok := parseDependsOn(d.Pull.Body)
	switch {
	case !ok:
		return fail("no valid `Depends on:` line, so the dependency cannot be checked")
	case n == 0:
		return pass("depends on nothing")
	case d.DependsOn == nil:
		return outcome{result: resultError, reason: fmt.Sprintf("PR #%d named in `Depends on` was not fetched", n)}
	case !d.DependsOn.Merged:
		return fail("depends on PR #%d, which is %s and not merged; wait for it or open this PR as a draft", n, d.DependsOn.State)
	}
	return pass(fmt.Sprintf("PR #%d it depends on is merged", n))
}

func ruleUpToDate(d *data) outcome {
	var problems []string
	if b := d.CompareMain.BehindBy; b > 0 {
		problems = append(problems, fmt.Sprintf("%d commit(s) behind %s; rebase on it", b, d.Pull.Base.Ref))
	}
	switch m := d.Pull.Mergeable; {
	case m == nil:
		return outcome{result: resultError, reason: "GitHub has not computed whether the PR can merge; run again"}
	case !*m:
		problems = append(problems, "conflicts with "+d.Pull.Base.Ref)
	}
	if len(problems) > 0 {
		return fail("%s", strings.Join(problems, "; "))
	}
	return pass("up to date with " + d.Pull.Base.Ref)
}

// fixTitleRE matches the commit titles upstream treats as a fix: "Fix ...", "fix: ..." and "<area>: fix ...".
var fixTitleRE = regexp.MustCompile(`^(?:Fix\b|fix(?:\([^)]*\))?:|[\w./-]+: fix\b)`)

// fixEvidence lists what marks the PR as a fix besides the template: fix-titled commits and added "### Fixed" sections.
func fixEvidence(d *data) []string {
	var why []string
	for _, c := range d.Commits {
		title, _, _ := strings.Cut(c.Commit.Message, "\n")
		if fixTitleRE.MatchString(title) {
			why = append(why, fmt.Sprintf("commit %s %q", short(c.SHA), title))
		}
	}
	for _, f := range d.Files {
		if isFragment(f.Filename) && slices.Contains(addedLines(f.Patch), "### Fixed") {
			why = append(why, f.Filename+" adds a ### Fixed section")
		}
	}
	return why
}

func isFragment(name string) bool {
	return path.Dir(name) == "changelog.d" && strings.HasSuffix(name, ".md")
}

func changed(d *data, name string) bool {
	return slices.ContainsFunc(d.Files, func(f prFile) bool { return f.Filename == name })
}

func ruleBugLedger(d *data) outcome {
	isFix, ok := yesNo(d.Pull.Body, "Fix")
	evidence := fixEvidence(d)
	switch {
	case ok && !isFix && len(evidence) > 0:
		return fail("the template says `Fix: no`, but %s", strings.Join(evidence, "; "))
	case !ok && len(evidence) == 0:
		return fail("cannot tell whether this is a fix: the template has no `Fix: yes` or `Fix: no`")
	case !isFix && len(evidence) == 0:
		return pass("not a fix")
	case !changed(d, "docs/bug-ledger.json"):
		return fail("a fix needs its bug ledger entry: run `make bug-ledger` and commit docs/bug-ledger.json and docs/site/docs/bug-ledger.md")
	}
	return pass("the fix updates docs/bug-ledger.json")
}

var (
	fragmentNameRE = regexp.MustCompile(`^changelog\.d/[a-z0-9][a-z0-9-]*-([0-9a-f]{7,40})\.md$`)
	fixMarkerRE    = regexp.MustCompile(`<!-- fix: ([0-9a-f, ]+) -->`)
)

// prCommit reports whether prefix names a commit of the PR.
func (d *data) prCommit(prefix string) bool {
	return slices.ContainsFunc(d.Commits, func(c prCommit) bool { return strings.HasPrefix(c.SHA, prefix) })
}

func ruleChangelog(d *data) outcome {
	isFix, fixOK := yesNo(d.Pull.Body, "Fix")
	userFacing, ufOK := yesNo(d.Pull.Body, "User-facing")
	isFix = isFix || len(fixEvidence(d)) > 0
	if !fixOK && !ufOK && !isFix {
		return fail("cannot tell whether a fragment is needed: the template has no `Fix:` or `User-facing:` answer")
	}
	if !isFix && !userFacing {
		return pass("no user-facing change and no fix")
	}
	var fragments []prFile
	for _, f := range d.Files {
		if isFragment(f.Filename) && f.Status != "removed" {
			fragments = append(fragments, f)
		}
	}
	if len(fragments) == 0 {
		return fail("a fix or user-facing change needs a fragment changelog.d/<lane>-<sha>.md (see AGENTS.md, Commit hygiene)")
	}
	var problems []string
	sawFixed := false
	for _, f := range fragments {
		m := fragmentNameRE.FindStringSubmatch(f.Filename)
		switch {
		case m == nil:
			problems = append(problems, f.Filename+" is not named <lane>-<sha>.md")
		case !d.prCommit(m[1]):
			problems = append(problems, fmt.Sprintf("%s names %s, which is not a commit of this PR (rename it after a rebase)", f.Filename, m[1]))
		}
		section := ""
		for _, line := range addedLines(f.Patch) {
			switch {
			case strings.HasPrefix(line, "### "):
				section = strings.TrimPrefix(line, "### ")
				sawFixed = sawFixed || section == "Fixed"
			case section == "Fixed" && strings.HasPrefix(line, "- "):
				mm := fixMarkerRE.FindStringSubmatch(line)
				if mm == nil {
					problems = append(problems, fmt.Sprintf("%s: a ### Fixed bullet has no <!-- fix: <sha> --> marker", f.Filename))
					continue
				}
				for sha := range strings.SplitSeq(mm[1], ",") {
					if sha = strings.TrimSpace(sha); !d.prCommit(sha) {
						problems = append(problems, fmt.Sprintf("%s: fix marker %s is not a commit of this PR", f.Filename, sha))
					}
				}
			}
		}
	}
	if isFix && !sawFixed {
		problems = append(problems, "a fix needs a ### Fixed bullet in its fragment")
	}
	if len(problems) > 0 {
		return fail("%s", strings.Join(problems, "; "))
	}
	return pass(fmt.Sprintf("%d valid fragment(s)", len(fragments)))
}

// addedLines returns the lines a unified diff patch adds, without the leading "+".
func addedLines(patch string) []string {
	var out []string
	for line := range strings.SplitSeq(patch, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			out = append(out, line[1:])
		}
	}
	return out
}

func removedLines(patch string) []string {
	var out []string
	for line := range strings.SplitSeq(patch, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			out = append(out, line[1:])
		}
	}
	return out
}

// The expectation heuristic of test/parity/cmd/testprune: a line with a comparison or expectation name and a literal.
var (
	expectRE  = regexp.MustCompile(`(?i)(?:\bwant\w*\b|\bexpect\w*\b|\bgot\b|!=|==|Equal|\bgolden\b|Contains)`)
	literalRE = regexp.MustCompile("(?:\"[^\"]{2,}\"|`[^`]{2,}`|\\b\\d{2,}\\b)")
	piCiteRE  = regexp.MustCompile(`(?m)^Pi-Behavior-Change:[ \t]*(\S+\.ts):\d+`)
)

var (
	// nilCheckRE matches an error or nil comparison, which compares no expected value.
	nilCheckRE = regexp.MustCompile(`[!=]= nil\b`)
	// reportRE matches a test's failure or log message: its literals are the message, not an expectation.
	reportRE = regexp.MustCompile(`^\s*t\.(?:Fatal|Fatalf|Error|Errorf|Log|Logf|Skip|Skipf)\(`)
	quotedRE = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`")
)

// isExpectation reports a line that compares with or names an expected value and holds a literal. The comparison or
// name must stand outside the line's string literals; nil checks and failure messages do not count.
func isExpectation(line string) bool {
	if reportRE.MatchString(line) || !literalRE.MatchString(line) {
		return false
	}
	code := nilCheckRE.ReplaceAllString(quotedRE.ReplaceAllString(line, `""`), "")
	return expectRE.MatchString(code)
}

// expectationEdits counts the expected literals a patch replaced: removed by one expectation line and not re-added,
// bounded by those added and not removed, so a renamed identifier or a moved path does not count.
func expectationEdits(patch string) int {
	collect := func(lines []string) map[string]int {
		m := map[string]int{}
		for _, line := range lines {
			if !isExpectation(line) {
				continue
			}
			for _, l := range literalRE.FindAllString(line, -1) {
				if !pathLike(l) {
					m[l]++
				}
			}
		}
		return m
	}
	gone, came := collect(removedLines(patch)), collect(addedLines(patch))
	var g, c int
	for l, n := range gone {
		g += max(0, n-came[l])
	}
	for l, n := range came {
		c += max(0, n-gone[l])
	}
	return min(g, c)
}

// pathLike reports a quoted literal that names a file or package path: moving code changes these without changing behavior.
func pathLike(literal string) bool {
	body := strings.Trim(literal, "\"`")
	return strings.Contains(body, "/") && !strings.ContainsAny(body, " \t\n")
}

func isProdGo(p string) bool {
	return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") && !strings.Contains(p, "/testdata/")
}

func ruleTestExpectation(d *data) outcome {
	prodDirs := map[string]bool{}
	for _, f := range d.Files {
		if isProdGo(f.Filename) {
			prodDirs[path.Dir(f.Filename)] = true
		}
	}
	var edited []string
	for _, f := range d.Files {
		if strings.HasSuffix(f.Filename, "_test.go") && prodDirs[path.Dir(f.Filename)] {
			if n := expectationEdits(f.Patch); n > 0 {
				edited = append(edited, fmt.Sprintf("%s (%d value(s))", f.Filename, n))
			}
		}
	}
	if len(edited) == 0 {
		return pass("no expected value changed together with production code of its directory")
	}
	inBody := piCiteRE.MatchString(d.Pull.Body)
	inCommit := slices.ContainsFunc(d.Commits, func(c prCommit) bool { return piCiteRE.MatchString(c.Commit.Message) })
	var missing []string
	if !inBody {
		missing = append(missing, "the PR body")
	}
	if !inCommit {
		missing = append(missing, "a commit trailer")
	}
	if len(missing) > 0 {
		return fail("%s change expected values together with production code; cite the Pi behavior that changed as `Pi-Behavior-Change: <pi file.ts:line>` in %s", strings.Join(edited, ", "), strings.Join(missing, " and "))
	}
	// Whether the cited file is a real Pi source is checked against the inventories of main in a scan step.
	return scan("cited; the cited Pi file is checked against PORT_MAP.md and the mappings")
}

func ruleDependencyReview(d *data) outcome {
	var bad []string
	for _, c := range d.Dependencies {
		if c.ChangeType != "added" {
			continue
		}
		for _, v := range c.Vulnerabilities {
			if v.Severity == "high" || v.Severity == "critical" {
				bad = append(bad, fmt.Sprintf("%s %s %s (%s %s)", c.Ecosystem, c.Name, c.Version, v.Severity, v.AdvisoryID))
			}
		}
	}
	if len(bad) > 0 {
		return fail("added dependencies with a high or critical advisory: %s", strings.Join(bad, ", "))
	}
	return pass(fmt.Sprintf("%d dependency change(s), no high or critical advisory", len(d.Dependencies)))
}

var (
	spdxLicenseRE = regexp.MustCompile(`SPDX-License-Identifier:\s*([^\n*]+?)\s*(?:\*/|-->|$)`)
	licenseIDRE   = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9.+-]*`)
	copyrightRE   = regexp.MustCompile(`(?i)\b(?:SPDX-FileCopyrightText:|Copyright\s+(?:\(c\)|©|\d{4}))`)
	licenseTextRE = regexp.MustCompile(`(?i)Licensed under the Apache License|GNU (?:Lesser |Affero )?General Public License|Mozilla Public License|Eclipse Public License|Creative Commons|Redistribution and use in source and binary forms|Permission is hereby granted, free of charge`)
	// codeFileRE matches the files a new SPDX header is expected in.
	codeFileRE = regexp.MustCompile(`\.(?:go|sh|py|ts|mts|js|mjs|rs|lisp|c|h|ps1)$`)
)

// noticeFile reports the files whose job is to carry other licenses' texts and notices.
func noticeFile(name string) bool {
	return strings.HasPrefix(name, "LICENSES/") || name == "NOTICE" || name == "THIRD_PARTY_NOTICES.md" || name == "REUSE.toml" || strings.HasSuffix(name, "/FORK.md")
}

// ruleReuse fails on a license the repository does not ship in LICENSES/, and warns on what a reviewer should see:
// added copyright notices, license texts outside the notice files, and new code files without an SPDX header (the
// blanket MIT of REUSE.toml then covers them). reuse lint-file runs after it.
func ruleReuse(d *data) outcome {
	var problems []string
	var warnings []warning
	for _, f := range d.Files {
		if f.Status == "removed" || noticeFile(f.Filename) {
			continue
		}
		hasHeader := false
		for _, l := range addedNumbered(f.Patch) {
			line := l.text
			if m := spdxLicenseRE.FindStringSubmatch(line); m != nil {
				hasHeader = true
				for _, id := range licenseIDRE.FindAllString(m[1], -1) {
					if up := strings.ToUpper(id); up == "AND" || up == "OR" || up == "WITH" {
						continue
					}
					if !slices.Contains(d.Licenses, id) {
						problems = append(problems, fmt.Sprintf("%s:%d declares %s, which is not in LICENSES/", f.Filename, l.n, id))
					}
				}
			}
			if copyrightRE.MatchString(line) {
				warnings = append(warnings, warning{File: f.Filename, Line: l.n, Blob: f.SHA, Notice: strings.TrimSpace(line), Text: "adds a copyright notice"})
			}
			if m := licenseTextRE.FindString(line); m != "" {
				warnings = append(warnings, warning{File: f.Filename, Line: l.n, Blob: f.SHA, Notice: strings.TrimSpace(line), Text: fmt.Sprintf("adds license text (%q): is this code from a third party?", m)})
			}
		}
		if f.Status == "added" && codeFileRE.MatchString(f.Filename) && !hasHeader {
			warnings = append(warnings, warning{File: f.Filename, Blob: f.SHA, Notice: "no SPDX-License-Identifier header", Text: "is new and has no SPDX-License-Identifier header; REUSE.toml's blanket MIT covers it"})
		}
	}
	if len(problems) > 0 {
		o := fail("%s; add the license text to LICENSES/ and an annotation in REUSE.toml, or remove the code", strings.Join(problems, "; "))
		o.warnings = warnings
		return o
	}
	o := scan("no foreign license declared; reuse lint-file checks the changed files")
	o.warnings = warnings
	return o
}

var gitleaksAllowRE = regexp.MustCompile(`gitleaks:allow`)

// ruleGitleaks leaves the scan to its step and warns on every added inline suppression, so a reviewer sees each one.
func ruleGitleaks(d *data) outcome {
	o := scan("Gitleaks scans the added lines with main's configuration")
	for _, f := range d.Files {
		for _, l := range addedNumbered(f.Patch) {
			if gitleaksAllowRE.MatchString(l.text) {
				o.warnings = append(o.warnings, warning{File: f.Filename, Line: l.n, Blob: f.SHA, Notice: strings.TrimSpace(l.text), Text: "adds an inline `gitleaks:allow` suppression"})
			}
		}
	}
	return o
}
