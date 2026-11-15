// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"errors"
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
