//go:build unix

package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// QUALITY-AUDIT-050 item 16: reap-test-processes.sh read /proc and reported nothing on macOS, so a test's leaked tmux server, Node
// oracle or helper kept running there. Without /proc the script reads `ps -E`, which appends each process's environment to its
// command line. The stand-in ps prints that format for real processes, so the same matching runs on any host (REAP_USE_PS=1).
func TestReapTestProcessesReadsTheEnvironmentFromPsWithoutProc(t *testing.T) {
	root := testenv.ModuleRoot(t)
	scratch := filepath.Join(t.TempDir(), "scratch")
	if err := os.Mkdir(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	// exited closes when the test's own Wait reaps the process, so liveness needs no /proc (macOS has none).
	type started struct {
		cmd    *exec.Cmd
		exited chan struct{}
	}
	start := func(duration, tmpdir string) started {
		cmd := exec.Command("sleep", duration)
		cmd.Env = append(os.Environ(), "TMPDIR="+tmpdir)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(exited) }()
		t.Cleanup(func() { _ = cmd.Process.Kill(); <-exited })
		return started{cmd, exited}
	}
	mark := strconv.Itoa(700000 + os.Getpid()%90000)
	leaked := start(mark, scratch)
	nested := start(mark+"1", filepath.Join(scratch, "TestX001"))
	lookalike := start(mark+"2", scratch+"-other")
	elsewhere := start(mark+"3", "/tmp/unrelated")

	bin := t.TempDir()
	lines := []string{}
	for _, entry := range []struct {
		process started
		tmpdir  string
		args    string
	}{
		{leaked, scratch, "sleep " + mark},
		{nested, filepath.Join(scratch, "TestX001"), "sleep " + mark + "1"},
		{lookalike, scratch + "-other", "sleep " + mark + "2"},
		{elsewhere, "/tmp/unrelated", "sleep " + mark + "3"},
	} {
		// BSD ps -E: the command, then the environment as NAME=value words.
		lines = append(lines, strconv.Itoa(entry.process.cmd.Process.Pid)+" "+entry.args+" HOME=/home/x TMPDIR="+entry.tmpdir+" PATH=/usr/bin")
	}
	fakePS := "#!/bin/sh\ncase \"$*\" in\n  *ppid=*) echo 1 ;;\n  *) cat <<'PS'\n" + strings.Join(lines, "\n") + "\nPS\n  ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "ps"), []byte(fakePS), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), testenv.Bash(t), filepath.Join(root, "automation", "ci", "reap-test-processes.sh"), scratch)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "REAP_USE_PS=1")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "[reap-test-processes] 2 processes") {
		t.Fatalf("reaper: err=%v, want the two processes under %s reported\n%s", err, scratch, output)
	}
	// The reaper sends SIGKILL before it exits: a killed process is gone soon after, and a spared one is still running a grace period later.
	for _, killed := range []struct {
		name    string
		process started
	}{{"process under the scratch directory", leaked}, {"process under a directory beneath it", nested}} {
		select {
		case <-killed.process.exited:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: still running after the reaper ran, want it killed", killed.name)
		}
	}
	time.Sleep(time.Second)
	for _, spared := range []struct {
		name    string
		process started
	}{{"process under a sibling directory with the same prefix", lookalike}, {"process under an unrelated TMPDIR", elsewhere}} {
		select {
		case <-spared.process.exited:
			t.Fatalf("%s: killed by the reaper, want it spared", spared.name)
		default:
		}
	}
}
