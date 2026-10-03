package nodeerrno

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// TestWindowsCodeMatchesLibuv pins libuv's uv_translate_sys_error
// (src/win/error.c in libuv 1.52.1, bundled by Node 24.19.0): every Windows
// system and Winsock error it names, in its order. The errno values are
// winerror.h and winsock2.h numbers, so the table is checked on every
// platform.
func TestWindowsCodeMatchesLibuv(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		errno syscall.Errno
		want  string
	}{
		{"WSAEACCES", 10013, "EACCES"},
		{"ERROR_ELEVATION_REQUIRED", 740, "EACCES"},
		{"ERROR_CANT_ACCESS_FILE", 1920, "EACCES"},
		{"ERROR_ADDRESS_ALREADY_ASSOCIATED", 1227, "EADDRINUSE"},
		{"WSAEADDRINUSE", 10048, "EADDRINUSE"},
		{"WSAEADDRNOTAVAIL", 10049, "EADDRNOTAVAIL"},
		{"WSAEAFNOSUPPORT", 10047, "EAFNOSUPPORT"},
		{"WSAEWOULDBLOCK", 10035, "EAGAIN"},
		{"ERROR_NO_DATA", 232, "EAGAIN"},
		{"WSAEALREADY", 10037, "EALREADY"},
		{"ERROR_INVALID_FLAGS", 1004, "EBADF"},
		{"ERROR_INVALID_HANDLE", 6, "EBADF"},
		{"ERROR_LOCK_VIOLATION", 33, "EBUSY"},
		{"ERROR_PIPE_BUSY", 231, "EBUSY"},
		{"ERROR_SHARING_VIOLATION", 32, "EBUSY"},
		{"ERROR_OPERATION_ABORTED", 995, "ECANCELED"},
		{"WSAEINTR", 10004, "ECANCELED"},
		{"ERROR_NO_UNICODE_TRANSLATION", 1113, "ECHARSET"},
		{"ERROR_CONNECTION_ABORTED", 1236, "ECONNABORTED"},
		{"WSAECONNABORTED", 10053, "ECONNABORTED"},
		{"ERROR_CONNECTION_REFUSED", 1225, "ECONNREFUSED"},
		{"WSAECONNREFUSED", 10061, "ECONNREFUSED"},
		{"ERROR_NETNAME_DELETED", 64, "ECONNRESET"},
		{"WSAECONNRESET", 10054, "ECONNRESET"},
		{"ERROR_ALREADY_EXISTS", 183, "EEXIST"},
		{"ERROR_FILE_EXISTS", 80, "EEXIST"},
		{"ERROR_NOACCESS", 998, "EFAULT"},
		{"WSAEFAULT", 10014, "EFAULT"},
		{"ERROR_HOST_UNREACHABLE", 1232, "EHOSTUNREACH"},
		{"WSAEHOSTUNREACH", 10065, "EHOSTUNREACH"},
		{"ERROR_INSUFFICIENT_BUFFER", 122, "EINVAL"},
		{"ERROR_INVALID_DATA", 13, "EINVAL"},
		{"ERROR_INVALID_PARAMETER", 87, "EINVAL"},
		{"ERROR_SYMLINK_NOT_SUPPORTED", 1464, "EINVAL"},
		{"WSAEINVAL", 10022, "EINVAL"},
		{"WSAEPFNOSUPPORT", 10046, "EINVAL"},
		{"ERROR_BEGINNING_OF_MEDIA", 1102, "EIO"},
		{"ERROR_BUS_RESET", 1111, "EIO"},
		{"ERROR_CRC", 23, "EIO"},
		{"ERROR_DEVICE_DOOR_OPEN", 1166, "EIO"},
		{"ERROR_DEVICE_REQUIRES_CLEANING", 1165, "EIO"},
		{"ERROR_DISK_CORRUPT", 1393, "EIO"},
		{"ERROR_EOM_OVERFLOW", 1129, "EIO"},
		{"ERROR_FILEMARK_DETECTED", 1101, "EIO"},
		{"ERROR_GEN_FAILURE", 31, "EIO"},
		{"ERROR_INVALID_BLOCK_LENGTH", 1106, "EIO"},
		{"ERROR_IO_DEVICE", 1117, "EIO"},
		{"ERROR_NO_DATA_DETECTED", 1104, "EIO"},
		{"ERROR_NO_SIGNAL_SENT", 205, "EIO"},
		{"ERROR_OPEN_FAILED", 110, "EIO"},
		{"ERROR_SETMARK_DETECTED", 1103, "EIO"},
		{"ERROR_SIGNAL_REFUSED", 156, "EIO"},
		{"WSAEISCONN", 10056, "EISCONN"},
		{"ERROR_CANT_RESOLVE_FILENAME", 1921, "ELOOP"},
		{"ERROR_TOO_MANY_OPEN_FILES", 4, "EMFILE"},
		{"WSAEMFILE", 10024, "EMFILE"},
		{"WSAEMSGSIZE", 10040, "EMSGSIZE"},
		{"ERROR_BUFFER_OVERFLOW", 111, "ENAMETOOLONG"},
		{"ERROR_FILENAME_EXCED_RANGE", 206, "ENAMETOOLONG"},
		{"ERROR_NETWORK_UNREACHABLE", 1231, "ENETUNREACH"},
		{"WSAENETUNREACH", 10051, "ENETUNREACH"},
		{"WSAENOBUFS", 10055, "ENOBUFS"},
		{"ERROR_BAD_PATHNAME", 161, "ENOENT"},
		{"ERROR_DIRECTORY", 267, "ENOENT"},
		{"ERROR_ENVVAR_NOT_FOUND", 203, "ENOENT"},
		{"ERROR_FILE_NOT_FOUND", 2, "ENOENT"},
		{"ERROR_INVALID_NAME", 123, "ENOENT"},
		{"ERROR_INVALID_DRIVE", 15, "ENOENT"},
		{"ERROR_INVALID_REPARSE_DATA", 4392, "ENOENT"},
		{"ERROR_MOD_NOT_FOUND", 126, "ENOENT"},
		{"ERROR_PATH_NOT_FOUND", 3, "ENOENT"},
		{"WSAHOST_NOT_FOUND", 11001, "ENOENT"},
		{"WSANO_DATA", 11004, "ENOENT"},
		{"ERROR_NOT_ENOUGH_MEMORY", 8, "ENOMEM"},
		{"ERROR_OUTOFMEMORY", 14, "ENOMEM"},
		{"ERROR_CANNOT_MAKE", 82, "ENOSPC"},
		{"ERROR_DISK_FULL", 112, "ENOSPC"},
		{"ERROR_EA_TABLE_FULL", 277, "ENOSPC"},
		{"ERROR_END_OF_MEDIA", 1100, "ENOSPC"},
		{"ERROR_HANDLE_DISK_FULL", 39, "ENOSPC"},
		{"ERROR_NOT_CONNECTED", 2250, "ENOTCONN"},
		{"WSAENOTCONN", 10057, "ENOTCONN"},
		{"ERROR_DIR_NOT_EMPTY", 145, "ENOTEMPTY"},
		{"WSAENOTSOCK", 10038, "ENOTSOCK"},
		{"ERROR_NOT_SUPPORTED", 50, "ENOTSUP"},
		{"ERROR_BROKEN_PIPE", 109, "EOF"},
		{"ERROR_ACCESS_DENIED", 5, "EPERM"},
		{"ERROR_PRIVILEGE_NOT_HELD", 1314, "EPERM"},
		{"ERROR_BAD_PIPE", 230, "EPIPE"},
		{"ERROR_PIPE_NOT_CONNECTED", 233, "EPIPE"},
		{"WSAESHUTDOWN", 10058, "EPIPE"},
		{"WSAEPROTONOSUPPORT", 10043, "EPROTONOSUPPORT"},
		{"ERROR_WRITE_PROTECT", 19, "EROFS"},
		{"ERROR_SEM_TIMEOUT", 121, "ETIMEDOUT"},
		{"WSAETIMEDOUT", 10060, "ETIMEDOUT"},
		{"ERROR_NOT_SAME_DEVICE", 17, "EXDEV"},
		{"ERROR_INVALID_FUNCTION", 1, "EISDIR"},
		{"ERROR_META_EXPANSION_TOO_LONG", 208, "E2BIG"},
		{"WSAESOCKTNOSUPPORT", 10044, "ESOCKTNOSUPPORT"},
		{"ERROR_BAD_EXE_FORMAT", 193, "EFTYPE"},
	}
	if len(cases) != len(windowsCodes) {
		t.Fatalf("windowsCodes has %d entries, the libuv table here has %d", len(windowsCodes), len(cases))
	}
	for _, tc := range cases {
		got, ok := windowsCode(tc.errno)
		if !ok || got != tc.want {
			t.Errorf("windowsCode(%s=%d) = %q, %v; want %q", tc.name, int(tc.errno), got, ok, tc.want)
		}
		if _, ok := Description(tc.want); !ok {
			t.Errorf("%s: code %s has no uv_strerror description", tc.name, tc.want)
		}
	}
	// ERROR_NOT_READY (21), ERROR_BAD_NET_NAME (67) and ERROR_COMMITMENT_LIMIT
	// (1455) are libuv UNKNOWN. Code names none of them; the caller decides what
	// to report.
	for _, errno := range []syscall.Errno{0, 21, 67, 1455} {
		if got, ok := windowsCode(errno); ok {
			t.Errorf("windowsCode(%d) = %q, want no code", int(errno), got)
		}
	}
}

// TestErrorCodeUnwrapsWindowsErrno checks that a Windows system error wrapped
// the way os reports it is named by its libuv code, not left as Go text.
func TestErrorCodeUnwrapsWindowsErrno(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want string
	}{
		{&fs.PathError{Op: "open", Path: `C:\locked.txt`, Err: syscall.Errno(5)}, "EPERM"},
		{&fs.PathError{Op: "open", Path: `C:\missing\file.txt`, Err: syscall.Errno(3)}, "ENOENT"},
		{fmt.Errorf("wrapped: %w", &fs.PathError{Op: "open", Path: `C:\gone.txt`, Err: syscall.Errno(2)}), "ENOENT"},
		{&fs.PathError{Op: "read", Path: `C:\dir`, Err: syscall.Errno(1)}, "EISDIR"},
		{&fs.PathError{Op: "read", Path: `C:\x`, Err: syscall.Errno(6)}, "EBADF"},
		{&fs.PathError{Op: "open", Path: `C:\x`, Err: syscall.Errno(21)}, ""},
		{fs.ErrNotExist, ""},
	}
	for _, tc := range cases {
		if got := errorCode(tc.err, windowsCode); got != tc.want {
			t.Errorf("errorCode(%v, windows) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// TestCodeNamesPlatformErrnos checks that Code names the running platform's
// errnos for every libuv code, including the ones file-system calls meet
// beyond the common few, and that every errno it names is a libuv code.
func TestCodeNamesPlatformErrnos(t *testing.T) {
	t.Parallel()
	cases := map[syscall.Errno]string{
		syscall.ENOENT:       "ENOENT",
		syscall.EACCES:       "EACCES",
		syscall.EPERM:        "EPERM",
		syscall.EISDIR:       "EISDIR",
		syscall.EEXIST:       "EEXIST",
		syscall.EFAULT:       "EFAULT",
		syscall.ELOOP:        "ELOOP",
		syscall.ENAMETOOLONG: "ENAMETOOLONG",
		syscall.EMLINK:       "EMLINK",
		syscall.ENOMEM:       "ENOMEM",
		syscall.EINTR:        "EINTR",
		syscall.EOVERFLOW:    "EOVERFLOW",
		syscall.EAGAIN:       "EAGAIN",
		syscall.ENFILE:       "ENFILE",
		syscall.ETXTBSY:      "ETXTBSY",
		syscall.EFBIG:        "EFBIG",
		syscall.ENODEV:       "ENODEV",
		syscall.ENXIO:        "ENXIO",
		syscall.ENOTSUP:      "ENOTSUP",
		syscall.ETIMEDOUT:    "ETIMEDOUT",
	}
	// On Windows syscall.ENOTDIR is ERROR_PATH_NOT_FOUND, which libuv names
	// ENOENT.
	if runtime.GOOS == "windows" {
		cases[syscall.ENOTDIR] = "ENOENT"
	} else {
		cases[syscall.ENOTDIR] = "ENOTDIR"
	}
	for errno, want := range cases {
		if got, ok := Code(errno); !ok || got != want {
			t.Errorf("Code(%v) = %q, %v; want %q", errno, got, ok, want)
		}
	}
	for errno, code := range posixCodes {
		if _, ok := codes[code]; !ok {
			t.Errorf("posix errno %d is named %s, which libuv does not name", int(errno), code)
		}
	}
}

// TestErrorCodeFromFileSystem checks the codes the running platform reports
// for a missing file, a missing parent directory, and a regular file used as
// a directory, as Node reports them there.
func TestErrorCodeFromFileSystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := os.Open(filepath.Join(dir, "missing.txt"))
	if got := ErrorCode(err); got != "ENOENT" {
		t.Errorf("missing file: ErrorCode(%v) = %q, want ENOENT", err, got)
	}
	_, err = os.Open(filepath.Join(dir, "missing", "file.txt"))
	if got := ErrorCode(err); got != "ENOENT" {
		t.Errorf("missing parent: ErrorCode(%v) = %q, want ENOENT", err, got)
	}
	_, err = os.Open(filepath.Join(file, "child.txt"))
	want := "ENOTDIR"
	if runtime.GOOS == "windows" {
		want = "ENOENT"
	}
	if got := ErrorCode(err); got != want {
		t.Errorf("file as directory: ErrorCode(%v) = %q, want %s", err, got, want)
	}
}

// nodeSystemErrorMap is Node's util.getSystemErrorMap on the running platform: errno to [code, description].
func nodeSystemErrorMap(t *testing.T) map[int][2]string {
	t.Helper()
	const script = `console.log(JSON.stringify([...require('node:util').getSystemErrorMap()]));`
	out, err := exec.CommandContext(t.Context(), "node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var entries [][2]json.RawMessage // [errno, [code, description]]
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("Node system error map %q: %v", out, err)
	}
	node := map[int][2]string{}
	for _, entry := range entries {
		var errno int
		var code [2]string
		if err := json.Unmarshal(entry[0], &errno); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(entry[1], &code); err != nil {
			t.Fatal(err)
		}
		node[errno] = code
	}
	return node
}

// TestCodesMatchNode checks the code table against Node's
// util.getSystemErrorMap on the running platform, in both directions: every
// code Node names is here with its description, and the errno Errno returns
// for each code maps to the same code and description in Node.
func TestCodesMatchNode(t *testing.T) {
	node := nodeSystemErrorMap(t)
	for errno, entry := range node {
		code, description := entry[0], entry[1]
		if got, ok := Description(code); !ok || got != description {
			t.Errorf("Node names %s (%d) %q; Description(%s) = %q, %v", code, errno, description, code, got, ok)
		}
	}
	if len(codes) != len(node) {
		t.Errorf("codes has %d entries, Node's map has %d", len(codes), len(node))
	}
	for code, entry := range codes {
		errno, ok := Errno(code)
		if !ok {
			t.Errorf("Errno(%s) has no value", code)
			continue
		}
		if got := node[errno]; got != [2]string{code, entry.description} {
			t.Errorf("Errno(%s) = %d, which Node reports as %q; want %s %q", code, errno, got, code, entry.description)
		}
	}
}

// TestNameMatchesNode checks Name against libuv's uv_err_name and uv_strerror
// as Node exposes them (util.getSystemErrorName and
// util.getSystemErrorMessage), for every errno Node names and for errnos libuv
// does not name, which both report as "Unknown system error <errno>".
func TestNameMatchesNode(t *testing.T) {
	errnos := []int{-1, -122, -4021, -9999}
	for errno := range nodeSystemErrorMap(t) {
		errnos = append(errnos, errno)
	}
	list := make([]string, len(errnos))
	for i, errno := range errnos {
		list[i] = strconv.Itoa(errno)
	}
	const script = `const util = require('node:util');
const out = {};
for (const errno of process.argv[1].split(',').filter(Boolean).map(Number)) out[errno] = [util.getSystemErrorName(errno), util.getSystemErrorMessage(errno)];
console.log(JSON.stringify(out));`
	// The leading comma keeps Node from reading a negative first errno as an option.
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, ","+strings.Join(list, ",")).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var node map[string][2]string
	if err := json.Unmarshal(out, &node); err != nil {
		t.Fatalf("Node names %q: %v", out, err)
	}
	for _, errno := range errnos {
		want := node[strconv.Itoa(errno)]
		if code, description := Name(errno); [2]string{code, description} != want {
			t.Errorf("Name(%d) = %q, %q; Node reports %q", errno, code, description, want)
		}
	}
}
