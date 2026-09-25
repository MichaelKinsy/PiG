package subprocess

import "testing"

// TestWithStderrLog covers ATTACK-POINTS #9: an extension's stderr goes only
// to a temp file the user is never told about. Every place that builds a
// crash/disable reason must fold that path in when one was captured, and
// leave the reason untouched when none was (e.g. an in-process/fused
// extension has no stderr file).
func TestWithStderrLog(t *testing.T) {
	cases := []struct {
		name          string
		reason        string
		stderrLogPath string
		want          string
	}{
		{
			name:          "reason and log path",
			reason:        "extension crashed",
			stderrLogPath: "/tmp/pig-ext-foo-123.log",
			want:          "extension crashed (stderr: /tmp/pig-ext-foo-123.log)",
		},
		{
			name:          "empty reason with log path",
			reason:        "",
			stderrLogPath: "/tmp/pig-ext-foo-123.log",
			want:          "(stderr: /tmp/pig-ext-foo-123.log)",
		},
		{
			name:          "no log path leaves reason untouched",
			reason:        "extension crashed",
			stderrLogPath: "",
			want:          "extension crashed",
		},
		{
			name:          "no reason and no log path",
			reason:        "",
			stderrLogPath: "",
			want:          "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := withStderrLog(tc.reason, tc.stderrLogPath); got != tc.want {
				t.Fatalf("withStderrLog(%q, %q) = %q, want %q", tc.reason, tc.stderrLogPath, got, tc.want)
			}
		})
	}
}

// TestQuarantineNotice covers ATTACK-POINTS #5: a packed-cell crash notice
// must say which extensions stopped and that /reload brings them back,
// never assert the shared process is still alive.
func TestQuarantineNotice(t *testing.T) {
	cases := []struct {
		name         string
		reason       string
		stoppedNames []string
		want         string
	}{
		{
			name:         "lists stopped extensions and reload recovery",
			reason:       "packed process exited: signal: killed",
			stoppedNames: []string{"dupa", "dupb", "dupc"},
			want:         "packed process exited: signal: killed; dupa, dupb, dupc stopped; /reload restarts them",
		},
		{
			name:         "no members leaves reason untouched",
			reason:       "packed process exited",
			stoppedNames: nil,
			want:         "packed process exited",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := quarantineNotice(tc.reason, tc.stoppedNames); got != tc.want {
				t.Fatalf("quarantineNotice(%q, %v) = %q, want %q", tc.reason, tc.stoppedNames, got, tc.want)
			}
		})
	}
}
