package workers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"sync"
)

// Ports packages/chord/src/next-node/workers/lock.ts. Process locks (slice 10 §3, Chord v1's `processLock`): one live
// worker per address. A lock is a listening socket the kernel owns for the holder's lifetime (an abstract socket on Linux
// and Android, a named pipe on Windows, a socket file elsewhere), so there is no race between two would-be holders; a
// socket file a crashed holder left fails closed for a taker (the error names it: a takeover would race another taker), and
// only the one process that starts the holders of a name clears it (ClearStaleLock; decision "Stale lock files are cleared by
// the starter"). A child
// finding the lock held waits without polling: it connects to the holder's socket, which the holder never answers, and
// tries again when that connection closes (the holder exited).
//
// Forced Go differences. `processLock` and the `ProcessLock` type share a name upstream; the function keeps it and the type
// is ProcessLockHandle. An awaited Promise is a blocking call returning (value, error); a rejection with the abort's
// reason is `context.Cause(ctx)`. Node's `process.once("exit", removeFile)` is atExit (workers_exit.go): Go has no exit
// event, so a worker's own exit path runs it. A held lock's accepted connections are owned by one goroutine that Release
// joins, never detached.

// lockKind is where a lock lives: an abstract socket (Linux, Android), a named pipe (Windows) or a socket file (elsewhere).
type lockKind string

const (
	lockAbstract lockKind = "abstract"
	lockPipe     lockKind = "pipe"
	lockFile     lockKind = "file"
)

// lockLocation is Pi's `LockAddress`.
type lockLocation struct {
	kind    lockKind
	address string
}

// ProcessLock takes the lock name, waiting while another process holds it (until ctx aborts: it returns its cause). It
// returns what listening returns besides "in use", and for a stale socket file (macOS, the BSDs) an error naming it.
func ProcessLock(name string, ctx context.Context) (*ProcessLockHandle, error) {
	location, err := lockAddress(name)
	if err != nil {
		return nil, err
	}
	return lockAt(location, ctx)
}

// ClearStaleLock removes the socket file of the lock name when nobody accepts on it (a holder killed without its exit hooks
// left it); it returns whether it did. Abstract sockets and named pipes end with their holder: nothing to clear. Only one
// process clears a name, one clear at a time: two clearers could both find it stale, one remove it, a new holder take it,
// and the other remove the new holder's file.
func ClearStaleLock(name string, ctx context.Context) (bool, error) {
	location, err := lockAddress(name)
	if err != nil {
		return false, err
	}
	return clearStaleLockAt(location, ctx)
}

// lockAddress is where the lock name lives on this platform (`lockAddress` upstream).
func lockAddress(name string) (lockLocation, error) {
	sum := sha256.Sum256([]byte(name))
	//portlint:allow utf16units the slice is of a hex digest, ASCII only, so bytes are UTF-16 units; lock.ts `.digest("hex").slice(0, 32)`
	key := hex.EncodeToString(sum[:])[:32]
	return platformLockLocation(key)
}

// On Linux the lock is an abstract socket with no file to go stale.
// clearStaleLockAt is ClearStaleLock at location: a socket file nobody accepts on is removed.
func clearStaleLockAt(location lockLocation, ctx context.Context) (bool, error) {
	if ctx.Err() != nil {
		return false, context.Cause(ctx)
	}
	if location.kind != lockFile || !nobodyAccepts(ctx, location) {
		return false, nil
	}
	if err := os.Remove(location.address); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// nobodyAccepts is whether the path of location exists and nobody accepts on it: the connect's errors that mean a socket
// file nobody listens on, or no socket there (`NOBODY_ACCEPTS`).
func nobodyAccepts(ctx context.Context, location lockLocation) bool {
	conn, err := platformDialLock(location, ctx)
	if err != nil {
		return lockRefused(err)
	}
	_ = conn.Close()
	return false
}

// lockAt takes the lock at location, waiting while it is held. A socket file nobody accepts on (a crashed holder's) fails
// closed: taking it over races another taker (two could both unlink and listen), so the error names the file to remove.
func lockAt(location lockLocation, ctx context.Context) (*ProcessLockHandle, error) {
	address := location.address
	file := location.kind == lockFile
	var listener net.Listener
	for {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		taken, err := listenLock(location)
		if err == nil {
			listener = taken
			break
		}
		if !lockInUse(err) {
			return nil, err
		}
		held, err := waitWhileHeld(location, ctx)
		if err != nil {
			return nil, err
		}
		if !held && file {
			return nil, fmt.Errorf("lock: %s is a stale lock file (no process holds it); remove it if no worker runs", address)
		}
	}
	lock := &ProcessLockHandle{listener: listener, waiters: map[net.Conn]struct{}{}, accepting: make(chan struct{})}
	if file {
		lock.removeExitHook = atExit(func() {
			// Gone already.
			_ = os.Remove(address)
		})
	}
	//portlint:allow golifetime the accept loop returns when Release closes the listener, and Release joins it
	go lock.accept() //portlint:allow asyncorder the accept loop is lock.ts:45 createServer((socket) => ...) connection callback, an event source with no caller to order against; Release joins it
	return lock, nil
}

// ProcessLockHandle is a held lock: its listening socket and the connections of the waiters, kept open and never answered.
// Release frees it (the process's exit does too).
type ProcessLockHandle struct {
	listener       net.Listener
	removeExitHook func()
	accepting      chan struct{}

	// waiting joins the goroutines that read the waiters' connections: Release closes the connections, so they end.
	waiting sync.WaitGroup

	mu       sync.Mutex
	waiters  map[net.Conn]struct{}
	released bool
	result   error
}

// accept keeps every waiter's connection open: a waiter learns of the release when its connection closes, and the holder
// learns of a waiter that closed its own (it gave up, or died) and forgets it.
func (lock *ProcessLockHandle) accept() {
	defer close(lock.accepting)
	for {
		conn, err := lock.listener.Accept()
		if err != nil {
			return
		}
		lock.mu.Lock()
		if lock.released {
			lock.mu.Unlock()
			_ = conn.Close()
			continue
		}
		lock.waiters[conn] = struct{}{}
		lock.waiting.Add(1)
		lock.mu.Unlock()
		//portlint:allow golifetime the reader ends with its connection, which its waiter or Release closes, and Release joins it
		go lock.watch(conn) //portlint:allow asyncorder the reader is lock.ts:48 socket.once("close", () => waiters.delete(socket)), an event with no ordering against other work; Release joins it
	}
}

// watch reads conn, which is never answered, until it closes, then forgets it (`socket.once("close", ...)`).
func (lock *ProcessLockHandle) watch(conn net.Conn) {
	defer lock.waiting.Done()
	var sink [1]byte
	for {
		if _, err := conn.Read(sink[:]); err != nil {
			break
		}
	}
	lock.mu.Lock()
	delete(lock.waiters, conn)
	lock.mu.Unlock()
	_ = conn.Close()
}

// Release closes the lock's socket and its waiters' connections (they see them close) once it is gone; releasing again
// returns the first result.
func (lock *ProcessLockHandle) Release() error {
	lock.mu.Lock()
	if lock.released {
		defer lock.mu.Unlock()
		return lock.result
	}
	lock.released = true
	lock.mu.Unlock()
	if lock.removeExitHook != nil {
		lock.removeExitHook()
	}
	err := lock.listener.Close()
	<-lock.accepting
	lock.mu.Lock()
	for conn := range lock.waiters {
		_ = conn.Close()
	}
	clear(lock.waiters)
	lock.mu.Unlock()
	lock.waiting.Wait()
	lock.mu.Lock()
	lock.result = err
	lock.mu.Unlock()
	return err
}

// listenLock is Node's `server.listen(address)` of the lock's kind; the error is the kernel's.
func listenLock(location lockLocation) (net.Listener, error) {
	return platformListenLock(location)
}

// waitWhileHeld connects to the holder of the lock and returns when that connection closes or the address is gone
// (true: try again), or false when nobody accepts on it (a leftover socket file); ctx's abort returns its cause.
func waitWhileHeld(location lockLocation, ctx context.Context) (bool, error) {
	if ctx.Err() != nil {
		return false, context.Cause(ctx)
	}
	conn, err := platformDialLock(location, ctx)
	if err != nil {
		if ctx.Err() != nil {
			return false, context.Cause(ctx)
		}
		return !lockRefused(err), nil
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	// The holder never answers: the read ends when it closes the connection.
	var sink [1]byte
	for {
		if _, err := conn.Read(sink[:]); err != nil {
			break
		}
	}
	stop()
	_ = conn.Close()
	if ctx.Err() != nil {
		return false, context.Cause(ctx)
	}
	return true, nil
}
