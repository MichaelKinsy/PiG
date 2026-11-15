// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

// printedBy runs the whole check on d and returns the job log and the PR comment.
func printedBy(t *testing.T, d *data) (log, md string) {
	t.Helper()
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	d.Repo, d.PR, d.Mode, d.Relevant = "o/r", 7, modeFull, []string{"test-expectation"}
	ev := evaluate(cfg, d, ledgerEnv(t))
	var out bytes.Buffer
	report(&out, cfg, ev)
	return out.String(), markdown(cfg, d, ev)
}

// logLines splits the way the runner does: a carriage return ends a line as a newline does.
func logLines(log string) []string {
	return strings.FieldsFunc(log, func(r rune) bool { return r == '\n' || r == '\r' })
}

// workflowCommands are the lines of the log the runner would run as a workflow command.
func workflowCommands(log string) []string {
	var out []string
	for _, l := range logLines(log) {
		if strings.HasPrefix(strings.TrimLeft(l, " \t"), "::") {
			out = append(out, l)
		}
	}
	return out
}

// outsideCodeSpans is s without its Markdown code spans: a run of n backticks up to the next run of exactly n.
func outsideCodeSpans(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '`' {
			out.WriteByte(s[i])
			i++
			continue
		}
		n := 0
		for i+n < len(s) && s[i+n] == '`' {
			n++
		}
		end := -1
		for j := i + n; j < len(s); {
			if s[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(s) && s[j+m] == '`' {
				m++
			}
			if m == n {
				end = j + m
				break
			}
			j += m
		}
		if end < 0 {
			out.WriteString(s[i : i+n])
			i += n
			continue
		}
		i = end
	}
	return out.String()
}

// rowOf is the single line of md that holds marker; a text that could end the line would give none or several.
func rowOf(t *testing.T, md, marker string) string {
	t.Helper()
	var rows []string
	for l := range strings.SplitSeq(md, "\n") {
		if strings.Contains(l, marker) {
			rows = append(rows, l)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("%d lines hold %q, want one table row:\n%s", len(rows), marker, md)
	}
	return rows[0]
}

const commandOfNotice = "::notice title=pre-flight test-expectation #7::"

// Text of the ledger row comes from the pull request. Printed, it must not start a workflow command or a second line
// of the log.
func TestAdmittedExpectationTextCannotStartAWorkflowCommand(t *testing.T) {
	for _, tc := range []struct{ name, cite, reason string }{
		{"a newline and a command in the reason", "", "ok\n::error file=x::injected"},
		{"a carriage return and a mask in the reason", "", "a\r\n::add-mask::x"},
		{"an escape already in the reason", "", "%0A::warning::y"},
		{"a unicode line separator in the reason", "", "a\u2028::error::z"},
		{"a command in the cite", "c\n::error::cite", "A reason."},
		{"a mask in the cite", "c\r::add-mask::x", "A reason."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &data{
				Files:          ledgerFiles(ledgerTestA),
				Commits:        []prCommit{commit(ledgerCommitA, "a: b", true)},
				ReviewedLedger: ledgerRow(ledgerCommitA, ledgerTestA, "NOBEHAVIOR", tc.cite, tc.reason),
			}
			log, _ := printedBy(t, d)
			cmds := workflowCommands(log)
			if len(cmds) != 1 || !strings.HasPrefix(cmds[0], commandOfNotice) {
				t.Errorf("the log runs %q, want the one notice of the admission:\n%s", cmds, log)
			}
			var notes int
			for _, l := range logLines(log) {
				switch {
				case strings.HasPrefix(l, "NOTE "):
					notes++
				case strings.HasPrefix(l, "PASS "), strings.HasPrefix(l, commandOfNotice):
				default:
					t.Errorf("the log has the line %q, which the report does not print", l)
				}
			}
			if notes != 1 {
				t.Errorf("%d NOTE line(s), want one:\n%q", notes, log)
			}
		})
	}
}

// The comment row of an admission is one table row whose text is inert: in a code span, so no table cell, mention,
// HTML tag, link or fence comes of it. The reason and the cite are cut at maxNotice characters.
func TestAdmittedExpectationTextIsInertInTheComment(t *testing.T) {
	hostile := "| a | b |\n```\n@someone <img src=x> [x](http://e) `tick` ``twice`` \\| end " + strings.Repeat("long ", 2000)
	for _, tc := range []struct{ name, cite, reason string }{
		{"the reason", "", hostile},
		{"the cite", hostile, "A reason."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &data{
				Files:          ledgerFiles(ledgerTestA),
				Commits:        []prCommit{commit(ledgerCommitA, "a: b", true)},
				ReviewedLedger: ledgerRow(ledgerCommitA, ledgerTestA, "NOBEHAVIOR", tc.cite, tc.reason),
			}
			_, md := printedBy(t, d)
			row := rowOf(t, md, "📝")
			if rest := outsideCodeSpans(row); rest != "| 📝 | test-expectation |  |" {
				t.Errorf("outside a code span the row holds %q, want the table cells only:\n%s", rest, row)
			}
			if limit := len(ledgerTestA) + 2*utf8.UTFMax*maxNotice + 200; len(row) > limit {
				t.Errorf("the row is %d bytes, want at most %d (the text is cut at %d characters)", len(row), limit, maxNotice)
			}
			if !strings.Contains(row, "…") {
				t.Errorf("the cut is not marked with an ellipsis:\n%s", row)
			}
		})
	}
}

// A file name of the pull request is printed in the findings of the check. It cannot name a file of a ledger row, but
// it reaches the log and the comment all the same.
func TestFindingTextOfThePullRequestIsInert(t *testing.T) {
	name := "a/` @someone <b>\n::error::injected [x](http://e) | z_test.go"
	d := &data{
		Files:   ledgerFiles(name),
		Commits: []prCommit{commit(ledgerCommitA, "a: b", true)},
	}
	log, md := printedBy(t, d)
	if cmds := workflowCommands(log); len(cmds) != 1 || !strings.HasPrefix(cmds[0], "::error title=pre-flight test-expectation #7::") {
		t.Errorf("the log runs %q, want the one error of the finding:\n%s", cmds, log)
	}
	row := rowOf(t, md, "| test-expectation |")
	if rest := outsideCodeSpans(row); rest != "| ❌ | test-expectation |  |" {
		t.Errorf("outside a code span the row holds %q, want the table cells only:\n%s", rest, row)
	}
}

// A ledger that cannot be read is reported with what the parser says; the parser quotes the text.
func TestUnreadableLedgerTextCannotStartAWorkflowCommand(t *testing.T) {
	d := &data{
		Files:          ledgerFiles(ledgerTestA),
		Commits:        []prCommit{commit(ledgerCommitA, "a: b", true)},
		ReviewedLedger: "[[entry]]\npath = \"x\"\n::error::injected = 1\nreason = \"a\n::add-mask::y\"\n",
	}
	log, _ := printedBy(t, d)
	if cmds := workflowCommands(log); len(cmds) != 1 || !strings.HasPrefix(cmds[0], "::error title=pre-flight test-expectation #7::") {
		t.Errorf("the log runs %q, want the one error of the finding:\n%s", cmds, log)
	}
}
