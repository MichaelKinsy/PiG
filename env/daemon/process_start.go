package daemon

// Ports packages/env/daemon/src/exec.rs (spawn, Launch)
//
// A `process` program starts as an `exec` argv command does: through Node's spawn rules (nodespawn), in a process group of its
// own, with the environment rules of `exec`.

import (
	"maps"
	"os"
	"os/exec"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// program is a started `process` program: the process and the daemon's ends of its pipes.
type program struct {
	cmd                   *exec.Cmd
	pid                   int
	stdin, stdout, stderr *os.File
}

// programEnvironment is the program's environment as `exec` builds it: the daemon's unless inheritEnv is false, with the
// request's over it. Go maps keep no insertion order, so the request's names are added in sorted order.
func programEnvironment(request *processRequest) []nodespawn.EnvProperty {
	var environment []nodespawn.EnvProperty
	if request.inheritEnv {
		environment = nodespawn.EnvProperties(nodespawn.ProcessEnv())
	}
	for _, name := range slices.Sorted(maps.Keys(request.env)) {
		environment = append(environment, nodespawn.EnvProperty{Name: name, Value: request.env[name]})
	}
	return environment
}

// startProgram starts the program with plain pipes for stdin, stdout and stderr.
func startProgram(request *processRequest) (*program, *Failure) {
	failure := func(err error) *Failure {
		if text, ok := rustIOError(err); ok {
			return newFailure("spawn_error", text)
		}
		return newFailure("spawn_error", nodespawn.SpawnMessage(request.argv[0], err))
	}
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, failure(err)
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		closeFiles(stdinRead, stdinWrite)
		return nil, failure(err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		closeFiles(stdinRead, stdinWrite, stdoutRead, stdoutWrite)
		return nil, failure(err)
	}
	//nolint:gosec // G204 (class d): the program and arguments are what the client asked the daemon to run, as for `exec`.
	cmd := exec.Command(request.argv[0], request.argv[1:]...)
	cmd.Dir = request.cwd
	nodespawn.SetEnvProperties(cmd, programEnvironment(request))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinRead, stdoutWrite, stderrWrite
	cmd.SysProcAttr = programAttributes()
	nodespawn.HideWindow(cmd, nodespawn.Pipe, nodespawn.Pipe, nodespawn.Pipe)
	nodespawn.SetProgram(cmd)
	nodespawn.SetCommandLine(cmd)
	// Start closes the child's ends of the pipes, whether or not the program starts.
	if err := startCommand(cmd); err != nil {
		closeFiles(stdinWrite, stdoutRead, stderrRead)
		return nil, failure(err)
	}
	return &program{cmd: cmd, pid: cmd.Process.Pid, stdin: stdinWrite, stdout: stdoutRead, stderr: stderrRead}, nil
}

// closePipes closes the daemon's ends of the pipes, which ends a read or write waiting on them.
func (p *program) closePipes() { closeFiles(p.stdin, p.stdout, p.stderr) }

func closeFiles(files ...*os.File) {
	for _, file := range files {
		_ = file.Close()
	}
}
