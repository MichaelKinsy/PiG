package ciimages

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// test-json-report.py prints the output of failed tests and of tests still running when their package failed (a timeout or a crash ends the binary before those tests report), hides passing tests' output, marks cached packages, and copies the raw events.
func TestTestJSONReportShowsFailuresTimeoutsAndCachedPackages(t *testing.T) {
	type event struct {
		Action  string
		Package string
		Test    string  `json:",omitempty"`
		Output  string  `json:",omitempty"`
		Elapsed float64 `json:",omitempty"`
	}
	events := []event{
		{Action: "start", Package: "m/a"},
		{Action: "run", Package: "m/a", Test: "TestPass"},
		{Action: "output", Package: "m/a", Test: "TestPass", Output: "pass-detail\n"},
		{Action: "pass", Package: "m/a", Test: "TestPass", Elapsed: 0.5},
		{Action: "run", Package: "m/a", Test: "TestFail"},
		{Action: "output", Package: "m/a", Test: "TestFail", Output: "fail-detail\n"},
		{Action: "fail", Package: "m/a", Test: "TestFail", Elapsed: 0.25},
		{Action: "output", Package: "m/a", Output: "FAIL\tm/a\t1.00s\n"},
		{Action: "fail", Package: "m/a", Elapsed: 1},
		// go test -timeout: the alarm panics while TestHang runs, and the binary exits before TestHang reports.
		{Action: "start", Package: "m/d"},
		{Action: "run", Package: "m/d", Test: "TestHang"},
		{Action: "output", Package: "m/d", Test: "TestHang", Output: "panic: test timed out after 3s\n"},
		{Action: "output", Package: "m/d", Test: "TestHang", Output: "\trunning tests:\n\t\tTestHang (3s)\n"},
		{Action: "output", Package: "m/d", Output: "FAIL\tm/d\t3.01s\n"},
		{Action: "fail", Package: "m/d", Elapsed: 3.01},
		{Action: "start", Package: "m/c"},
		{Action: "output", Package: "m/c", Output: "ok  \tm/c\t(cached)\n"},
		{Action: "pass", Package: "m/c"},
	}
	var input strings.Builder
	for _, e := range events {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		input.Write(line)
		input.WriteByte('\n')
	}
	raw := filepath.Join(t.TempDir(), "events.json")
	cmd := exec.CommandContext(t.Context(), hostPython(), repoScript(t, "automation/ci/test-json-report.py"), "--out", raw)
	cmd.Stdin = strings.NewReader(input.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test-json-report.py: %v\n%s", err, out)
	}
	report := string(out)
	for _, want := range []string{"fail-detail\n", "FAIL\tm/a\t1.00s\n", "panic: test timed out after 3s\n", "\t\tTestHang (3s)\n", "FAIL\tm/d\t3.01s\n", "ok  \tm/c\t(cached)\n", "2 package(s) failed\n"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "pass-detail") {
		t.Errorf("report shows a passing test's output:\n%s", report)
	}
	if got, err := os.ReadFile(raw); err != nil || string(got) != input.String() {
		t.Errorf("raw events = %q, %v; want the input stream", got, err)
	}
}
