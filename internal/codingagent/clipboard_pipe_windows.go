//go:build windows

package codingagent

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Pi spawns clipboard helpers with windowsHide: true (clipboard-command.ts:13).
func hideClipboardWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// clipboardPipe returns a connected byte pipe for a clipboard command's stdin or stdout. As libuv does for a child's stdio pipe, the parent end is a named pipe server opened for overlapped I/O and the child end is a synchronous client. Closing the overlapped parent end cancels its pending read or write through the runtime poller; closing a synchronous anonymous pipe does not reliably cancel a write that a descendant keeps blocked. Only the current user can open the pipe's unguessable name.
func clipboardPipe(parentReads bool) (parentEnd, childEnd *os.File, err error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, fmt.Errorf("read current user: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, nil, fmt.Errorf("clipboard pipe security descriptor: %w", err)
	}
	attrs := &windows.SecurityAttributes{SecurityDescriptor: sd}
	attrs.Length = uint32(unsafe.Sizeof(*attrs))
	path := `\\.\pipe\pig-clipboard-` + rand.Text()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil, err
	}
	// Go probes FileModeInformation when os.NewFile wraps the overlapped server. A write-only server lacks FILE_READ_ATTRIBUTES; duplex grants it. Libuv also grants the client the opposite direction's attribute right so child runtimes can query and configure the pipe.
	serverAccess, clientAccess := uint32(windows.PIPE_ACCESS_DUPLEX), uint32(windows.GENERIC_READ|windows.FILE_WRITE_ATTRIBUTES)
	if parentReads {
		serverAccess, clientAccess = windows.PIPE_ACCESS_INBOUND, windows.GENERIC_WRITE|windows.FILE_READ_ATTRIBUTES
	}
	server, err := windows.CreateNamedPipe(name, serverAccess|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 64<<10, 64<<10, 0, attrs)
	if err != nil {
		return nil, nil, &os.PathError{Op: "create named pipe", Path: path, Err: err}
	}
	client, err := windows.CreateFile(name, clientAccess, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		_ = windows.CloseHandle(server)
		return nil, nil, &os.PathError{Op: "open named pipe", Path: path, Err: err}
	}
	return os.NewFile(uintptr(server), path), os.NewFile(uintptr(client), path), nil
}
