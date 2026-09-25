//go:build windows

package subprocess

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// ListenExtension listens for one extension process and returns the address
// the process connects to. Go, Rust, and Python extensions connect to the
// AF_UNIX socket at sockPath. Node's net module treats every path on Windows
// as a named pipe and cannot reach AF_UNIX, so a Node extension gets a named
// pipe with an unguessable name that only the current user can open.
func ListenExtension(sockPath string, node bool) (net.Listener, string, error) {
	if !node {
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			return nil, "", err
		}
		return ln, sockPath, nil
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, "", fmt.Errorf("read current user: %w", err)
	}
	pipe := `\\.\pipe\pig-ext-` + rand.Text()
	ln, err := listenPipe(pipe, "D:P(A;;GA;;;"+user.User.Sid.String()+")")
	if err != nil {
		return nil, "", err
	}
	return ln, pipe, nil
}

// pipeListener serves a named pipe the way a Unix socket's backlog serves a
// socket: a client never waits for Accept. Before the pipe's name is handed
// out, and again after every accepted connection, the listener creates a pipe
// instance and issues its ConnectNamedPipe, so an instance is always waiting.
// Node's pipe connect then completes at once instead of taking libuv's
// ERROR_PIPE_BUSY path, which waits for an instance for up to 30 seconds.
//
// A packed Node member whose factory fails reports it by connecting to its
// pipe and closing the connection, often while the host is still accepting an
// earlier member. That connection completes the pending connect, and the
// host's later Accept returns it and reads end of file. If a client ever
// connects and closes before the connect is issued, ConnectNamedPipe returns
// ERROR_NO_DATA, and Accept returns that the same way instead of waiting for
// another client, as go-winio's listener does.
type pipeListener struct {
	path  string
	attrs *windows.SecurityAttributes

	mu        sync.Mutex
	closed    bool
	next      *pipeInstance // waiting for the next Accept
	accepting *pipeInstance // an Accept is waiting on it
}

// pipeInstance is one server instance of the pipe and its connect.
type pipeInstance struct {
	handle     windows.Handle
	overlapped *windows.Overlapped
	// done is set when the connect finished when it was issued.
	done       bool
	clientGone bool
}

func listenPipe(path, sddl string) (*pipeListener, error) {
	attrs, err := pipeSecurityAttributes(sddl)
	if err != nil {
		return nil, err
	}
	first, err := newPipeInstance(path, attrs, true)
	if err != nil {
		return nil, err
	}
	return &pipeListener{path: path, attrs: attrs, next: first}, nil
}

func pipeSecurityAttributes(sddl string) (*windows.SecurityAttributes, error) {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("pipe security descriptor: %w", err)
	}
	attrs := &windows.SecurityAttributes{SecurityDescriptor: sd}
	attrs.Length = uint32(unsafe.Sizeof(*attrs))
	return attrs, nil
}

// newPipeInstance creates an instance and issues its connect.
func newPipeInstance(path string, attrs *windows.SecurityAttributes, first bool) (*pipeInstance, error) {
	h, err := createPipeHandle(path, attrs, first)
	if err != nil {
		return nil, err
	}
	return connectPipeInstance(path, h)
}

func createPipeHandle(path string, attrs *windows.SecurityAttributes, first bool) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	openMode := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		openMode |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	pipeMode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	h, err := windows.CreateNamedPipe(name, openMode, pipeMode, windows.PIPE_UNLIMITED_INSTANCES, 64<<10, 64<<10, 0, attrs)
	if err != nil {
		return windows.InvalidHandle, &os.PathError{Op: "create named pipe", Path: path, Err: err}
	}
	return h, nil
}

// connectPipeInstance issues the connect for the pipe instance h.
func connectPipeInstance(path string, h windows.Handle) (*pipeInstance, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	instance := &pipeInstance{handle: h, overlapped: &windows.Overlapped{HEvent: event}}
	switch err := windows.ConnectNamedPipe(h, instance.overlapped); {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		instance.done = true
	case errors.Is(err, windows.ERROR_NO_DATA):
		instance.done, instance.clientGone = true, true
	case errors.Is(err, windows.ERROR_IO_PENDING):
	default:
		instance.release()
		return nil, &os.PathError{Op: "connect named pipe", Path: path, Err: err}
	}
	return instance, nil
}

// wait returns once a client has connected to the instance, or the connect
// was cancelled.
func (p *pipeInstance) wait() error {
	if p.done {
		return nil
	}
	var transferred uint32
	err := windows.GetOverlappedResult(p.handle, p.overlapped, &transferred, true)
	p.done = true
	if errors.Is(err, windows.ERROR_NO_DATA) {
		p.clientGone = true
		return nil
	}
	return err
}

// cancel stops a pending connect and waits for the cancellation, so the
// kernel no longer writes to the instance's overlapped.
func (p *pipeInstance) cancel() {
	if !p.done {
		_ = windows.CancelIoEx(p.handle, p.overlapped)
		_ = p.wait()
	}
}

// release closes the instance's event and, unless it was handed to a
// connection, its pipe handle.
func (p *pipeInstance) release() {
	_ = windows.CloseHandle(p.overlapped.HEvent)
	if p.handle != windows.InvalidHandle {
		_ = windows.CloseHandle(p.handle)
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, net.ErrClosed
	}
	instance := l.next
	l.next = nil
	if instance == nil {
		var err error
		if instance, err = newPipeInstance(l.path, l.attrs, false); err != nil {
			l.mu.Unlock()
			return nil, err
		}
	}
	l.accepting = instance
	l.mu.Unlock()

	err := instance.wait()

	l.mu.Lock()
	l.accepting = nil
	closed := l.closed
	if err == nil && !closed {
		// Keep an instance waiting for the next client before this one
		// is handed out.
		if next, nextErr := newPipeInstance(l.path, l.attrs, false); nextErr == nil {
			l.next = next
		} else {
			err = nextErr
		}
	}
	l.mu.Unlock()
	if err != nil {
		instance.release()
		if closed {
			return nil, net.ErrClosed
		}
		return nil, &os.PathError{Op: "connect named pipe", Path: l.path, Err: err}
	}
	if instance.clientGone {
		instance.release()
		server, client := net.Pipe()
		_ = client.Close()
		return server, nil
	}
	handle := instance.handle
	instance.handle = windows.InvalidHandle
	instance.release()
	file, err := winio.NewOpenFile(handle)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	conn, ok := file.(pipeFile)
	if !ok {
		_ = file.Close()
		return nil, fmt.Errorf("named pipe %s: %T has no deadlines", l.path, file)
	}
	return &pipeConn{pipeFile: conn, addr: pipeAddr(l.path)}, nil
}

// Close stops accepting. A waiting Accept returns net.ErrClosed.
func (l *pipeListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.accepting != nil {
		// The waiting Accept sees the cancellation and releases it.
		_ = windows.CancelIoEx(l.accepting.handle, l.accepting.overlapped)
	}
	if l.next != nil {
		l.next.cancel()
		l.next.release()
		l.next = nil
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr(l.path) }

// pipeFile is go-winio's file for an overlapped handle: its reads and writes
// complete through go-winio's I/O completion port, one operation of each kind
// at a time, and a broken pipe reads as end of file.
type pipeFile interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// pipeConn is the connected server end of a named pipe.
type pipeConn struct {
	pipeFile
	addr pipeAddr
}

func (c *pipeConn) LocalAddr() net.Addr  { return c.addr }
func (c *pipeConn) RemoteAddr() net.Addr { return c.addr }

func (c *pipeConn) SetDeadline(t time.Time) error {
	return errors.Join(c.SetReadDeadline(t), c.SetWriteDeadline(t))
}

type pipeAddr string

func (pipeAddr) Network() string  { return "pipe" }
func (a pipeAddr) String() string { return string(a) }
