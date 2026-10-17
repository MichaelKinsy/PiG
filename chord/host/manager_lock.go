package host

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
)

// ProcessLock is manager.ts processLock(name): a process lock owned by the kernel for the holder's lifetime. An
// abstract socket (Linux, Android) or a named pipe (Windows) vanishes with its process, so there is no stale state to
// take over and no race between two would-be owners. Other platforms need a socket file, removed on release; a
// leftover one nobody accepts on is taken over. The socket file lives in the temporary directory, or in the private
// per-user directory /tmp/chord2-<uid> when that path does not fit in sun_path. The child acquires it (before opening
// storage), so the lock follows the process that owns the data. A held lock fails with unavailable/locked. The
// returned function releases it.
//
// Pi also removes the socket file at process exit; Go has no exit hook, so a file left by a process that exited
// without releasing is a stale lock the next holder takes over.
func ProcessLock(name string) (func() error, error) {
	address, file, err := lockAddress(lockKey(name))
	if err != nil {
		return nil, err
	}
	var listener net.Listener
	for {
		listener, err = listenLock(address)
		if err == nil {
			break
		}
		if !lockInUse(err) {
			return nil, err
		}
		if !file || accepting(address) {
			return nil, NewChordError(CodeUnavailable, "lock "+name+" is held by another process", ChordErrorOptions{Reason: "locked"})
		}
		if err := os.Remove(address); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	// createServer((socket) => socket.end()): a connection is accepted and ended.
	//portlint:allow golifetime the accept loop returns when the returned release closes the listener
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return listener.Close, nil
}

// lockKey is the first 32 hex digits of the name's SHA-256.
func lockKey(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])[:32]
}
