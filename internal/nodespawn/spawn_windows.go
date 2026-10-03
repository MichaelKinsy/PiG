//go:build windows

package nodespawn

import (
	"bytes"
	"errors"
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

// start is cmd.Start, or startProcess when os/exec would drop an entry of
// cmd.Env. Either way it first gives cmd.Env the order os/exec writes the
// block in (createEnvBlockOrder).
func start(cmd *exec.Cmd) error {
	cmd.Env = createEnvBlockOrder(cmd.Env)
	if !sharesEnvKey(cmd.Env) {
		return cmd.Start()
	}
	return startProcess(cmd)
}

// createEnvBlockOrder is env in the order in which os/exec writes it into the
// child's block, where the order of entries that os/exec compares equal is
// their order in env. syscall.createEnvBlock (envSorted) compares the text
// before each entry's first "=", with ASCII letters uppercased, byte by byte,
// and sorts a block that is not in that order with an unstable sort, which
// would shuffle the entries of libuv's block that share a name, such as
// "A=one" and "A=B=two", whenever a name outside ASCII puts the block out of
// os/exec's order. A block that is in os/exec's order goes to the child as it
// is.
func createEnvBlockOrder(env []string) []string {
	key := func(entry string) []byte {
		before, _, ok := strings.Cut(entry, "=")
		if !ok {
			return nil
		}
		name := []byte(before)
		for j, c := range name {
			if 'a' <= c && c <= 'z' {
				name[j] = c - ('a' - 'A')
			}
		}
		return name
	}
	compare := func(a, b string) int { return bytes.Compare(key(a), key(b)) }
	if slices.IsSortedFunc(env, compare) {
		return env
	}
	env = slices.Clone(env)
	slices.SortStableFunc(env, compare)
	return env
}

// startProcess starts cmd as cmd.Start does, with os.StartProcess, but with
// cmd.Env as the environment block: os/exec's Cmd.environ would drop entries
// from it. It starts cmd.Path, the program libuv finds (SetProgram), without
// the PATHEXT lookup cmd.Start adds (lookExtensions). A nil Stdin, Stdout, or
// Stderr is the NUL device, which it opens for the start as cmd.Start does.
// cmd.Wait then waits for the child, since cmd has no copying goroutine and no
// context to watch (Start).
func startProcess(cmd *exec.Cmd) error {
	if cmd.Process != nil {
		return errors.New("exec: already started")
	}
	if cmd.Path == "" && cmd.Err == nil {
		cmd.Err = errors.New("exec: no command")
	}
	if cmd.Err != nil {
		return cmd.Err
	}
	var devNull []*os.File
	defer func() {
		for _, file := range devNull {
			_ = file.Close()
		}
	}()
	files := make([]*os.File, 0, 3+len(cmd.ExtraFiles))
	for i, stdio := range childStdio(cmd) {
		file, _ := stdio.(*os.File)
		if file == nil {
			flag := os.O_WRONLY
			if i == 0 {
				flag = os.O_RDONLY
			}
			var err error
			if file, err = os.OpenFile(os.DevNull, flag, 0); err != nil {
				return err
			}
			devNull = append(devNull, file)
		}
		files = append(files, file)
	}
	files = append(files, cmd.ExtraFiles...)
	argv := cmd.Args
	if len(argv) == 0 {
		argv = []string{cmd.Path}
	}
	process, err := os.StartProcess(cmd.Path, argv, &os.ProcAttr{Dir: cmd.Dir, Env: cmd.Env, Files: files, Sys: cmd.SysProcAttr})
	if err != nil {
		return err
	}
	cmd.Process = process
	return nil
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

// pathOf is the PATH libuv reads for a spawn with env, the block that
// SetEnvProperties builds: libuv's find_path takes the value of the first
// entry that starts with "PATH=" regardless of case, which is also the
// child's PATH, and the entry of a name such as "PATH=Z" starts so. When env
// has no such entry, libuv reads PiG's PATH.
func pathOf(env []string) string {
	for _, e := range env {
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
