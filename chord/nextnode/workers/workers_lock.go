package workers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"sync"
)

// Ports packages/chord/src/next-node/workers/lock.ts. Process locks (slice 10 §3, Chord v1's `processLock`): one live
// worker per address. A lock is a listening socket the kernel owns for the holder's lifetime (an abstract socket on Linux
// and Android, a named pipe on Windows, a socket file elsewhere), so there is no race between two would-be holders; a
// socket file a crashed holder left fails closed (the error names it: a takeover would race another taker). A child
// finding the lock held waits without polling: it connects to the holder's socket, which the holder never answers, and
// tries again when that connection closes (the holder exited).
//
// Forced Go differences. `processLock` and the `ProcessLock` type share a name upstream; the function keeps it and the type
// is ProcessLockHandle. An awaited Promise is a blocking call returning (value, error); a rejection with the abort's
// reason is `context.Cause(ctx)`. Node's `process.once("exit", removeFile)` is atExit (workers_exit.go): Go has no exit
// event, so a worker's own exit path runs it. A held lock's accepted connections are owned by one goroutine that Release
// joins, never detached.

// ProcessLockHandle is a held lock: Release frees it (the process's exit does too).
type ProcessLockHandle interface {
	Release() error
}

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
func ProcessLock(name string, ctx context.Context) (ProcessLockHandle, error) {
	sum := sha256.Sum256([]byte(name))
	key := hex.EncodeToString(sum[:])[:32]
	location, err := platformLockLocation(key)
	if err != nil {
		return nil, err
	}
	return lockAt(location, ctx)
}

// lockAt takes the lock at location, waiting while it is held. A socket file nobody accepts on (a crashed holder's) fails
// closed: taking it over races another taker (two could both unlink and listen), so the error names the file to remove.
func lockAt(location lockLocation, ctx context.Context) (ProcessLockHandle, error) {
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
	lock := &heldLock{listener: listener, waiters: map[net.Conn]struct{}{}, accepting: make(chan struct{})}
	if file {
		lock.removeExitHook = atExit(func() {
			// Gone already.
			_ = os.Remove(address)
		})
	}
	//portlint:allow golifetime the accept loop returns when Release closes the listener, and Release joins it
	go lock.accept()
	return lock, nil
}

// heldLock is a lock taken: its listening socket and the connections of the waiters, kept open and never answered.
type heldLock struct {
	listener       net.Listener
	removeExitHook func()
	accepting      chan struct{}

	mu       sync.Mutex
	waiters  map[net.Conn]struct{}
	released bool
	result   error
}

// accept keeps every waiter's connection open: a waiter learns of the release when its connection closes.
func (lock *heldLock) accept() {
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
		lock.mu.Unlock()
	}
}

// Release closes the lock's socket and its waiters' connections (they see them close) once it is gone; releasing again
// returns the first result.
func (lock *heldLock) Release() error {
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
