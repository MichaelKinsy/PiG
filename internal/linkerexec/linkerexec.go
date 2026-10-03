// Package linkerexec starts programs on Android builds that forbid executing
// files in an app's data directory.
//
// Termux from Google Play cannot execve its own binaries: Android's SELinux
// policy blocks the execution of an app data file. Termux works around it with
// termux-exec, an LD_PRELOAD library that rewrites a libc execve into
// "/system/bin/linker64 <program> <args>", where the linker maps a PIE
// executable itself. PiG's Go runtime issues the execve system call without
// libc, so the library never sees it; this package makes the same rewrite
// before os/exec starts a program. The rewrite also covers a script: the kernel
// cannot run its "#!" line either, so the script starts through its
// interpreter, with the interpreter paths /bin and /usr/bin that Android lacks
// mapped to $PREFIX/bin as termux-exec maps them.
//
// A process the linker started has the linker as /proc/self/exe, so Executable
// replaces os.Executable.
package linkerexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// systemShell is Android's own shell, which the kernel starts directly.
const systemShell = "/system/bin/sh"

// maxInterpreters bounds a chain of scripts naming scripts, as the kernel does.
const maxInterpreters = 5

// headSize is how much of a file decides how it starts: the kernel's
// BINPRM_BUF_SIZE.
const headSize = 256

// Starter rewrites program starts for one Android environment. The zero value
// rewrites nothing.
type Starter struct {
	// Linker is the system linker that maps a PIE executable, such as
	// /system/bin/linker64. It is empty when programs start directly.
	Linker string
	// DataDir is the app data directory. Only a program below it needs the
	// linker; a system program such as /system/bin/sh starts directly.
	DataDir string
	// LegacyDataDir is the same directory under its /data/data/<package> name.
	// Termux may set TERMUX_APP__DATA_DIR to /data/user/0/<package> while
	// $PREFIX and every program path use /data/data/<package>; termux-exec
	// treats a path under either as an app data file, and so does Resolve.
	LegacyDataDir string
	// Prefix is Termux's $PREFIX, the home of the interpreters a script's
	// "#!/usr/bin/env" or "#!/bin/sh" line names.
	Prefix string
	// ReadHead returns the first bytes of the file at path.
	ReadHead func(path string) ([]byte, error)
	// LookPath finds a command in the PATH of the program being started.
	LookPath func(name string) (string, error)
}

// Active reports whether programs below the data directory start through the
// linker.
func (s Starter) Active() bool {
	return s.Linker != "" && s.DataDir != ""
}

// Resolve returns the program and argument vector (argv[0] first) that start
// the program at path with args, which hold argv[0] first. A program that the
// kernel can start directly comes back unchanged.
func (s Starter) Resolve(path string, args []string) (string, []string, error) {
	if !s.Active() {
		return path, args, nil
	}
	for range maxInterpreters + 1 {
		if !below(s.DataDir, path) && (s.LegacyDataDir == "" || !below(s.LegacyDataDir, path)) {
			return path, args, nil
		}
		head, err := s.ReadHead(path)
		if err != nil {
			return "", nil, err
		}
		switch {
		case bytes.HasPrefix(head, []byte("\x7fELF")):
			return s.Linker, append([]string{s.Linker, path}, args[1:]...), nil
		case bytes.HasPrefix(head, []byte("#!")):
			path, args = s.throughInterpreter(head, path, args)
		default:
			// The kernel rejects a file with no magic (ENOEXEC) and execvp
			// then runs it with the shell.
			return systemShell, append([]string{systemShell, path}, args[1:]...), nil
		}
	}
	return "", nil, &os.PathError{Op: "exec", Path: path, Err: errors.New("too many levels of interpreters")}
}

// throughInterpreter returns the program and argv that run the script at path
// with its "#!" line's interpreter.
func (s Starter) throughInterpreter(head []byte, path string, args []string) (string, []string) {
	interpreter, optionalArg := parseShebang(head)
	interpreter = s.mapInterpreter(interpreter)
	argv := []string{interpreter}
	if optionalArg != "" {
		argv = append(argv, optionalArg)
	}
	if filepath.Base(interpreter) == "env" && s.LookPath != nil && isCommandName(optionalArg) {
		// env would exec the command with a libc execve, which only
		// termux-exec's preload can redirect, and a caller's environment may
		// not carry the preload.
		if command, err := s.LookPath(optionalArg); err == nil {
			interpreter, argv = command, []string{command}
		}
	}
	return interpreter, append(argv, append([]string{path}, args[1:]...)...)
}

// parseShebang returns the interpreter and the single optional argument of the
// "#!" line at the start of head, as the kernel splits it: the first word, then
// the rest of the line, trimmed, as one argument.
func parseShebang(head []byte) (interpreter, optionalArg string) {
	line := string(head[2:])
	if end := strings.IndexAny(line, "\n\x00"); end >= 0 {
		line = line[:end]
	}
	line = strings.TrimLeft(line, " \t")
	end := strings.IndexAny(line, " \t")
	if end < 0 {
		return line, ""
	}
	return line[:end], strings.TrimSpace(line[end:])
}

// mapInterpreter maps the directories Android lacks, /bin and /usr/bin, to
// $PREFIX/bin.
func (s Starter) mapInterpreter(interpreter string) string {
	if s.Prefix == "" {
		return interpreter
	}
	for _, dir := range []string{"/usr/bin/", "/bin/"} {
		if name, ok := strings.CutPrefix(interpreter, dir); ok {
			return filepath.Join(s.Prefix, "bin", name)
		}
	}
	return interpreter
}

// isCommandName reports whether arg is the one word "env" runs: not an option,
// an assignment, or a path.
func isCommandName(arg string) bool {
	return arg != "" && !strings.ContainsAny(arg, " \t=/") && !strings.HasPrefix(arg, "-")
}

// below reports whether path is inside dir.
func below(dir, path string) bool {
	return strings.HasPrefix(filepath.Clean(path), strings.TrimSuffix(dir, "/")+"/")
}

// Prepare makes cmd start through the linker when s requires it. It returns
// without a change for an Err already set or a program that starts directly.
func (s Starter) Prepare(cmd *exec.Cmd) {
	if !s.Active() || cmd.Err != nil || len(cmd.Args) == 0 {
		return
	}
	if value, ok := environmentPath(cmd.Env); ok {
		s.LookPath = pathLookup(value)
	}
	path := cmd.Path
	if !filepath.IsAbs(path) {
		dir := cmd.Dir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		path = filepath.Join(dir, path)
	}
	program, args, err := s.Resolve(path, cmd.Args)
	if err != nil {
		cmd.Err = err
		return
	}
	cmd.Path, cmd.Args = program, args
}

// environmentPath returns the PATH of env, the environment a program starts
// with, and false when env has none.
func environmentPath(env []string) (string, bool) {
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "PATH="); ok {
			return value, true
		}
	}
	return "", false
}

// pathLookup returns a search of the directories of the PATH value path for an
// executable regular file.
func pathLookup(path string) func(string) (string, error) {
	return func(name string) (string, error) {
		for dir := range strings.SplitSeq(path, ":") {
			if dir == "" {
				continue
			}
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
				return candidate, nil
			}
		}
		return "", exec.ErrNotFound
	}
}

// readHead reads the first bytes of the file at path.
func readHead(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	head := make([]byte, headSize)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return head[:n], nil
}

// current describes the running process. It is the zero Starter off Android
// and when the process did not start through the linker.
var current = sync.OnceValue(func() Starter {
	if runtime.GOOS != "android" {
		return Starter{}
	}
	return starterFor(os.Getenv, func() (string, error) { return os.Readlink("/proc/self/exe") })
})

// override replaces the running process's Starter in a test.
var override *Starter

// Current returns the Starter for the running process.
func Current() Starter {
	if override != nil {
		return *override
	}
	return current()
}

// SetStarterForTest makes s the running process's Starter until the returned
// function runs. It is not safe for concurrent use.
func SetStarterForTest(s Starter) (restore func()) {
	previous := override
	override = &s
	return func() { override = previous }
}

// Prepare makes cmd start through the linker when the running process needs
// it.
func Prepare(cmd *exec.Cmd) { Current().Prepare(cmd) }

// Command is exec.Command for a command that starts through the linker when
// the running process needs it.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	Prepare(cmd)
	return cmd
}

// CommandContext is exec.CommandContext for a command that starts through the
// linker when the running process needs it.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	Prepare(cmd)
	return cmd
}

// starterFor builds the Starter of a process whose /proc/self/exe link is
// selfExe. The process started through the linker exactly when that link is the
// linker, which is how Termux runs every program of the Google Play build; a
// process that started directly, as under F-Droid's Termux, starts programs the
// same way.
func starterFor(getenv func(string) string, selfExe func() (string, error)) Starter {
	exe, err := selfExe()
	if err != nil || !isLinker(exe) {
		return Starter{}
	}
	prefix := getenv("PREFIX")
	dataDir := getenv("TERMUX_APP__DATA_DIR")
	if dataDir == "" && prefix != "" {
		// $PREFIX is <data dir>/files/usr.
		dataDir = filepath.Dir(filepath.Dir(prefix))
	}
	// termux-exec's legacy data directory: TERMUX_APP__LEGACY_DATA_DIR, else
	// /data/data/<package>, the package being the data directory's last element.
	legacy := getenv("TERMUX_APP__LEGACY_DATA_DIR")
	if legacy == "" && dataDir != "" {
		legacy = filepath.Join("/data/data", filepath.Base(dataDir))
	}
	if legacy == dataDir {
		legacy = ""
	}
	return Starter{
		Linker:        exe,
		LegacyDataDir: legacy,
		DataDir:       dataDir,
		Prefix:        prefix,
		ReadHead:      readHead,
		LookPath:      pathLookup(getenv("PATH")),
	}
}

// isLinker reports whether path is Android's dynamic linker.
func isLinker(path string) bool {
	switch filepath.Base(path) {
	case "linker64", "linker":
		return true
	}
	return false
}

// Executable returns the path of the running program, as os.Executable does.
// A process the linker started has the linker as /proc/self/exe, so the program
// is the one termux-exec records in TERMUX_EXEC__PROC_SELF_EXE, or argv[0] when
// the linker made it absolute.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil || !isLinker(exe) {
		return exe, err
	}
	return programOfLinkedProcess(os.Getenv, os.Args)
}

// programOfLinkedProcess finds the program a linker-started process runs.
func programOfLinkedProcess(getenv func(string) string, args []string) (string, error) {
	if recorded := getenv("TERMUX_EXEC__PROC_SELF_EXE"); filepath.IsAbs(recorded) {
		return recorded, nil
	}
	if len(args) > 0 && filepath.IsAbs(args[0]) {
		return args[0], nil
	}
	return "", errors.New("linkerexec: the running program's path is unknown: started through the linker without an absolute argv[0]")
}
