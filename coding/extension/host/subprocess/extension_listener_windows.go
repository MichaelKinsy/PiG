//go:build windows

package subprocess

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
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
//
// Accept may be called concurrently. Each call waits on an instance of its
// own, the listener records every one, and Close ends them all. The listener
// owns at most one spare instance that no Accept is waiting on.
//
// Close ends a pending connect by closing the instance's pipe handle, which
// the pipe driver completes at once. A connect is issued on whichever thread
// runs the listener code, and Go then runs other goroutines on that thread.
// CancelIoEx of the connect waits until that thread can process the
// cancellation, which it cannot while a goroutine holds it in a synchronous
// read: os/exec copies a silent Node cell's output that way for as long as
// the cell lives.
type pipeListener struct {
	path  string
	attrs *windows.SecurityAttributes

	mu        sync.Mutex
	closed    bool
	next      *pipeInstance   // the spare, waiting for the next Accept
	accepting []*pipeInstance // the instance each waiting Accept waits on
}

// pipeInstance is one server instance of the pipe and its connect. While the
// instance is the listener's spare or a waiting Accept's, Close may close its
// pipe handle under the listener's lock.
//
// The connect never queues a completion packet: its overlapped carries the
// event with the low-order bit set. Accept hands the handle to go-winio, which
// associates it with its completion port, and the kernel sets a finished
// connect's event before it looks for that port. A packet queued in between
// would carry this overlapped to go-winio, which would take it for one of its
// own operations.
type pipeInstance struct {
	handle     windows.Handle
	event      windows.Handle
	overlapped *windows.Overlapped
	// done is set once the connect has finished: when it was issued, or
	// when wait saw its event set. The kernel no longer writes to the
	// overlapped after that.
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
	instance := &pipeInstance{handle: h, event: event, overlapped: &windows.Overlapped{HEvent: event | 1}}
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

// wait returns once the instance's connect has finished: a client connected,
// or Close closed the instance. It waits on the connect's event, not on the
// pipe handle, which Close may already have closed. After wait returns, the
// kernel no longer writes to the instance's overlapped.
func (p *pipeInstance) wait() error {
	if p.done {
		return nil
	}
	if _, err := windows.WaitForSingleObject(p.event, windows.INFINITE); err != nil {
		return err
	}
	p.done = true
	// The kernel stores the connect's NTSTATUS in the overlapped.
	status := windows.NTStatus(p.overlapped.Internal)
	if int32(status) >= 0 {
		return nil
	}
	err := status.Errno()
	if errors.Is(err, windows.ERROR_NO_DATA) {
		p.clientGone = true
		return nil
	}
	return err
}

// abort closes the instance's pipe handle. The pipe driver then completes a
// pending connect at once, whatever the thread that issued it is doing.
func (p *pipeInstance) abort() {
	if p.handle != windows.InvalidHandle {
		_ = windows.CloseHandle(p.handle)
		p.handle = windows.InvalidHandle
	}
}

// release closes the instance's event and, unless it was handed to a
// connection, its pipe handle.
func (p *pipeInstance) release() {
	_ = windows.CloseHandle(p.event)
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
	// A concurrent Accept that finds the spare taken waits on a new instance.
	instance := l.next
	l.next = nil
	if instance == nil {
		var err error
		if instance, err = newPipeInstance(l.path, l.attrs, false); err != nil {
			l.mu.Unlock()
			return nil, err
		}
	}
	l.accepting = append(l.accepting, instance)
	l.mu.Unlock()

	err := instance.wait()

	l.mu.Lock()
	l.accepting = slices.DeleteFunc(l.accepting, func(p *pipeInstance) bool { return p == instance })
	closed := l.closed
	if err == nil && !closed && l.next == nil {
		// Keep an instance waiting for the next client before this one
		// is handed out. A concurrent Accept may already have made it.
		if next, nextErr := newPipeInstance(l.path, l.attrs, false); nextErr == nil {
			l.next = next
		} else {
			err = nextErr
		}
	}
	l.mu.Unlock()
	// A Close that ran while this instance was waiting closed its handle,
	// even when a client had connected first.
	if err != nil || closed {
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

// Close stops accepting. Every waiting Accept returns net.ErrClosed.
func (l *pipeListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	for _, instance := range l.accepting {
		// The waiting Accept sees its connect end and releases the rest.
		instance.abort()
	}
	if l.next != nil {
		l.next.abort()
		_ = l.next.wait()
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
