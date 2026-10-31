//go:build unix

package workers

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// freeze stops this whole process for d, as a JavaScript busy loop leaves nothing of Node running: a helper process
// (fixtureEnv "resume") continues it, then it goes on. A SIGKILL ends it meanwhile.
func freeze(d time.Duration) {
	helper := exec.Command(os.Args[0])
	helper.Env = append(os.Environ(), fixtureEnv+"=resume", "CHORD_FIXTURE_PID="+strconv.Itoa(os.Getpid()), "CHORD_FIXTURE_MS="+strconv.Itoa(int(d.Milliseconds())))
	if err := helper.Start(); err != nil {
		panic(err)
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
}

// resumeFixture is the helper: it waits, continues the stopped process and exits.
func resumeFixture() {
	pid, _ := strconv.Atoi(os.Getenv("CHORD_FIXTURE_PID"))
	ms, _ := strconv.Atoi(os.Getenv("CHORD_FIXTURE_MS"))
	time.Sleep(time.Duration(ms) * time.Millisecond)
	_ = syscall.Kill(pid, syscall.SIGCONT)
	os.Exit(0)
}
