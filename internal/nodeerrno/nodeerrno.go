// Package nodeerrno maps operating-system errors to the error codes,
// descriptions and errnos Node reports for them. Node takes them from libuv:
// uv_translate_sys_error converts a Windows system error to a libuv errno
// (error.errno), uv_err_name names the errno (error.code), and uv_strerror
// describes it.
package nodeerrno

import (
	"errors"
	"io/fs"
	"maps"
	"strconv"
	"syscall"
)

// libuvCode is what libuv reports for one of its error codes: uv_strerror's
// description and libuv's own errno for the code.
type libuvCode struct {
	description string
	libuvErrno  int
}

// codes are every code libuv 1.52.1, as bundled by Node 24.19.0, names
// (UV_ERRNO_MAP in include/uv/errno.h), with uv_strerror's description and
// libuv's own errno for the code (UV__E* in include/uv/errno.h). Windows uses
// that errno for every code; elsewhere a code whose POSIX errno the platform
// defines uses the negated POSIX errno, and the others keep libuv's own.
var codes = map[string]libuvCode{
	"E2BIG":           {"argument list too long", -4093},
	"EACCES":          {"permission denied", -4092},
	"EADDRINUSE":      {"address already in use", -4091},
	"EADDRNOTAVAIL":   {"address not available", -4090},
	"EAFNOSUPPORT":    {"address family not supported", -4089},
	"EAGAIN":          {"resource temporarily unavailable", -4088},
	"EAI_ADDRFAMILY":  {"address family not supported", -3000},
	"EAI_AGAIN":       {"temporary failure", -3001},
	"EAI_BADFLAGS":    {"bad ai_flags value", -3002},
	"EAI_BADHINTS":    {"invalid value for hints", -3013},
	"EAI_CANCELED":    {"request canceled", -3003},
	"EAI_FAIL":        {"permanent failure", -3004},
	"EAI_FAMILY":      {"ai_family not supported", -3005},
	"EAI_MEMORY":      {"out of memory", -3006},
	"EAI_NODATA":      {"no address", -3007},
	"EAI_NONAME":      {"unknown node or service", -3008},
	"EAI_OVERFLOW":    {"argument buffer overflow", -3009},
	"EAI_PROTOCOL":    {"resolved protocol is unknown", -3014},
	"EAI_SERVICE":     {"service not available for socket type", -3010},
	"EAI_SOCKTYPE":    {"socket type not supported", -3011},
	"EALREADY":        {"connection already in progress", -4084},
	"EBADF":           {"bad file descriptor", -4083},
	"EBUSY":           {"resource busy or locked", -4082},
	"ECANCELED":       {"operation canceled", -4081},
	"ECHARSET":        {"invalid Unicode character", -4080},
	"ECONNABORTED":    {"software caused connection abort", -4079},
	"ECONNREFUSED":    {"connection refused", -4078},
	"ECONNRESET":      {"connection reset by peer", -4077},
	"EDESTADDRREQ":    {"destination address required", -4076},
	"EEXIST":          {"file already exists", -4075},
	"EFAULT":          {"bad address in system call argument", -4074},
	"EFBIG":           {"file too large", -4036},
	"EFTYPE":          {"inappropriate file type or format", -4028},
	"EHOSTDOWN":       {"host is down", -4031},
	"EHOSTUNREACH":    {"host is unreachable", -4073},
	"EILSEQ":          {"illegal byte sequence", -4027},
	"EINTR":           {"interrupted system call", -4072},
	"EINVAL":          {"invalid argument", -4071},
	"EIO":             {"i/o error", -4070},
	"EISCONN":         {"socket is already connected", -4069},
	"EISDIR":          {"illegal operation on a directory", -4068},
	"ELOOP":           {"too many symbolic links encountered", -4067},
	"EMFILE":          {"too many open files", -4066},
	"EMLINK":          {"too many links", -4032},
	"EMSGSIZE":        {"message too long", -4065},
	"ENAMETOOLONG":    {"name too long", -4064},
	"ENETDOWN":        {"network is down", -4063},
	"ENETUNREACH":     {"network is unreachable", -4062},
	"ENFILE":          {"file table overflow", -4061},
	"ENOBUFS":         {"no buffer space available", -4060},
	"ENODATA":         {"no data available", -4024},
	"ENODEV":          {"no such device", -4059},
	"ENOENT":          {"no such file or directory", -4058},
	"ENOEXEC":         {"exec format error", -4022},
	"ENOMEM":          {"not enough memory", -4057},
	"ENONET":          {"machine is not on the network", -4056},
	"ENOPROTOOPT":     {"protocol not available", -4035},
	"ENOSPC":          {"no space left on device", -4055},
	"ENOSYS":          {"function not implemented", -4054},
	"ENOTCONN":        {"socket is not connected", -4053},
	"ENOTDIR":         {"not a directory", -4052},
	"ENOTEMPTY":       {"directory not empty", -4051},
	"ENOTSOCK":        {"socket operation on non-socket", -4050},
	"ENOTSUP":         {"operation not supported on socket", -4049},
	"ENOTTY":          {"inappropriate ioctl for device", -4029},
	"ENXIO":           {"no such device or address", -4033},
	"EOF":             {"end of file", -4095},
	"EOVERFLOW":       {"value too large for defined data type", -4026},
	"EPERM":           {"operation not permitted", -4048},
	"EPIPE":           {"broken pipe", -4047},
	"EPROTO":          {"protocol error", -4046},
	"EPROTONOSUPPORT": {"protocol not supported", -4045},
	"EPROTOTYPE":      {"protocol wrong type for socket", -4044},
	"ERANGE":          {"result too large", -4034},
	"EREMOTEIO":       {"remote I/O error", -4030},
	"EROFS":           {"read-only file system", -4043},
	"ESHUTDOWN":       {"cannot send after transport endpoint shutdown", -4042},
	"ESOCKTNOSUPPORT": {"socket type not supported", -4025},
	"ESPIPE":          {"invalid seek", -4041},
	"ESRCH":           {"no such process", -4040},
	"ETIMEDOUT":       {"connection timed out", -4039},
	"ETXTBSY":         {"text file is busy", -4038},
	"EUNATCH":         {"protocol driver not attached", -4023},
	"EXDEV":           {"cross-device link not permitted", -4037},
	"UNKNOWN":         {"unknown error", -4094},
}

// windowsTranslations is libuv's uv_translate_sys_error (src/win/error.c,
// libuv 1.52.1 as bundled by Node 24.19.0) in its order: the Windows system
// and Winsock errors libuv names. libuv reports every other Windows error as
// UNKNOWN. The errnos are winerror.h and winsock2.h numbers, so the table
// builds and is tested on every platform.
var windowsTranslations = [...]struct {
	errno syscall.Errno
	name  string
	code  string
}{
	{10013, "WSAEACCES", "EACCES"},
	{740, "ERROR_ELEVATION_REQUIRED", "EACCES"},
	{1920, "ERROR_CANT_ACCESS_FILE", "EACCES"},
	{1227, "ERROR_ADDRESS_ALREADY_ASSOCIATED", "EADDRINUSE"},
	{10048, "WSAEADDRINUSE", "EADDRINUSE"},
	{10049, "WSAEADDRNOTAVAIL", "EADDRNOTAVAIL"},
	{10047, "WSAEAFNOSUPPORT", "EAFNOSUPPORT"},
	{10035, "WSAEWOULDBLOCK", "EAGAIN"},
	{232, "ERROR_NO_DATA", "EAGAIN"},
	{10037, "WSAEALREADY", "EALREADY"},
	{1004, "ERROR_INVALID_FLAGS", "EBADF"},
	{6, "ERROR_INVALID_HANDLE", "EBADF"},
	{33, "ERROR_LOCK_VIOLATION", "EBUSY"},
	{231, "ERROR_PIPE_BUSY", "EBUSY"},
	{32, "ERROR_SHARING_VIOLATION", "EBUSY"},
	{995, "ERROR_OPERATION_ABORTED", "ECANCELED"},
	{10004, "WSAEINTR", "ECANCELED"},
	{1113, "ERROR_NO_UNICODE_TRANSLATION", "ECHARSET"},
	{1236, "ERROR_CONNECTION_ABORTED", "ECONNABORTED"},
	{10053, "WSAECONNABORTED", "ECONNABORTED"},
	{1225, "ERROR_CONNECTION_REFUSED", "ECONNREFUSED"},
	{10061, "WSAECONNREFUSED", "ECONNREFUSED"},
	{64, "ERROR_NETNAME_DELETED", "ECONNRESET"},
	{10054, "WSAECONNRESET", "ECONNRESET"},
	{183, "ERROR_ALREADY_EXISTS", "EEXIST"},
	{80, "ERROR_FILE_EXISTS", "EEXIST"},
	{998, "ERROR_NOACCESS", "EFAULT"},
	{10014, "WSAEFAULT", "EFAULT"},
	{1232, "ERROR_HOST_UNREACHABLE", "EHOSTUNREACH"},
	{10065, "WSAEHOSTUNREACH", "EHOSTUNREACH"},
	{122, "ERROR_INSUFFICIENT_BUFFER", "EINVAL"},
	{13, "ERROR_INVALID_DATA", "EINVAL"},
	{87, "ERROR_INVALID_PARAMETER", "EINVAL"},
	{1464, "ERROR_SYMLINK_NOT_SUPPORTED", "EINVAL"},
	{10022, "WSAEINVAL", "EINVAL"},
	{10046, "WSAEPFNOSUPPORT", "EINVAL"},
	{1102, "ERROR_BEGINNING_OF_MEDIA", "EIO"},
	{1111, "ERROR_BUS_RESET", "EIO"},
	{23, "ERROR_CRC", "EIO"},
	{1166, "ERROR_DEVICE_DOOR_OPEN", "EIO"},
	{1165, "ERROR_DEVICE_REQUIRES_CLEANING", "EIO"},
	{1393, "ERROR_DISK_CORRUPT", "EIO"},
	{1129, "ERROR_EOM_OVERFLOW", "EIO"},
	{1101, "ERROR_FILEMARK_DETECTED", "EIO"},
	{31, "ERROR_GEN_FAILURE", "EIO"},
	{1106, "ERROR_INVALID_BLOCK_LENGTH", "EIO"},
	{1117, "ERROR_IO_DEVICE", "EIO"},
	{1104, "ERROR_NO_DATA_DETECTED", "EIO"},
	{205, "ERROR_NO_SIGNAL_SENT", "EIO"},
	{110, "ERROR_OPEN_FAILED", "EIO"},
	{1103, "ERROR_SETMARK_DETECTED", "EIO"},
	{156, "ERROR_SIGNAL_REFUSED", "EIO"},
	{10056, "WSAEISCONN", "EISCONN"},
	{1921, "ERROR_CANT_RESOLVE_FILENAME", "ELOOP"},
	{4, "ERROR_TOO_MANY_OPEN_FILES", "EMFILE"},
	{10024, "WSAEMFILE", "EMFILE"},
	{10040, "WSAEMSGSIZE", "EMSGSIZE"},
	{111, "ERROR_BUFFER_OVERFLOW", "ENAMETOOLONG"},
	{206, "ERROR_FILENAME_EXCED_RANGE", "ENAMETOOLONG"},
	{1231, "ERROR_NETWORK_UNREACHABLE", "ENETUNREACH"},
	{10051, "WSAENETUNREACH", "ENETUNREACH"},
	{10055, "WSAENOBUFS", "ENOBUFS"},
	{161, "ERROR_BAD_PATHNAME", "ENOENT"},
	{267, "ERROR_DIRECTORY", "ENOENT"},
	{203, "ERROR_ENVVAR_NOT_FOUND", "ENOENT"},
	{2, "ERROR_FILE_NOT_FOUND", "ENOENT"},
	{123, "ERROR_INVALID_NAME", "ENOENT"},
	{15, "ERROR_INVALID_DRIVE", "ENOENT"},
	{4392, "ERROR_INVALID_REPARSE_DATA", "ENOENT"},
	{126, "ERROR_MOD_NOT_FOUND", "ENOENT"},
	{3, "ERROR_PATH_NOT_FOUND", "ENOENT"},
	{11001, "WSAHOST_NOT_FOUND", "ENOENT"},
	{11004, "WSANO_DATA", "ENOENT"},
	{8, "ERROR_NOT_ENOUGH_MEMORY", "ENOMEM"},
	{14, "ERROR_OUTOFMEMORY", "ENOMEM"},
	{82, "ERROR_CANNOT_MAKE", "ENOSPC"},
	{112, "ERROR_DISK_FULL", "ENOSPC"},
	{277, "ERROR_EA_TABLE_FULL", "ENOSPC"},
	{1100, "ERROR_END_OF_MEDIA", "ENOSPC"},
	{39, "ERROR_HANDLE_DISK_FULL", "ENOSPC"},
	{2250, "ERROR_NOT_CONNECTED", "ENOTCONN"},
	{10057, "WSAENOTCONN", "ENOTCONN"},
	{145, "ERROR_DIR_NOT_EMPTY", "ENOTEMPTY"},
	{10038, "WSAENOTSOCK", "ENOTSOCK"},
	{50, "ERROR_NOT_SUPPORTED", "ENOTSUP"},
	{109, "ERROR_BROKEN_PIPE", "EOF"},
	{5, "ERROR_ACCESS_DENIED", "EPERM"},
	{1314, "ERROR_PRIVILEGE_NOT_HELD", "EPERM"},
	{230, "ERROR_BAD_PIPE", "EPIPE"},
	{233, "ERROR_PIPE_NOT_CONNECTED", "EPIPE"},
	{10058, "WSAESHUTDOWN", "EPIPE"},
	{10043, "WSAEPROTONOSUPPORT", "EPROTONOSUPPORT"},
	{19, "ERROR_WRITE_PROTECT", "EROFS"},
	{121, "ERROR_SEM_TIMEOUT", "ETIMEDOUT"},
	{10060, "WSAETIMEDOUT", "ETIMEDOUT"},
	{17, "ERROR_NOT_SAME_DEVICE", "EXDEV"},
	{1, "ERROR_INVALID_FUNCTION", "EISDIR"},
	{208, "ERROR_META_EXPANSION_TOO_LONG", "E2BIG"},
	{10044, "WSAESOCKTNOSUPPORT", "ESOCKTNOSUPPORT"},
	{193, "ERROR_BAD_EXE_FORMAT", "EFTYPE"},
}

// windowsCodes maps each Windows error of windowsTranslations to its code.
var windowsCodes = func() map[syscall.Errno]string {
	translated := make(map[syscall.Errno]string, len(windowsTranslations))
	for _, entry := range windowsTranslations {
		translated[entry.errno] = entry.code
	}
	return translated
}()

// commonPosixCodes names the errnos, among libuv's codes, that the syscall
// package defines on Linux, macOS and Windows. On Windows these are the POSIX
// errnos Go invents (syscall.Open reports EISDIR for a directory opened for
// writing), except syscall.ENOENT and syscall.ENOTDIR, which alias
// ERROR_FILE_NOT_FOUND and ERROR_PATH_NOT_FOUND and are named by windowsCodes
// first.
var commonPosixCodes = map[syscall.Errno]string{
	syscall.E2BIG:           "E2BIG",
	syscall.EACCES:          "EACCES",
	syscall.EADDRINUSE:      "EADDRINUSE",
	syscall.EADDRNOTAVAIL:   "EADDRNOTAVAIL",
	syscall.EAFNOSUPPORT:    "EAFNOSUPPORT",
	syscall.EAGAIN:          "EAGAIN",
	syscall.EALREADY:        "EALREADY",
	syscall.EBADF:           "EBADF",
	syscall.EBUSY:           "EBUSY",
	syscall.ECANCELED:       "ECANCELED",
	syscall.ECONNABORTED:    "ECONNABORTED",
	syscall.ECONNREFUSED:    "ECONNREFUSED",
	syscall.ECONNRESET:      "ECONNRESET",
	syscall.EDESTADDRREQ:    "EDESTADDRREQ",
	syscall.EEXIST:          "EEXIST",
	syscall.EFAULT:          "EFAULT",
	syscall.EFBIG:           "EFBIG",
	syscall.EHOSTDOWN:       "EHOSTDOWN",
	syscall.EHOSTUNREACH:    "EHOSTUNREACH",
	syscall.EILSEQ:          "EILSEQ",
	syscall.EINTR:           "EINTR",
	syscall.EINVAL:          "EINVAL",
	syscall.EIO:             "EIO",
	syscall.EISCONN:         "EISCONN",
	syscall.EISDIR:          "EISDIR",
	syscall.ELOOP:           "ELOOP",
	syscall.EMFILE:          "EMFILE",
	syscall.EMLINK:          "EMLINK",
	syscall.EMSGSIZE:        "EMSGSIZE",
	syscall.ENAMETOOLONG:    "ENAMETOOLONG",
	syscall.ENETDOWN:        "ENETDOWN",
	syscall.ENETUNREACH:     "ENETUNREACH",
	syscall.ENFILE:          "ENFILE",
	syscall.ENOBUFS:         "ENOBUFS",
	syscall.ENODEV:          "ENODEV",
	syscall.ENOENT:          "ENOENT",
	syscall.ENOEXEC:         "ENOEXEC",
	syscall.ENOMEM:          "ENOMEM",
	syscall.ENOPROTOOPT:     "ENOPROTOOPT",
	syscall.ENOSPC:          "ENOSPC",
	syscall.ENOSYS:          "ENOSYS",
	syscall.ENOTCONN:        "ENOTCONN",
	syscall.ENOTDIR:         "ENOTDIR",
	syscall.ENOTEMPTY:       "ENOTEMPTY",
	syscall.ENOTSOCK:        "ENOTSOCK",
	syscall.ENOTSUP:         "ENOTSUP",
	syscall.ENOTTY:          "ENOTTY",
	syscall.ENXIO:           "ENXIO",
	syscall.EOVERFLOW:       "EOVERFLOW",
	syscall.EPERM:           "EPERM",
	syscall.EPIPE:           "EPIPE",
	syscall.EPROTO:          "EPROTO",
	syscall.EPROTONOSUPPORT: "EPROTONOSUPPORT",
	syscall.EPROTOTYPE:      "EPROTOTYPE",
	syscall.ERANGE:          "ERANGE",
	syscall.EROFS:           "EROFS",
	syscall.ESHUTDOWN:       "ESHUTDOWN",
	syscall.ESOCKTNOSUPPORT: "ESOCKTNOSUPPORT",
	syscall.ESPIPE:          "ESPIPE",
	syscall.ESRCH:           "ESRCH",
	syscall.ETIMEDOUT:       "ETIMEDOUT",
	syscall.ETXTBSY:         "ETXTBSY",
	syscall.EXDEV:           "EXDEV",
}

// posixCodes names every errno, among libuv's codes, that the running
// platform's syscall package defines.
var posixCodes = func() map[syscall.Errno]string {
	named := maps.Clone(commonPosixCodes)
	maps.Copy(named, platformPosixCodes)
	return named
}()

// codeByErrno is the code libuv's uv_err_name gives each errno on the running
// platform.
var codeByErrno = func() map[int]string {
	named := make(map[int]string, len(codes))
	for code := range codes {
		if errno, ok := Errno(code); ok {
			named[errno] = code
		}
	}
	return named
}()

// windowsCode returns the Node error code libuv reports on Windows for the
// system error errno.
func windowsCode(errno syscall.Errno) (string, bool) {
	code, ok := windowsCodes[errno]
	return code, ok
}

// posixCode returns the Node error code for one of the running platform's
// syscall errnos.
func posixCode(errno syscall.Errno) (string, bool) {
	code, ok := posixCodes[errno]
	return code, ok
}

// ErrorCode returns the Node error code (error.code) for the syscall.Errno
// that err wraps, or "" when err wraps none or its errno has no code here.
func ErrorCode(err error) string {
	return errorCode(err, Code)
}

func errorCode(err error, code func(syscall.Errno) (string, bool)) string {
	errno, ok := errors.AsType[syscall.Errno](err)
	if !ok {
		return ""
	}
	name, _ := code(errno)
	return name
}

// Description returns libuv's uv_strerror text for a Node error code.
func Description(code string) (string, bool) {
	entry, ok := codes[code]
	return entry.description, ok
}

// Name returns libuv's uv_err_name and uv_strerror for a libuv errno on the
// running platform. libuv names an errno it does not know "Unknown system
// error <errno>" in both (src/uv-common.c uv__unknown_err_code).
func Name(errno int) (code, description string) {
	if code, ok := codeByErrno[errno]; ok {
		return code, codes[code].description
	}
	unknown := "Unknown system error " + strconv.Itoa(errno)
	return unknown, unknown
}

// Describe returns the error.code, the uv_strerror text and the error.errno of
// the error Node's fs module reports for err, the failure of an fs call. An
// error without an errno is ENOENT when it is fs.ErrNotExist and UNKNOWN
// otherwise.
func Describe(err error) (code, description string, errno int) {
	if sysErrno, ok := errors.AsType[syscall.Errno](err); ok {
		return describeErrno(sysErrno)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return describeCode("ENOENT")
	}
	return describeCode("UNKNOWN")
}

func describeCode(code string) (string, string, int) {
	errno, _ := Errno(code)
	return code, codes[code].description, errno
}
