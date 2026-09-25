package main

import (
	"reflect"
	"testing"
)

// Upstream main.ts validateForkFlags: --fork conflicts with --session,
// --continue, --resume and --no-session, listed in that order.
func TestValidateForkFlagsMatchesUpstreamConflicts(t *testing.T) {
	cases := []struct {
		args []string
		want []argDiagnostic
	}{
		{args: []string{"--fork", "abc"}},
		{args: []string{"--no-session"}},
		{args: []string{"--fork", "abc", "--no-session"}, want: []argDiagnostic{{Type: "error", Message: "--fork cannot be combined with --no-session"}}},
		{
			args: []string{"--no-session", "-r", "-c", "--session", "s", "--fork", "abc"},
			want: []argDiagnostic{{Type: "error", Message: "--fork cannot be combined with --session, --continue, --resume, --no-session"}},
		},
	}
	for _, tc := range cases {
		if got := validateForkFlags(parseFlags(tc.args)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("validateForkFlags(%v) = %#v, want %#v", tc.args, got, tc.want)
		}
	}
}
