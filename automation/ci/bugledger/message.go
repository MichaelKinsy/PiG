// SPDX-License-Identifier: MIT

package main

import (
	"regexp"
	"strings"
	"unicode"
)

// A fix states its cause and its regression tests in one of two places, and the ledger takes what is stated as written:
//
//   - the changelog bullet, which can still be edited after the fix is committed: "<!-- cause: <text> -->" and "<!-- tests: <test>[, <test>...] -->" beside its fix marker (changelog.go);
//   - the fix commit's message: a "Root-Cause: <text>" trailer, or a paragraph that starts "Root cause:" or "Cause:" (or with a full stop in place of the colon), and "Regression-Test: <test>[, <test>...]" trailers.
//
// The bullet wins over the message. A <test> is a Go test name, "<file>::<Name>", a parity scenario "<area>/<nn-name>" or the path of a test file.
//
// When neither states it, the cause is read from the prose of the message and of the bullet (rootCauseOf) and the tests from the commit's diff (testsOfCommit). A paragraph that reports how the change was checked is never the cause, and a test the prose only mentions is no regression test of the fix.

// commitMessage is what the ledger reads from the message of a fix commit.
type commitMessage struct {
	// body is the prose after the subject, one paragraph per element on one line: without the trailer block and without the paragraphs that report how the change was checked.
	body []string
	// cause is the cause the message states outright; "" when it states none.
	cause string
	// tests are the tests its Regression-Test trailers name, as written.
	tests []string
}

var (
	// trailerLine is one "Key: value" line of the trailer block that ends a message. A key is one token, as git writes it: "Root cause:" is prose.
	trailerLine = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*):[ \t]*(.*)$`)
	// cherryPickLine is the line git cherry-pick -x adds to the trailer block.
	cherryPickLine = regexp.MustCompile(`^\(cherry picked from commit [0-9a-f]+\)$`)
	causeLabel     = regexp.MustCompile(`(?i)^(root[ -]cause|cause)[:.]\s+`)
	listItem       = regexp.MustCompile(`^(?:[-*+]|\d+[.)]) `)
	// evidenceHead matches the opening of a paragraph that reports how a change was checked rather than why the bug happened: a label ("Tests:", "Tests (go1.27.4, job race1).", "Ran:", "Evidence:", "Verified:", "Job race1:"), a check with where or how it ran ("Verified on ...", "Measured with ..."), the name of a test, or `go test` itself. A list item may open the same way. A sentence whose subject is one of these words ("Results were dropped because ...", "Ran out of memory ...") is prose, and so is one that opens with a call ("run() awaits ...").
	evidenceHead = regexp.MustCompile(`^(?:[-*+] )?(?:` +
		`(?i:tests?|testing|tested|test plan|ran|run|evidence|verified|verification|checked|checks|gates?|proof|measured|measurements?|benchmarks?|results?|validation|validated|ci|not green|not run|jobs?(?: [\w.-]+)?)(?::| \()` +
		`|(?i:verified|measured|tested|checked|ran|reproduced|validated) (?:on|with|by|in|under|against|locally)\b` +
		`|(?:Test|Fuzz|Benchmark)[A-Z][A-Za-z0-9_]*\b` +
		"|`?go test\\b)")
)

// trailers reads the paragraph that ends a message as its trailer block: every line is "Key: value", a continuation of the line before, or the cherry-pick line, and one key is hyphenated as every trailer of this project is (each commit is signed off). It reports false for prose that only happens to hold a colon, such as a one-line "Cause: ..." paragraph.
func trailers(paragraph string) (map[string][]string, bool) {
	out := map[string][]string{}
	hyphenated := false
	last := ""
	for line := range strings.SplitSeq(paragraph, "\n") {
		switch m := trailerLine.FindStringSubmatch(line); {
		case m != nil:
			last = strings.ToLower(m[1])
			hyphenated = hyphenated || strings.Contains(last, "-")
			out[last] = append(out[last], strings.TrimSpace(m[2]))
		case last != "" && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")):
			values := out[last]
			values[len(values)-1] = strings.TrimSpace(values[len(values)-1] + " " + strings.TrimSpace(line))
		case cherryPickLine.MatchString(line):
		default:
			return nil, false
		}
	}
	return out, hyphenated
}

// testList splits the value of a Regression-Test trailer or of a tests marker into the tests it names.
func testList(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

// readMessage reads a commit message: its subject, a blank line, and its body.
func readMessage(msg string) commitMessage {
	var m commitMessage
	var raw []string
	for p := range strings.SplitSeq(strings.ReplaceAll(msg, "\r\n", "\n"), "\n\n") {
		if p = strings.Trim(p, "\n"); strings.TrimSpace(p) != "" {
			raw = append(raw, p)
		}
	}
	if len(raw) < 2 {
		return m // a subject alone
	}
	raw = raw[1:]
	if t, ok := trailers(raw[len(raw)-1]); ok {
		raw = raw[:len(raw)-1]
		for _, key := range []string{"root-cause", "cause"} {
			if m.cause == "" && len(t[key]) > 0 {
				m.cause = t[key][0]
			}
		}
		for _, key := range []string{"regression-test", "regression-tests"} {
			for _, value := range t[key] {
				m.tests = append(m.tests, testList(value)...)
			}
		}
	}
	// checked is true while the paragraphs read are the items of an evidence list.
	checked := false
	for _, p := range raw {
		p = strings.TrimSpace(spaceRun.ReplaceAllString(p, " "))
		switch loc := causeLabel.FindStringIndex(p); {
		case loc != nil:
			if m.cause == "" {
				m.cause = p[loc[1]:]
			}
			checked = false
		case evidenceHead.MatchString(p):
			checked = true
		case checked && listItem.MatchString(p):
		default:
			checked = false
			m.body = append(m.body, p)
		}
	}
	return m
}
