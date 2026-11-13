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

// pig divergence (D631): when the reader closes a socket that holds unread data, the writer's next write fails once with ECONNRESET and no SIGPIPE,
// so Pi's `exec yes` over the cap goes on to report the error on stderr and exits 1 when it gets that far; the write of a pipe raises SIGPIPE at once,
// so PiG's ends by a signal and reports nothing. Each run's stderr goes to a file of its own, which the closed pipes of a truncated call do not hide.
func TestExecCommandCutOffWriterMeetsSIGPIPEWherePisMeetsAReset(t *testing.T) {
	const runs = 200
	const report = "yes: standard output: Connection reset by peer\n"
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
		if text != "" {
			reports++
		}
		if !outcome.Truncated || outcome.Code == 1 && text != report || text != "" && !strings.HasPrefix(report, text) {
			t.Errorf("Pi run %d = %+v with %q on stderr, want a truncated result, the whole reset report when the code is 1, and nothing else on stderr", i, outcome, text)
		}
	}
	if reports == 0 {
		t.Errorf("none of Pi's %d runs began the report %q: its command no longer meets a reset", runs, report)
	}

	list, files = scenarios("pig")
	for i, scenario := range list {
		outcome := pigExecOutcome(context.Background(), scenario)
		if text := said(files[i]); !outcome.Truncated || outcome.Code != 141 && outcome.Code != 143 || text != "" {
			t.Errorf("Pig run %d = %+v with %q on stderr, want a truncated result that SIGPIPE (141) or SIGTERM (143) ended and nothing on stderr", i, outcome, text)
		}
	}
}
