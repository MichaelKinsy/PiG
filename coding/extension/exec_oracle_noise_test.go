package extension

import (
	"regexp"
	"testing"
)

// cygwinKillNoise is the line the MSYS runtime prints on stderr when the process that was starting a child dies under it: Windows native-4 (CI run
// 42384052028) read scenario 23 (a shell that ignores TERM, then execs sleep 60) with a 1000 ms timeout as
//
//	0 [main] sleep (10964) child_copy: cygheap read copy failed, 0x800000000..0x80000CE30, done 0, windows pid 10964, Win32 error 299
//
// from Pig against no stderr from Pi.
var cygwinKillNoise = regexp.MustCompile(`(?m)^ *\d+ \[main\] \S+ \(\d+\) child_copy: [^\n]*\n`)

// sameButForCygwinKillNoise reports whether got and want are the outcomes of one timer row that differ only in that MSYS line on stderr.
//
// Pi does not stop collecting at the kill: core/exec.ts:81-87 appends every stderr chunk until waitForChildProcess settles (utils/child-process.ts:93-97 re-arms
// the 100 ms grace on each chunk that follows the exit), so the line is in Pi's result whenever it reaches the pipe inside the grace, as in Pig's. Both
// sides kill the shell alone (exec.ts:55 proc.kill("SIGTERM"), TerminateProcess on Windows), so a kill that lands while the shell is starting its
// `exec` is the same race for both, and only a host slow enough to make a shell take a timeout to start shows it. Everything else must match.
func sameButForCygwinKillNoise(scenario execScenario, got, want execOutcome) bool {
	if scenario.Timeout == nil || !got.Killed || !want.Killed {
		return false
	}
	gotStderr, wantStderr := cygwinKillNoise.ReplaceAllString(got.Stderr, ""), cygwinKillNoise.ReplaceAllString(want.Stderr, "")
	got.Stderr, want.Stderr = gotStderr, wantStderr
	return got == want
}

func TestOracleIgnoresOnlyTheCygwinKillNoiseOfATimerRow(t *testing.T) {
	timeout := 1000.0
	timer := execScenario{Command: "sh", Args: []string{"-c", "trap '' TERM; exec sleep 60"}, Timeout: &timeout}
	noise := "      0 [main] sleep (10964) child_copy: cygheap read copy failed, 0x800000000..0x80000CE30, done 0, windows pid 10964, Win32 error 299\n"
	killed := execOutcome{Killed: true}
	for _, tc := range []struct {
		name      string
		scenario  execScenario
		got, want execOutcome
		same      bool
	}{
		{"noise only in Pig", timer, execOutcome{Stderr: noise, Killed: true}, killed, true},
		{"noise only in Pi", timer, killed, execOutcome{Stderr: noise, Killed: true}, true},
		{"noise on both sides", timer, execOutcome{Stderr: noise, Killed: true}, execOutcome{Stderr: noise, Killed: true}, true},
		{"other stderr stays", timer, execOutcome{Stderr: noise + "boom\n", Killed: true}, killed, false},
		{"stdout differs", timer, execOutcome{Stdout: "late\n", Stderr: noise, Killed: true}, killed, false},
		{"code differs", timer, execOutcome{Stderr: noise, Killed: true, Code: 1}, killed, false},
		{"not killed in Pig", timer, execOutcome{Stderr: noise}, killed, false},
		{"not killed in Pi", timer, execOutcome{Stderr: noise, Killed: true}, execOutcome{}, false},
		{"no timer", execScenario{Command: "sh"}, execOutcome{Stderr: noise, Killed: true}, killed, false},
	} {
		if got := sameButForCygwinKillNoise(tc.scenario, tc.got, tc.want); got != tc.same {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.same)
		}
	}
}
