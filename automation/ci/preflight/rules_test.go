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
