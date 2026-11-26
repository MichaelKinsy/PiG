// SPDX-License-Identifier: MIT

package codingagent

// Ports packages/coding-agent/test/program-resume-reporter.test.ts (Pi PR #12557, commit cf419553da817fefd2f86f170ff3e93eff0bfd28).

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

const testProgramResumeToken = "test-token-0123456789abcdef"

type decodedProgramResumeReport struct {
	kind   string
	argv   []string
	cwd    string
	hasCwd bool
}

var osc7577RE = regexp.MustCompile(`^\x1b\]7577;(.*):mac=([0-9a-f]{64})\x1b\\$`)

// decodeProgramResumeReport checks mac, pid and time like a terminal would.
func decodeProgramResumeReport(t *testing.T, sequence string, wantPID int, wantTime int64) decodedProgramResumeReport {
	t.Helper()
	m := osc7577RE.FindStringSubmatch(sequence)
	if m == nil {
		t.Fatalf("sequence %q does not match OSC 7577 format", sequence)
	}
	body, mac := m[1], m[2]
	sum := sha256.Sum256([]byte(body + ":" + testProgramResumeToken))
	if got := hex.EncodeToString(sum[:]); got != mac {
		t.Fatalf("mac = %s, want %s for body %q", mac, got, body)
	}
	pairs := map[string]string{}
	for pair := range strings.SplitSeq(body, ":") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			t.Fatalf("malformed pair %q in %q", pair, body)
		}
		pairs[k] = v
	}
	if pairs["pid"] != strconv.Itoa(wantPID) {
		t.Fatalf("pid = %q, want %d", pairs["pid"], wantPID)
	}
	if pairs["time"] != strconv.FormatInt(wantTime, 10) {
		t.Fatalf("time = %q, want %d", pairs["time"], wantTime)
	}
	if pairs["clear"] == "1" {
		return decodedProgramResumeReport{kind: "clear"}
	}
	decodeText := func(v string) string {
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			t.Fatalf("base64 decode %q: %v", v, err)
		}
		return string(b)
	}
	rawArgv, ok := pairs["argv"]
	if !ok {
		t.Fatalf("missing argv in %q", body)
	}
	parts := strings.Split(rawArgv, ",")
	argv := make([]string, len(parts))
	for i, p := range parts {
		argv[i] = decodeText(p)
	}
	rep := decodedProgramResumeReport{kind: "resume", argv: argv}
	if rawCwd, ok := pairs["cwd"]; ok {
		rep.hasCwd = true
		rep.cwd = decodeText(rawCwd)
	}
	return rep
}

type programResumeCaptureTerminal struct {
	tui.Terminal
	writes []string
}

func (c *programResumeCaptureTerminal) Write(data string) {
	c.writes = append(c.writes, data)
}

func setupProgramResumeTest(t *testing.T, initial *tui.ProgramResumeCommand) (*ProgramResumeReporter, **tui.ProgramResumeCommand, func() []decodedProgramResumeReport, *[]string) {
	t.Helper()
	term := &programResumeCaptureTerminal{}
	cmd := initial
	fixedTime := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	fixedPID := os.Getpid()
	reporter := NewProgramResumeReporter(testProgramResumeToken, func() tui.Terminal { return term }, func() *tui.ProgramResumeCommand { return cmd })
	reporter.now = func() time.Time { return fixedTime }
	reporter.pid = func() int { return fixedPID }
	reports := func() []decodedProgramResumeReport {
		out := make([]decodedProgramResumeReport, len(term.writes))
		for i, w := range term.writes {
			out[i] = decodeProgramResumeReport(t, w, fixedPID, fixedTime.Unix())
		}
		return out
	}
	return reporter, &cmd, reports, &term.writes
}

func testResumeSession(id string) *tui.ProgramResumeCommand {
	return &tui.ProgramResumeCommand{Argv: []string{AppName, "--session", id}, Cwd: "/home/user/project"}
}

func wantResumeReport(cmd *tui.ProgramResumeCommand) decodedProgramResumeReport {
	return decodedProgramResumeReport{kind: "resume", argv: cmd.Argv, cwd: cmd.Cwd, hasCwd: cmd.Cwd != ""}
}

func TestProgramResumeReporterUpstream(t *testing.T) {
	t.Run("reports the resume command with an authenticated pid and time", func(t *testing.T) {
		reporter, _, reports, _ := setupProgramResumeTest(t, testResumeSession("a"))
		reporter.Report()
		want := []decodedProgramResumeReport{wantResumeReport(testResumeSession("a"))}
		if got := reports(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("reports only when the command changes", func(t *testing.T) {
		reporter, cmd, reports, _ := setupProgramResumeTest(t, testResumeSession("a"))
		reporter.Report()
		reporter.Report()
		*cmd = testResumeSession("b")
		reporter.Report()
		want := []decodedProgramResumeReport{
			wantResumeReport(testResumeSession("a")),
			wantResumeReport(testResumeSession("b")),
		}
		if got := reports(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("sends nothing until there is a session to resume, and clears when it goes away", func(t *testing.T) {
		reporter, cmd, reports, _ := setupProgramResumeTest(t, nil)
		reporter.Report()
		if got := reports(); len(got) != 0 {
			t.Fatalf("got %+v, want empty", got)
		}
		*cmd = testResumeSession("a")
		reporter.Report()
		// For example /new: the new session has no file until its first message.
		*cmd = nil
		reporter.Report()
		want := []decodedProgramResumeReport{
			wantResumeReport(testResumeSession("a")),
			{kind: "clear"},
		}
		if got := reports(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("ReportIfNone checks only while no command is reported", func(t *testing.T) {
		term := &programResumeCaptureTerminal{}
		fixedTime := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
		fixedPID := os.Getpid()
		var current *tui.ProgramResumeCommand
		calls := 0
		reporter := NewProgramResumeReporter(testProgramResumeToken, func() tui.Terminal { return term }, func() *tui.ProgramResumeCommand {
			calls++
			return current
		})
		reporter.now = func() time.Time { return fixedTime }
		reporter.pid = func() int { return fixedPID }

		reporter.ReportIfNone()
		if len(term.writes) != 0 {
			t.Fatalf("writes = %v, want empty", term.writes)
		}
		// The first message creates the session file.
		current = testResumeSession("a")
		reporter.ReportIfNone()
		reporter.ReportIfNone()
		reporter.ReportIfNone()
		if len(term.writes) != 1 || !reflect.DeepEqual(decodeProgramResumeReport(t, term.writes[0], fixedPID, fixedTime.Unix()), wantResumeReport(testResumeSession("a"))) {
			t.Fatalf("writes = %v, want single resume report for a", term.writes)
		}
		if calls != 2 {
			t.Fatalf("getResumeCommand calls = %d, want 2", calls)
		}
		// A session switch (/new) clears through Report(); ReportIfNone() checks again until the next file.
		current = nil
		reporter.Report()
		reporter.ReportIfNone()
		if len(term.writes) != 2 || decodeProgramResumeReport(t, term.writes[1], fixedPID, fixedTime.Unix()).kind != "clear" {
			t.Fatalf("writes = %v, want [resume, clear]", term.writes)
		}
		if calls != 4 {
			t.Fatalf("getResumeCommand calls = %d, want 4", calls)
		}
	})

	t.Run("reports an unchanged command again after a suspend", func(t *testing.T) {
		reporter, _, reports, _ := setupProgramResumeTest(t, testResumeSession("a"))
		reporter.Report()
		reporter.ReportAgain()
		want := []decodedProgramResumeReport{
			wantResumeReport(testResumeSession("a")),
			wantResumeReport(testResumeSession("a")),
		}
		if got := reports(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("does not report again when there was and is nothing to resume", func(t *testing.T) {
		reporter, _, reports, _ := setupProgramResumeTest(t, nil)
		reporter.ReportAgain()
		if got := reports(); len(got) != 0 {
			t.Fatalf("got %+v, want empty", got)
		}
	})

	t.Run("clears the command on quit, once", func(t *testing.T) {
		reporter, _, reports, _ := setupProgramResumeTest(t, testResumeSession("a"))
		reporter.Clear()
		if got := reports(); len(got) != 0 {
			t.Fatalf("got %+v, want empty", got)
		}
		reporter.Report()
		reporter.Clear()
		reporter.Clear()
		want := []decodedProgramResumeReport{
			wantResumeReport(testResumeSession("a")),
			{kind: "clear"},
		}
		if got := reports(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("clears instead of reporting a command the protocol cannot carry", func(t *testing.T) {
		reporter, cmd, reports, _ := setupProgramResumeTest(t, testResumeSession("a"))
		reporter.Report()
		*cmd = &tui.ProgramResumeCommand{Argv: []string{AppName, "--session", "a"}, Cwd: "/home/user/line\nbreak"}
		reporter.Report()
		want := []decodedProgramResumeReport{
			wantResumeReport(testResumeSession("a")),
			{kind: "clear"},
		}
		if got := reports(); !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})
}
