//go:build !windows

package nodespawn

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// SetCommandLine does nothing outside Windows, where Node's spawn and os/exec
// both pass cmd.Args to execve unchanged.
func SetCommandLine(*exec.Cmd) {}

// start is cmd.Start: SetProgram arranges the trampoline for an environment
// that os/exec would change.
func start(cmd *exec.Cmd) error { return cmd.Start() }

// HideWindow does nothing outside Windows, where libuv ignores windowsHide.
func HideWindow(*exec.Cmd, ...Stdio) {}

// SetProgram makes Start return the *Error that Node's spawn(cmd.Args[0],
// cmd.Args[1:], { cwd: cmd.Dir, env: cmd.Env }) throws for its arguments, and
// makes cmd start what libuv's execvp starts: the program it finds in the PATH
// of cmd.Env (PiG's PATH when cmd.Env is nil), or the file with a slash, run
// with /bin/sh when the kernel does not recognize it, as lookPathIn describes.
// Call it after the last change to cmd.Args, cmd.Dir, and cmd.Env.
func SetProgram(cmd *exec.Cmd) {
	setProgram(cmd)
	// The linker starts a program and, for a trampoline, the copy of PiG that
	// starts it.
	linkerexec.Prepare(cmd)
	useTrampoline(cmd)
	linkerexec.Prepare(cmd)
}

func setProgram(cmd *exec.Cmd) {
	if len(cmd.Args) == 0 {
		return
	}
	if err := checkArguments(cmd.Args[0], cmd.Args[1:], cmd.Dir, cmd.Env); err != nil {
		cmd.Err = err
		return
	}
	file := cmd.Args[0]
	switch {
	case strings.Contains(file, "/"):
	case cmd.Env != nil || errors.Is(cmd.Err, exec.ErrNotFound) || errors.Is(cmd.Err, exec.ErrDot):
		// exec.Command's own lookup found no program, or refused one in the
		// working directory that execvp starts. It also reports every
		// failure as not found.
		name, err := search(file, cmd.Dir, childEnvironment(cmd.Env))
		if err != nil {
			cmd.Err = err
			return
		}
		cmd.Path, cmd.Err = startName(name), nil
		if runsWithShell(name, cmd.Dir) {
			runWithShell(cmd, name)
		}
		return
	}
	if cmd.Err == nil && runsWithShell(cmd.Path, cmd.Dir) {
		runWithShell(cmd, cmd.Path)
	}
}

// runWithShell is glibc's maybe_script_execute: execvp runs a file that
// execve does not recognize with "/bin/sh file args...", where args are the
// arguments after argv[0].
func runWithShell(cmd *exec.Cmd, name string) {
	cmd.Path = "/bin/sh"
	cmd.Args = append([]string{"/bin/sh", name}, cmd.Args[1:]...)
}

// runsWithShell reports that execve fails with ENOEXEC for name, which the
// child looks up after it changes to cwd, as execveErrno describes. It is not
// the whole kernel rule: a file that a binfmt_misc handler recognizes is not
// run with /bin/sh. libuv searches the path itself with posix_spawn on macOS,
// where this is not applied.
func runsWithShell(name, cwd string) bool {
	if runtime.GOOS == "darwin" {
		return false
	}
	return execveErrno(inDirectory(cwd, startName(name)), cwd, 0) == syscall.ENOEXEC
}

// inDirectory is name as the kernel opens it in the working directory cwd:
// cwd, a slash, and name for a relative name, and name itself otherwise or
// when cwd is empty. It does not clean the result, because the kernel resolves
// ".." after it follows the symlink before it.
func inDirectory(cwd, name string) string {
	if cwd == "" || strings.HasPrefix(name, "/") {
		return name
	}
	return cwd + "/" + name
}

// startName is name as os/exec starts it: a name without a slash would be
// looked up in the PATH.
func startName(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return "./" + name
}

// childEnvironment is the environment libuv's child searches: env, or PiG's
// when the spawn has none.
func childEnvironment(env []string) []string {
	if env == nil {
		return os.Environ()
	}
	return env
}

// SetDirectory does nothing outside Windows, where libuv passes the cwd option
// to chdir unchanged.
func SetDirectory(*exec.Cmd) {}

// LookPath returns the program that Node's child_process.spawn(file, args)
// starts after the argument checks of Node's spawn for file, cwd, and env,
// where "" is PiG's working directory and env is the spawn's env option. A nil
// env is PiG's environment. A file with a slash is the file itself; otherwise
// libuv searches the PATH of the environment, as lookPathIn describes.
func LookPath(file, cwd string, env []string) (string, error) {
	if err := checkArguments(file, nil, cwd, env); err != nil {
		return "", err
	}
	if strings.Contains(file, "/") {
		return exec.LookPath(file)
	}
	name, err := search(file, cwd, childEnvironment(env))
	if err != nil {
		return "", err
	}
	return startName(name), nil
}

// defaultPath is the PATH libuv's child searches when its environment has none:
// the confstr(_CS_PATH) value of glibc's execvp, which uv__process_child_init
// (src/unix/process.c, libuv 1.52.1) calls after it replaces environ with
// options->env, or _PATH_DEFPATH, which uv__spawn_resolve_and_spawn uses on
// macOS.
var defaultPath = func() string {
	if runtime.GOOS == "linux" {
		return "/bin:/usr/bin"
	}
	return "/usr/bin:/bin"
}()

// search is execvp's search for a file without a slash in the environment env,
// run after the child changes to cwd: the PATH of env, or defaultPath when it
// has none, split at each colon, where an empty entry is the working
// directory. It returns the first candidate, as glibc names it (the entry and
// the file joined, or the file for an empty entry), that is an executable
// file, relative to cwd when the entry is. The candidates that do not start
// follow glibc: EACCES, which a file without execute permission or a directory
// gives, is remembered and the search goes on; so do ENOENT, ENOTDIR, ESTALE,
// ENODEV, and ETIMEDOUT; any other error ends the search. When no candidate
// starts, the error is EACCES if a candidate gave it, and the last errno
// otherwise. ENOENT is exec.ErrNotFound, as exec.Command reports it. A
// candidate that exists in a PATH entry below a file gives ENOTDIR.
//
// The PATH is the first entry that starts with "PATH=", which the child's
// getenv sees: glibc's execvp reads getenv("PATH") after uv__process_child_init
// (src/unix/process.c, libuv 1.52.1) replaces environ with the block, and
// uv__spawn_find_path_in_env returns the first such entry on macOS. A property
// whose name starts with "PATH=" is such an entry, and useTrampoline passes the
// block whole when another entry shares its text before the first "=".
func search(file, cwd string, env []string) (string, error) {
	path := defaultPath
	for _, e := range env {
		if value, ok := strings.CutPrefix(e, "PATH="); ok {
			path = value
			break
		}
	}
	last := syscall.ENOENT
	denied := false
	for dir := range strings.SplitSeq(path, ":") {
		// execvp joins the entry and the file with a slash and leaves the
		// kernel to resolve "..", which follows a symlink before it, so the
		// candidate is not cleaned.
		candidate := file
		if dir != "" {
			candidate = dir + "/" + file
		}
		errno := candidateErrno(inDirectory(cwd, startName(candidate)), cwd)
		switch errno {
		case 0:
			return candidate, nil
		case syscall.EACCES:
			denied = true
		case syscall.ENOENT, syscall.ENOTDIR, syscall.ESTALE, syscall.ENODEV, syscall.ETIMEDOUT:
			last = errno
		default:
			return "", spawnError(file, errno)
		}
	}
	if denied {
		return "", spawnError(file, syscall.EACCES)
	}
	return "", spawnError(file, last)
}

// candidateErrno is the errno of execve for path, the candidate of search
// after cwd is joined to it, when execve does not fail with ENOEXEC: 0 for a
// program that starts. ENOEXEC is 0 here, because execvp then runs the file
// with /bin/sh.
func candidateErrno(path, cwd string) syscall.Errno {
	errno := execveErrno(path, cwd, 0)
	if errno == syscall.ENOEXEC {
		return 0
	}
	return errno
}

// maxInterpreterDepth is the deepest interpreter chain execve follows: the
// kernel's search_binary_handler (fs/exec.c) fails with ELOOP for a chain of
// six interpreters, as in a script that names a script that names a script.
const maxInterpreterDepth = 5

// binprmBufSize is the kernel's BINPRM_BUF_SIZE, the head of the file that its
// binary format handlers see.
const binprmBufSize = 256

// execveErrno is the errno of the execve of path, where a relative path is
// relative to the child's working directory cwd and depth is the number of
// interpreters execve has followed: 0 when it starts, and ENOEXEC when no
// binary format handler recognizes the file, so that execvp runs it with
// /bin/sh. It reads the file's head with a non-blocking open, because a FIFO
// fails with EACCES and must not block. It applies on Linux the rules of the
// kernel's "#!" handler (fs/binfmt_script.c, as scriptInterpreter describes;
// the interpreter's own errno is the script's) and of its ELF handlers (as
// elfErrno describes); other systems start every executable regular file.
//
// The kernel reads the head of a file that the user may execute but not read.
// PiG cannot, so it asks the kernel with probeExecve.
func execveErrno(path, cwd string, depth int) syscall.Errno {
	if errno := executableErrno(path); errno != 0 {
		return errno
	}
	if runtime.GOOS != "linux" {
		return 0
	}
	if depth > maxInterpreterDepth {
		return syscall.ELOOP
	}
	file, head, err := openHead(path)
	if errors.Is(err, os.ErrPermission) {
		// A file that the kernel cannot be asked about is assumed to start.
		errno, _ := probeExecve(path, cwd)
		return errno
	}
	if err != nil {
		return 0
	}
	defer func() { _ = file.Close() }()
	if bytes.HasPrefix(head, []byte("#!")) {
		interpreter, ok := scriptInterpreter(head)
		if !ok {
			return syscall.ENOEXEC
		}
		return execveErrno(inDirectory(cwd, startName(interpreter)), cwd, depth+1)
	}
	if bytes.HasPrefix(head, []byte("\x7fELF")) {
		return elfErrno(file, head, cwd)
	}
	return syscall.ENOEXEC
}

// executableErrno is the errno of opening path for execve: what a stat
// gives, and EACCES for a file that is not an executable regular file.
func executableErrno(path string) syscall.Errno {
	info, err := os.Stat(path)
	if err != nil {
		if errno, ok := errors.AsType[syscall.Errno](err); ok {
			return errno
		}
		return syscall.ENOENT
	}
	if !info.Mode().IsRegular() {
		return syscall.EACCES
	}
	if _, err := exec.LookPath(path); err != nil {
		return syscall.EACCES
	}
	return 0
}

// scriptInterpreter is the interpreter that the kernel's "#!" handler
// (load_script, fs/binfmt_script.c) opens for a file whose head is head, and
// false when the handler fails with ENOEXEC. The handler reads the first
// binprmBufSize bytes, zero padded: the line ends at the first newline before
// a NUL, or, when there is none, the interpreter must end with a space, tab,
// or NUL within the buffer. The interpreter is the first word after "#!" and
// any spaces and tabs, and it ends at a space, tab, or NUL. A line that starts
// with NUL names the empty interpreter, which open_exec opens as the working
// directory.
func scriptInterpreter(head []byte) (string, bool) {
	var buf [binprmBufSize]byte
	copy(buf[:], head)
	last := len(buf) - 1
	end := -1
	for i, c := range buf {
		if c == 0 {
			break
		}
		if c == '\n' {
			end = i
			break
		}
	}
	if end < 0 {
		end = nextNonSpacetab(buf[:], 2, last)
		if end < 0 || nextTerminator(buf[:], end, last) < 0 {
			return "", false
		}
		end = last
	}
	for spacetab(buf[end-1]) {
		end--
	}
	name := nextNonSpacetab(buf[:], 2, end)
	if name < 0 || name == end {
		return "", false
	}
	if separator := nextTerminator(buf[:], name, end); separator >= 0 {
		end = separator
	}
	return string(buf[name:end]), true
}

func spacetab(c byte) bool { return c == ' ' || c == '\t' }

// nextNonSpacetab is the index of the first byte of buf[first:last+1] that is
// not a space or tab, or -1.
func nextNonSpacetab(buf []byte, first, last int) int {
	for ; first <= last; first++ {
		if !spacetab(buf[first]) {
			return first
		}
	}
	return -1
}

// nextTerminator is the index of the first space, tab, or NUL in
// buf[first:last+1], or -1.
func nextTerminator(buf []byte, first, last int) int {
	for ; first <= last; first++ {
		if spacetab(buf[first]) || buf[first] == 0 {
			return first
		}
	}
	return -1
}

// openHead opens the regular file at path and reads its first binprmBufSize
// bytes. Its error is the open error, which is os.ErrPermission for a file
// without the read permission.
func openHead(path string) (*os.File, []byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, errors.New("not a regular file")
	}
	head := make([]byte, binprmBufSize)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		_ = file.Close()
		return nil, nil, err
	}
	return file, head[:n], nil
}

// spawnError is the error libuv's execvp reports for file: exec.ErrNotFound
// for ENOENT, and an emitted spawn error with the errno's code otherwise.
func spawnError(file string, errno syscall.Errno) error {
	if errno == syscall.ENOENT {
		return &exec.Error{Name: file, Err: exec.ErrNotFound}
	}
	code, _ := nodeerrno.Code(errno)
	return &Error{Code: code, Syscall: "spawn " + file, errno: errno}
}
