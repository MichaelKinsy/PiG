// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsExpectation(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{`		{"a", want: "bc"},`, true},
		{`	if got != "abc" {`, true},
		{`	want := 42`, true},
		{`	assert.Equal(t, "abc", got)`, true},
		{`	if output, err := exec.CommandContext(ctx, "cc", "-o", oracle).CombinedOutput(); err != nil {`, false},
		{`	if err == nil && x == "" {`, false},
		{`		t.Fatalf("display=%+v, want %+v", got, tc.want)`, false},
		{`	name := "want this"`, false},
		{`	x := 1`, false},
	} {
		if got := isExpectation(tc.line); got != tc.want {
			t.Errorf("isExpectation(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestExpectationEditsCountsChangedValuesNotRenames(t *testing.T) {
	changed := "@@ -1 +1 @@\n-\twant := \"old value\"\n+\twant := \"new value\"\n"
	if n := expectationEdits(changed); n != 1 {
		t.Fatalf("changed value: %d, want 1", n)
	}
	renamed := "@@ -1 +1 @@\n-\twantOld := \"same value\"\n+\twantNew := \"same value\"\n"
	if n := expectationEdits(renamed); n != 0 {
		t.Fatalf("renamed identifier: %d, want 0", n)
	}
	added := "@@ -0,0 +1 @@\n+\twant := \"new case\"\n"
	if n := expectationEdits(added); n != 0 {
		t.Fatalf("pure addition: %d, want 0", n)
	}
}

func TestRuleLicenseNotices(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "LICENSES"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"MIT", "Apache-2.0"} {
		if err := os.WriteFile(filepath.Join(root, "LICENSES", id+".txt"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := &ruleEnv{root: root}
	p := params{"notice-files": []any{"THIRD_PARTY_NOTICES.md"}, "code-files": `\.go$`}
	d := &data{Files: []prFile{
		{Filename: "a/new.go", Status: "added", Patch: "@@ -0,0 +1,2 @@\n+package a\n+// Copyright (c) 2020 Someone Else"},
		{Filename: "b/ok.go", Status: "added", Patch: "@@ -0,0 +1,2 @@\n+// SPDX-License-Identifier: Apache-2.0 AND MIT\n+package b"},
		{Filename: "THIRD_PARTY_NOTICES.md", Status: "modified", Patch: "@@ -1 +1,2 @@\n x\n+GNU General Public License"},
	}}
	o := ruleLicenseNotices(d, p, env)
	if o.result != resultPass || len(o.warnings) != 2 {
		t.Fatalf("outcome %+v, want a pass with a copyright and a missing-header warning", o)
	}
	d.Files = append(d.Files, prFile{Filename: "c/gpl.go", Status: "added", Patch: "@@ -0,0 +1 @@\n+// SPDX-License-Identifier: GPL-3.0-only"})
	if o := ruleLicenseNotices(d, p, env); o.result != resultFail || !strings.Contains(o.reason, "c/gpl.go:1 declares GPL-3.0-only") {
		t.Fatalf("outcome %+v, want a fail naming GPL-3.0-only", o)
	}
}

func TestRuleAddedLinesWarn(t *testing.T) {
	d := &data{Files: []prFile{{Filename: "x.go", Status: "modified", Patch: "@@ -3 +3,2 @@\n ctx\n+key := \"abc\" // gitleaks:allow"}}}
	o := ruleAddedLinesWarn(d, params{"pattern": "gitleaks:allow", "text": "suppression"}, nil)
	if o.result != resultPass || len(o.warnings) != 1 || o.warnings[0].File != "x.go" || o.warnings[0].Line != 4 {
		t.Fatalf("outcome %+v", o)
	}
}

func TestAcknowledgedWarnings(t *testing.T) {
	w := warning{File: "a.sh", Line: 2, Blob: "blob1", Notice: "# Copyright 2020 X", Text: "adds a copyright notice"}
	d := &data{Files: []prFile{{Filename: "a.sh", SHA: "blob1"}, {Filename: "b.sh", SHA: "blob9"}}}
	ev := evaluation{Checks: map[string]verdict{"reuse": {Result: resultScan, Warnings: []warning{w}}}}
	acks, err := parseAcknowledged(ackEntry("reuse", w) + "\n" + ackEntry("reuse", warning{File: "b.sh", Blob: "blob8", Notice: "old"}))
	if err != nil {
		t.Fatal(err)
	}
	applyAcknowledged(&ev, d, acks)
	got := ev.Checks["reuse"].Warnings
	if len(got) != 2 || !got[1].Acknowledged {
		t.Fatalf("a matching entry did not acknowledge the warning: %+v", got)
	}
	if got[0].File != "b.sh" || !strings.Contains(got[0].Text, "the file changed") {
		t.Fatalf("want a stale-entry warning for b.sh first, got %+v", got)
	}
	applyAcknowledged(&ev, d, acks)
	if again := ev.Checks["reuse"].Warnings; len(again) != 2 {
		t.Fatalf("applied twice: %+v", again)
	}
	w.Blob = "blob2"
	ev = evaluation{Checks: map[string]verdict{"reuse": {Result: resultScan, Warnings: []warning{w}}}}
	d.Files[0].SHA = "blob2"
	applyAcknowledged(&ev, d, acks)
	if ev.Checks["reuse"].Warnings[0].Acknowledged {
		t.Fatal("an entry for an older blob acknowledged the warning")
	}
}

func TestParseAcknowledgedRejects(t *testing.T) {
	for _, text := range []string{"[[ack]]\ncheck = 'reuse'\n", "[[ack]]\ncheck='a'\nfile='b'\nblob='d'\nextra=1\n", "not toml ="} {
		if _, err := parseAcknowledged(text); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
}

func commit(sha, message string, verified bool) prCommit {
	var c prCommit
	c.SHA = sha
	c.Commit.Message = message
	c.Commit.Verification.Verified = verified
	c.Commit.Verification.Reason = "unsigned"
	if verified {
		c.Commit.Verification.Reason = "valid"
	}
	return c
}

func shippedRule(t *testing.T, name string) (builtinRule, params) {
	t.Helper()
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cfg.Checks {
		if c.Name == name {
			return builtinRules[c.Rule], params(c.With)
		}
	}
	t.Fatalf("no check %q", name)
	return builtinRule{}, nil
}

func TestRuleBodyFields(t *testing.T) {
	r, p := shippedRule(t, "template")
	full := "Tracking: https://github.com/o/r/issues/1\nDepends on: None\nFix: no\nUser-facing: yes\n"
	for _, tc := range []struct {
		name, body string
		result     string
		missing    string
	}{
		{"filled in", full, resultPass, ""},
		{"no issue with a reason", strings.Replace(full, "https://github.com/o/r/issues/1", "No issue: a typo", 1), resultPass, ""},
		{"empty body", "  \n", resultFail, "is empty"},
		{"placeholder tracking", strings.Replace(full, "https://github.com/o/r/issues/1", "<issue URL>", 1), resultFail, "Tracking"},
		{"depends on a word", strings.Replace(full, "None", "TBD", 1), resultFail, "Depends on"},
		{"fix maybe", strings.Replace(full, "Fix: no", "Fix: maybe", 1), resultFail, "`Fix: yes` or `Fix: no`"},
		{"missing user-facing", strings.Replace(full, "User-facing: yes\n", "", 1), resultFail, "User-facing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := r.run(&data{Pull: pull{Body: tc.body}}, p, nil)
			if o.result != tc.result || !strings.Contains(o.reason, tc.missing) {
				t.Fatalf("outcome %+v, want %s naming %q", o, tc.result, tc.missing)
			}
		})
	}
}

func TestRuleAllCommits(t *testing.T) {
	signed, sp := shippedRule(t, "signed")
	signoff, op := shippedRule(t, "signoff")
	good := commit("aaaaaaaaaa", "x: y\n\nSigned-off-by: A B <a@b.c>", true)
	unsigned := commit("bbbbbbbbbb", "x: z\n\nSigned-off-by: A B <a@b.c>", false)
	noSignoff := commit("cccccccccc", "x: w", true)
	if o := signed.run(&data{Commits: []prCommit{good, noSignoff}}, sp, nil); o.result != resultPass {
		t.Fatalf("signed: %+v", o)
	}
	if o := signed.run(&data{Commits: []prCommit{good, unsigned}}, sp, nil); o.result != resultFail || !strings.Contains(o.reason, "bbbbbbbbb (unsigned)") || strings.Contains(o.reason, "aaaaaaaaa") {
		t.Fatalf("unsigned: %+v", o)
	}
	if o := signoff.run(&data{Commits: []prCommit{good, unsigned}}, op, nil); o.result != resultPass {
		t.Fatalf("signoff: %+v", o)
	}
	if o := signoff.run(&data{Commits: []prCommit{good, noSignoff}}, op, nil); o.result != resultFail || !strings.Contains(o.reason, "ccccccccc") {
		t.Fatalf("no sign-off: %+v", o)
	}
}

func TestRuleDependsOn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		dependsOn *pull
		result    string
		reason    string
	}{
		{"none", "Depends on: None", nil, resultPass, "nothing"},
		{"no line", "", nil, resultFail, "no valid"},
		{"merged", "Depends on: #7", &pull{Number: 7, Merged: true, State: "closed"}, resultPass, "#7"},
		{"open", "Depends on: #7", &pull{Number: 7, State: "open"}, resultFail, "#7, which is open"},
		{"not fetched", "Depends on: #7", nil, resultError, "not fetched"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := ruleDependsOn(&data{Pull: pull{Body: tc.body}, DependsOn: tc.dependsOn}, nil, nil)
			if o.result != tc.result || !strings.Contains(o.reason, tc.reason) {
				t.Fatalf("outcome %+v", o)
			}
		})
	}
}

func TestRuleUpToDate(t *testing.T) {
	base := func(behind int, mergeable *bool) *data {
		d := &data{CompareMain: comparison{BehindBy: behind}}
		d.Pull.Mergeable, d.Pull.Base.Ref = mergeable, "main"
		return d
	}
	for _, tc := range []struct {
		name   string
		d      *data
		result string
		reason string
	}{
		{"current", base(0, new(true)), resultPass, "up to date"},
		{"behind", base(3, new(true)), resultFail, "3 commit(s) behind main"},
		{"conflict", base(0, new(false)), resultFail, "conflicts with main"},
		{"both", base(2, new(false)), resultFail, "behind main; rebase on it; conflicts"},
		{"not computed", base(0, nil), resultPending, "the next run decides it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if o := ruleUpToDate(tc.d, nil, nil); o.result != tc.result || !strings.Contains(o.reason, tc.reason) {
				t.Fatalf("outcome %+v", o)
			}
		})
	}
}

func TestRuleFixLedger(t *testing.T) {
	r, p := shippedRule(t, "bug-ledger")
	fixCommit := commit("aaaaaaaaaa", "exec: fix the stall race", true)
	plain := commit("bbbbbbbbbb", "exec: tidy", true)
	ledger := prFile{Filename: "docs/bug-ledger.json"}
	for _, tc := range []struct {
		name    string
		body    string
		commits []prCommit
		files   []prFile
		result  string
		reason  string
	}{
		{"not a fix", "Fix: no", []prCommit{plain}, nil, resultPass, "not a fix"},
		{"says no but a fix title", "Fix: no", []prCommit{fixCommit}, nil, resultFail, "says `Fix: no`, but commit aaaaaaaaa"},
		{"unknown", "", []prCommit{plain}, nil, resultFail, "cannot tell"},
		{"fix without ledger", "Fix: yes", []prCommit{plain}, nil, resultFail, "make bug-ledger"},
		{"fix title without field still needs the ledger", "", []prCommit{fixCommit}, nil, resultFail, "make bug-ledger"},
		{"fix with ledger", "Fix: yes", []prCommit{fixCommit}, []prFile{ledger}, resultPass, "docs/bug-ledger.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := r.run(&data{Pull: pull{Body: tc.body}, Commits: tc.commits, Files: tc.files}, p, nil)
			if o.result != tc.result || !strings.Contains(o.reason, tc.reason) {
				t.Fatalf("outcome %+v", o)
			}
		})
	}
}

func TestRuleChangelog(t *testing.T) {
	c1 := commit("1234567890abcdef", "x: change", true)
	fragment := func(name, patch string) prFile { return prFile{Filename: name, Status: "added", Patch: patch} }
	fixed := "@@ -0,0 +1,3 @@\n+### Fixed\n+\n+- It works. <!-- fix: 1234567890 -->"
	for _, tc := range []struct {
		name   string
		body   string
		files  []prFile
		result string
		reason string
	}{
		{"no answer", "", nil, resultFail, "cannot tell"},
		{"neither fix nor user-facing", "Fix: no\nUser-facing: no", nil, resultPass, "no user-facing change"},
		{"user-facing without fragment", "Fix: no\nUser-facing: yes", nil, resultFail, "needs a fragment"},
		{"user-facing with fragment", "Fix: no\nUser-facing: yes", []prFile{fragment("changelog.d/lane-1234567.md", "@@ -0,0 +1 @@\n+### Added")}, resultPass, "1 valid"},
		{"misnamed", "Fix: no\nUser-facing: yes", []prFile{fragment("changelog.d/notes.md", "@@ -0,0 +1 @@\n+### Added")}, resultFail, "not named"},
		{"names another commit", "Fix: no\nUser-facing: yes", []prFile{fragment("changelog.d/lane-abcdef1.md", "@@ -0,0 +1 @@\n+### Added")}, resultFail, "not a commit of this PR"},
		{"fix with a marked bullet", "Fix: yes", []prFile{fragment("changelog.d/lane-1234567.md", fixed)}, resultPass, "1 valid"},
		{"fix without a marker", "Fix: yes", []prFile{fragment("changelog.d/lane-1234567.md", "@@ -0,0 +1,2 @@\n+### Fixed\n+- It works.")}, resultFail, "no <!-- fix: <sha> --> marker"},
		{"fix marker of another commit", "Fix: yes", []prFile{fragment("changelog.d/lane-1234567.md", strings.Replace(fixed, "1234567890", "fedcba9876", 1))}, resultFail, "fix marker fedcba9876"},
		{"fix without a fixed section", "Fix: yes", []prFile{fragment("changelog.d/lane-1234567.md", "@@ -0,0 +1 @@\n+### Changed")}, resultFail, "needs a ### Fixed bullet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := ruleChangelog(&data{Pull: pull{Body: tc.body}, Commits: []prCommit{c1}, Files: tc.files}, nil, nil)
			if o.result != tc.result || !strings.Contains(o.reason, tc.reason) {
				t.Fatalf("outcome %+v", o)
			}
		})
	}
}

func TestRuleTestExpectation(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "parity", "PORT_MAP.md"), []byte("| packages/coding-agent/src/core/agent.ts | x |\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, p := shippedRule(t, "test-expectation")
	edit := []prFile{
		{Filename: "a/a.go", Status: "modified", Patch: "@@ -1 +1 @@\n-x := 1\n+x := 2"},
		{Filename: "a/a_test.go", Status: "modified", Patch: "@@ -1 +1 @@\n-\twant := \"old value\"\n+\twant := \"new value\""},
	}
	cite := "Pi-Behavior-Change: packages/coding-agent/src/core/agent.ts:12"
	for _, tc := range []struct {
		name   string
		body   string
		msg    string
		files  []prFile
		result string
		reason string
	}{
		{"test only", "", "", edit[1:], resultPass, "no expected value"},
		{"uncited", "", "", edit, resultFail, "in the PR body and a commit trailer"},
		{"body only", cite, "", edit, resultFail, "in a commit trailer"},
		{"cited in both", cite, "a: b\n\n" + cite, edit, resultPass, "cited"},
		{"unknown Pi file", strings.Replace(cite, "agent.ts", "invented.ts", 1), "a: b\n\n" + strings.Replace(cite, "agent.ts", "invented.ts", 1), edit, resultFail, "invented.ts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &data{Pull: pull{Body: tc.body}, Commits: []prCommit{commit("aaaaaaaaaa", tc.msg, true)}, Files: tc.files}
			o := r.run(d, p, &ruleEnv{root: root})
			if o.result != tc.result || !strings.Contains(o.reason, tc.reason) {
				t.Fatalf("outcome %+v", o)
			}
		})
	}
}

func TestRuleNoAdvisories(t *testing.T) {
	r, p := shippedRule(t, "dependency-review")
	change := func(changeType, severity string) dependencyChange {
		c := dependencyChange{ChangeType: changeType, Ecosystem: "npm", Name: "left-pad", Version: "1.0.0"}
		c.Vulnerabilities = append(c.Vulnerabilities, struct {
			Severity    string `json:"severity"`
			AdvisoryID  string `json:"advisory_ghsa_id"`
			AdvisoryURL string `json:"advisory_url"`
		}{Severity: severity, AdvisoryID: "GHSA-x"})
		return c
	}
	if o := r.run(&data{Dependencies: []dependencyChange{change("added", "moderate")}}, p, nil); o.result != resultPass {
		t.Fatalf("moderate: %+v", o)
	}
	if o := r.run(&data{Dependencies: []dependencyChange{change("removed", "critical")}}, p, nil); o.result != resultPass {
		t.Fatalf("a removed dependency: %+v", o)
	}
	if o := r.run(&data{Dependencies: []dependencyChange{change("added", "high")}}, p, nil); o.result != resultFail || !strings.Contains(o.reason, "left-pad 1.0.0 (high GHSA-x)") {
		t.Fatalf("high: %+v", o)
	}
}

func TestShippedRulesAreAllUsed(t *testing.T) {
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, c := range cfg.Checks {
		used[c.Rule] = true
	}
	for name := range builtinRules {
		if !used[name] {
			t.Errorf("built-in rule %q is used by no check of config.yaml", name)
		}
	}
}

func TestRuleParamsRejected(t *testing.T) {
	for _, tc := range []struct{ name, check, want string }{
		{"body-fields without help", `{name: a, when: {always: true}, stage: cheap, reads: [pull], rule: body-fields, with: {fields: [{name: Fix, pattern: x}]}}`, "needs help"},
		{"body-fields with a broken pattern", `{name: a, when: {always: true}, stage: cheap, reads: [pull], rule: body-fields, with: {fields: [{name: Fix, pattern: '(', help: h}]}}`, "pattern:"},
		{"body-fields without fields", `{name: a, when: {always: true}, stage: cheap, reads: [pull], rule: body-fields}`, "needs fields"},
		{"all-commits without help", `{name: a, when: {always: true}, stage: cheap, reads: [commits], rule: all-commits, with: {verified: true}}`, "needs help"},
		{"all-commits with a broken message", `{name: a, when: {always: true}, stage: cheap, reads: [commits], rule: all-commits, with: {message: '[', help: h}}`, "message:"},
		{"license-notices without notice files", `{name: a, when: {always: true}, stage: cheap, reads: [files], rule: license-notices, with: {code-files: x}}`, "notice-files"},
		{"license-notices with an absolute notice file", `{name: a, when: {always: true}, stage: cheap, reads: [files], rule: license-notices, with: {notice-files: [/x], code-files: x}}`, "must be relative"},
		{"license-notices without code files", `{name: a, when: {always: true}, stage: cheap, reads: [files], rule: license-notices, with: {notice-files: [x]}}`, "needs code-files"},
		{"license-notices with a broken code-files", `{name: a, when: {always: true}, stage: cheap, reads: [files], rule: license-notices, with: {notice-files: [x], code-files: '('}}`, "code-files:"},
		{"added-lines-warn without text", `{name: a, when: {always: true}, stage: cheap, reads: [files], rule: added-lines-warn, with: {pattern: x}}`, "needs text"},
		{"added-lines-warn with a broken pattern", `{name: a, when: {always: true}, stage: cheap, reads: [files], rule: added-lines-warn, with: {pattern: '(', text: t}}`, "pattern:"},
		{"fix-ledger without ledger", `{name: a, when: {always: true}, stage: cheap, reads: [files], rule: fix-ledger}`, "needs ledger"},
		{"test-expectation without inventories", `{name: a, when: {always: true}, stage: cheap, reads: [files], rule: test-expectation}`, "inventories"},
		{"no-advisories without severities", `{name: a, when: {always: true}, stage: cheap, reads: [dependency-graph], rule: no-advisories}`, "severities"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadConfig([]byte("engine-revision: 1\nchecks: [" + tc.check + "]"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestRuleChangelogEdges(t *testing.T) {
	c1 := commit("1234567890abcdef", "x: change", true)
	run := func(body string, files ...prFile) outcome {
		return ruleChangelog(&data{Pull: pull{Body: body}, Commits: []prCommit{c1}, Files: files}, nil, nil)
	}
	if o := run("Fix: no"); o.result != resultPass {
		t.Errorf("only Fix answered, no fix: %+v", o)
	}
	if o := run("User-facing: yes", prFile{Filename: "docs/notes.md", Status: "added"}, prFile{Filename: "changelog.d/lane-1234567.txt", Status: "added"}); !strings.Contains(o.reason, "needs a fragment") {
		t.Errorf("a .md outside changelog.d or a non-.md inside it counted as a fragment: %+v", o)
	}
	if o := run("User-facing: yes", prFile{Filename: "changelog.d/lane-1234567.md", Status: "removed"}); !strings.Contains(o.reason, "needs a fragment") {
		t.Errorf("a removed fragment counted: %+v", o)
	}
	two := "@@ -0,0 +1,3 @@\n+### Fixed\n+- One.\n+- Two. <!-- fix: fedcba9876 -->"
	o := run("Fix: yes", prFile{Filename: "changelog.d/lane-1234567.md", Status: "added", Patch: two})
	if !strings.Contains(o.reason, "no <!-- fix: <sha> --> marker") || !strings.Contains(o.reason, "fix marker fedcba9876") {
		t.Errorf("a bullet after an unmarked one was not checked: %+v", o)
	}
}

func TestExpectationEditsCounts(t *testing.T) {
	patch := func(removed, added []string) string {
		var b strings.Builder
		b.WriteString("@@ -1,9 +1,9 @@\n")
		for _, l := range removed {
			b.WriteString("-" + l + "\n")
		}
		for _, l := range added {
			b.WriteString("+" + l + "\n")
		}
		return b.String()
	}
	for _, tc := range []struct {
		name           string
		removed, added []string
		want           int
	}{
		{"a one-word value", []string{`want := "abc"`}, []string{`want := "def"`}, 1},
		{"two values on one line", []string{`want := []string{"aa", "bb"}`}, []string{`want := []string{"cc", "dd"}`}, 2},
		{"a moved path", []string{`want := "a/b"`}, []string{`want := "a/c"`}, 0},
		{"a kept value plus two new ones", []string{`want := "xx"`}, []string{`want := "xx"`, `want := "yy"`, `want := "zz"`}, 0},
		{"two removed, one kept", []string{`want := "xx"`, `want := "yy"`, `want := "zz"`}, []string{`want := "xx"`}, 0},
		{"a value after a plain line", []string{`x := 1`, `want := "abc"`}, []string{`x := 2`, `want := "def"`}, 1},
		{"three replaced by two", []string{`want := "xx"`, `want := "yy"`, `want := "zz"`}, []string{`want := "pp"`, `want := "qq"`}, 2},
		{"two replaced by three", []string{`want := "xx"`, `want := "yy"`}, []string{`want := "pp"`, `want := "qq"`, `want := "rr"`}, 2},
	} {
		if got := expectationEdits(patch(tc.removed, tc.added)); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestRuleTestExpectationEdges(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs", "parity"), 0o755); err != nil {
		t.Fatal(err)
	}
	inventory := "packages/a/src/first.ts\npackages/b/src/second.ts\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "parity", "PORT_MAP.md"), []byte(inventory), 0o600); err != nil {
		t.Fatal(err)
	}
	r, p := shippedRule(t, "test-expectation")
	edit := []prFile{
		{Filename: "a/a.go", Status: "modified", Patch: "@@ -1 +1 @@\n-x := 1\n+x := 2"},
		{Filename: "a/a_test.go", Status: "modified", Patch: "@@ -1 +1 @@\n-\twant := \"old value\"\n+\twant := \"new value\""},
	}
	run := func(body, message string, files []prFile) outcome {
		return r.run(&data{Pull: pull{Body: body}, Commits: []prCommit{commit("aaaaaaaaaa", message, true)}, Files: files}, p, &ruleEnv{root: root})
	}
	untouched := []prFile{edit[0], {Filename: "a/a_test.go", Status: "modified", Patch: "@@ -1 +1,2 @@\n x\n+// a comment"}}
	if o := run("", "", untouched); o.result != resultPass {
		t.Errorf("a test change without an expected value failed: %+v", o)
	}
	second := "Pi-Behavior-Change: packages/b/src/second.ts:3"
	if o := run(second, "a: b\n\n"+second, edit); o.result != resultPass {
		t.Errorf("the second path of an inventory was not read: %+v", o)
	}
	both := "Pi-Behavior-Change: packages/a/src/first.ts:1\nPi-Behavior-Change: packages/x/invented.ts:2"
	if o := run(both, "a: b\n\nPi-Behavior-Change: packages/a/src/first.ts:1", edit); !strings.Contains(o.reason, "invented.ts") {
		t.Errorf("a second citation in the body was not checked: %+v", o)
	}
	if o := run("Pi-Behavior-Change: packages/a/src/first.ts:1", "a: b\n\n"+both, edit); !strings.Contains(o.reason, "invented.ts") {
		t.Errorf("a second citation in a commit was not checked: %+v", o)
	}
}

func TestRuleNoAdvisoriesAfterARemoval(t *testing.T) {
	r, p := shippedRule(t, "dependency-review")
	mk := func(changeType, severity string) dependencyChange {
		c := dependencyChange{ChangeType: changeType, Name: changeType}
		c.Vulnerabilities = append(c.Vulnerabilities, struct {
			Severity    string `json:"severity"`
			AdvisoryID  string `json:"advisory_ghsa_id"`
			AdvisoryURL string `json:"advisory_url"`
		}{Severity: severity})
		return c
	}
	if o := r.run(&data{Dependencies: []dependencyChange{mk("removed", "critical"), mk("added", "high")}}, p, nil); o.result != resultFail {
		t.Fatalf("an added advisory after a removed dependency passed: %+v", o)
	}
}

func TestRuleLicenseNoticesEdges(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "LICENSES"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "LICENSES", "MIT.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r, p := shippedRule(t, "licenses")
	env := &ruleEnv{root: root}
	expression := prFile{Filename: "x.go", Status: "added", Patch: "@@ -0,0 +1 @@\n+// SPDX-License-Identifier: MIT AND GPL-3.0-only"}
	if o := r.run(&data{Files: []prFile{expression}}, p, env); o.result != resultFail || !strings.Contains(o.reason, "GPL-3.0-only") {
		t.Errorf("the license after AND was not checked: %+v", o)
	}
	quiet := []prFile{
		{Filename: "old.go", Status: "modified", Patch: "@@ -1 +1,2 @@\n x\n+y"},
		{Filename: "notes.txt", Status: "added", Patch: "@@ -0,0 +1 @@\n+text"},
	}
	if o := r.run(&data{Files: quiet}, p, env); len(o.warnings) != 0 {
		t.Errorf("a modified code file or a new non-code file got a missing-header warning: %+v", o.warnings)
	}
}
