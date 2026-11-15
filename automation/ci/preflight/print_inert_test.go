// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// logf is where an error reaches the log, and an error of the API or of git can quote text of the pull request.
func TestLogfPrintsAnErrorOnOneLine(t *testing.T) {
	for _, h := range hostileTexts {
		t.Run(h.name, func(t *testing.T) {
			var out bytes.Buffer
			logf(&out, "source files failed after retries: %v", errors.New("HTTP 502: "+h.text))
			if cmds := workflowCommands(out.String()); len(cmds) != 0 {
				t.Errorf("the log runs %q:\n%q", cmds, out.String())
			}
			if n := len(logLines(out.String())); n != 1 {
				t.Errorf("%d line(s), want one:\n%q", n, out.String())
			}
		})
	}
}

// The alert comment names the author on purpose and quotes the verdict's reason, which holds text of the pull request.
func TestAlertBodyQuotesTheReasonInertly(t *testing.T) {
	for _, h := range hostileTexts {
		t.Run(h.name, func(t *testing.T) {
			var d data
			d.Pull.User.Login = "author"
			reason := "a/f.go:3 @someone <img src=x> [x](http://e) | `tick` " + h.text
			ev := evaluation{Head: "0123456789abcdef", Checks: map[string]verdict{"gitleaks": {Result: resultFail, Reason: reason}}}
			body := alertBody(check{Name: "gitleaks", Alert: "Please remove the secret."}, &d, ev)
			paragraphs := strings.Split(body, "\n\n")
			if len(paragraphs) != 3 || !strings.HasPrefix(paragraphs[0], "@author Please") {
				t.Fatalf("the alert has %d paragraph(s), the first %q, want the mention, the verdict line, the marker:\n%s", len(paragraphs), paragraphs[0], body)
			}
			if rest := outsideCodeSpans(paragraphs[1]); rest != "gitleaks on : " {
				t.Errorf("outside a code span the verdict line holds %q:\n%s", rest, paragraphs[1])
			}
		})
	}
}
