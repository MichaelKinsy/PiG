// SPDX-License-Identifier: MIT

package tui

import (
	"maps"
	"os"
	"strings"
	"testing"
)

// Token, pid and time of the examples in the spec (packages/tui/test/program-resume.test.ts, Pi cf419553da),
// whose mac values are the real SHA-256 hashes.
var programResumeSpecSender = ProgramResumeSender{
	Token: "dummy-token-replace-me",
	Pid:   74877,
	Time:  1795543200,
}

func TestFormatProgramResumeUpstream(t *testing.T) {
	t.Run("matches the spec's Neovim example", func(t *testing.T) {
		got, ok := FormatProgramResume(ProgramResumeCommand{
			Argv: []string{"nvim", "-S", "/home/user/.local/state/nvim/sessions/8f2c1d.vim"},
		}, programResumeSpecSender)
		want := "\x1b]7577;pid=74877:time=1795543200:argv=bnZpbQ==,LVM=,L2hvbWUvdXNlci8ubG9jYWwvc3RhdGUvbnZpbS9zZXNzaW9ucy84ZjJjMWQudmlt:mac=9dbb99bfb856749a87563839d7ce9062818d78cf25909db93f309e0cd3b1909b\x1b\\"
		if !ok || got != want {
			t.Fatalf("got (%q, %v), want (%q, true)", got, ok, want)
		}
	})

	t.Run("matches the spec's lazygit example with a cwd", func(t *testing.T) {
		got, ok := FormatProgramResume(ProgramResumeCommand{
			Argv: []string{"lazygit"},
			Cwd:  "/home/user/src/project",
		}, programResumeSpecSender)
		want := "\x1b]7577;pid=74877:time=1795543200:argv=bGF6eWdpdA==:cwd=L2hvbWUvdXNlci9zcmMvcHJvamVjdA==:mac=35cb909805c380336efd3689a125acc774a9dec8422a1d21492ebec06ca758e1\x1b\\"
		if !ok || got != want {
			t.Fatalf("got (%q, %v), want (%q, true)", got, ok, want)
		}
	})

	t.Run("matches the spec's wrapper example", func(t *testing.T) {
		sender := programResumeSpecSender
		sender.Pid = 90412
		got, ok := FormatProgramResume(ProgramResumeCommand{
			Argv: []string{"resume-wrap", "ssh", "devbox"},
		}, sender)
		want := "\x1b]7577;pid=90412:time=1795543200:argv=cmVzdW1lLXdyYXA=,c3No,ZGV2Ym94:mac=e76149d0ba54a83570ffab0583867dacdfa23928ba9d18604f0badea88b62b65\x1b\\"
		if !ok || got != want {
			t.Fatalf("got (%q, %v), want (%q, true)", got, ok, want)
		}
	})

	t.Run("keeps empty arguments after the program", func(t *testing.T) {
		got, ok := FormatProgramResume(ProgramResumeCommand{Argv: []string{"pi", ""}}, programResumeSpecSender)
		if !ok || !strings.Contains(got, ":argv=cGk=,:mac=") {
			t.Fatalf("got (%q, %v), want substring :argv=cGk=,:mac=", got, ok)
		}
	})

	t.Run("returns false for commands the protocol cannot carry", func(t *testing.T) {
		overLimit := make([]string, 65)
		overLimit[0] = "pi"
		for i := 1; i < len(overLimit); i++ {
			overLimit[i] = "x"
		}
		unreportable := []ProgramResumeCommand{
			{Argv: nil},
			{Argv: []string{""}},
			{Argv: []string{"./run.sh"}},
			{Argv: []string{"bin/pi"}},
			{Argv: []string{"-pi"}},
			{Argv: []string{"pi", "line\nbreak"}},
			{Argv: []string{"pi", "c1\u009bcontrol"}},
			{Argv: []string{"pi", "invalid \xff utf8"}},
			{Argv: []string{"pi", "lone \xed\xa0\x80 surrogate"}},
			{Argv: []string{"pi"}, Cwd: "project"},
			{Argv: []string{"pi"}, Cwd: "/tmp/\x1b"},
			{Argv: overLimit},
			{Argv: []string{"pi", strings.Repeat("x", 2048)}},
		}
		for _, cmd := range unreportable {
			if got, ok := FormatProgramResume(cmd, programResumeSpecSender); ok || got != "" {
				t.Errorf("FormatProgramResume(%+v) = (%q, %v), want (\"\", false)", cmd, got, ok)
			}
		}
		if got, ok := FormatProgramResume(ProgramResumeCommand{Argv: []string{"/usr/local/bin/pi"}, Cwd: "/"}, programResumeSpecSender); !ok || got == "" {
			t.Fatalf("expected absolute program and cwd to succeed, got (%q, %v)", got, ok)
		}
	})
}

func TestFormatProgramResumeClearUpstream(t *testing.T) {
	got := FormatProgramResumeClear(programResumeSpecSender)
	want := "\x1b]7577;pid=74877:time=1795543200:clear=1:mac=c30603369f98fef581a4098be889175b0c74f0641e49f8b25f4326cb38f97c93\x1b\\"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestTakeProgramResumeTokenUpstream(t *testing.T) {
	t.Run("returns the token and removes it from the map", func(t *testing.T) {
		env := map[string]string{ProgramResumeTokenEnv: "abc", "PATH": "/bin"}
		if got := TakeProgramResumeTokenFromMap(env); got != "abc" {
			t.Fatalf("got %q, want abc", got)
		}
		if !maps.Equal(env, map[string]string{"PATH": "/bin"}) {
			t.Fatalf("env after take = %v, want map[PATH:/bin]", env)
		}
	})

	t.Run("treats a missing or empty token as no support", func(t *testing.T) {
		env := map[string]string{ProgramResumeTokenEnv: ""}
		if got := TakeProgramResumeTokenFromMap(env); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
		if len(env) != 0 {
			t.Fatalf("env after take = %v, want empty", env)
		}
		if got := TakeProgramResumeTokenFromMap(map[string]string{}); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("removes OSC7577_TOKEN from process environment", func(t *testing.T) {
		t.Setenv(ProgramResumeTokenEnv, "secret-token-xyz")
		if got := TakeProgramResumeToken(); got != "secret-token-xyz" {
			t.Fatalf("got %q, want secret-token-xyz", got)
		}
		if _, present := os.LookupEnv(ProgramResumeTokenEnv); present {
			t.Fatalf("%s still present in process environment after TakeProgramResumeToken", ProgramResumeTokenEnv)
		}
	})
}

func TestIsNodeAbsolutePathForOS(t *testing.T) {
	cases := []struct {
		path      string
		wantPosix bool
		wantWin   bool
	}{
		{"", false, false},
		{"/", true, true},
		{"/home/user/project", true, true},
		{`\`, false, true},
		{`\foo`, false, true},
		{`\\server\share`, false, true},
		{"C:", false, false},
		{"C:foo", false, false},
		{`C:\`, false, true},
		{"C:/", false, true},
		{`C:\Users\dev`, false, true},
		{`1:\foo`, false, false},
		{"./rel", false, false},
		{"foo", false, false},
	}
	for _, tc := range cases {
		if got := isNodeAbsolutePathForOS(tc.path, "linux"); got != tc.wantPosix {
			t.Errorf("posix isNodeAbsolutePathForOS(%q) = %v, want %v", tc.path, got, tc.wantPosix)
		}
		if got := isNodeAbsolutePathForOS(tc.path, "windows"); got != tc.wantWin {
			t.Errorf("windows isNodeAbsolutePathForOS(%q) = %v, want %v", tc.path, got, tc.wantWin)
		}
	}
}
