//go:build windows

package host

import (
	"errors"
	"net"
	"sync"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func lockAddress(key string) (string, bool, error) { return `\\.\pipe\chord2-` + key, false, nil }

// listenLock creates the pipe's only instance (FILE_FLAG_FIRST_PIPE_INSTANCE, one instance); a second owner's create
// fails with ERROR_ACCESS_DENIED. The lock is that one handle: Close closes it synchronously, so the name is free when
// release returns. (A go-winio listener closes through its own goroutine and keeps spare instances alive while
// accepting, so the name was not provably free on return.)
func listenLock(address string) (net.Listener, error) {
	name, err := windows.UTF16PtrFromString(address)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateNamedPipe(name,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 0, 0, 0, nil)
	if err != nil {
		return nil, err
	}
	return &lockListener{handle: handle, address: address, done: make(chan struct{})}, nil
}

// lockListener holds the pipe handle; nothing is ever accepted, so Accept just waits for Close.
type lockListener struct {
	handle  windows.Handle
	address string
	done    chan struct{}
	once    sync.Once
	err     error
}

func (l *lockListener) Accept() (net.Conn, error) {
	<-l.done
	return nil, net.ErrClosed
}

func (l *lockListener) Close() error {
	l.once.Do(func() {
		l.err = windows.CloseHandle(l.handle)
		close(l.done)
	})
	return l.err
}

func (l *lockListener) Addr() net.Addr { return pipeAddr(l.address) }

type pipeAddr string

func (pipeAddr) Network() string  { return "pipe" }
func (a pipeAddr) String() string { return string(a) }

func lockInUse(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY)
}

// accepting is manager.ts accepting(address); named pipes are never files, so ProcessLock does not reach it.
func accepting(address string) bool {
	conn, err := winio.DialPipe(address, nil)
	if err == nil {
		_ = conn.Close()
		return true
	}
	return false
}
