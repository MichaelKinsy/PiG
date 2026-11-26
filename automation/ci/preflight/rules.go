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

	"github.com/MichaelKinsy/PiG/automation/ci/preflight/reviewed"
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
	// notices name what a rule let pass that a reviewer should see; a notice asks for nothing.
	notices []string
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
	// fixExempt is config.yaml's fix-title-exempt list.
	fixExempt []fixTitleExemption

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
		"changed-paths-warn": {validateChangedPathsWarn, ruleChangedPathsWarn},
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

// exemption is the fix-title-exempt entry of pull request pr, or nil.
func (e *ruleEnv) exemption(pr int) *fixTitleExemption {
	if e == nil {
		return nil
	}
	for i := range e.fixExempt {
		if e.fixExempt[i].PR == pr {
			return &e.fixExempt[i]
		}
	}
	return nil
}

// fixEvidence returns what says this pull request fixes something, and the fix-titled commits that its fix-title-exempt
// entry lists, which say nothing. A changelog fragment is never exempt.
func fixEvidence(d *data, env *ruleEnv) (why []string, exempt []string) {
	entry := env.exemption(d.PR)
	for _, c := range d.Commits {
		title, _, _ := strings.Cut(c.Commit.Message, "\n")
		switch {
		case !fixTitleRE.MatchString(title):
		case entry != nil && slices.Contains(entry.Commits, c.SHA):
			exempt = append(exempt, short(c.SHA))
		case entry != nil:
			why = append(why, fmt.Sprintf("commit %s %q, which the fix-title-exempt entry of PR #%d in automation/ci/preflight/config.yaml does not list (its list must be reviewed again before it names this commit)", short(c.SHA), title, d.PR))
		default:
			why = append(why, fmt.Sprintf("commit %s %q", short(c.SHA), title))
		}
	}
	for _, f := range d.Files {
		if isFragment(f.Filename) && slices.Contains(addedLines(f.Patch), "### Fixed") {
			why = append(why, f.Filename+" adds a ### Fixed section")
		}
	}
	return why, exempt
}

func isFragment(name string) bool {
	return path.Dir(name) == "changelog.d" && strings.HasSuffix(name, ".md")
}

func changed(d *data, name string) bool {
	return slices.ContainsFunc(d.Files, func(f prFile) bool { return f.Filename == name })
}

func ruleFixLedger(d *data, p params, env *ruleEnv) outcome {
	isFix, ok := yesNo(d.Pull.Body, "Fix")
	evidence, exempt := fixEvidence(d, env)
	switch {
	case ok && !isFix && len(evidence) > 0:
		return fail("the template says `Fix: no`, but %s", strings.Join(evidence, "; "))
	case !ok && len(evidence) == 0:
		return fail("cannot tell whether this is a fix: the template has no `Fix: yes` or `Fix: no`")
	case !isFix && len(evidence) == 0 && len(exempt) > 0:
		o := pass(fmt.Sprintf("not a fix: %d fix-titled commit(s) are exempt", len(exempt)))
		o.notices = []string{fmt.Sprintf("PR #%d: fix-title-exempt in automation/ci/preflight/config.yaml exempts %d fix-titled commit(s) (%s): %s", d.PR, len(exempt), strings.Join(exempt, ", "), strings.TrimSpace(env.exemption(d.PR).Reason))}
		return o
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

func ruleChangelog(d *data, _ params, env *ruleEnv) outcome {
	isFix, fixOK := yesNo(d.Pull.Body, "Fix")
	userFacing, ufOK := yesNo(d.Pull.Body, "User-facing")
	if why, _ := fixEvidence(d, env); len(why) > 0 {
		isFix = true
	}
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

// editedTest is a test file of the PR that changes expected values together with production code of its directory.
type editedTest struct {
	name  string
	edits int
}

func (e editedTest) String() string { return fmt.Sprintf("%s (%d value(s))", e.name, e.edits) }

func joinEdited(edited []editedTest) string {
	names := make([]string, len(edited))
	for i, e := range edited {
		names[i] = e.String()
	}
	return strings.Join(names, ", ")
}

// divergenceRecorded tells whether the record of divergence id is in main or is added by the PR.
func divergenceRecorded(d *data, env *ruleEnv) func(string) bool {
	return func(id string) bool {
		file := reviewed.DivergenceFile(id)
		if slices.ContainsFunc(d.Files, func(f prFile) bool { return f.Filename == file && f.Status != "removed" }) {
			return true
		}
		_, err := os.Stat(filepath.Join(env.root, filepath.FromSlash(file)))
		return err == nil
	}
}

// maxLedgerCommits bounds the commits whose file lists the pre-flight fetches for ledger rows: one call per commit that
// a row names for a changed test file. A PR with rows of more commits is a finding that asks to split it.
const maxLedgerCommits = 50

// commitOfRow is the commit of the PR that the row's commit prefix names, and how many commits of the PR it names.
func commitOfRow(d *data, r reviewed.Entry) (sha string, named int) {
	for _, c := range d.Commits {
		if r.NamesCommit(c.SHA) {
			sha = c.SHA
			named++
		}
	}
	return sha, named
}

// ledgerCommits are the commits whose file lists decide the admissions: those that a row names, alone among the PR's
// commits, for a test file the check flags. Each is listed once, in the order of the ledger.
func ledgerCommits(d *data, ledger []reviewed.Entry) []string {
	var out []string
	for _, e := range editedTests(d) {
		for _, r := range ledger {
			if sha, named := commitOfRow(d, r); r.Path == e.name && named == 1 && !slices.Contains(out, sha) {
				out = append(out, sha)
			}
		}
	}
	return out
}

// admitByLedger splits the edited test files into those a reviewed ledger row admits and those it leaves open. A row
// applies to a test file when its path is the file and its commit prefix names exactly one commit of the PR. An
// applying row that fails the ledger's validation, whose prefix names several commits of the PR, or whose commit does
// not change the file (the line lint requires it too) is a problem: the file is not admitted. Each admission has a
// notice naming the row.
func admitByLedger(d *data, ledger []reviewed.Entry, known map[string]bool, recorded func(string) bool, edited []editedTest) (open []editedTest, notices, problems []string) {
	for _, e := range edited {
		admitted := false
		for _, r := range ledger {
			if r.Path != e.name {
				continue
			}
			sha, named := commitOfRow(d, r)
			row := fmt.Sprintf("%s %s", short10(r.Commit), r.Path)
			files, listed := d.CommitFiles[sha]
			switch err := r.Validate(known, recorded); {
			case named == 0:
			case named > 1:
				problems = append(problems, fmt.Sprintf("ledger row %s names %d commits of the PR; give a longer commit prefix", row, named))
			case err != nil:
				problems = append(problems, fmt.Sprintf("ledger row %s %s", row, err))
			case !listed:
				problems = append(problems, fmt.Sprintf("ledger row %s: the files of that commit were not fetched, so the row cannot be checked", row))
			case !slices.Contains(files, r.Path):
				problems = append(problems, fmt.Sprintf("ledger row %s: that commit does not change the file", row))
			default:
				admitted = true
				notices = append(notices, fmt.Sprintf("%s: expected-value change admitted by reviewed ledger row %s, class %s%s: %s", e.name, short10(r.Commit), r.Class, citeOf(r), ledgerText(r.Reason)))
			}
		}
		if !admitted {
			open = append(open, e)
		}
	}
	return open, notices, problems
}

// ledgerText is a text of a ledger row as a notice holds it: on one line and whole. A notice is stored in the record
// of the verdict and a later run reuses it, so it is never cut here; the report cuts it when it prints (shownNotice).
// The text is bounded by the ledger file, which the contents endpoint serves up to 1 MiB.
func ledgerText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func citeOf(r reviewed.Entry) string {
	if r.Cite == "" {
		return ""
	}
	return " " + ledgerText(r.Cite)
}

func short10(sha string) string { return sha[:min(len(sha), 10)] }

// skipTooling splits the edited test files into those that lie under a [[tooling]] prefix of test/testdata-provenance.toml
// on main, which the line lint leaves out of the expected-value rule too, and the others. Each skip is a notice. The list
// is main's checkout only: a prefix that the pull request adds does not exempt the pull request. A list that is missing,
// empty or not TOML is an error of the rule, never a silent skip.
func skipTooling(edited []editedTest, env *ruleEnv) (open []editedTest, notices []string, err error) {
	text := env.mainFiles(reviewed.ProvenanceFile)
	if strings.TrimSpace(text) == "" {
		return nil, nil, fmt.Errorf("%s is missing or empty on main", reviewed.ProvenanceFile)
	}
	list, err := reviewed.ParseTooling(text)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range edited {
		if prefix, ok := reviewed.InTooling(list, e.name); ok {
			notices = append(notices, fmt.Sprintf("%s: expected-value change skipped, tooling prefix %s of %s on main", e.name, prefix, reviewed.ProvenanceFile))
		} else {
			open = append(open, e)
		}
	}
	return open, notices, nil
}

func ruleTestExpectation(d *data, p params, env *ruleEnv) outcome {
	edited := editedTests(d)
	if len(edited) == 0 {
		return pass("no expected value changed together with production code of its directory")
	}
	edited, tooling, err := skipTooling(edited, env)
	if err != nil {
		return fail("test files change expected values together with production code, and the tooling directories cannot be read, so no file is skipped: %v", err)
	}
	if len(edited) == 0 {
		o := pass(fmt.Sprintf("%d changed test file(s) under tooling prefixes of %s on main", len(tooling), reviewed.ProvenanceFile))
		o.notices = tooling
		return o
	}
	known := map[string]bool{}
	for _, glob := range p.strs("inventories") {
		for _, m := range piPathRE.FindAllString(env.mainFiles(glob), -1) {
			known[path.Base(m)] = true
		}
	}
	ledger, err := reviewed.Parse(d.ReviewedLedger)
	if err != nil {
		o := fail("%s change expected values together with production code, and the reviewed ledger at the PR head cannot be read, so no row admits them: %v", joinEdited(edited), err)
		o.notices = tooling
		return o
	}
	if n := len(ledgerCommits(d, ledger)); n > maxLedgerCommits {
		o := fail("the reviewed ledger holds rows of %d commits of the PR for its changed test files; the pre-flight checks at most %d: split the PR (%s change expected values together with production code)", n, maxLedgerCommits, joinEdited(edited))
		o.notices = tooling
		return o
	}
	open, notices, problems := admitByLedger(d, ledger, known, divergenceRecorded(d, env), edited)
	notices = append(tooling, notices...)
	if len(problems) > 0 {
		o := fail("%s", strings.Join(problems, "; "))
		o.notices = notices
		return o
	}
	if len(open) == 0 {
		o := pass(fmt.Sprintf("%d changed test file(s) admitted by reviewed ledger rows or skipped by tooling prefixes", len(notices)))
		o.notices = notices
		return o
	}
	o := citedExpectation(d, open, known)
	o.notices = notices
	return o
}

// editedTests are the test files of the PR that change an expected value together with production code of their
// directory.
func editedTests(d *data) []editedTest {
	prodDirs := map[string]bool{}
	for _, f := range d.Files {
		if isProdGo(f.Filename) {
			prodDirs[path.Dir(f.Filename)] = true
		}
	}
	var edited []editedTest
	for _, f := range d.Files {
		if strings.HasSuffix(f.Filename, "_test.go") && prodDirs[path.Dir(f.Filename)] {
			if n := expectationEdits(f.Patch); n > 0 {
				edited = append(edited, editedTest{f.Filename, n})
			}
		}
	}
	return edited
}

// citedExpectation decides the edited test files no ledger row admits: the Pi behavior that changed is cited in the PR
// body and in a commit trailer, and the cited Pi file is one a main inventory names.
func citedExpectation(d *data, edited []editedTest, known map[string]bool) outcome {
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
		return fail("%s change expected values together with production code; cite the Pi behavior that changed as `Pi-Behavior-Change: <pi file.ts:line>` in %s, or add a row for the commit and the file to %s in a later commit of the PR", joinEdited(edited), strings.Join(missing, " and "), reviewed.File)
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

func validateChangedPathsWarn(p params) error {
	if err := needList(p, "paths"); err != nil {
		return err
	}
	for _, glob := range p.strs("paths") {
		if _, err := globRegexp(glob); err != nil {
			return fmt.Errorf("paths: %w", err)
		}
	}
	return p.need("text")
}

func ruleChangedPathsWarn(d *data, p params, _ *ruleEnv) outcome {
	var matchers []*regexp.Regexp
	for _, glob := range p.strs("paths") {
		matchers = append(matchers, mustGlobRegexp(glob))
	}
	o := pass("checked changed paths")
	for _, f := range d.Files {
		for _, re := range matchers {
			if re.MatchString(f.Filename) {
				o.warnings = append(o.warnings, warning{
					File:   f.Filename,
					Blob:   f.SHA,
					Notice: f.Status + " " + f.Filename,
					Text:   p.str("text"),
				})
				break
			}
		}
	}
	return o
}

func mustGlobRegexp(glob string) *regexp.Regexp {
	re, err := globRegexp(glob)
	if err != nil {
		panic(err)
	}
	return re
}
