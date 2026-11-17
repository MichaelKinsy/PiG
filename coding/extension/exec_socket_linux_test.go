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

// pig divergence (D631): when the reader closes a socket that holds unread data, the writer's write fails with ECONNRESET and no SIGPIPE, so Pi's
// `exec yes` over the cap goes on to report the error on stderr and exits 1 when it gets that far; the write of a pipe raises SIGPIPE at once, so
// PiG's ends by a signal and reports nothing. Every run of Pi shows one of the ends measured for it (cutOffEnds), so an end Pi did not have is
// seen here, and every run of PiG shows the one end that a pipe shares with a socket. Each run's stderr goes to a file of its own, which the
// closed pipes of a truncated call do not hide.
//
// That Pi's writer does meet the reset is asked of a yes that ignores SIGTERM. The SIGTERM of exec.ts:113 can end yes before it reports, and where
// yes waits for a CPU it mostly does: on 2 CPUs shared with 8 busy loops, 3,993 of Pi's 4,000 runs of `exec yes` ended so and 7 held a report,
// so 200 runs held none in 60 of 100 test runs. A yes that ignores SIGTERM lives to report what its write met: 5,087 of Pi's 10,000 such runs
// alone, 2,625 of 4,000 beside 32 busy loops and 3,200 of 4,000 on those 2 CPUs ended 1 with a whole report and the others 141, as each of
// PiG's 8,000 did.
func TestExecCommandCutOffWriterMeetsSIGPIPEWherePisMeetsAReset(t *testing.T) {
	const runs = 200
	dir := t.TempDir()
	zero := 0.0
	said := func(file string) string {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		return string(text)
	}
	for _, row := range []struct {
		name, script string
		// reports is set where one of Pi's runs has to report a failed write.
		reports bool
	}{
		{name: "yes", script: "exec yes"},
		{name: "deaf-yes", script: "trap '' TERM; exec yes", reports: true},
	} {
		scenarios := func(side string) (list []execScenario, files []string) {
			for i := range runs {
				file := filepath.Join(dir, fmt.Sprintf("%s-%s-%d", row.name, side, i))
				files = append(files, file)
				list = append(list, execScenario{Command: "sh", Args: []string{"-c", row.script + " 2>'" + file + "'"}, CWD: dir, MaxBuffer: &zero})
			}
			return list, files
		}

		list, files := scenarios("pi")
		reports := 0
		for i, outcome := range piExecOutcomes(t, list) {
			text := said(files[i])
			if end := cutOffEndOf(outcome, text); end == nil {
				t.Errorf("Pi run %d of %q = %+v with %q on stderr, which is none of the ends measured for Pi (cutOffEnds)", i, row.script, outcome, text)
			} else if end.socket {
				reports++
			}
		}
		if row.reports && reports == 0 {
			t.Errorf("none of Pi's %d runs of %q reported a failed write: its writer no longer meets a reset", runs, row.script)
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
			t.Errorf("Pig run %d of %q = %+v with %q on stderr, want a truncated result that SIGPIPE (141) or SIGTERM (143) ended and nothing on stderr: this is %s", i, row.script, outcome, text, is)
		}
	}
}
