package nodespawn

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// binfmtMiscDir is where the kernel's binfmt_misc file system is mounted. PiG reads the instance mounted there; since Linux 6.7 the kernel uses the instance of the nearest user namespace that has one, which differs only where a container or a private mount replaces it.
var binfmtMiscDir = "/proc/sys/fs/binfmt_misc"

// binfmtMiscHandler is an enabled binfmt_misc entry as /proc/sys/fs/binfmt_misc/<name> prints it (entry_status, fs/binfmt_misc.c): an extension handler, or a handler that matches magic, under mask, at offset in the head of the file.
type binfmtMiscHandler struct {
	interpreter string
	// fixBinary is flag F: the kernel opened the interpreter when the handler was registered, so execve does not look it up.
	fixBinary bool
	// openBinary is flag O, which entry_status also prints for flag C: the kernel passes the file to the interpreter as an open descriptor, and exec_binprm (fs/exec.c) then lets no later handler set an interpreter.
	openBinary bool
	extension  string
	offset     int
	magic      []byte
	mask       []byte
}

// binfmtMiscHandlers returns the enabled handlers in the order the kernel tries them (search_binfmt_handler, check_file before Linux 6.7): the directory's own order, newest entry first, as binfmt_misc lists its entries. It returns none when binfmt_misc is not mounted or its status is disabled.
func binfmtMiscHandlers() []binfmtMiscHandler {
	status, err := os.ReadFile(filepath.Join(binfmtMiscDir, "status"))
	if err != nil || strings.TrimSpace(string(status)) != "enabled" {
		return nil
	}
	dir, err := os.Open(binfmtMiscDir)
	if err != nil {
		return nil
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil
	}
	var handlers []binfmtMiscHandler
	for _, entry := range entries {
		if name := entry.Name(); name == "status" || name == "register" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(binfmtMiscDir, entry.Name()))
		if err != nil {
			continue
		}
		if handler, ok := parseBinfmtMiscEntry(data); ok {
			handlers = append(handlers, handler)
		}
	}
	return handlers
}

// parseBinfmtMiscEntry reads an entry that entry_status printed, and reports false for a disabled or malformed one.
func parseBinfmtMiscEntry(data []byte) (binfmtMiscHandler, bool) {
	var handler binfmtMiscHandler
	lines := slices.Collect(strings.Lines(string(data)))
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != "enabled" {
		return handler, false
	}
	hasMagic, hasExtension := false, false
	for _, line := range lines[1:] {
		key, value, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
		switch key {
		case "interpreter":
			handler.interpreter = value
		case "flags:":
			handler.fixBinary = strings.ContainsRune(value, 'F')
			handler.openBinary = strings.ContainsRune(value, 'O')
		case "extension":
			handler.extension, hasExtension = strings.TrimPrefix(value, "."), true
		case "offset":
			offset, err := strconv.Atoi(value)
			if err != nil || offset < 0 {
				return handler, false
			}
			handler.offset = offset
		case "magic":
			magic, err := hex.DecodeString(value)
			if err != nil {
				return handler, false
			}
			handler.magic, hasMagic = magic, true
		case "mask":
			mask, err := hex.DecodeString(value)
			if err != nil {
				return handler, false
			}
			handler.mask = mask
		}
	}
	switch {
	case handler.interpreter == "" || hasMagic == hasExtension:
		return handler, false
	case hasMagic && (len(handler.magic) == 0 || handler.offset+len(handler.magic) > binprmBufSize):
		return handler, false
	case handler.mask != nil && len(handler.mask) != len(handler.magic):
		return handler, false
	}
	return handler, true
}

// matches is search_binfmt_handler (fs/binfmt_misc.c) for one handler. An extension handler compares the text after the last "." of path, the file's name as the kernel records it (bprm->interp): the path that execve was given, or an interpreter's path one level deeper. A magic handler compares the file's head, which the kernel reads into a zeroed buffer of BINPRM_BUF_SIZE bytes, at offset under mask.
func (h binfmtMiscHandler) matches(path string, head []byte) bool {
	if h.magic == nil {
		dot := strings.LastIndexByte(path, '.')
		return dot >= 0 && path[dot+1:] == h.extension
	}
	buf := make([]byte, binprmBufSize)
	copy(buf, head)
	for i, want := range h.magic {
		got := buf[h.offset+i]
		if h.mask != nil {
			got, want = got&h.mask[i], want&h.mask[i]
		}
		if got != want {
			return false
		}
	}
	return true
}

// miscErrno is the errno of the kernel's binfmt_misc handler (load_misc_binary) for path at depth, whose head is head, which execve tries before the "#!" and ELF handlers: the errno of the interpreter of the first enabled handler that recognizes the file, as interpreterErrno describes, where execfd reports an earlier handler with flag O. Without flag F the kernel opens the interpreter in the child's working directory cwd, as a "#!" interpreter. With flag F it runs the file it opened at registration, so an interpreter that is not an executable file at its path when PiG looks is assumed to start. It reports false when no handler recognizes the file, and the "#!" and ELF handlers decide.
func miscErrno(path, cwd string, head []byte, depth int, execfd bool) (syscall.Errno, bool) {
	for _, handler := range binfmtMiscHandlers() {
		if !handler.matches(path, head) {
			continue
		}
		nextExecfd := execfd || handler.openBinary
		if !handler.fixBinary {
			return interpreterErrno(inDirectory(cwd, startName(handler.interpreter)), cwd, depth, execfd, nextExecfd), true
		}
		switch {
		case execfd:
			return syscall.ENOEXEC, true
		case depth+1 > maxInterpreterDepth:
			return syscall.ELOOP, true
		case executableErrno(handler.interpreter) != 0:
			return 0, true
		}
		return execveLevelErrno(handler.interpreter, cwd, depth+1, nextExecfd), true
	}
	return 0, false
}
