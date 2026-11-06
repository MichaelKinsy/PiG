//go:build windows

package childwait

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pipeBuffer is the size of the pipe's buffer, which is Node's (libuv creates its stdio pipes with 64 KiB).
const pipeBuffer = 64 * 1024

// NewPipe is the pipe a child's output goes through: the read end for Start, and the write end for the child's stdout or stderr.
//
// os.Pipe's handles are synchronous, and the I/O on a synchronous handle is serialized on its file object, so a reader that waits for data on one
// holds up the PeekNamedPipe that the reader of a later pipe makes on it, and nothing can wait for data without a thread blocked in the read. The read
// end here is created overlapped (a named pipe instance, as libuv's uv__create_stdio_pipe_pair makes it): Go's os.NewFile finds that out and parks the
// reader on the completion port, a peek never waits, and pollReadable waits for data on an event instead of sleeping. The write end is a synchronous
// handle, inheritable, because the child is an ordinary program that writes with no OVERLAPPED. It is opened with FILE_READ_ATTRIBUTES besides write access,
// as CreatePipe and libuv do: a child that asks the pipe about itself (Node closing its stdout, Cygwin and MSYS choosing a chunk size, GetNamedPipeInfo)
// is refused without it.
func NewPipe() (read, write *os.File, err error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, nil, err
	}
	name, err := windows.UTF16PtrFromString(`\\.\pipe\pig-childwait-` + strconv.Itoa(os.Getpid()) + "-" + hex.EncodeToString(random[:]))
	if err != nil {
		return nil, nil, err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, nil, err
	}
	server, err := windows.CreateNamedPipe(name,
		windows.PIPE_ACCESS_INBOUND|windows.FILE_FLAG_FIRST_PIPE_INSTANCE|windows.FILE_FLAG_OVERLAPPED,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, pipeBuffer, pipeBuffer, 0,
		&windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}) //nolint:gosec // G103: the descriptor and the attribute struct outlive the call; x/sys has no safer way to size the struct.
	if err != nil {
		return nil, nil, err
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1} //nolint:gosec // G103: the size of the struct passed to CreateFile.
	client, err := windows.CreateFile(name, windows.GENERIC_WRITE|windows.FILE_READ_ATTRIBUTES, 0, &attributes, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		_ = windows.CloseHandle(server)
		return nil, nil, err
	}
	return os.NewFile(uintptr(server), "|0"), os.NewFile(uintptr(client), "|1"), nil
}
