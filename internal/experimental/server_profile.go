package experimental

// Ports packages/coding-agent/src/experimental/server.ts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// ServerProfile owns the launcher lock for one persistent logical server identity.
type ServerProfile struct {
	ServerID string
	release  func() error
}

// Release releases the launcher lock once; a second call fails like proper-lockfile's release.
func (p *ServerProfile) Release() error { return p.release() }

// errLockAlreadyReleased is proper-lockfile 4.1.2's ERELEASED error (lib/lockfile.js:255-258 (the ERELEASED branch of the release closure)).
var errLockAlreadyReleased = errors.New("Lock is already released")

// releaseOnce returns proper-lockfile's release function: the first call releases and later calls reject.
func releaseOnce(release func() error) func() error {
	var released atomic.Bool
	return func() error {
		if released.Swap(true) {
			return errLockAlreadyReleased
		}
		return release()
	}
}

// serverIdPattern mirrors packages/protocol/src/protocol.ts:12-18: canonical lowercase UUID v4 only.
var serverIdPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// trimECMAScript includes BOM but excludes U+0085, matching String.prototype.trim and Number string conversion.
func trimECMAScript(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return strings.ContainsRune("\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff", r)
	})
}

// AcquireServerProfile serializes launchers of the same ID. Nil requestedServerID selects or atomically creates the directory's default identity.
func AcquireServerProfile(ctx context.Context, directory string, requestedServerID *string) (*ServerProfile, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	var id string
	if requestedServerID != nil {
		id = *requestedServerID
		if !serverIdPattern.MatchString(id) {
			return nil, fmt.Errorf("Invalid experimental server ID: %s", id)
		}
	} else {
		path := filepath.Join(directory, "default-server-id")
		read := func() (string, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			value := trimECMAScript(string(data))
			if !serverIdPattern.MatchString(value) {
				return "", fmt.Errorf("Invalid default experimental server identity in %s", path)
			}
			return value, nil
		}
		var err error
		id, err = read()
		if errors.Is(err, os.ErrNotExist) {
			candidate := uuid.NewString()
			file, writeErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if writeErr == nil {
				_, writeErr = file.WriteString(candidate)
				writeErr = errors.Join(writeErr, file.Close())
				id = candidate
			}
			if errors.Is(writeErr, os.ErrExist) {
				id, writeErr = read()
			}
			err = writeErr
		}
		if err != nil {
			return nil, err
		}
	}
	// upstream: packages/coding-agent/src/experimental/server.ts:LOCK_STALE_MS
	lock, err := pilock.AcquireWithOptions(ctx, filepath.Join(directory, "launcher-"+id), pilock.AcquireOptions{Stale: 30_000 * time.Millisecond, Update: 10_000 * time.Millisecond, Retry: 25 * time.Millisecond, Wait: 30_000 * time.Millisecond, OnCompromised: lockCompromised})
	if err != nil {
		return nil, err
	}
	return &ServerProfile{ServerID: id, release: releaseOnce(lock.Release)}, nil
}
