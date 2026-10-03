//go:build !windows

package nodespawn

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// trampolineEnv names the only variable of the environment of a trampoline.
// Its value is the number of arguments of the program to execute. The
// trampoline's arguments after its own name are the program's path, its
// arguments, and its environment block, each as the raw string that execve
// takes, so a name, an argument, or a value need not be UTF-8.
const trampolineEnv = "PIG_NODESPAWN_EXEC"

// runTrampoline runs before main in every program that links this package. A
// process that was started by useTrampoline replaces itself with the program
// that its arguments describe, the environment block of which is exactly the
// rest of its arguments.
func runTrampoline() {
	value, ok := os.LookupEnv(trampolineEnv)
	if !ok || len(os.Environ()) != 1 {
		return
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > len(os.Args)-2 {
		fmt.Fprintf(os.Stderr, "pig: invalid %s=%s for %d arguments\n", trampolineEnv, value, len(os.Args))
		os.Exit(127)
	}
	path, args, env := os.Args[1], os.Args[2:2+n], os.Args[2+n:]
	err = syscall.Exec(path, args, env)
	fmt.Fprintf(os.Stderr, "pig: exec %s: %v\n", path, err)
	os.Exit(127)
}

func init() { runTrampoline() }

// useTrampoline makes cmd start a copy of this program that executes the
// program cmd.Path with cmd.Args and the environment cmd.Env unchanged,
// because os/exec would remove an entry of that environment (sharesEnvKey).
// cmd.Dir and every other option apply to the trampoline, which keeps its
// process, its file descriptors, and its working directory when it executes.
// The trampoline's argv holds each argument and each variable as one string,
// so the kernel limits (E2BIG) apply to them as they apply to the program's own
// execve, except for the few bytes of the trampoline's name, the path, and
// its variable. On Linux the copy is /proc/self/exe, the running image, which
// stays valid when an update replaces or removes the file PiG started from.
func useTrampoline(cmd *exec.Cmd) {
	if cmd.Err != nil || len(cmd.Args) == 0 || !sharesEnvKey(cmd.Env) {
		return
	}
	self := "/proc/self/exe"
	if runtime.GOOS != "linux" || linkerexec.Current().Active() {
		// The linker is /proc/self/exe in a process it started.
		var err error
		if self, err = linkerexec.Executable(); err != nil {
			cmd.Err = err
			return
		}
	}
	args := make([]string, 0, 2+len(cmd.Args)+len(cmd.Env))
	args = append(args, self, cmd.Path)
	args = append(args, cmd.Args...)
	args = append(args, cmd.Env...)
	count := strconv.Itoa(len(cmd.Args))
	cmd.Path, cmd.Args, cmd.Env = self, args, []string{trampolineEnv + "=" + count}
}
