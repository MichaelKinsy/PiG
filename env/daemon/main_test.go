package daemon

// The test binary doubles as the programs the process tests start: with PIG_DAEMON_TEST_HELPER set it runs the named helper instead of the tests.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// helperEnv names the helper a started test binary runs.
const helperEnv = "PIG_DAEMON_TEST_HELPER"

// helperModes are the helpers by name. Files that need one register it in an init function.
var helperModes = map[string]func(){
	// wrapper waits for a line on stdin, starts a sleeper without waiting for it, prints the sleeper's pid and exits: a program such as `npx` that leaves its server running.
	"wrapper": func() {
		waitForLine()
		fmt.Println(startSleeper().Process.Pid)
	},
	// tree starts a sleeper, prints its pid and keeps running.
	"tree": func() {
		fmt.Println(startSleeper().Process.Pid)
		time.Sleep(10 * time.Minute)
	},
	// linger waits for a line on stdin and exits.
	"linger": func() { waitForLine() },
	// exit exits at once.
	"exit": func() {},
	// sleeper runs until it is killed.
	"sleeper": func() { time.Sleep(10 * time.Minute) },
}

func waitForLine() { _, _ = bufio.NewReader(os.Stdin).ReadString('\n') }

// startSleeper starts the test binary as a sleeper with no standard files, and leaves it unwaited.
func startSleeper() *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"=sleeper")
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	return cmd
}

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		run, ok := helperModes[mode]
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown helper %q\n", mode)
			os.Exit(2)
		}
		run()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// startHelper starts the helper mode through startProgram, as the daemon starts a `process` program.
func startHelper(t *testing.T, mode string) *program {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	started, failure := startProgram(&processRequest{argv: []string{exe}, cwd: t.TempDir(), env: map[string]string{helperEnv: mode}, inheritEnv: true})
	if failure != nil {
		t.Fatal(failure.toJSON())
	}
	t.Cleanup(started.closePipes)
	return started
}

// readPid reads the pid a helper printed on its stdout, once the helper closed it or ended the line.
func readPid(t *testing.T, started *program) int {
	t.Helper()
	line, err := bufio.NewReader(started.stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("the helper printed %q: %v", line, err)
	}
	var pid int
	if _, err := fmt.Sscan(strings.TrimSpace(line), &pid); err != nil {
		t.Fatalf("the helper printed %q: %v", line, err)
	}
	return pid
}
