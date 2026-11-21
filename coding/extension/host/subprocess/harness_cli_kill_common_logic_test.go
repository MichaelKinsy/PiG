package subprocess

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestHarnessSnapshotHelpersHandleWindowsShapedProcessTrees(t *testing.T) {
	entries := []harnessProcessSnapshotEntry{
		{PID: 100, PPID: 4, Name: "subprocess.test.exe"},
		{PID: 200, PPID: 100, Name: "node.exe"},
		{PID: 300, PPID: 200, Name: "node.exe"},
		{PID: 400, PPID: 300, Name: "subprocess.test.exe"},
		{PID: 500, PPID: 999, Name: "unrelated.exe"},
	}

	if got := harnessParentFromSnapshot(entries, 400); got != 300 {
		t.Fatalf("parent of binary 400 = %d, want supervisor 300", got)
	}
	if got := harnessParentFromSnapshot(entries, 300); got != 200 {
		t.Fatalf("parent of supervisor 300 = %d, want entry 200", got)
	}
	if got := harnessParentFromSnapshot(entries, 9999); got != 0 {
		t.Fatalf("parent of absent pid = %d, want 0", got)
	}

	children := harnessChildrenByParent(entries, 200, 300)
	if len(children) != 2 || children[0].PID != 300 || children[1].PID != 400 {
		t.Fatalf("children of entry+supervisor = %+v, want pids 300 and 400", children)
	}

	// Verify CRLF pid parsing as Windows helpers may emit.
	if pid, ok := harnessPublishedPID([]byte("400\r\n")); !ok || pid != 400 {
		t.Fatalf("harnessPublishedPID(CRLF) = (%d, %v), want (400, true)", pid, ok)
	}
	if _, ok := harnessPublishedPID([]byte("400")); ok {
		t.Fatal("harnessPublishedPID accepted an incomplete line without newline")
	}

	diag := formatHarnessProcessSnapshot(entries, 200, 300, 400)
	for _, want := range []string{"pid=200 ppid=100 exe=node.exe", "pid=300 ppid=200 exe=node.exe", "pid=400 ppid=300 exe=subprocess.test.exe"} {
		if !strings.Contains(diag, want) {
			t.Fatalf("diagnostic snapshot %q missing %q", diag, want)
		}
	}
	if strings.Contains(diag, "unrelated.exe") {
		t.Fatalf("diagnostic snapshot %q should not contain unrelated process", diag)
	}
}

func TestHarnessHelperBinaryWorksThroughEntry(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	cli, err := filepath.Abs(filepath.Join("runtime-node", "harness", "cli.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	binary := harnessSelfBinaryPath(t)

	t.Run("exit code 7", func(t *testing.T) {
		dir := t.TempDir()
		pidFile := filepath.Join(dir, "binary.pid")
		cmd := exec.Command(node, cli)
		cmd.Env = append(os.Environ(),
			"PIG_HARNESS_BINARY="+binary,
			harnessTestHelperEnv+"=exit",
			harnessTestHelperPIDEnv+"="+pidFile,
			harnessTestHelperExitEnv+"=7",
		)
		startHarnessEntry(t, cmd)
		_ = waitHarnessChildPID(t, pidFile)
		var exitErr *exec.ExitError
		if err := cmd.Wait(); !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
			t.Fatalf("entry exit = %v, want code 7", err)
		}
	})

	t.Run("stdio and args normal exit", func(t *testing.T) {
		dir := t.TempDir()
		pidFile := filepath.Join(dir, "binary.pid")
		argsFile := filepath.Join(dir, "binary.args.json")
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(node, cli, "--mode", "rpc x")
		cmd.Stdin = strings.NewReader("hello-helper\n")
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		cmd.Env = append(os.Environ(),
			"PIG_HARNESS_BINARY="+binary,
			harnessTestHelperEnv+"=stdio-exit",
			harnessTestHelperPIDEnv+"="+pidFile,
			harnessTestHelperArgsEnv+"="+argsFile,
			harnessTestHelperExitEnv+"=0",
		)
		startHarnessEntry(t, cmd)
		_ = waitHarnessChildPID(t, pidFile)
		if err := cmd.Wait(); err != nil {
			t.Fatalf("entry exit = %v, want nil (stderr=%q)", err, stderr.String())
		}
		if got, want := strings.ReplaceAll(stdout.String(), "\r\n", "\n"), "out:hello-helper|args:--mode,rpc x\n"; got != want {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
		if got, want := strings.ReplaceAll(stderr.String(), "\r\n", "\n"), "err:hello-helper\n"; got != want {
			t.Fatalf("stderr = %q, want %q", got, want)
		}
		rawArgs, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("read args file: %v", err)
		}
		var gotArgs []string
		if err := json.Unmarshal(rawArgs, &gotArgs); err != nil {
			t.Fatalf("unmarshal args: %v", err)
		}
		if !slices.Equal(gotArgs, []string{"--mode", "rpc x"}) {
			t.Fatalf("args = %q, want [--mode rpc x]", gotArgs)
		}
	})
}
