//go:build !windows

package nodespawn

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// execvpOutcome is what Node's spawnSync reports: the error code of a spawn
// that failed, or the output of the program that ran.
type execvpOutcome struct {
	Code   string `json:"code"`
	Status int    `json:"status"`
	Stdout string `json:"stdout"`
}

func goErrorCode(err error) string {
	if spawnErr, ok := errors.AsType[*Error](err); ok {
		return spawnErr.Code
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "ENOENT"
	}
	return nodeerrno.ErrorCode(err)
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// libuv (src/unix/process.c, libuv 1.52.1) starts a program with execvp, and
// glibc's execvp is the oracle here. It remembers EACCES and goes on to the
// next PATH entry, reports EACCES when nothing else matches (a file without
// execute permission and a directory both fail execve with EACCES), reports
// the errno of the last entry otherwise (ENOTDIR for an entry below a file),
// and runs a file that execve does not recognize (ENOEXEC) with /bin/sh. Node's
// spawnSync with the same arguments is the oracle.
func TestSetProgramStartsWhatExecvpStarts(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("libuv searches the path with posix_spawn on macOS")
	}
	nodeBinary, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	script := `printf '%s|%s' "$0" "$*"`
	plain := filepath.Join(root, "plain")
	writeFile(t, filepath.Join(plain, "noshebang"), script, 0o755)
	writeFile(t, filepath.Join(plain, "empty"), "", 0o755)
	writeFile(t, filepath.Join(plain, "shebang"), "#!/bin/sh\n"+script, 0o755)
	writeFile(t, filepath.Join(plain, "twice"), "#!/bin/sh\n"+script, 0o755)
	denied := filepath.Join(root, "denied")
	writeFile(t, filepath.Join(denied, "noexec"), "#!/bin/sh\n"+script, 0o644)
	writeFile(t, filepath.Join(denied, "noshebang"), script, 0o644)
	writeFile(t, filepath.Join(denied, "twice"), "#!/bin/sh\n"+script, 0o644)
	if err := os.MkdirAll(filepath.Join(root, "dirs", "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dirs", "twice"), 0o755); err != nil {
		t.Fatal(err)
	}
	below := filepath.Join(plain, "shebang", "below")
	nothing := filepath.Join(root, "nothing")
	if err := os.Mkdir(nothing, 0o755); err != nil {
		t.Fatal(err)
	}
	dirs := filepath.Join(root, "dirs")
	// The kernel's script handler makes the interpreter's errno the script's
	// (fs/binfmt_script.c), and execvp goes on after ENOENT.
	missing := filepath.Join(root, "missing-interpreter")
	writeFile(t, filepath.Join(missing, "bad", "twice"), "#!/nonexistent/runner\n", 0o755)
	writeFile(t, filepath.Join(missing, "good", "twice"), "#!/bin/sh\n"+script, 0o755)
	writeFile(t, filepath.Join(missing, "only", "only"), "#!/nonexistent/runner\n", 0o755)
	writeFile(t, filepath.Join(missing, "denied", "denied"), "#!"+filepath.Join(denied, "noexec")+"\n", 0o755)
	writeFile(t, filepath.Join(missing, "shell", "viaplain"), "#!"+filepath.Join(plain, "noshebang")+"\nprintf ran", 0o755)
	writeFile(t, filepath.Join(missing, "spaced", "spaced"), "#! \t/bin/sh -x\n"+script, 0o755)
	writeFile(t, filepath.Join(missing, "noword", "noword"), "#!\n"+script, 0o755)
	writeFile(t, filepath.Join(missing, "relative", "relative"), "#!plain/shebang\n"+script, 0o755)
	// load_script ends the interpreter at a NUL, and it needs a newline before
	// any NUL or a terminator within the 256 bytes it reads.
	writeFile(t, filepath.Join(missing, "nul", "nul"), "#!/bin/sh\x00ignored\n"+script, 0o755)
	writeFile(t, filepath.Join(missing, "nul", "nulfirst"), "#!\x00/bin/sh\n"+script, 0o755)
	writeFile(t, filepath.Join(missing, "long", "long"), "#!/"+strings.Repeat("a", 300)+"\n"+script, 0o755)
	for i := range 8 {
		body := "#!" + filepath.Join(missing, "chain", "c"+strconv.Itoa(i+1)) + "\n"
		if i == 7 {
			body = "#!/bin/sh\n" + script
		}
		writeFile(t, filepath.Join(missing, "chain", "c"+strconv.Itoa(i)), body, 0o755)
	}
	// The kernel reads the head of a file it executes without the read
	// permission, so it runs a script that has none and recognizes no format
	// with /bin/sh, which cannot read it either.
	execOnly := filepath.Join(root, "execonly")
	writeFile(t, filepath.Join(execOnly, "noshebang"), script, 0o111)
	writeFile(t, filepath.Join(execOnly, "shebang"), "#!/bin/sh\n"+script, 0o111)
	writeFile(t, filepath.Join(execOnly, "empty"), "", 0o111)
	writeFile(t, filepath.Join(execOnly, "twice"), script, 0o111)
	writeFile(t, filepath.Join(execOnly, "relative"), "#!plain/shebang\n"+script, 0o111)
	// hop links to nest/deep, so the kernel resolves hop/.. to nest, while a
	// lexically cleaned hop/../bin is root/bin, which does not exist.
	writeFile(t, filepath.Join(root, "nest", "bin", "noshebang"), script, 0o755)
	writeFile(t, filepath.Join(root, "nest", "bin", "shebang"), "#!/bin/sh\n"+script, 0o755)
	if err := os.MkdirAll(filepath.Join(root, "nest", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "nest", "deep"), filepath.Join(root, "hop")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(missing, "hop", "viahop"), "#!hop/../bin/shebang\n"+script, 0o755)
	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fifo, 0o755); err != nil {
		t.Fatal(err)
	}
	fifoDir := filepath.Join(root, "fifodir")
	if err := os.Mkdir(fifoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(fifoDir, "twice"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A kernel that recognizes the magic still rejects a header it cannot run.
	elfDir := filepath.Join(root, "elf")
	writeFile(t, filepath.Join(elfDir, "magic"), "\x7fELF", 0o755)
	writeFile(t, filepath.Join(elfDir, "short"), string(elfHeader(elfTypeExec, 30)), 0o755)
	writeFile(t, filepath.Join(elfDir, "relocatable"), string(elfHeader(1, 64)), 0o755)
	writeFile(t, filepath.Join(elfDir, "wrongmachine"), string(elfHeader(elfTypeDyn, 64, func(h []byte) { binary.NativeEndian.PutUint16(h[18:], 0x7ff) })), 0o755)
	writeFile(t, filepath.Join(elfDir, "nophdr"), string(elfHeader(elfTypeDyn, 64, func(h []byte) { binary.NativeEndian.PutUint16(h[56:], 0) })), 0o755)
	writeFile(t, filepath.Join(elfDir, "badphentsize"), string(elfHeader(elfTypeDyn, 64, func(h []byte) { binary.NativeEndian.PutUint16(h[54:], 10) })), 0o755)
	writeFile(t, filepath.Join(elfDir, "twice"), "\x7fELF", 0o755)
	shell, err := os.ReadFile("/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(elfDir, "realelf"), string(shell), 0o755)
	writeFile(t, filepath.Join(elfDir, "execonly"), string(shell), 0o111)
	writeFile(t, filepath.Join(elfDir, "execonlymagic"), "\x7fELF", 0o111)
	// A missing program interpreter shows that a handler accepted the header
	// and read the program header table; a rejected one runs with /bin/sh.
	layout, native := elfLayout64, elfNativeMachines[runtime.GOARCH]
	if strconv.IntSize == 32 {
		layout = elfLayout32
	}
	perPage := min(os.Getpagesize(), elfMaxProgramHeaderBytes) / layout.phdrSize
	writeFile(t, filepath.Join(elfDir, "interp", "twice"), string(elfWithInterpreter(layout, native, 2, 1, "/nonexistent/ld.so\x00")), 0o755)
	writeFile(t, filepath.Join(elfDir, "interp", "pagefull"), string(elfWithInterpreter(layout, native, 2, perPage, "/nonexistent/ld.so\x00")), 0o755)
	writeFile(t, filepath.Join(elfDir, "interp", "overpage"), string(elfWithInterpreter(layout, native, 2, perPage+1, "/nonexistent/ld.so\x00")), 0o755)
	writeFile(t, filepath.Join(elfDir, "interp", "unterminated"), string(elfWithInterpreter(layout, native, 2, 1, "/nonexistent/ld.so")), 0o755)
	writeFile(t, filepath.Join(elfDir, "interp", "onebyte"), string(elfWithInterpreter(layout, native, 2, 1, "\x00")), 0o755)
	writeFile(t, filepath.Join(elfDir, "interp", "empty"), string(elfWithInterpreter(layout, native, 2, 1, "\x00\x00")), 0o755)
	writeFile(t, filepath.Join(elfDir, "interp", "noclass"), string(elfWithInterpreter(layout, native, 0, 1, "/nonexistent/ld.so\x00")), 0o755)
	writeFile(t, filepath.Join(elfDir, "interp", "compat"), string(elfWithInterpreter(elfLayout32, 3, 2, 1, "/nonexistent/ld.so\x00")), 0o755)
	shortTable := elfWithInterpreter(layout, native, 2, 1, "")
	writeFile(t, filepath.Join(elfDir, "interp", "shorttable"), string(shortTable[:len(shortTable)-1]), 0o755)

	cases := []struct {
		name string
		file string
		args []string
		cwd  string
		path string
	}{
		{"a file without execute permission is EACCES", "noexec", nil, "", denied},
		{"a file without execute permission does not stop the search", "twice", nil, "", denied + ":" + plain},
		{"a directory is EACCES", "adir", nil, "", dirs},
		{"a directory does not stop the search", "twice", nil, "", dirs + ":" + plain},
		{"EACCES wins over a later ENOENT", "noexec", nil, "", denied + ":" + nothing},
		{"EACCES wins over an earlier ENOENT", "noexec", nil, "", nothing + ":" + denied},
		{"an entry below a file is ENOTDIR", "shebang", nil, "", below},
		{"ENOTDIR after ENOENT is ENOTDIR", "shebang", nil, "", nothing + ":" + below},
		{"ENOENT after ENOTDIR is ENOENT", "missing", nil, "", below + ":" + nothing},
		{"nothing matches is ENOENT", "missing", nil, "", nothing},
		{"a script without #! runs with /bin/sh", "noshebang", []string{"a", "b c"}, "", plain},
		{"an empty file runs with /bin/sh", "empty", nil, "", plain},
		{"a script with #! runs", "shebang", []string{"a"}, "", plain},
		{"a script without #! and execute permission is EACCES", "noshebang", nil, "", denied},
		{"a script without #! is found in the working directory", "noshebang", []string{"x"}, plain, ""},
		{"a script without #! is found by a relative entry", "noshebang", nil, root, "plain"},
		{"a script without #! is run by an absolute name", filepath.Join(plain, "noshebang"), []string{"a"}, "", plain},
		{"an absolute name without execute permission is EACCES", filepath.Join(denied, "noshebang"), nil, "", plain},
		{"a script with a missing interpreter is ENOENT", "only", nil, "", filepath.Join(missing, "only")},
		{"a script with a missing interpreter does not stop the search", "twice", nil, "", filepath.Join(missing, "bad") + ":" + filepath.Join(missing, "good")},
		{"a script with a missing interpreter by an absolute name is ENOENT", filepath.Join(missing, "only", "only"), nil, "", plain},
		{"a script whose interpreter is not executable is EACCES", "denied", nil, "", filepath.Join(missing, "denied")},
		{"a script whose interpreter has no format runs with /bin/sh", "viaplain", []string{"a"}, "", filepath.Join(missing, "shell")},
		{"an interpreter is the first word after blanks", "spaced", []string{"a"}, "", filepath.Join(missing, "spaced")},
		{"a script without an interpreter runs with /bin/sh", "noword", []string{"a"}, "", filepath.Join(missing, "noword")},
		{"an interpreter ends at a NUL", "nul", []string{"a"}, "", filepath.Join(missing, "nul")},
		{"an interpreter line that starts with a NUL names the working directory", "nulfirst", nil, root, filepath.Join(missing, "nul")},
		{"an interpreter longer than the kernel's buffer runs with /bin/sh", "long", []string{"a"}, "", filepath.Join(missing, "long")},
		{"a relative interpreter is relative to the working directory", "relative", []string{"a"}, plain + "/..", filepath.Join(missing, "relative")},
		{"a script without #! is run with /bin/sh through a relative entry's .. after a symlink", "noshebang", []string{"a"}, root, "hop/../bin"},
		{"a relative interpreter's .. after a symlink is resolved by the kernel", "viahop", []string{"a"}, root, filepath.Join(missing, "hop")},
		{"five interpreters are followed", "c3", nil, "", filepath.Join(missing, "chain")},
		{"six interpreters are ELOOP", "c2", nil, "", filepath.Join(missing, "chain")},
		{"an unreadable script without #! runs with /bin/sh", "noshebang", []string{"a"}, "", execOnly},
		{"an unreadable empty file runs with /bin/sh", "empty", nil, "", execOnly},
		{"an unreadable script with #! is run by its interpreter", "shebang", []string{"a"}, "", execOnly},
		{"an unreadable script without #! does not stop the search", "twice", nil, "", execOnly + ":" + plain},
		{"an unreadable script without #! by an absolute name", filepath.Join(execOnly, "noshebang"), []string{"a"}, "", plain},
		{"an unreadable script's relative interpreter is relative to the working directory", "relative", []string{"a"}, root, execOnly},
		{"a FIFO by an absolute name is EACCES", fifo, nil, "", plain},
		{"a FIFO by a name in the working directory is EACCES", "./fifo", nil, root, plain},
		{"a FIFO in the path does not stop the search", "twice", nil, "", fifoDir + ":" + plain},
		{"a FIFO in the path is EACCES", "twice", nil, "", fifoDir},
	}
	if runtime.GOOS == "linux" {
		elfPath := elfDir
		// sh runs a rejected header as a script, whose bytes redirect output to
		// files in its working directory.
		scratch := t.TempDir()
		cases = append(cases, []struct {
			name string
			file string
			args []string
			cwd  string
			path string
		}{
			{"a file with only the ELF magic runs with /bin/sh", "magic", nil, scratch, elfPath},
			{"a short ELF header runs with /bin/sh", "short", nil, scratch, elfPath},
			{"a relocatable ELF header runs with /bin/sh", "relocatable", nil, scratch, elfPath},
			{"an ELF header for another machine runs with /bin/sh", "wrongmachine", nil, scratch, elfPath},
			{"an ELF header without program headers runs with /bin/sh", "nophdr", nil, scratch, elfPath},
			{"an ELF header with a wrong program header size runs with /bin/sh", "badphentsize", nil, scratch, elfPath},
			{"an ELF file with only the magic does not stop the search", "twice", nil, scratch, elfPath + ":" + plain},
			{"an unreadable ELF executable runs", "execonly", []string{"-c", "printf ran"}, "", elfPath},
			{"an unreadable file with only the ELF magic runs with /bin/sh", "execonlymagic", nil, scratch, elfPath},
			{"an ELF executable runs", "realelf", []string{"-c", "printf ran"}, "", elfPath},
			{"a missing ELF interpreter is ENOENT", "twice", nil, scratch, filepath.Join(elfPath, "interp")},
			{"a missing ELF interpreter does not stop the search", "twice", nil, scratch, filepath.Join(elfPath, "interp") + ":" + plain},
			{"a program header table of a page is read", "pagefull", nil, scratch, filepath.Join(elfPath, "interp")},
			{"a program header table over a page is read as the kernel reads it", "overpage", nil, scratch, filepath.Join(elfPath, "interp")},
			{"a program header table past the end runs with /bin/sh", "shorttable", nil, scratch, filepath.Join(elfPath, "interp")},
			{"an ELF interpreter without a NUL runs with /bin/sh", "unterminated", nil, scratch, filepath.Join(elfPath, "interp")},
			{"an ELF interpreter of one byte runs with /bin/sh", "onebyte", nil, scratch, filepath.Join(elfPath, "interp")},
			{"an empty ELF interpreter names the working directory", "empty", nil, scratch, filepath.Join(elfPath, "interp")},
		}...)
		if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
			// elf_check_arch does not read EI_CLASS on these machines.
			cases = append(cases, struct {
				name string
				file string
				args []string
				cwd  string
				path string
			}{"an ELF header is read whatever its class", "noclass", nil, scratch, filepath.Join(elfPath, "interp")})
		}
		if runtime.GOARCH == "amd64" {
			cases = append(cases, struct {
				name string
				file string
				args []string
				cwd  string
				path string
			}{"a 32-bit ELF header goes to the IA32 handler", "compat", nil, scratch, filepath.Join(elfPath, "interp")})
		}
	}
	type input struct {
		File string            `json:"file"`
		Args []string          `json:"args"`
		Cwd  string            `json:"cwd"`
		Env  map[string]string `json:"env"`
	}
	var inputs []input
	for _, c := range cases {
		inputs = append(inputs, input{c.file, c.args, c.cwd, map[string]string{"PATH": c.path}})
	}
	data, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), nodeBinary, "-e", `
const { spawnSync } = require("node:child_process");
const cases = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
process.stdout.write(JSON.stringify(cases.map(({ file, args, cwd, env }) => {
  const result = spawnSync(file, args ?? [], { cwd: cwd || undefined, env, encoding: "utf8" });
  if (result.error) return { code: result.error.code };
  return { status: result.status, stdout: result.stdout };
})));
`)
	node.Stdin = bytes.NewReader(data)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawn: %v; output %s", err, out)
	}
	var want []execvpOutcome
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(c.file, c.args...)
			cmd.Dir = c.cwd
			SetEnv(cmd, []string{"PATH=" + c.path})
			SetProgram(cmd)
			output, err := cmd.Output()
			if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && want[i].Code == "" {
				if exitErr.ExitCode() != want[i].Status {
					t.Errorf("exit status %d; Node's is %d", exitErr.ExitCode(), want[i].Status)
				}
				err = nil
			} else if err == nil && want[i].Status != 0 {
				t.Errorf("exit status 0; Node's is %d", want[i].Status)
			}
			if want[i].Code != "" {
				if got := goErrorCode(err); got != want[i].Code {
					t.Fatalf("Start error = %v (code %q, output %q); Node emits %s", err, got, output, want[i].Code)
				}
				return
			}
			if err != nil {
				t.Fatalf("%v; Node runs it with status %d and output %q", err, want[i].Status, want[i].Stdout)
			}
			if string(output) != want[i].Stdout {
				t.Errorf("output %q; Node's is %q", output, want[i].Stdout)
			}
		})
	}
}

// A nil cmd.Env is PiG's environment, and libuv's execvp searches its PATH the
// same way: exec.Command's own lookup reports ENOENT for a name that only
// matches without execute permission.
func TestSetProgramSearchesPiGPathAsExecvpDoes(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("libuv searches the path with posix_spawn on macOS")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "denied", "pig-denied-command"), "#!/bin/sh\n", 0o644)
	writeFile(t, filepath.Join(root, "plain", "pig-plain-command"), "printf ran", 0o755)
	t.Setenv("PATH", filepath.Join(root, "denied"))
	denied := exec.Command("pig-denied-command")
	SetProgram(denied)
	if _, err := denied.Output(); goErrorCode(err) != "EACCES" {
		t.Errorf("a file without execute permission: error = %v; Node emits EACCES", err)
	}
	t.Setenv("PATH", filepath.Join(root, "plain"))
	plain := exec.Command("pig-plain-command")
	SetProgram(plain)
	if output, err := plain.Output(); err != nil || string(output) != "ran" {
		t.Errorf("a script without #!: %q, %v; Node runs it with /bin/sh", output, err)
	}
}

// elfHeader is an ELF header of the running machine's class, cut to size bytes,
// after edit changes it. It has one program header of the kernel's size.
func elfHeader(typ uint16, size int, edit ...func([]byte)) []byte {
	header := make([]byte, 64)
	copy(header, "\x7fELF")
	header[4], header[5], header[6] = 2, 1, 1
	machines := map[string]uint16{"amd64": 62, "arm64": 183, "riscv64": 243, "ppc64le": 21, "s390x": 22, "loong64": 258}
	binary.NativeEndian.PutUint16(header[16:], typ)
	binary.NativeEndian.PutUint16(header[18:], machines[runtime.GOARCH])
	binary.NativeEndian.PutUint16(header[54:], 56)
	binary.NativeEndian.PutUint16(header[56:], 1)
	for _, f := range edit {
		f(header)
	}
	return header[:size]
}

// elfNativeMachines are the e_machine values of each GOARCH's native ELF
// handler.
var elfNativeMachines = map[string]uint16{"amd64": 62, "386": 3, "arm64": 183, "arm": 40, "riscv64": 243, "ppc64le": 21, "ppc64": 21, "s390x": 22, "loong64": 258, "mips64le": 8}

// elfWithInterpreter is an ELF executable in layout for machine with EI_CLASS
// class and phnum program headers, the first a PT_INTERP whose contents are
// interpreter, which follows the table.
func elfWithInterpreter(layout elfLayout, machine uint16, class byte, phnum int, interpreter string) []byte {
	headerSize := 52
	if layout.word == 8 {
		headerSize = 64
	}
	file := make([]byte, headerSize+phnum*layout.phdrSize)
	copy(file, "\x7fELF")
	file[4], file[5], file[6] = class, 1, 1
	order := binary.NativeEndian
	put := func(b []byte, v uint64) {
		if layout.word == 4 {
			order.PutUint32(b, uint32(v))
		} else {
			order.PutUint64(b, v)
		}
	}
	order.PutUint16(file[16:], elfTypeDyn)
	order.PutUint16(file[18:], machine)
	put(file[layout.phoff:], uint64(headerSize))
	order.PutUint16(file[layout.phentsize:], uint16(layout.phdrSize))
	order.PutUint16(file[layout.phnum:], uint16(phnum))
	phdr := file[headerSize:]
	order.PutUint32(phdr, elfProgramInterpreter)
	put(phdr[layout.pOffset:], uint64(len(file)))
	put(phdr[layout.pFilesz:], uint64(len(interpreter)))
	return append(file, interpreter...)
}

// execve fails with EACCES for a FIFO, and Node's spawnSync emits it; a program
// path must not be read before it starts, because opening a FIFO blocks until
// a writer arrives.
func TestSetProgramDoesNotBlockOnAFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(fifo, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(fifo)
	done := make(chan struct{})
	go func() {
		defer close(done)
		SetProgram(cmd)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		// Release the reader so the test's goroutine ends.
		if writer, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = writer.Close()
		}
		<-done
		t.Fatal("SetProgram blocked on a FIFO")
	}
	if err := cmd.Run(); goErrorCode(err) != "EACCES" {
		t.Errorf("Run error = %v; Node emits EACCES", err)
	}
}
