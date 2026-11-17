//go:build parity

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const fakeHungProgram = `package main

import (
	"fmt"
	"time"
)

// blockForeverInKnownFunction is the frame the test looks for in the goroutine dump.
//
//go:noinline
func blockForeverInKnownFunction() {
	time.Sleep(time.Hour)
}

func main() {
	fmt.Println("fake program booting, never ready")
	blockForeverInKnownFunction()
}
`

func buildFakeHungProgram(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(fakeHungProgram), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "fake-hung")
	if output, err := exec.CommandContext(t.Context(), "go", "build", "-o", out, source).CombinedOutput(); err != nil {
		t.Fatalf("go build fake program: %v\n%s", err, output)
	}
	return out
}

// countSignals wraps the runner's signalling seam: the real signal is still sent, and each pid is counted.
func countSignals(t *testing.T) *atomic.Int32 {
	t.Helper()
	var count atomic.Int32
	real := signalHungPig
	signalHungPig = func(pid int) error {
		count.Add(1)
		return real(pid)
	}
	t.Cleanup(func() { signalHungPig = real })
	return &count
}

func hangScenario() *Scenario {
	return &Scenario{Name: "hang-dump", Tmux: TmuxDriverConfig{Width: 100, Height: 12, ReadyPatternPig: "NEVER-PRINTED", ReadyPatternPi: "NEVER-PRINTED", ReadyTimeoutSeconds: 2}}
}

// A pig whose ready wait times out leaves, in the failure output and in Result.HangDump, its last pane, its process line and the goroutine dump that SIGQUIT makes a Go program print. Pi gets the pane and the tree and no signal.
func TestReadyTimeoutRecordsHangDump(t *testing.T) {
	program := buildFakeHungProgram(t)
	for _, label := range []string{"pig", "pi"} {
		t.Run(label, func(t *testing.T) {
			signals := countSignals(t)
			result := (tmuxDriver{}).Run(t.Context(), t, BinaryRef{Label: label, Path: program}, hangScenario())
			if result.Err == nil || !strings.Contains(result.Err.Error(), "ready pattern") {
				t.Fatalf("Err = %v, want the ready timeout", result.Err)
			}
			t.Logf("failure output:\n%v", result.Err)
			for name, text := range map[string]string{"failure output": result.Err.Error(), "HangDump": result.HangDump} {
				for _, want := range []string{"fake program booting, never ready", program} {
					if !strings.Contains(text, want) {
						t.Errorf("%s lacks %q:\n%s", name, want, text)
					}
				}
			}
			if label == "pi" {
				if got := signals.Load(); got != 0 {
					t.Fatalf("pi was sent %d signals", got)
				}
				if strings.Contains(result.HangDump, "goroutine") {
					t.Fatalf("pi dump holds a goroutine dump:\n%s", result.HangDump)
				}
				return
			}
			if got := signals.Load(); got != 1 {
				t.Fatalf("pig was sent %d signals, want 1", got)
			}
			for name, text := range map[string]string{"failure output": result.Err.Error(), "HangDump": result.HangDump} {
				for _, want := range []string{"SIGQUIT: quit", "goroutine ", "main.blockForeverInKnownFunction"} {
					if !strings.Contains(text, want) {
						t.Errorf("%s lacks %q:\n%s", name, want, text)
					}
				}
			}
		})
	}
}

// A run whose waits pass records nothing and signals nothing.
func TestPassingWaitRecordsNoHangDump(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	signals := countSignals(t)
	sc := hangScenario()
	sc.Tmux.ReadyPatternPig = "READY"
	bin := BinaryRef{Label: "pig", Path: "sh", Args: []string{"-c", "printf 'READY\\n'; sleep 999"}}
	result := (tmuxDriver{}).Run(t.Context(), t, bin, sc)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.HangDump != "" {
		t.Fatalf("a passing run recorded a dump:\n%s", result.HangDump)
	}
	if got := signals.Load(); got != 0 {
		t.Fatalf("a passing run sent %d signals", got)
	}
}

// The launch shell's own command line contains the program's path, so only a row that begins with the path is the program.
func TestHungProgramPIDSkipsTheLaunchShell(t *testing.T) {
	rows := []hangProcessRow{
		{pid: 10, command: "/bin/sh -c cd /x && /opt/pig --model m; exit_code=$?"},
		{pid: 11, command: "/opt/pig --model m"},
		{pid: 12, command: "sleep 999"},
	}
	if pid, err := hungProgramPID(rows, "/opt/pig"); err != nil || pid != 11 {
		t.Fatalf("pid = %d, err = %v, want 11", pid, err)
	}
	if _, err := hungProgramPID(rows[:1], "/opt/pig"); err == nil {
		t.Fatal("the launch shell was taken for the program")
	}
	rows[2].command = "/opt/pig"
	if _, err := hungProgramPID(rows, "/opt/pig"); err == nil {
		t.Fatal("two matches were not refused")
	}
}

// The dump of the last run lands beside the other failure artifacts, one file per binary; a run without a dump writes none.
func TestFailureArtifactsKeepHangDump(t *testing.T) {
	root := t.TempDir()
	scenarioFile := filepath.Join(root, "scenario.toml")
	if err := os.WriteFile(scenarioFile, []byte("name = \"hang\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := &ScenarioOutcome{
		Scenario: &Scenario{Name: "hang", Driver: "interactive-tmux", SourcePath: scenarioFile},
		Pig:      SystemResults{Runs: []Result{{Output: "a"}, {Output: "b", HangDump: "pig dump text"}}},
		Pi:       SystemResults{Runs: []Result{{Output: "c"}}},
		Failures: []string{"ready"},
	}
	dir, err := WriteFailureArtifacts(t, filepath.Join(root, "artifacts"), o, BinaryRef{Label: "pig", Path: "/tmp/pig"}, BinaryRef{Label: "pi", Path: "/tmp/pi"}, FreshnessReport{Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "pig.hang-dump.txt")); err != nil || string(data) != "pig dump text" {
		t.Fatalf("pig.hang-dump.txt = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pi.hang-dump.txt")); err == nil {
		t.Fatal("a run without a dump wrote pi.hang-dump.txt")
	}
}

// A step wait that times out records the dump the same way as the ready wait: the program became ready, then never produced the output the step waits for.
func TestStepWaitTimeoutRecordsHangDump(t *testing.T) {
	program := buildFakeHungProgram(t)
	signals := countSignals(t)
	sc := hangScenario()
	sc.Tmux.ReadyPatternPig = "booting"
	sc.Tmux.Steps = []TmuxStep{{Keys: []string{"x"}, WaitContains: []string{"NEVER-PRINTED"}, WaitTimeoutSeconds: 2}}
	result := (tmuxDriver{}).Run(t.Context(), t, BinaryRef{Label: "pig", Path: program}, sc)
	if result.Err == nil || !strings.Contains(result.Err.Error(), "step output") {
		t.Fatalf("Err = %v, want the step timeout", result.Err)
	}
	if got := signals.Load(); got != 1 {
		t.Fatalf("pig was sent %d signals, want 1", got)
	}
	for name, text := range map[string]string{"failure output": result.Err.Error(), "HangDump": result.HangDump} {
		for _, want := range []string{"fake program booting, never ready", "SIGQUIT: quit", "main.blockForeverInKnownFunction"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q:\n%s", name, want, text)
			}
		}
	}
}
