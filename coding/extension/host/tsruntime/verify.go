package tsruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
type Verifier struct {
	lock  []LockEntry
	probe func(ctx context.Context, executable string) (string, error)

	mu   sync.Mutex
	done map[verifyKey]verifyResult
}

type verifyKey struct {
	path    string
	size    int64
	mtimeNs int64
	kind    Kind
	version string
	digest  string
}

type verifyResult struct {
	reason Reason
	err    error
}

// NewVerifier returns a verifier over the given lock that starts each executable with --version.
func NewVerifier(lock []LockEntry) *Verifier {
	return &Verifier{lock: lock, probe: probeVersion, done: map[verifyKey]verifyResult{}}
}

// Verify checks that executable has the manifest's digest, that a Bun executable is in the lock, and that the executable starts
// and answers --version (a Bun executable with the manifest's version). It returns the reason with the error.
func (v *Verifier) Verify(ctx context.Context, m Manifest, executable, digest string) (Reason, error) {
	info, err := os.Stat(executable)
	if err != nil {
		return ReasonVerifyFailed, err
	}
	if !info.Mode().IsRegular() {
		return ReasonVerifyFailed, errors.New("not a regular file")
	}
	key := verifyKey{path: executable, size: info.Size(), mtimeNs: info.ModTime().UnixNano(), kind: m.Kind, version: m.Version, digest: digest}
	v.mu.Lock()
	defer v.mu.Unlock()
	if result, ok := v.done[key]; ok {
		return result.reason, result.err
	}
	reason, err := v.verify(ctx, m, executable, digest)
	// A cancelled check says nothing about the executable.
	if ctx.Err() == nil {
		v.done[key] = verifyResult{reason: reason, err: err}
	}
	return reason, err
}

func (v *Verifier) verify(ctx context.Context, m Manifest, executable, digest string) (Reason, error) {
	if m.Kind == KindBun && !locked(v.lock, "bun", m.Version, m.Platform, digest) {
		return ReasonVerifyFailed, fmt.Errorf("bun %s for %s with digest %s is not in the runtime lock", m.Version, m.Platform, digest)
	}
	actual, err := fileDigest(executable)
	if err != nil {
		return ReasonVerifyFailed, err
	}
	if actual != digest {
		return ReasonVerifyFailed, fmt.Errorf("digest %s, manifest says %s", actual, digest)
	}
	version, err := v.probe(ctx, executable)
	if err != nil {
		return ReasonStartFailed, err
	}
	if m.Kind == KindBun && version != m.Version {
		return ReasonStartFailed, fmt.Errorf("--version printed %q, manifest says %q", version, m.Version)
	}
	return "", nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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
