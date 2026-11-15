// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"
)

// A fix marker names commits of the PR by 7 to 40 lowercase hex digits, the length a fragment's file name takes. An
// empty or shorter value is a prefix of too many commits to name one.
func TestFixMarkerNeedsACommitSHA(t *testing.T) {
	const sha = "1234567890abcdef1234567890abcdef12345678"
	c1 := commit(sha, "x: change", true)
	for _, tc := range []struct {
		marker string
		result string
		reason string
	}{
		{"<!-- fix: " + sha + " -->", resultPass, "1 valid"},
		{"<!-- fix: 1234567 -->", resultPass, "1 valid"},
		{"<!-- fix: 1234567890, 1234567890abcdef -->", resultPass, "1 valid"},
		{"<!-- fix:   -->", resultFail, "7 to 40 hex digits"},
		{"<!-- fix: , -->", resultFail, "7 to 40 hex digits"},
		{"<!-- fix: 1 -->", resultFail, `fix marker "1" is not a commit sha of 7 to 40 hex digits`},
		{"<!-- fix: 123456 -->", resultFail, `fix marker "123456" is not a commit sha of 7 to 40 hex digits`},
		{"<!-- fix: 1234567, -->", resultFail, `fix marker "" is not a commit sha of 7 to 40 hex digits`},
		{"<!-- fix: 1234567,123 -->", resultFail, `fix marker "123" is not a commit sha of 7 to 40 hex digits`},
		{"<!-- fix: 12345 67890 -->", resultFail, "7 to 40 hex digits"},
		{"<!-- fix: " + sha + "9 -->", resultFail, "7 to 40 hex digits"},
		{"<!-- fix: 12345zz -->", resultFail, "no <!-- fix: <sha> --> marker"},
		{"<!-- fix: 1234567A -->", resultFail, "no <!-- fix: <sha> --> marker"},
		{"<!-- fix: abcdef1 -->", resultFail, "fix marker abcdef1 is not a commit of this PR"},
	} {
		t.Run(tc.marker, func(t *testing.T) {
			patch := "@@ -0,0 +1,3 @@\n+### Fixed\n+\n+- It works. " + tc.marker
			files := []prFile{{Filename: "changelog.d/lane-1234567.md", Status: "added", Patch: patch}}
			o := ruleChangelog(&data{Pull: pull{Body: "Fix: yes"}, Commits: []prCommit{c1}, Files: files}, nil, nil)
			if o.result != tc.result || !strings.Contains(o.reason, tc.reason) {
				t.Fatalf("outcome %s %q, want %s containing %q", o.result, o.reason, tc.result, tc.reason)
			}
		})
	}
}
