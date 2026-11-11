// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

func TestCommandLine(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		output string
	}{
		{"no command", nil, 2, "usage"},
		{"unknown command", []string{"fly"}, 2, `unknown command "fly"`},
		{"checks", []string{"checks"}, 0, "template"},
		{"install needs a bin", []string{"install"}, 2, "--bin is required"},
		{"run needs its directories", []string{"run", "--repo", "o/r"}, 2, "--bin and --work are required"},
		{"run needs a token", []string{"run", "--repo", "o/r", "--bin", "b", "--work", "w"}, 2, "GITHUB_TOKEN or GH_TOKEN"},
		{"run rejects a PR that is not a number", []string{"run", "--prs", "12 x"}, 2, `"x" is not a PR number`},
		{"run rejects PR zero", []string{"run", "--prs", "0"}, 2, `"0" is not a PR number`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut strings.Builder
			code := run(tc.args, &out, &errOut)
			if code != tc.code || !strings.Contains(out.String()+errOut.String(), tc.output) {
				t.Fatalf("exit %d, output %q %q; want %d containing %q", code, out.String(), errOut.String(), tc.code, tc.output)
			}
		})
	}
}
