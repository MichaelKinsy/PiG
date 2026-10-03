package durableagent

// Ports packages/coding-agent/src/experimental/durable/sessions.ts and packages/coding-agent/src/experimental/vacation/sessions.ts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// SessionLocation is one session directory holding session.sqlite, locked by this process.
type SessionLocation struct {
	ID        string
	Directory string
	Database  string
	CWD       string
	Created   bool
	// Release releases the directory lock.
	Release func() error
}

// sessionDirectoryName is the name of a session directory: the creation time in milliseconds, padded to 13 digits, and a UUID.
var sessionDirectoryName = regexp.MustCompile(`^\d{13}-[0-9a-f-]{36}$`)

// sessionLockWait is how long a second process waits for a session lock: 12 retries one second apart. A lock left by a crashed process goes stale after 10 s, so the wait outlasts it.
var sessionLockWait = 12 * time.Second

// terminateOnLockCompromise is proper-lockfile's default onCompromised, which throws from the heartbeat timer and so ends the process: the error is printed, the other held locks are removed, and the exit status is 1.
func terminateOnLockCompromise(err error) {
	text := err.Error()
	if compromised := (*pilock.CompromisedError)(nil); errors.As(err, &compromised) {
		text = compromised.Inspect()
	}
	fmt.Fprintf(os.Stderr, "%s\n", text)
	pilock.RemoveHeldLocks()
	os.Exit(1)
}

// SelectSession returns a new session for cwdInput, or its newest one with continueSession. kind is "durable" or "vacation": the sessions live in <kind>-sessions under the agent directory's experimental directory.
func SelectSession(ctx context.Context, kind, cwdInput string, continueSession bool) (*SessionLocation, error) {
	absolute, err := filepath.Abs(cwdInput)
	if err != nil {
		return nil, err
	}
	cwd, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(cwd))
	root := filepath.Join(codingagent.AgentDir(), "experimental", kind+"-sessions", hex.EncodeToString(digest[:])[:24])
	if err := os.MkdirAll(root, 0o777); err != nil {
		return nil, err
	}

	var directory string
	created := false
	if continueSession {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, err
		}
		var names []string
		for _, entry := range entries {
			if entry.IsDir() && sessionDirectoryName.MatchString(entry.Name()) {
				names = append(names, entry.Name())
			}
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("No %s session exists for %s", kind, cwd)
		}
		directory = filepath.Join(root, slices.Max(names))
	} else {
		directory = filepath.Join(root, fmt.Sprintf("%013d-%s", time.Now().UnixMilli(), uuid.NewString()))
		if err := os.Mkdir(directory, 0o777); err != nil {
			return nil, err
		}
		created = true
	}

	lock, err := pilock.AcquireWithOptions(ctx, directory, pilock.AcquireOptions{
		Stale: 10 * time.Second, Update: 5 * time.Second, Retry: time.Second, Wait: sessionLockWait, OnCompromised: terminateOnLockCompromise,
	})
	if err != nil {
		return nil, &sessionLockedError{directory: directory, cause: err}
	}
	return &SessionLocation{ID: filepath.Base(directory), Directory: directory, Database: filepath.Join(directory, "session.sqlite"), CWD: cwd, Created: created, Release: lock.Release}, nil
}

// sessionLockedError is sessions.ts's `new Error("Session is already open in another process: ...", { cause })`: the message names only the directory, and the lock failure stays reachable as the cause.
type sessionLockedError struct {
	directory string
	cause     error
}

func (err *sessionLockedError) Error() string {
	return "Session is already open in another process: " + err.directory
}

func (err *sessionLockedError) Unwrap() error { return err.cause }
