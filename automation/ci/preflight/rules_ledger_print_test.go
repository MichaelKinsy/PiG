// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
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

// workflowCommands are the lines of the log the runner would run as a workflow command: "::command" and the legacy
// "##[command]", at the start of the line or after spaces and tabs.
func workflowCommands(log string) []string {
	var out []string
	for _, l := range logLines(log) {
		if at := strings.TrimLeft(l, " \t"); strings.HasPrefix(at, "::") || strings.HasPrefix(at, "##[") {
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

type hostileText struct{ name, text string }

// hostileTexts are what a pull request can put into a printed text: line breaks of every kind, GitHub's escapes as
// literal text, and a workflow command at the start and in the middle.
var hostileTexts = append(slices.Clone(plainHostileTexts), legacyHostileTexts()...)

var plainHostileTexts = []hostileText{
	{"a newline and a command", "ok\n::error file=x::injected"},
	{"a carriage return and a mask", "ok\r::add-mask::x"},
	{"a CRLF and a mask", "ok\r\n::add-mask::x"},
	{"a command at the start", "::error::first"},
	{"a command in the middle", "a ::error::mid b"},
	{"a percent sign", "100% of %"},
	{"the escape of a newline as text", "%0A::warning::y"},
	{"the escape of a carriage return as text", "%0D::warning::y"},
	{"the escape of a percent sign as text", "%25 %250A"},
	{"a unicode line separator", "a\u2028::error::z"},
}

// legacyHostileTexts are the legacy workflow commands in every place a pull request can put them.
func legacyHostileTexts() []hostileText {
	var out []hostileText
	for _, c := range []string{"##[error]x", "##[set-output name=a;]b", "##[add-mask]s"} {
		for _, p := range []struct{ name, text string }{
			{"alone", c},
			{"after a newline", "ok\n" + c},
			{"after a CRLF", "ok\r\n" + c},
			{"after spaces", "ok\n  \t " + c},
			{"in the middle", "a " + c + " b"},
		} {
			out = append(out, hostileText{"legacy " + c + " " + p.name, p.text})
		}
	}
	return out
}

// legacyLeft says whether the log holds the sequence that opens a legacy workflow command anywhere. The tool prints
// no such sequence of its own, so one in the log came from a pull request and was not made inert.
func legacyLeft(log string) bool { return strings.Contains(log, "##[") }

// inertForm is text with the sequence of a legacy workflow command made inert, the way the tool prints it.
func inertForm(s string) string { return strings.ReplaceAll(s, "##[", "# #[") }

// unescapeData is what the runner makes of the data of a workflow command.
func unescapeData(s string) string {
	return strings.NewReplacer("%0D", "\r", "%0A", "\n", "%25", "%").Replace(s)
}

// Text of the ledger row comes from the pull request. Printed, it must not start a workflow command or a second line
// of the log, the annotation data must unescape to the notice, and the comment row must stay one inert row.
func TestAdmittedExpectationTextCannotStartAWorkflowCommand(t *testing.T) {
	for _, where := range []string{"reason", "cite"} {
		for _, h := range hostileTexts {
			t.Run(where+": "+h.name, func(t *testing.T) {
				cite, reason := "", "A reason."
				if where == "cite" {
					cite = h.text
				} else {
					reason = h.text
				}
				d := &data{
					Files:          ledgerFiles(ledgerTestA),
					Commits:        []prCommit{commit(ledgerCommitA, "a: b", true)},
					ReviewedLedger: ledgerRow(ledgerCommitA, ledgerTestA, "NOBEHAVIOR", cite, reason),
					CommitFiles:    changedBy([]string{ledgerTestA}, ledgerCommitA),
				}
				log, md := printedBy(t, d)
				cmds := workflowCommands(log)
				if len(cmds) != 1 || !strings.HasPrefix(cmds[0], commandOfNotice) {
					t.Fatalf("the log runs %q, want the one notice of the admission:\n%q", cmds, log)
				}
				if legacyLeft(log) {
					t.Errorf("the log holds the sequence of a legacy workflow command:\n%q", log)
				}
				want := ledgerTestA + ": expected-value change admitted by reviewed ledger row aaaaaaaaaa, class NOBEHAVIOR"
				if cite != "" {
					want += " " + strings.Join(strings.Fields(cite), " ")
				}
				want += ": " + strings.Join(strings.Fields(reason), " ")
				want = inertForm(want)
				if got := unescapeData(strings.TrimPrefix(cmds[0], commandOfNotice)); got != want {
					t.Errorf("the annotation data unescapes to %q, want %q", got, want)
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
				row := rowOf(t, md, "📝")
				if rest := outsideCodeSpans(row); rest != "| 📝 | test-expectation |  |" {
					t.Errorf("outside a code span the row holds %q, want the table cells only:\n%s", rest, row)
				}
			})
		}
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
				CommitFiles:    changedBy([]string{ledgerTestA}, ledgerCommitA),
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

// A file name of the pull request, whatever it holds, is one line of the log and one inert row of the comment, and the
// error annotation of the finding carries it escaped the way GitHub unescapes.
func TestFindingTextCannotStartAWorkflowCommand(t *testing.T) {
	texts := append(slices.Clone(hostileTexts), hostileText{"control characters", "a\x1b[31mb\x7f"})
	for _, h := range texts {
		t.Run(h.name, func(t *testing.T) {
			name := "a/" + h.text + "_test.go"
			d := &data{Files: ledgerFiles(name), Commits: []prCommit{commit(ledgerCommitA, "a: b", true)}}
			log, md := printedBy(t, d)
			const commandOfError = "::error title=pre-flight test-expectation #7::"
			cmds := workflowCommands(log)
			if len(cmds) != 1 || !strings.HasPrefix(cmds[0], commandOfError) {
				t.Fatalf("the log runs %q, want the one error of the finding:\n%q", cmds, log)
			}
			if legacyLeft(log) {
				t.Errorf("the log holds the sequence of a legacy workflow command:\n%q", log)
			}
			name = inertForm(name)
			if got := unescapeData(strings.TrimPrefix(cmds[0], commandOfError)); !strings.Contains(got, name) {
				t.Errorf("the annotation data unescapes to %q, which lacks the file name %q", got, name)
			}
			var fails int
			for _, l := range logLines(log) {
				switch {
				case strings.HasPrefix(l, "FAIL "):
					fails++
				case strings.HasPrefix(l, "PASS "), strings.HasPrefix(l, commandOfError):
				default:
					t.Errorf("the log has the line %q, which the report does not print", l)
				}
			}
			if fails != 1 {
				t.Errorf("%d FAIL line(s), want one:\n%q", fails, log)
			}
			row := rowOf(t, md, "| test-expectation |")
			if rest := outsideCodeSpans(row); rest != "| ❌ | test-expectation |  |" {
				t.Errorf("outside a code span the row holds %q, want the table cells only:\n%s", rest, row)
			}
		})
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

// A notice is cut when it is printed, not when it is made: the stored record of a verdict holds the whole text of the
// ledger row, and a later run that reuses the record prints the same cut notice as the first run.
func TestClippedNoticeIsNotAStoredVerdict(t *testing.T) {
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	reason := strings.TrimSpace(strings.Repeat("reason words ", 800))
	if len(reason) < 10000 {
		t.Fatalf("the reason is %d bytes, want 10000 or more", len(reason))
	}
	d := &data{
		Files:          ledgerFiles(ledgerTestA),
		Commits:        []prCommit{commit(ledgerCommitA, "a: b", true)},
		ReviewedLedger: ledgerRow(ledgerCommitA, ledgerTestA, "NOBEHAVIOR", "", reason),
		CommitFiles:    changedBy([]string{ledgerTestA}, ledgerCommitA),
	}
	d.Repo, d.PR, d.Mode, d.Relevant = "o/r", 7, modeFull, []string{"test-expectation"}
	env := ledgerEnv(t)
	printed := func(ev evaluation) (log, row string) {
		var out bytes.Buffer
		report(&out, cfg, ev)
		return out.String(), rowOf(t, markdown(cfg, d, ev), "📝")
	}
	first := evaluate(cfg, d, env)
	log1, row1 := printed(first)
	if cmds := workflowCommands(log1); len(cmds) != 1 || !strings.Contains(cmds[0], "…") || len(cmds[0]) > 2000 {
		t.Fatalf("the notice is not cut with an ellipsis once (%d command(s)):\n%.600q", len(cmds), log1)
	}
	if !strings.Contains(row1, "…") {
		t.Errorf("the comment row is not cut with an ellipsis:\n%.300s", row1)
	}
	raw, err := json.Marshal(state{Engine: cfg.EngineRevision, Checks: first.Checks})
	if err != nil {
		t.Fatal(err)
	}
	var stored state
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if n := stored.Checks["test-expectation"].Notices; len(n) != 1 || !strings.Contains(n[0], reason) {
		t.Errorf("the stored record does not hold the whole reason (%d bytes): %.300q", len(reason), n)
	}
	d.Previous = &stored
	second := evaluate(cfg, d, env)
	if second.Checks["test-expectation"].Input != first.Checks["test-expectation"].Input {
		t.Fatal("the second evaluation has another input")
	}
	log2, row2 := printed(second)
	if log2 != log1 || row2 != row1 {
		t.Errorf("a reused record prints another notice:\nfirst  %.400q\nsecond %.400q", log1, log2)
	}
}

// tooledRun is an evaluation of n changed test files under a tooling prefix of main: each is skipped with a notice of its
// own, so a long list of long names makes a report too long for a comment.
func tooledRun(t *testing.T, n int) (cfg config, d *data, ev evaluation, env *ruleEnv) {
	t.Helper()
	cfg, err := loadConfig(configYAML)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for i := range n {
		names = append(names, fmt.Sprintf("automation/ci/preflight/%03d%s_test.go", i, strings.Repeat("n", 200)))
	}
	d = &data{Files: ledgerFiles(names...), Commits: []prCommit{commit(ledgerCommitA, "a: b", true)}}
	d.Repo, d.PR, d.Mode, d.Relevant, d.Version, d.BaseTip = "o/r", 7, modeFull, []string{"test-expectation"}, "v1", "basetip"
	d.Pull.Head.SHA = "headsha"
	env = toolingEnv(t, toolingFile)
	return cfg, d, evaluate(cfg, d, env), env
}

// When the report is too long for a comment, the comment shows the verdicts without their warnings and notices. The
// next run with unchanged inputs must not reuse that shortened record: its log, annotations and summary hold them.
func TestShortenedCommentIsNotReusedAsTheVerdicts(t *testing.T) {
	cfg, d, ev, env := tooledRun(t, 400)
	r := newRecording(cfg, d, ev, prComment{})
	if r.bodyErr != nil || !strings.Contains(r.body, shortened) {
		t.Fatalf("the comment is not the shortened one (err %v)", r.bodyErr)
	}
	prev := parseState(r.body)
	if prev == nil {
		t.Fatal("the comment carries no state")
	}
	mode, reason := decideMode(prev, d.Version, "headsha", "basetip", bodyHash(d.Pull.Body))
	if mode == modeReuse {
		t.Errorf("mode %s (%s): the shortened record is reused", mode, reason)
	}
	d.Mode, d.Previous = mode, prev
	again := evaluate(cfg, d, env)
	var first, second bytes.Buffer
	report(&first, cfg, ev)
	report(&second, cfg, again)
	if first.String() != second.String() {
		t.Errorf("the next run prints another log:\nfirst  %.300q\nsecond %.300q", first.String(), second.String())
	}
	if markdown(cfg, d, ev) != markdown(cfg, d, again) {
		t.Error("the next run has another report for the summary")
	}
	if n := strings.Count(second.String(), "::notice "); n != 400 {
		t.Errorf("the next run has %d notice annotation(s), want 400", n)
	}
}

// A report that fits a comment is stored whole and stays reusable.
func TestFittingCommentStoresTheWholeVerdicts(t *testing.T) {
	cfg, d, ev, _ := tooledRun(t, 3)
	r := newRecording(cfg, d, ev, prComment{})
	if strings.Contains(r.body, shortened) {
		t.Fatal("a fitting report was shortened")
	}
	prev := parseState(r.body)
	if prev == nil || len(prev.Checks["test-expectation"].Notices) != 3 {
		t.Fatalf("the state lacks the notices : %+v", prev)
	}
	if mode, reason := decideMode(prev, d.Version, "headsha", "basetip", bodyHash(d.Pull.Body)); mode != modeReuse {
		t.Errorf("mode %s (%s), want reuse", mode, reason)
	}
}
