package tsruntime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// probeTimeout bounds the --version start check of a slot runtime. Node's own preflight (node --version) has no bound of its own;
// a runtime that hangs here falls back to Node instead of holding every extension launch.
// pig additive (D19): the start check is part of the runtime slot, which Pi does not have.
const probeTimeout = 10 * time.Second

// Verifier checks slot executables against their manifests and the lock, once per executable state for the life of the process.
// Across processes, an executable whose file identity is unchanged since a passing check is not hashed again (see digestcache.go).
// On Linux the file it checks is the file it hands back to run (see executable.go).
type Verifier struct {
	lock  []LockEntry
	probe func(ctx context.Context, executable string) (string, error)
	// sum hashes an executable; settled reports whether a file's identity is old enough to be recorded. Tests replace them to count
	// hashes and to fix the moment the settle window is measured at.
	sum     func(io.Reader) (string, error)
	settled func(fileIdentity) bool

	mu   sync.Mutex
	done map[verifyKey]verifyResult
}

// verifyKey is one executable state. identity is the zero value where the platform has no trusted file identity.
type verifyKey struct {
	path     string
	size     int64
	mtimeNs  int64
	identity fileIdentity
	kind     Kind
	version  string
	digest   string
}

type verifyResult struct {
	reason Reason
	err    error
}

// NewVerifier returns a verifier over the given lock that starts each executable with --version.
func NewVerifier(lock []LockEntry) *Verifier {
	return &Verifier{lock: lock, probe: probeVersion, sum: sumSHA256, settled: func(i fileIdentity) bool { return i.settledAt(time.Now()) }, done: map[verifyKey]verifyResult{}}
}

// Verify checks that executable has the manifest's digest, that a Bun executable is in the lock, and that the executable starts
// and answers --version (a Bun executable with the manifest's version). It returns the reason with the error.
//
// On success it returns the verified executable: the open file that was checked, which a launch runs (Executable.ExecPath) so that
// the file that was verified is the file that runs. The caller releases it with Close once the process has started.
func (v *Verifier) Verify(ctx context.Context, m Manifest, executable, digest string) (*Executable, Reason, error) {
	file, err := os.Open(executable)
	if err != nil {
		return nil, ReasonVerifyFailed, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close() // Read-only: close cannot lose data.
		return nil, ReasonVerifyFailed, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close() // Read-only: close cannot lose data.
		return nil, ReasonVerifyFailed, errors.New("not a regular file")
	}
	exe := newExecutable(executable, file, info)
	key := verifyKey{path: executable, size: info.Size(), mtimeNs: info.ModTime().UnixNano(), identity: exe.identity, kind: m.Kind, version: m.Version, digest: digest}
	v.mu.Lock()
	defer v.mu.Unlock()
	result, ok := v.done[key]
	if !ok {
		result.reason, result.err = v.verify(ctx, m, exe, digest)
		// A cancelled check says nothing about the executable.
		if ctx.Err() == nil {
			v.done[key] = result
		}
	}
	if result.err != nil {
		_ = exe.Close()
		return nil, result.reason, result.err
	}
	exe.settle()
	return exe, "", nil
}

func (v *Verifier) verify(ctx context.Context, m Manifest, exe *Executable, digest string) (Reason, error) {
	if m.Kind == KindBun && !locked(v.lock, "bun", m.Version, m.Platform, digest) {
		return ReasonVerifyFailed, fmt.Errorf("bun %s for %s with digest %s is not in the runtime lock", m.Version, m.Platform, digest)
	}
	// pig additive (D19): an unchanged executable skips the hash; the digest comparison, the lock check above and the probe below still run.
	if err := v.checkDigest(exe, digest); err != nil {
		return ReasonVerifyFailed, err
	}
	// The probe starts the verified file too, so the file that answers --version is the file that was hashed.
	probePath, err := exe.probePath()
	if err != nil {
		return ReasonVerifyFailed, err
	}
	version, err := v.probe(ctx, probePath)
	if err != nil {
		if probePath != exe.path {
			err = errors.New(strings.ReplaceAll(err.Error(), probePath, exe.path))
		}
		return ReasonStartFailed, err
	}
	if m.Kind == KindBun && version != m.Version {
		return ReasonStartFailed, fmt.Errorf("--version printed %q, manifest says %q", version, m.Version)
	}
	return "", nil
}

func probeVersion(ctx context.Context, executable string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	output, err := linkerexec.CommandContext(ctx, executable, "--version").CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text != "" {
			return "", fmt.Errorf("--version: %w: %s", err, text)
		}
		return "", fmt.Errorf("--version: %w", err)
	}
	return text, nil
}
