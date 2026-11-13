package extension

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pig divergence (D631): Node gives a command socket pairs for stdout and stderr and ExecCommand gives it pipes. A socket cannot be opened again through
// /dev/stdout (ENXIO), a pipe can: Pi's command fails where PiG's writes. Both sides are asserted, so a change of either shows here.
func TestExecCommandReopensItsOutputWherePiCannot(t *testing.T) {
	scenario := execScenario{Command: "sh", Args: []string{"-c", "echo x > /dev/stdout"}, CWD: t.TempDir()}
	pi := piExecOutcomes(t, []execScenario{scenario})[0]
	if pi.Code == 0 || pi.Stdout != "" || !strings.Contains(pi.Stderr, "/dev/stdout: No such device or address") || pi.Killed || pi.Truncated {
		t.Errorf("Pi = %+v, want a nonzero code, no stdout and sh's ENXIO for /dev/stdout on stderr", pi)
	}
	pig := pigExecOutcome(context.Background(), scenario)
	if want := (execOutcome{Stdout: "x\n"}); pig != want {
		t.Errorf("Pig = %+v, want %+v", pig, want)
	}
}

// What coreutils' yes writes to stderr when a write to stdout fails, by the error: ECONNRESET and EPIPE.
const (
	yesResetReport = "yes: standard output: Connection reset by peer\n"
	yesPipeReport  = "yes: standard output: Broken pipe\n"
)

// A cutOffEnd is one way `sh -c "exec yes 2>file"` ends when its first chunk crosses the cap: the code the call resolves and what yes wrote to the
// file. The call closes its ends of the command's output and then sends SIGTERM (exec.ts:111-113), and the end of a run is what yes's write of that
// moment meets. Pi's yes writes a socket pair (exec.ts:58-62). The kernel names are those of Linux 7.0 (net/unix/af_unix.c, net/core/sock.c).
type cutOffEnd struct {
	name string
	// socket marks an end that only the writer of a socket meets.
	// pig divergence (D631): PiG's command writes a pipe and shows none of them.
	socket bool
	holds  func(code int, said string) bool
}

// cutOffEnds are the ends measured for Pi and nothing else: a run of Pi that shows none of them is a behaviour of Pi this file does not know.
// The counts are of tern-ci:v11 on xd670 with 200 calls per Node process, as the test below makes them: Pi's 20,000 runs alone and 10,000 beside
// 32 busy loops, and PiG's 20,000 and 10,000.
var cutOffEnds = []cutOffEnd{
	// A write that starts after the close (exec.ts:111) finds its end shut down and raises SIGPIPE, on a socket as on a pipe (141); or the SIGTERM
	// of exec.ts:113 is delivered before such a write has raised it (143). Either way yes reports nothing. Pi: 10,385 of 20,000 alone (141 in
	// 9,542) and 4,053 of 10,000 beside load (141 in 2,693). PiG: every run (141 in 19,794 of 20,000 alone and in 8,600 of 10,000 beside load).
	{name: "a signal before any report", holds: func(code int, said string) bool { return said == "" && (code == 141 || code == 143) }},
	// The close finds unread data and posts ECONNRESET on yes's end (unix_release_sock). A write that waits for room in the full socket, or is past
	// the entry check of unix_stream_sendmsg, returns that error and raises no signal (sock_alloc_send_pskb), so yes reports it in four writes and
	// exits 1; the SIGTERM ends it after any of them, the last included (143). A run cut off before the text of the error counts here. Pi: 9,604
	// of 20,000 alone (1 in 792) and 5,944 of 10,000 beside load (1 in 893).
	{name: "a reset report", socket: true, holds: reportEnds(yesResetReport)},
}

// reportEnds is the rule of an end in which yes reports a failed write: the whole report with yes's own exit code 1, or any beginning of it, the
// whole included, with the 143 of the SIGTERM.
func reportEnds(report string) func(code int, said string) bool {
	return func(code int, said string) bool {
		return said != "" && (code == 143 && strings.HasPrefix(report, said) || code == 1 && said == report)
	}
}

// cutOffEndOf returns the end of cutOffEnds that a truncated and killed call shows, or nil when it shows none of them.
func cutOffEndOf(outcome execOutcome, said string) *cutOffEnd {
	if !outcome.Truncated || !outcome.Killed || outcome.Thrown != "" {
		return nil
	}
	for i := range cutOffEnds {
		if cutOffEnds[i].holds(outcome.Code, said) {
			return &cutOffEnds[i]
		}
	}
	return nil
}

// Each (code, stderr) pair that Pi's 30,000 runs showed has its end, and a pair that none of them showed has none.
func TestCutOffEndsAreTheOutcomesMeasuredForPi(t *testing.T) {
	cut := func(code int) execOutcome { return execOutcome{Code: code, Killed: true, Truncated: true} }
	for _, row := range []struct {
		outcome execOutcome
		said    string
		end     string
	}{
		{cut(141), "", "a signal before any report"},
		{cut(143), "", "a signal before any report"},
		{cut(143), "yes: ", "a reset report"},
		{cut(143), "yes: standard output", "a reset report"},
		{cut(143), "yes: standard output: Connection reset by peer", "a reset report"},
		{cut(143), yesResetReport, "a reset report"},
		{cut(1), yesResetReport, "a reset report"},
		{cut(143), "yes: standard output: Broken pipe", "a broken-pipe report"},
		{cut(143), yesPipeReport, "a broken-pipe report"},
		{cut(1), yesPipeReport, "a broken-pipe report"},
		{cut(1), "", ""},
		{cut(1), "yes: standard output", ""},
		{cut(1), "yes: standard output: Broken pipe", ""},
		{cut(141), yesResetReport, ""},
		{cut(141), yesPipeReport, ""},
		{cut(137), "", ""},
		{cut(0), "", ""},
		{cut(143), "yes: standard output: Permission denied\n", ""},
		{execOutcome{Code: 143, Truncated: true}, "", ""},
		{execOutcome{Code: 143, Killed: true}, "", ""},
		{execOutcome{Code: 143, Killed: true, Truncated: true, Thrown: "x"}, "", ""},
	} {
		got := ""
		if end := cutOffEndOf(row.outcome, row.said); end != nil {
			got = end.name
		}
		if got != row.end {
			t.Errorf("%+v with %q on stderr is %q, want %q", row.outcome, row.said, got, row.end)
		}
	}
}

// pig divergence (D631): when the reader closes a socket that holds unread data, the writer's write fails with ECONNRESET and no SIGPIPE, so Pi's
// `exec yes` over the cap goes on to report the error on stderr and exits 1 when it gets that far; the write of a pipe raises SIGPIPE at once, so
// PiG's ends by a signal and reports nothing. Every run of Pi shows one of the ends measured for it (cutOffEnds), so an end Pi did not have is
// seen here, and every run of PiG shows the one end that a pipe shares with a socket. Each run's stderr goes to a file of its own, which the
// closed pipes of a truncated call do not hide.
func TestExecCommandCutOffWriterMeetsSIGPIPEWherePisMeetsAReset(t *testing.T) {
	const runs = 200
	dir := t.TempDir()
	zero := 0.0
	scenarios := func(side string) (list []execScenario, files []string) {
		for i := range runs {
			file := filepath.Join(dir, fmt.Sprintf("%s-%d", side, i))
			files = append(files, file)
			list = append(list, execScenario{Command: "sh", Args: []string{"-c", "exec yes 2>'" + file + "'"}, CWD: dir, MaxBuffer: &zero})
		}
		return list, files
	}
	said := func(file string) string {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(text)
	}

	list, files := scenarios("pi")
	reports := 0
	for i, outcome := range piExecOutcomes(t, list) {
		text := said(files[i])
		if end := cutOffEndOf(outcome, text); end == nil {
			t.Errorf("Pi run %d = %+v with %q on stderr, which is none of the ends measured for Pi (cutOffEnds)", i, outcome, text)
		} else if end.socket {
			reports++
		}
	}
	if reports == 0 {
		t.Errorf("none of Pi's %d runs reported a failed write: its command no longer writes a socket", runs)
	}

	list, files = scenarios("pig")
	for i, scenario := range list {
		outcome := pigExecOutcome(context.Background(), scenario)
		text := said(files[i])
		end := cutOffEndOf(outcome, text)
		if end != nil && !end.socket {
			continue
		}
		is := "none of Pi's ends"
		if end != nil {
			is = end.name + ", which only the writer of a socket meets"
		}
		t.Errorf("Pig run %d = %+v with %q on stderr, want a truncated result that SIGPIPE (141) or SIGTERM (143) ended and nothing on stderr: this is %s", i, outcome, text, is)
	}
}
