// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	toolingTest    = "automation/ci/preflight/x_test.go"
	nearToolingDir = "automation/ci/preflightx/y_test.go"
	toolingFile    = "[[tooling]]\nprefix = \"automation/ci/preflight/\"\nreason = \"CI tooling owned by PiG; no upstream Pi behaviour to cite\"\n"
)

// toolingEnv is a main checkout with one Pi inventory entry and the given provenance file ("-": none on main).
func toolingEnv(t *testing.T, provenance string) *ruleEnv {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{"docs/parity/PORT_MAP.md": "| packages/coding-agent/src/core/agent.ts | x |\n"}
	if provenance != "-" {
		files["test/testdata-provenance.toml"] = provenance
	}
	for name, text := range files {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &ruleEnv{root: root}
}

// The line lint leaves the tooling directories of test/testdata-provenance.toml out of the expected-value rule; the
// pre-flight does the same, with the list of main, and prints each skip.
func TestRuleTestExpectationSkipsToolingDirectoriesOfMain(t *testing.T) {
	r, p := shippedRule(t, "test-expectation")
	const note = "tooling prefix automation/ci/preflight/ of test/testdata-provenance.toml on main"
	nobehavior := ledgerRow(ledgerCommitA, toolingTest, "NOBEHAVIOR", "", "A row nobody needs.")
	for _, tc := range []struct {
		name       string
		provenance string
		ledger     string
		tests      []string
		result     string
		reason     string
		notices    int
	}{
		{"a file under a tooling prefix of main", toolingFile, "", []string{toolingTest}, resultPass, "", 1},
		{"a file outside every prefix", toolingFile, "", []string{ledgerTestA}, resultFail, "in the PR body and a commit trailer", 0},
		{"a prefix ends with a slash: the directory next to it is outside", toolingFile, "", []string{nearToolingDir}, resultFail, "in the PR body and a commit trailer", 0},
		{"a prefix without a slash is a plain prefix, as for the lint", strings.Replace(toolingFile, "preflight/", "preflight", 1), "", []string{nearToolingDir}, resultPass, "", 1},
		{"one file under a prefix, one outside", toolingFile, "", []string{toolingTest, ledgerTestA}, resultFail, ledgerTestA, 1},
		{"a file under a prefix with a ledger row: the prefix skips it, the row is not needed", toolingFile, nobehavior, []string{toolingTest}, resultPass, "", 1},
		{"every flagged file is tooling: a ledger that cannot be read does not matter", toolingFile, "[[entry]\npath = ", []string{toolingTest}, resultPass, "", 1},
		{"no provenance file on main", "-", "", []string{toolingTest}, resultFail, "test/testdata-provenance.toml", 0},
		{"an empty provenance file on main", "", "", []string{toolingTest}, resultFail, "test/testdata-provenance.toml", 0},
		{"a provenance file that is not TOML", "[[tooling]\nprefix = ", "", []string{toolingTest}, resultFail, "test/testdata-provenance.toml", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &data{Files: ledgerFiles(tc.tests...), ReviewedLedger: tc.ledger, Commits: []prCommit{commit(ledgerCommitA, "a: b", true)}}
			o := r.run(d, p, toolingEnv(t, tc.provenance))
			if o.result != tc.result || !strings.Contains(o.reason, tc.reason) || len(o.notices) != tc.notices {
				t.Fatalf("outcome %+v, want %s %q with %d notice(s)", o, tc.result, tc.reason, tc.notices)
			}
			if tc.result == resultPass && !strings.Contains(strings.Join(o.notices, "\n"), "tooling prefix") {
				t.Errorf("the skip is not printed: %q", o.notices)
			}
			if tc.name == "a file under a tooling prefix of main" && (!strings.HasPrefix(o.notices[0], toolingTest+": ") || !strings.Contains(o.notices[0], note)) {
				t.Errorf("the notice is %q, want it to name the file and %q", o.notices[0], note)
			}
		})
	}
}

// A prefix that only the pull request adds does not exempt that same pull request.
func TestRuleTestExpectationToolingPrefixOfThePullRequestDoesNotCount(t *testing.T) {
	r, p := shippedRule(t, "test-expectation")
	files := append(ledgerFiles(toolingTest), prFile{Filename: "test/testdata-provenance.toml", Status: "modified", Patch: "@@ -1 +1,4 @@\n x\n+[[tooling]]\n+prefix = \"automation/ci/preflight/\"\n+reason = \"mine\""})
	d := &data{Files: files, Commits: []prCommit{commit(ledgerCommitA, "a: b", true)}}
	for _, provenance := range []string{"[[tooling]]\nprefix = \"automation/ci/other/\"\nreason = \"r\"\n", "[[golden]]\npaths = [\"a\"]\n"} {
		if o := r.run(d, p, toolingEnv(t, provenance)); o.result != resultFail || !strings.Contains(o.reason, "in the PR body and a commit trailer") || len(o.notices) != 0 {
			t.Errorf("a prefix of the PR exempted its own test with main's list %q: %+v", provenance, o)
		}
	}
}

// The NOTE of a skip names the file of the pull request; the log cannot run a command, old or legacy, from that name.
func TestToolingNoteTextCannotStartAWorkflowCommand(t *testing.T) {
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hostileTexts {
		t.Run(h.name, func(t *testing.T) {
			name := "automation/ci/preflight/" + h.text + "_test.go"
			d := &data{Files: ledgerFiles(name), Commits: []prCommit{commit(ledgerCommitA, "a: b", true)}}
			d.Repo, d.PR, d.Mode, d.Relevant = "o/r", 7, modeFull, []string{"test-expectation"}
			var out bytes.Buffer
			report(&out, cfg, evaluate(cfg, d, toolingEnv(t, toolingFile)))
			log := out.String()
			var commands, notes int
			for _, l := range logLines(log) {
				switch {
				case strings.HasPrefix(l, "NOTE "):
					notes++
				case strings.HasPrefix(l, "PASS "):
				case strings.HasPrefix(l, "::notice title=pre-flight test-expectation #7::"):
					commands++
				default:
					t.Errorf("the log has the line %q, which the report does not print", l)
				}
			}
			if cmds := workflowCommands(log); len(cmds) != 1 || commands != 1 || notes != 1 {
				t.Errorf("the log runs %q with %d NOTE line(s), want the one notice:\n%q", cmds, notes, log)
			}
			if legacyLeft(log) {
				t.Errorf("the log holds the sequence of a legacy workflow command:\n%q", log)
			}
		})
	}
}
