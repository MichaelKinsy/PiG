//go:build windows

package nodespawn

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SetCommandLine gives cmd the Windows command line Node's spawn builds for
// the same file and arguments: CommandLine(cmd.Args). The line is a snapshot:
// call it after the last assignment to cmd.SysProcAttr and the last change to
// cmd.Args, and before Start.
func SetCommandLine(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = CommandLine(cmd.Args)
}

// HideWindow gives cmd the window flags of Node's spawn with windowsHide:
// true, where stdio is the spawn's stdio option. libuv's uv_spawn
// (src/win/process.c) starts the child with STARTF_USESHOWWINDOW and SW_HIDE.
// When no stdio entry inherits a parent handle it also passes
// CREATE_NO_WINDOW, so a console program gets a console without a window
// instead of sharing PiG's console or opening a new window. Call it after the
// last assignment to cmd.SysProcAttr.
func HideWindow(cmd *exec.Cmd, stdio ...Stdio) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// os/exec sets STARTF_USESHOWWINDOW and SW_HIDE for HideWindow.
	cmd.SysProcAttr.HideWindow = true
	if len(stdio) > 0 && !slices.Contains(stdio, Inherit) {
		cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
	}
}

// SetProgram makes cmd start the program that Node's spawn(cmd.Args[0],
// cmd.Args[1:], { cwd: cmd.Dir, env: cmd.Env }) starts without a shell, as
// LookPath finds it, in the working directory libuv gives it: cmd.Dir, or
// its short form when libuv shortens it. When spawn would throw for the
// arguments or LookPath fails, Start returns the *Error. The lookup is a
// snapshot: call it after the last change to cmd.Args, cmd.Dir, and cmd.Env.
func SetProgram(cmd *exec.Cmd) {
	if len(cmd.Args) == 0 {
		return
	}
	if err := checkArguments(cmd.Args[0], cmd.Args[1:], cmd.Dir, cmd.Env); err != nil {
		cmd.Err = err
		return
	}
	program, dir, err := findProgram(cmd.Args[0], cmd.Dir, cmd.Env)
	if err != nil {
		cmd.Err = err
		return
	}
	cmd.Path, cmd.Dir, cmd.Err = program, dir, nil
}

// SetDirectory gives cmd, whose program is already chosen, the working
// directory libuv gives a child whose spawn has cmd.Dir as its cwd option, as
// spawnDirectory describes. When libuv fails the spawn for that directory,
// Start returns the *Error. SetProgram does this itself; call SetDirectory
// after the last change to cmd.Dir for a spawn whose program is found another
// way, as cross-spawn finds it.
func SetDirectory(cmd *exec.Cmd) {
	if len(cmd.Args) == 0 || cmd.Err != nil {
		return
	}
	dir, err := spawnDirectory(cmd.Args[0], cmd.Dir)
	if err != nil {
		cmd.Err = err
		return
	}
	cmd.Dir = dir
}

// LookPath returns the program that Node's child_process.spawn(file, args)
// starts on Windows without a shell, or the *Error spawn reports. cwd is the
// spawn's cwd option, where "" is PiG's working directory, and env is its env
// option, where nil is PiG's environment.
//
// Node first rejects an empty file and a NUL in file, cwd, or env: spawn
// throws ERR_INVALID_ARG_VALUE. It rejects a batch file name next: spawn
// throws EINVAL. libuv then takes the working directory spawnDirectory
// describes and looks the name up as searchPath describes, in the PATH of env
// or, when env has none, PiG's PATH, and the child emits ENOENT when nothing
// matches. The name never resolves through PATHEXT, so "npm" does not find
// npm.cmd. A bare name is looked up in the working directory first unless the
// NoDefaultCurrentDirectoryInExePath environment variable is set. The result
// is absolute: CreateProcessW resolves a relative candidate against PiG's
// working directory.
func LookPath(file, cwd string, env []string) (string, error) {
	if err := checkArguments(file, nil, cwd, env); err != nil {
		return "", err
	}
	program, _, err := findProgram(file, cwd, env)
	return program, err
}

// findProgram returns LookPath's program and the working directory the child
// starts in.
func findProgram(file, cwd string, env []string) (string, string, error) {
	if isWindowsBatchFile(file) {
		return "", "", batchFileError()
	}
	dir, err := spawnDirectory(file, cwd)
	if err != nil {
		return "", "", err
	}
	search := dir
	if search == "" {
		wd, err := syscall.Getwd()
		if err != nil {
			return "", "", err
		}
		search = wd
	}
	program := searchPath(file, search, pathOf(env), needCurrentDirectoryForExePath(), isFile)
	if program == "" {
		return "", "", notFoundError(file)
	}
	if !filepath.IsAbs(program) {
		if program, err = filepath.Abs(program); err != nil {
			return "", "", err
		}
	}
	return program, dir, nil
}

// maxPath is Windows' MAX_PATH.
const maxPath = 260

// spawnDirectory is the working directory libuv's uv_spawn (src/win/process.c)
// uses for the program search and CreateProcessW when the spawn's cwd option
// is cwd. libuv passes a cwd of MAX_PATH or more UTF-16 units through
// GetShortPathNameW, with the length as the buffer size, and fails the spawn
// when that fails. node.exe does not declare longPathAware
// (src/res/node.exe.extra.manifest, Node 24.19.0), so in Pi's process the call fails for
// such a path unless it has the \\?\ prefix, and spawn emits ENOENT. A
// prefixed path becomes its short form, still prefixed, or stays as it is when
// the short form is no shorter. An empty cwd is PiG's working directory, which
// libuv shortens the same way, but a Node process cannot have a working
// directory of MAX_PATH units, so Pi never does.
func spawnDirectory(file, cwd string) (string, error) {
	length := utf16Length(cwd)
	if length < maxPath {
		return cwd, nil
	}
	if !strings.HasPrefix(cwd, `\\?\`) {
		return "", notFoundError(file)
	}
	long, err := windows.UTF16PtrFromString(cwd)
	if err != nil {
		return "", err
	}
	short := make([]uint16, length)
	n, err := windows.GetShortPathName(long, &short[0], uint32(length))
	switch {
	case n == 0:
		return "", spawnFailure(file, err)
	case int(n) >= length:
		return cwd, nil
	}
	return windows.UTF16ToString(short[:n]), nil
}

// pathOf is the PATH libuv reads for a spawn with env: the child's PATH,
// which os/exec takes from the last entry whose name matches regardless of
// case, or PiG's PATH when env has no PATH.
func pathOf(env []string) string {
	for _, e := range slices.Backward(env) {
		if name, value, ok := strings.Cut(e, "="); ok && strings.EqualFold(name, "PATH") {
			return value
		}
	}
	return os.Getenv("PATH")
}

var procNeedCurrentDirectoryForExePathW = windows.NewLazySystemDLL("kernel32.dll").NewProc("NeedCurrentDirectoryForExePathW")

// needCurrentDirectoryForExePath is libuv's NeedCurrentDirectoryForExePathW(L"")
// call.
func needCurrentDirectoryForExePath() bool {
	var empty uint16
	need, _, _ := procNeedCurrentDirectoryForExePathW.Call(uintptr(unsafe.Pointer(&empty))) //nolint:gosec // G103: x/sys has no wrapper; the API reads the fixed empty UTF-16 string, which stays live for the call, and no CLI-supplied pointer reaches Windows.
	return need != 0
}

// isFile is libuv's candidate test: GetFileAttributesW succeeds and the
// candidate is not a directory.
func isFile(name string) bool {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	attributes, err := windows.GetFileAttributes(path)
	return err == nil && attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0
}
