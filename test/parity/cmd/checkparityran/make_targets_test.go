package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// Every make target that runs the parity suite must count the scenarios that executed. TestParity skips before running any scenario when it cannot resolve Pi or pig, and `go test` exits 0 for a skip, so a target that does not read the results file reports ok for a run that compared nothing.
func TestEveryParityTargetRequiresScenariosToRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the parity make targets run on POSIX hosts")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Fatalf("make is required: %v", err)
	}
	root := repoRoot(t)
	for _, target := range []string{
		"parity", "parity-fast", "parity-family FAMILY=tools", "parity-driver DRIVER=rpc-mode",
		"parity-stress", "parity-durable", "parity-live", "parity-perf",
	} {
		t.Run(target, func(t *testing.T) {
			cmd := exec.Command("make", append([]string{"-n", "-s"}, strings.Fields(target)...)...)
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("make -n %s: %v\n%s", target, err, out)
			}
			text := string(out)
			if !strings.Contains(text, "-pig-parity.results=") {
				t.Errorf("make %s does not ask the runner for a results file, so nothing can count the scenarios that ran", target)
			}
			if !strings.Contains(text, "go run ./test/parity/cmd/checkparityran") {
				t.Errorf("make %s does not run require-parity-ran, so a run that skipped every scenario reports ok", target)
			}
		})
	}
}

func TestQCSmokeRequiresScenariosToRun(t *testing.T) {
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "qc-smoke.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-pig-parity.results", "checkparityran"} {
		if !strings.Contains(string(script), want) {
			t.Errorf("qc-smoke.sh does not use %q: its parity subset can skip every scenario and still print ok", want)
		}
	}
}
