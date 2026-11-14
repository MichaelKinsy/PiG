// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

const (
	resultPass     = "pass"
	resultFail     = "fail"
	resultScan     = "scan"
	resultError    = "error"
	resultPending  = "pending"
	resultDeferred = "deferred"
)

type outcome struct {
	result   string
	reason   string
	warnings []warning
}

func pass(reason string) outcome { return outcome{result: resultPass, reason: reason} }

func fail(format string, args ...any) outcome {
	return outcome{result: resultFail, reason: fmt.Sprintf(format, args...)}
}

var compiled sync.Map

func pattern(src string) *regexp.Regexp {
	if re, ok := compiled.Load(src); ok {
		return re.(*regexp.Regexp)
	}
	re := regexp.MustCompile(src)
	compiled.Store(src, re)
	return re
}

type params map[string]any

func (p params) str(key string) string {
	s, _ := p[key].(string)
	return s
}

func (p params) strs(key string) []string {
	var out []string
	if list, ok := p[key].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func (p params) maps(key string) []params {
	var out []params
	if list, ok := p[key].([]any); ok {
		for _, v := range list {
			if m, ok := v.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func (p params) need(keys ...string) error {
	for _, k := range keys {
		if p.str(k) == "" {
			return fmt.Errorf("needs %s", k)
		}
	}
	return nil
}

func (p params) regexps(keys ...string) error {
	for _, k := range keys {
		if s := p.str(k); s != "" {
			if _, err := regexp.Compile(s); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
		}
	}
	return nil
}

type ruleEnv struct {
	root string

	once     sync.Once
	licenses []string
	files    sync.Map
}

func (e *ruleEnv) mainLicenses() []string {
	e.once.Do(func() {
		entries, _ := os.ReadDir(filepath.Join(e.root, "LICENSES"))
		for _, entry := range entries {
			if id, ok := strings.CutSuffix(entry.Name(), ".txt"); ok {
				e.licenses = append(e.licenses, id)
			}
		}
	})
	return e.licenses
}

func (e *ruleEnv) mainFiles(glob string) string {
	if v, ok := e.files.Load(glob); ok {
		return v.(string)
	}
	var b strings.Builder
	names, _ := filepath.Glob(filepath.Join(e.root, filepath.FromSlash(glob)))
	slices.Sort(names)
	for _, n := range names {
		raw, err := os.ReadFile(n)
		if err == nil {
			b.Write(raw)
			b.WriteByte('\n')
		}
	}
	e.files.Store(glob, b.String())
	return b.String()
}

type builtinRule struct {
	validate func(params) error
	run      func(*data, params, *ruleEnv) outcome
}

func noParams(params) error { return nil }

var builtinRules map[string]builtinRule

func init() {
	builtinRules = map[string]builtinRule{
		"body-fields":        {validateBodyFields, ruleBodyFields},
		"all-commits":        {validateAllCommits, ruleAllCommits},
		"depends-on-merged":  {noParams, ruleDependsOn},
		"up-to-date":         {noParams, ruleUpToDate},
		"fix-ledger":         {func(p params) error { return p.need("ledger") }, ruleFixLedger},
		"changelog-fragment": {noParams, ruleChangelog},
		"test-expectation":   {func(p params) error { return needList(p, "inventories") }, ruleTestExpectation},
		"no-advisories":      {func(p params) error { return needList(p, "severities") }, ruleNoAdvisories},
		"license-notices":    {validateLicenseNotices, ruleLicenseNotices},
		"added-lines-warn":   {validateAddedLinesWarn, ruleAddedLinesWarn},
	}
}

func needList(p params, key string) error {
	if len(p.strs(key)) == 0 {
		return fmt.Errorf("needs a list %s", key)
	}
	return nil
}

func templateField(body, name string) (string, bool) {
	m := pattern(`(?mi)^[ \t]*` + regexp.QuoteMeta(name) + `:[ \t]*(\S.*?)[ \t]*$`).FindStringSubmatch(body)
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

func validateBodyFields(p params) error {
	fields := p.maps("fields")
	if len(fields) == 0 {
		return errors.New("needs fields")
	}
	for _, f := range fields {
		if err := f.need("name", "pattern", "help"); err != nil {
			return err
		}
		if err := f.regexps("pattern"); err != nil {
			return err
		}
	}
	return nil
}

func ruleBodyFields(d *data, p params, _ *ruleEnv) outcome {
	if strings.TrimSpace(d.Pull.Body) == "" {
		return fail("the PR body is empty: fill in .github/pull_request_template.md")
	}
	var missing []string
	for _, f := range p.maps("fields") {
		v, ok := templateField(d.Pull.Body, f.str("name"))
		if !ok || !pattern(f.str("pattern")).MatchString(v) {
			missing = append(missing, f.str("help"))
		}
	}
	if len(missing) > 0 {
		return fail("the PR body misses %s", strings.Join(missing, ", "))
	}
	return pass("the template is filled in")
}

func validateAllCommits(p params) error {
	_, verified := p["verified"].(bool)
	if verified == (p.str("message") != "") {
		return errors.New("needs either verified: true or a message pattern")
	}
	if err := p.need("help"); err != nil {
		return err
	}
	if v, ok := p[exemptUpdateBranch]; ok {
		if _, isBool := v.(bool); !isBool || verified {
			return errors.New(exemptUpdateBranch + " is true or false, and goes with a message pattern")
		}
	}
	return p.regexps("message")
}

func ruleAllCommits(d *data, p params, _ *ruleEnv) outcome {
	var bad, undecided []string
	message := p.str("message")
	exemptMerges, _ := p[exemptUpdateBranch].(bool)
	exempted := 0
	for _, c := range d.Commits {
		switch {
		case message == "" && !c.Commit.Verification.Verified:
			bad = append(bad, fmt.Sprintf("%s (%s)", short(c.SHA), c.Commit.Verification.Reason))
		case message == "" || pattern(message).MatchString(c.Commit.Message):
		case !exemptMerges:
			bad = append(bad, short(c.SHA))
		default:
			exempt, why, err := updateBranchMerge(c, d.Pull.Base.Ref)
			switch {
			case exempt:
				exempted++
			case err != nil:
				undecided = append(undecided, fmt.Sprintf("%s (%v)", short(c.SHA), err))
			case why != "":
				bad = append(bad, fmt.Sprintf("%s (%s)", short(c.SHA), why))
			default:
				bad = append(bad, short(c.SHA))
			}
		}
	}
	switch {
	case len(bad) > 0:
		return fail("commit(s) %s: %s", strings.Join(bad, ", "), p.str("help"))
	case len(undecided) > 0:
		return outcome{result: resultError, reason: fmt.Sprintf("git could not tell whether commit(s) %s only merge %s", strings.Join(undecided, ", "), d.Pull.Base.Ref)}
	case exempted > 0:
		return pass(fmt.Sprintf("all %d commit(s) pass: %d of them GitHub's update-branch merge of %s, which holds nothing to sign off", len(d.Commits), exempted, d.Pull.Base.Ref))
	}
	return pass(fmt.Sprintf("all %d commit(s) pass", len(d.Commits)))
}

func ruleDependsOn(d *data, _ params, _ *ruleEnv) outcome {
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

func ruleUpToDate(d *data, _ params, _ *ruleEnv) outcome {
	var problems []string
	if b := d.CompareMain.BehindBy; b > 0 {
		problems = append(problems, fmt.Sprintf("%d commit(s) behind %s; rebase on it", b, d.Pull.Base.Ref))
	}
	switch m := d.Pull.Mergeable; {
	case m == nil:
		return outcome{result: resultPending, reason: "neither GitHub nor git merge-tree could tell whether the PR can merge yet; the next run decides it"}
	case !*m:
		problems = append(problems, "conflicts with "+d.Pull.Base.Ref)
	}
	if len(problems) > 0 {
		return fail("%s", strings.Join(problems, "; "))
	}
	return pass("up to date with " + d.Pull.Base.Ref)
}

var fixTitleRE = regexp.MustCompile(`^(?:Fix\b|fix(?:\([^)]*\))?:|[\w./-]+: fix\b)`)

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

func ruleFixLedger(d *data, p params, _ *ruleEnv) outcome {
	isFix, ok := yesNo(d.Pull.Body, "Fix")
	evidence := fixEvidence(d)
	switch {
	case ok && !isFix && len(evidence) > 0:
		return fail("the template says `Fix: no`, but %s", strings.Join(evidence, "; "))
	case !ok && len(evidence) == 0:
		return fail("cannot tell whether this is a fix: the template has no `Fix: yes` or `Fix: no`")
	case !isFix && len(evidence) == 0:
		return pass("not a fix")
	case !changed(d, p.str("ledger")):
		return fail("a fix needs its bug ledger entry: %s", p.str("help"))
	}
	return pass("the fix updates " + p.str("ledger"))
}

var (
	fragmentNameRE = regexp.MustCompile(`^changelog\.d/[a-z0-9][a-z0-9-]*-([0-9a-f]{7,40})\.md$`)
	fixMarkerRE    = regexp.MustCompile(`<!-- fix: ([0-9a-f, ]*) -->`)
	// The length a fragment's file name takes: fewer than 7 digits name too many commits, and a sha has 40.
	fixSHARE = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

func (d *data) prCommit(prefix string) bool {
	return slices.ContainsFunc(d.Commits, func(c prCommit) bool { return strings.HasPrefix(c.SHA, prefix) })
}

func ruleChangelog(d *data, _ params, _ *ruleEnv) outcome {
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
				problems = append(problems, fixMarkerProblems(d, f.Filename, line)...)
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

func fixMarkerProblems(d *data, fragment, bullet string) []string {
	m := fixMarkerRE.FindStringSubmatch(bullet)
	if m == nil {
		return []string{fragment + ": a ### Fixed bullet has no <!-- fix: <sha> --> marker"}
	}
	var problems []string
	for sha := range strings.SplitSeq(m[1], ",") {
		switch sha = strings.TrimSpace(sha); {
		case !fixSHARE.MatchString(sha):
			problems = append(problems, fmt.Sprintf("%s: fix marker %q is not a commit sha of 7 to 40 hex digits", fragment, sha))
		case !d.prCommit(sha):
			problems = append(problems, fmt.Sprintf("%s: fix marker %s is not a commit of this PR", fragment, sha))
		}
	}
	return problems
}

func addedLines(patch string) []string {
	var out []string
	for _, l := range addedNumbered(patch) {
		out = append(out, l.text)
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

var (
	expectRE   = regexp.MustCompile(`(?i)(?:\bwant\w*\b|\bexpect\w*\b|\bgot\b|!=|==|Equal|\bgolden\b|Contains)`)
	literalRE  = regexp.MustCompile("(?:\"[^\"]{2,}\"|`[^`]{2,}`|\\b\\d{2,}\\b)")
	piCiteRE   = regexp.MustCompile(`(?m)^Pi-Behavior-Change:[ \t]*(\S+\.ts):\d+`)
	piPathRE   = regexp.MustCompile(`packages/[A-Za-z0-9_./-]+\.ts\b`)
	nilCheckRE = regexp.MustCompile(`[!=]= nil\b`)
	reportRE   = regexp.MustCompile(`^\s*t\.(?:Fatal|Fatalf|Error|Errorf|Log|Logf|Skip|Skipf)\(`)
	quotedRE   = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`")
)

func isExpectation(line string) bool {
	if reportRE.MatchString(line) || !literalRE.MatchString(line) {
		return false
	}
	code := nilCheckRE.ReplaceAllString(quotedRE.ReplaceAllString(line, `""`), "")
	return expectRE.MatchString(code)
}

func pathLike(literal string) bool {
	body := strings.Trim(literal, "\"`")
	return strings.Contains(body, "/") && !strings.ContainsAny(body, " \t\n")
}

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

func isProdGo(p string) bool {
	return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") && !strings.Contains(p, "/testdata/")
}

func ruleTestExpectation(d *data, p params, env *ruleEnv) outcome {
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
	cited := map[string]bool{}
	inBody := false
	for _, m := range piCiteRE.FindAllStringSubmatch(d.Pull.Body, -1) {
		inBody, cited[m[1]] = true, true
	}
	inCommit := false
	for _, c := range d.Commits {
		for _, m := range piCiteRE.FindAllStringSubmatch(c.Commit.Message, -1) {
			inCommit, cited[m[1]] = true, true
		}
	}
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
	known := map[string]bool{}
	for _, glob := range p.strs("inventories") {
		for _, m := range piPathRE.FindAllString(env.mainFiles(glob), -1) {
			known[path.Base(m)] = true
		}
	}
	var unknown []string
	for name := range cited {
		if !known[path.Base(name)] {
			unknown = append(unknown, name)
		}
	}
	slices.Sort(unknown)
	if len(unknown) > 0 {
		return fail("Pi-Behavior-Change cites %s, which no inventory of main names as a Pi source", strings.Join(unknown, ", "))
	}
	return pass("expected values changed with a cited Pi behavior change")
}

func ruleNoAdvisories(d *data, p params, _ *ruleEnv) outcome {
	severities := p.strs("severities")
	var bad []string
	for _, c := range d.Dependencies {
		if c.ChangeType != "added" {
			continue
		}
		for _, v := range c.Vulnerabilities {
			if slices.Contains(severities, v.Severity) {
				bad = append(bad, fmt.Sprintf("%s %s %s (%s %s)", c.Ecosystem, c.Name, c.Version, v.Severity, v.AdvisoryID))
			}
		}
	}
	if len(bad) > 0 {
		return fail("added dependencies with a %s advisory: %s", strings.Join(severities, " or "), strings.Join(bad, ", "))
	}
	return pass(fmt.Sprintf("%d dependency change(s), none with a %s advisory", len(d.Dependencies), strings.Join(severities, " or ")))
}

var (
	// REUSE-IgnoreStart
	// The tag declares a license where it opens its line or follows only a comment opener: "//", "/*", the "*" of a
	// block comment, "#", "<#", ";", "<!--", "--", "..", "%", "REM" or "::". After anything else on the line (a quote,
	// a key, code, prose) it is text of the file, not a declaration.
	spdxLicenseRE = regexp.MustCompile(`^\s*(?:(?://+!?|/\*+|\*+|#+|<#|;+|<!--|--+|\.\.|%+|(?i:@?rem)|::)\s*)?SPDX-License-Identifier:\s*([^\n*]+?)\s*(?:\*/|-->|$)`)
	licenseIDRE   = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9.+-]*`)
	copyrightRE   = regexp.MustCompile(`(?i)\b(?:SPDX-FileCopyrightText:|Copyright\s+(?:\(c\)|©|\d{4}))`)
	// REUSE-IgnoreEnd
	licenseTextRE = regexp.MustCompile(`(?i)Licensed under the Apache License|GNU (?:Lesser |Affero )?General Public License|Mozilla Public License|Eclipse Public License|Creative Commons|Redistribution and use in source and binary forms|Permission is hereby granted, free of charge`)
)

func validateLicenseNotices(p params) error {
	if err := needList(p, "notice-files"); err != nil {
		return err
	}
	for _, g := range p.strs("notice-files") {
		if _, err := globRegexp(g); err != nil {
			return err
		}
	}
	if err := p.need("code-files"); err != nil {
		return err
	}
	return p.regexps("code-files")
}

func ruleLicenseNotices(d *data, p params, env *ruleEnv) outcome {
	var notices []*regexp.Regexp
	for _, g := range p.strs("notice-files") {
		re, _ := globRegexp(g)
		notices = append(notices, re)
	}
	codeFile := pattern(p.str("code-files"))
	licenses := env.mainLicenses()
	var problems []string
	var warnings []warning
	for _, f := range d.Files {
		if f.Status == "removed" || slices.ContainsFunc(notices, func(re *regexp.Regexp) bool { return re.MatchString(f.Filename) }) {
			continue
		}
		hasHeader := false
		for _, l := range addedNumbered(f.Patch) {
			ids, header := declaredLicenses(l.text)
			hasHeader = hasHeader || header
			for _, id := range ids {
				if !slices.Contains(licenses, id) {
					problems = append(problems, fmt.Sprintf("%s:%d declares %s, which is not in LICENSES/", f.Filename, l.n, id))
				}
			}
			warnings = append(warnings, noticeWarnings(f, l)...)
		}
		if f.Status == "added" && codeFile.MatchString(f.Filename) && !hasHeader {
			warnings = append(warnings, warning{File: f.Filename, Blob: f.SHA, Notice: "no SPDX-License-Identifier header", Text: "is new and has no SPDX-License-Identifier header; REUSE.toml's blanket license covers it"})
		}
	}
	o := pass("no foreign license declared")
	if len(problems) > 0 {
		o = fail("%s; add the license text to LICENSES/ and an annotation in REUSE.toml, or remove the code", strings.Join(problems, "; "))
	}
	o.warnings = warnings
	return o
}

func declaredLicenses(line string) (ids []string, header bool) {
	m := spdxLicenseRE.FindStringSubmatch(line)
	if m == nil {
		return nil, false
	}
	for _, id := range licenseIDRE.FindAllString(m[1], -1) {
		if up := strings.ToUpper(id); up != "AND" && up != "OR" && up != "WITH" {
			ids = append(ids, id)
		}
	}
	return ids, true
}

func noticeWarnings(f prFile, l numberedLine) []warning {
	var ws []warning
	if copyrightRE.MatchString(l.text) {
		ws = append(ws, warning{File: f.Filename, Line: l.n, Blob: f.SHA, Notice: strings.TrimSpace(l.text), Text: "adds a copyright notice"})
	}
	if m := licenseTextRE.FindString(l.text); m != "" {
		ws = append(ws, warning{File: f.Filename, Line: l.n, Blob: f.SHA, Notice: strings.TrimSpace(l.text), Text: fmt.Sprintf("adds license text (%q): is this code from a third party?", m)})
	}
	return ws
}

func validateAddedLinesWarn(p params) error {
	if err := p.need("pattern", "text"); err != nil {
		return err
	}
	return p.regexps("pattern")
}

func ruleAddedLinesWarn(d *data, p params, _ *ruleEnv) outcome {
	re := pattern(p.str("pattern"))
	o := pass("checked the added lines")
	for _, f := range d.Files {
		for _, l := range addedNumbered(f.Patch) {
			if re.MatchString(l.text) {
				o.warnings = append(o.warnings, warning{File: f.Filename, Line: l.n, Blob: f.SHA, Notice: strings.TrimSpace(l.text), Text: p.str("text")})
			}
		}
	}
	return o
}
