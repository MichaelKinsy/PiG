// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

const (
	exemptSHA1  = "1111111111111111111111111111111111111111"
	exemptSHA2  = "2222222222222222222222222222222222222222"
	unlistedSHA = "3333333333333333333333333333333333333333"
	exemptWhy   = "the fix-titled commits of this pull request repair code that the pull request itself adds"
)

func exemptEnv() *ruleEnv {
	return &ruleEnv{fixExempt: []fixTitleExemption{{PR: 404, Reason: exemptWhy, Commits: []string{exemptSHA1, exemptSHA2}}}}
}

func TestFixTitleExemptionRule(t *testing.T) {
	fixed := "@@ -0,0 +1,2 @@\n+### Fixed\n+- It works. <!-- fix: 1111111 -->"
	for _, tc := range []struct {
		name    string
		env     *ruleEnv
		pr      int
		body    string
		commits []prCommit
		files   []prFile
		result  string
		reason  string // a part of the reason
		notice  string // a part of the single notice, "" for none
		absent  string // a part the reason must not name
	}{
		{"listed commit, Fix: no", exemptEnv(), 404, "Fix: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, nil, resultPass, "not a fix", exemptWhy, ""},
		{"two listed commits, one notice", exemptEnv(), 404, "Fix: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true), commit(exemptSHA2, "rpc: fix y", true)}, nil, resultPass, "2 fix-titled", "PR #404", ""},
		{"listed commit beside plain ones", exemptEnv(), 404, "Fix: no", []prCommit{commit("4444444444444444444444444444444444444444", "feat: x", true), commit(exemptSHA1, "Fix x", true)}, nil, resultPass, "not a fix", exemptWhy, ""},
		{"unlisted commit in the same PR", exemptEnv(), 404, "Fix: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true), commit(unlistedSHA, "fix(rpc): later", true)}, nil, resultFail, "fix-title-exempt entry of PR #404", "", short(exemptSHA1)},
		{"listed sha in a different PR", exemptEnv(), 405, "Fix: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, nil, resultFail, "the template says `Fix: no`", "", "fix-title-exempt"},
		{"no entry at all", &ruleEnv{}, 404, "Fix: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, nil, resultFail, "the template says `Fix: no`", "", "fix-title-exempt"},
		{"nil environment", nil, 404, "Fix: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, nil, resultFail, "the template says `Fix: no`", "", ""},
		{"a sha prefix is not a listing", exemptEnv(), 404, "Fix: no", []prCommit{commit(exemptSHA1[:12], "fix(rpc): x", true)}, nil, resultFail, "fix-title-exempt entry of PR #404", "", ""},
		{"fragment with Fixed is never exempt", exemptEnv(), 404, "Fix: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, []prFile{{Filename: "changelog.d/lane-1111111.md", Status: "added", Patch: fixed}}, resultFail, "adds a ### Fixed section", "", "commit"},
		{"listed commit, Fix: yes, no ledger", exemptEnv(), 404, "Fix: yes", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, nil, resultFail, "needs its bug ledger entry", "", ""},
		{"listed commit, Fix: yes, ledger changed", exemptEnv(), 404, "Fix: yes", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, []prFile{{Filename: "docs/bug-ledger.json", Status: "modified"}}, resultPass, "updates docs/bug-ledger.json", "", ""},
		{"listed commit, no Fix answer", exemptEnv(), 404, "User-facing: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, nil, resultFail, "cannot tell whether this is a fix", "", ""},
		{"no fix title in an exempt PR", exemptEnv(), 404, "Fix: no", []prCommit{commit(unlistedSHA, "feat(rpc): x", true)}, nil, resultPass, "not a fix", "", ""},
	} {
		t.Run("ledger/"+tc.name, func(t *testing.T) {
			d := &data{PR: tc.pr, Pull: pull{Body: tc.body}, Commits: tc.commits, Files: tc.files}
			o := ruleFixLedger(d, params{"ledger": "docs/bug-ledger.json", "help": "h"}, tc.env)
			if o.result != tc.result || !strings.Contains(o.reason, tc.reason) || (tc.absent != "" && strings.Contains(o.reason, tc.absent)) {
				t.Fatalf("outcome %+v", o)
			}
			switch {
			case tc.notice == "" && len(o.notices) != 0:
				t.Fatalf("notices %q, want none", o.notices)
			case tc.notice != "" && (len(o.notices) != 1 || !strings.Contains(o.notices[0], tc.notice) || !strings.Contains(o.notices[0], "PR #404")):
				t.Fatalf("notices %q, want one naming PR #404 and %q", o.notices, tc.notice)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		env     *ruleEnv
		pr      int
		body    string
		commits []prCommit
		files   []prFile
		result  string
		reason  string
	}{
		{"listed commit, Fix: no, User-facing: no", exemptEnv(), 404, "Fix: no\nUser-facing: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, nil, resultPass, "no user-facing change and no fix"},
		{"unlisted commit in the same PR", exemptEnv(), 404, "Fix: no\nUser-facing: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true), commit(unlistedSHA, "fix(rpc): later", true)}, nil, resultFail, "needs a fragment"},
		{"listed sha in a different PR", exemptEnv(), 405, "Fix: no\nUser-facing: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, nil, resultFail, "needs a fragment"},
		{"Fix: yes still needs its fragment", exemptEnv(), 404, "Fix: yes", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, nil, resultFail, "needs a fragment"},
		{"Fixed fragment still counts", exemptEnv(), 404, "Fix: no\nUser-facing: no", []prCommit{commit(exemptSHA1, "fix(rpc): x", true)}, []prFile{{Filename: "changelog.d/lane-1111111.md", Status: "added", Patch: "@@ -0,0 +1,2 @@\n+### Fixed\n+- It works."}}, resultFail, "<!-- fix: <sha> --> marker"},
	} {
		t.Run("changelog/"+tc.name, func(t *testing.T) {
			d := &data{PR: tc.pr, Pull: pull{Body: tc.body}, Commits: tc.commits, Files: tc.files}
			if o := ruleChangelog(d, nil, tc.env); o.result != tc.result || !strings.Contains(o.reason, tc.reason) {
				t.Fatalf("outcome %+v", o)
			}
		})
	}
}

func TestFixTitleExemptionConfig(t *testing.T) {
	const cheap = `{name: bug-ledger, when: {always: true}, stage: cheap, reads: [pull, commits, files], rule: fix-ledger, with: {ledger: l, help: h}}`
	const other = `{name: up, when: {always: true}, stage: cheap, reads: [pull], rule: up-to-date}`
	const reason = "reason: 'the fix-titled commits of this pull request repair code that the pull request itself adds'"
	const sha = "1111111111111111111111111111111111111111"
	load := func(entries string) (config, error) {
		return loadConfig([]byte("engine-revision: 1\nchecks: [" + cheap + ", " + other + "]\nfix-title-exempt: " + entries))
	}
	for _, tc := range []struct{ name, entries, want string }{
		{"no reason", "[{pr: 404, commits: [" + sha + "]}]", "reason has 0 characters"},
		{"short reason", "[{pr: 404, reason: tbd, commits: [" + sha + "]}]", "reason has 3 characters"},
		{"blank reason", "[{pr: 404, reason: '                                                                      ', commits: [" + sha + "]}]", "reason has 0 characters"},
		{"short sha", "[{pr: 404, " + reason + ", commits: [1111111]}]", "not a full 40-digit"},
		{"upper-case sha", "[{pr: 404, " + reason + ", commits: [" + strings.ToUpper(sha[:39]) + "A]}]", "not a full 40-digit"},
		{"long sha", "[{pr: 404, " + reason + ", commits: [" + sha + "1]}]", "not a full 40-digit"},
		{"duplicate PR", "[{pr: 404, " + reason + ", commits: [" + sha + "]}, {pr: 404, " + reason + ", commits: [" + strings.Repeat("2", 40) + "]}]", "#404 is listed twice"},
		{"duplicate sha", "[{pr: 404, " + reason + ", commits: [" + sha + ", " + sha + "]}]", "listed twice"},
		{"no commits", "[{pr: 404, " + reason + ", commits: []}]", "lists no commits"},
		{"no pr", "[{" + reason + ", commits: [" + sha + "]}]", "needs pr"},
		{"unknown key", "[{pr: 404, " + reason + ", commits: [" + sha + "], any: true}]", "field any not found"},
		{"misspelled key", "[{pr: 404, " + reason + ", commit: [" + sha + "]}]", "field commit not found"},
	} {
		t.Run("rejects/"+tc.name, func(t *testing.T) {
			if _, err := load(tc.entries); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
	t.Run("accepts", func(t *testing.T) {
		cfg, err := load("[{pr: 404, " + reason + ", commits: [" + sha + "]}, {pr: 405, " + reason + ", commits: [" + strings.Repeat("2", 40) + "]}]")
		if err != nil || len(cfg.FixTitleExempt) != 2 {
			t.Fatalf("config %+v, error %v", cfg.FixTitleExempt, err)
		}
		bare, err := load("[]")
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Checks[0].definition == bare.Checks[0].definition {
			t.Error("a new entry did not decide the fix-ledger check again")
		}
		if cfg.Checks[1].definition != bare.Checks[1].definition {
			t.Error("a new entry decided a check that does not read it again")
		}
	})
}

// TestFixTitleExemptionReplay replays a synthetic description of pull request 404 through the shipped rules: it passes
// with the entry and fails without it.
func TestFixTitleExemptionReplay(t *testing.T) {
	const checks = `
checks:
  - {name: bug-ledger, when: {always: true}, stage: cheap, reads: [pull, commits, files], rule: fix-ledger, with: {ledger: docs/bug-ledger.json, help: h}}
  - {name: changelog, when: {always: true}, stage: cheap, reads: [pull, commits, files], rule: changelog-fragment}
`
	entry := "fix-title-exempt:\n  - pr: 404\n    reason: " + exemptWhy + "\n    commits: [" + exemptSHA1 + "]\n"
	d := func() *data {
		return &data{
			PR: 404, Pull: pull{Number: 404, Body: "Fix: no\nUser-facing: no"}, Relevant: []string{"bug-ledger", "changelog"},
			Commits: []prCommit{commit(exemptSHA1, "fix(rpc): repair the listener this PR adds", true), commit("4444444444444444444444444444444444444444", "feat(rpc): add --listen", true)},
		}
	}
	for _, tc := range []struct {
		name, yaml string
		want       string
	}{{"with the entry", "engine-revision: 1\n" + entry + checks, resultPass}, {"without it", "engine-revision: 1\n" + checks, resultFail}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadConfig([]byte(tc.yaml))
			if err != nil {
				t.Fatal(err)
			}
			ev := evaluate(cfg, d(), &ruleEnv{root: t.TempDir(), fixExempt: cfg.FixTitleExempt})
			for _, name := range []string{"bug-ledger", "changelog"} {
				if got := ev.Checks[name].Result; got != tc.want {
					t.Errorf("%s: %+v, want %s", name, ev.Checks[name], tc.want)
				}
			}
			if tc.want == resultPass && len(ev.Checks["bug-ledger"].Notices) != 1 {
				t.Errorf("notices %q, want one", ev.Checks["bug-ledger"].Notices)
			}
		})
	}
}
