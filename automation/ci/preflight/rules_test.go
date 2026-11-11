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
	if !got[0].Acknowledged {
		t.Fatal("a matching entry did not acknowledge the warning")
	}
	if len(got) != 2 || got[1].File != "b.sh" || !strings.Contains(got[1].Text, "the file changed") {
		t.Fatalf("want a stale-entry warning for b.sh, got %+v", got)
	}
	// A changed blob is a new warning: the entry for the old blob does not cover it.
	w.Blob = "blob2"
	ev = evaluation{Checks: map[string]verdict{"reuse": {Result: resultScan, Warnings: []warning{w}}}}
	d.Files[0].SHA = "blob2"
	applyAcknowledged(&ev, d, acks)
	if ev.Checks["reuse"].Warnings[0].Acknowledged {
		t.Fatal("an entry for an older blob acknowledged the warning")
	}
}

func TestParseAcknowledgedRejects(t *testing.T) {
	for _, text := range []string{"[[ack]]\ncheck = 'reuse'\n", "[[ack]]\ncheck='a'\nfile='b'\nnotice='c'\nblob='d'\nextra=1\n", "not toml ="} {
		if _, err := parseAcknowledged(text); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

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
		{"current", base(0, boolPtr(true)), resultPass, "up to date"},
		{"behind", base(3, boolPtr(true)), resultFail, "3 commit(s) behind main"},
		{"conflict", base(0, boolPtr(false)), resultFail, "conflicts with main"},
		{"both", base(2, boolPtr(false)), resultFail, "behind main; rebase on it; conflicts"},
		{"not computed", base(0, nil), resultError, "run again"},
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
