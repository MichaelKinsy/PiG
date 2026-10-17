//go:build linux || darwin

package daemon

// Ports packages/env/daemon/src/pty.rs

import (
	"errors"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// ptyProcess is a program running in a pseudo-terminal: the master side of the terminal and the program, which leads
// the terminal's session and so its process group.
type ptyProcess struct {
	master *os.File
	cmd    *exec.Cmd
	pid    int
}

func winsize(cols, rows uint16) *unix.Winsize { return &unix.Winsize{Row: rows, Col: cols} }

// control runs fn on the master's descriptor, which stays valid while it runs.
func (p *ptyProcess) control(fn func(fd int) error) error {
	raw, err := p.master.SyscallConn()
	if err != nil {
		return err
	}
	var result error
	if err := raw.Control(func(fd uintptr) { result = fn(int(fd)) }); err != nil {
		return err
	}
	return result
}

func (p *ptyProcess) setSize(cols, rows uint16) error {
	return p.control(func(fd int) error { return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, winsize(cols, rows)) })
}

// foreground is the terminal's foreground process group.
func (p *ptyProcess) foreground() (int, error) {
	group := 0
	err := p.control(func(fd int) (err error) {
		group, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP)
		return err
	})
	return group, err
}

// nameMax is NAME_MAX, the longest file name execvp searches PATH for.
const nameMax = 255

// lookPath is where execvp, which Rust's Command calls in the child, finds name for a program started with environment:
// through the first PATH the program gets, not the daemon's, or the C library's default (execPathDefault). An empty name
// is ENOENT, a name with a slash is used as it is, and a PATH entry that has name but cannot run it is passed over and
// reported as EACCES when no other entry runs it.
func lookPath(name string, environment []string) (string, error) {
	if name == "" {
		return "", syscall.ENOENT
	}
	if strings.Contains(name, "/") {
		return name, nil
	}
	//portlint:allow utf16units execvp's NAME_MAX limit is in bytes (pty.rs runs the C library's execvp); no JavaScript string is involved
	if len(name) > nameMax {
		return "", syscall.ENAMETOOLONG
	}
	path := execPathDefault
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			path = value
			break
		}
	}
	denied := false
	for directory := range strings.SplitSeq(path, ":") {
		candidate := name
		if directory != "" {
			candidate = directory + "/" + name
		}
		info, err := os.Stat(candidate)
		switch {
		case errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR):
			continue
		case errors.Is(err, syscall.EACCES):
			denied = true
			continue
		case err != nil:
			return "", err
		case info.IsDir() || unix.Access(candidate, unix.X_OK) != nil:
			denied = true
			continue
		}
		return candidate, nil
	}
	if denied {
		return "", syscall.EACCES
	}
	return "", syscall.ENOENT
}

// spawnFailure is the spawn_error of a program that could not start, in the words of Rust's io::Error that pty.rs
// passes on: the system's text for the error number, then `(os error N)`.
func spawnFailure(err error) *Failure {
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		text := errno.Error()
		if text != "" {
			text = strings.ToUpper(text[:1]) + text[1:]
		}
		return newFailure("spawn_error", text+" (os error "+strconv.Itoa(int(errno))+")")
	}
	return newFailure("spawn_error", err.Error())
}

// command is the program to run: argv, or the user's shell as a login shell (`-` before its name, as `login` does).
func command(request *ptyRequest, environment []string) (*exec.Cmd, error) {
	if request.hasArgv {
		path, err := lookPath(request.argv[0], environment)
		if err != nil {
			return nil, err
		}
		return &exec.Cmd{Path: path, Args: request.argv}, nil
	}
	shell, ok := request.env["SHELL"]
	if !ok {
		shell = os.Getenv("SHELL")
	}
	if shell == "" || !pathExists(shell) {
		shell = "/bin/sh"
	}
	name := shell[strings.LastIndex(shell, "/")+1:]
	return &exec.Cmd{Path: shell, Args: []string{"-" + name}}, nil
}

// ptyEnvironment is the program's environment: the daemon's unless inheritEnv is false, with request.env over it, ordered
// by name as Rust's Command passes a changed environment (CommandEnv::capture builds a BTreeMap). A request that changes
// nothing passes the daemon's environment as it is, in its own order (CommandEnv::capture_if_changed).
func ptyEnvironment(request *ptyRequest) []string {
	if request.inheritEnv && len(request.env) == 0 {
		return os.Environ()
	}
	values := map[string]string{}
	if request.inheritEnv {
		for _, entry := range os.Environ() {
			if name, value, ok := strings.Cut(entry, "="); ok {
				values[name] = value
			}
		}
	}
	maps.Copy(values, request.env)
	environment := make([]string, 0, len(values))
	for _, name := range slices.Sorted(maps.Keys(values)) {
		environment = append(environment, name+"="+values[name])
	}
	return environment
}

// startPty spawns the program in a new terminal of the requested size, as the leader of a new session with the
// terminal as its controlling terminal, so job control and SIGHUP work.
func startPty(request *ptyRequest) (*ptyProcess, *Failure) {
	master, slave, err := openPty()
	if err != nil {
		return nil, spawnFailure(err)
	}
	defer func() { _ = slave.Close() }()
	environment := ptyEnvironment(request)
	cmd, err := command(request, environment)
	if err != nil {
		_ = master.Close()
		return nil, spawnFailure(err)
	}
	cmd.Dir = request.cwd
	cmd.Env = environment
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	proc := &ptyProcess{master: master, cmd: cmd}
	if err := proc.setSize(request.cols, request.rows); err != nil {
		_ = master.Close()
		return nil, spawnFailure(err)
	}
	if err := withEmptySignalMask(cmd.Start); err != nil {
		_ = master.Close()
		return nil, spawnFailure(err)
	}
	proc.pid = cmd.Process.Pid
	return proc, nil
}

// killGroup kills the program's process group.
func (p *ptyProcess) killGroup() {
	if err := syscall.Kill(-p.pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(p.pid, syscall.SIGKILL)
	}
}

// wait waits for the program to exit and returns its exit code, 128 plus the signal for a program a signal ended.
func (p *ptyProcess) wait() (int, error) {
	err := p.cmd.Wait()
	if err == nil {
		return 0, nil
	}
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		return 0, err
	}
	state := p.cmd.ProcessState
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal()), nil
	}
	return state.ExitCode(), nil
}

// read delivers the terminal's output until it closes (Linux reports EIO once no program holds it), onData returns
// false, or done is closed and the terminal is closed.
func (p *ptyProcess) read(done <-chan struct{}, onData func([]byte) bool) {
	buffer := make([]byte, 64*1024)
	for {
		select {
		case <-done:
			return
		default:
		}
		count, err := p.master.Read(buffer)
		if count > 0 && !onData(append([]byte(nil), buffer[:count]...)) {
			return
		}
		if err != nil {
			return
		}
	}
}

// writeAll writes all of bytes, waiting while the program does not read its input; false once the terminal is gone.
func (p *ptyProcess) writeAll(bytes []byte, done <-chan struct{}) bool {
	select {
	case <-done:
		return false
	default:
	}
	_, err := p.master.Write(bytes)
	return err == nil
}

// close closes the terminal, which ends a read or write waiting on it.
func (p *ptyProcess) close() { _ = p.master.Close() }

const ptySupported = true
