package runtimecell

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// cellReadyFile is the readiness marker written last into a published cache
// entry. Its presence plus a matching artifact size is what makes an entry
// usable; a directory without it (a legacy entry, or a writer that crashed
// mid-build) is treated as absent and rebuilt.
const (
	cellReadyFile   = "ready.json"
	cellFailureFile = "failure.json"
)

// cellEntryMeta records the identity and integrity of a published cell artifact.
// InputDigest is the content-addressed key (also the directory name); it proves
// the inputs. ArtifactDigest + Size prove the published bytes, so truncated or
// partially written entries are rejected. This is a Pig-owned, unversioned
// current-shape format.
type cellEntryMeta struct {
	InputDigest    string `json:"inputDigest"`
	ArtifactDigest string `json:"artifactDigest"`
	Size           int64  `json:"size"`
	Artifact       string `json:"artifact"`
	Language       string `json:"language,omitempty"`
	Target         string `json:"target"`
	Created        int64  `json:"created"`
}

type cellFailureMeta struct {
	InputDigest string `json:"inputDigest"`
	Language    string `json:"language"`
	Message     string `json:"message"`
	Created     int64  `json:"created"`
}

type cacheableBuildFailure struct {
	err error
}

func (e *cacheableBuildFailure) Error() string { return e.err.Error() }
func (e *cacheableBuildFailure) Unwrap() error { return e.err }

func cacheBuildFailure(err error) error {
	if err == nil {
		return nil
	}
	return &cacheableBuildFailure{err: err}
}

type cachedBuildFailureError struct {
	message string
}

func (e *cachedBuildFailureError) Error() string {
	return "cached build failure (inputs unchanged): " + e.message
}

// PublishedEntry is the result of resolving a content-addressed cache entry.
type PublishedEntry struct {
	Dir          string
	ArtifactPath string
	Reused       bool // an existing valid entry satisfied the request without building
}

// cellBuildTimeout bounds how long a caller with no deadline waits for a peer's
// build before giving up rather than blocking a session start forever. The
// expiry is a visible load error.
// pig additive (D20): the shared build cache and its lock have no upstream
// equivalent.
func cellBuildTimeout() time.Duration {
	if v := os.Getenv("PIG_CELL_BUILD_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 10 * time.Minute
}

// EntryIdentity is the publication identity a caller expects of a cache entry:
// the input digest that names it, its canonical artifact filename, and the
// language that built it.
type EntryIdentity struct {
	InputDigest string
	Artifact    string
	Language    string
}

func readCellFailureMetadata(finalDir string) (cellFailureMeta, bool) {
	data, err := os.ReadFile(filepath.Join(finalDir, cellFailureFile))
	if err != nil {
		return cellFailureMeta{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var meta cellFailureMeta
	if err := decoder.Decode(&meta); err != nil {
		return cellFailureMeta{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return cellFailureMeta{}, false
	}
	if meta.InputDigest == "" || meta.Language == "" || meta.Message == "" || meta.Created <= 0 {
		return cellFailureMeta{}, false
	}
	return meta, true
}

func cachedBuildFailure(finalDir, inputDigest, language string) (error, bool) {
	meta, ok := readCellFailureMetadata(finalDir)
	if !ok || meta.InputDigest != inputDigest || meta.Language != language {
		return nil, false
	}
	_ = TouchUsage(finalDir, time.Now())
	return &cachedBuildFailureError{message: meta.Message}, true
}

func publishCellFailure(finalDir, inputDigest, language, message string) error {
	parent := filepath.Dir(finalDir)
	scratch, err := os.MkdirTemp(parent, ".failed-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	data, err := json.Marshal(cellFailureMeta{
		InputDigest: inputDigest,
		Language:    language,
		Message:     message,
		Created:     time.Now().Unix(),
	})
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(scratch, cellFailureFile), data, 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(finalDir); err == nil {
		if err := os.RemoveAll(finalDir); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(scratch, finalDir); err != nil {
		return err
	}
	_ = TouchUsage(finalDir, time.Now())
	return nil
}

// validCellEntry reports whether finalDir holds a complete entry published for
// want on this platform and returns the absolute artifact path. The readiness
// marker must declare exactly the expected input digest, artifact, language, and
// target, record a SHA-256 artifact digest, and name an artifact that is a
// regular file of the recorded size directly inside the entry. A stale, foreign,
// or tampered entry is therefore never adopted. It does not re-hash the artifact
// on this hot path: publication verified the digest and a published entry is
// immutable.
func validCellEntry(finalDir string, want EntryIdentity) (string, bool) {
	meta, ok := readReadyMetadata(finalDir)
	if !ok || want.InputDigest == "" || meta.InputDigest != want.InputDigest || meta.Artifact != want.Artifact ||
		meta.Language != want.Language || meta.Target != runtime.GOOS+"/"+runtime.GOARCH || !isSHA256Hex(meta.ArtifactDigest) {
		return "", false
	}
	return filepath.Join(finalDir, meta.Artifact), true
}

func isSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// ReusePublishedArtifact returns the complete entry at finalDir published for
// want, if one exists, and records its use. It never builds or waits for a lock.
func ReusePublishedArtifact(finalDir string, want EntryIdentity) (PublishedEntry, bool) {
	art, ok := validCellEntry(finalDir, want)
	if !ok {
		return PublishedEntry{}, false
	}
	_ = TouchUsage(finalDir, time.Now())
	return PublishedEntry{Dir: finalDir, ArtifactPath: art, Reused: true}, true
}

// PublishArtifact deduplicates construction of a content-addressed cache entry
// across processes, for packed runtime cells and for compiled source-mode
// extension binaries alike. finalDir is the entry directory (named by inputDigest);
// canonicalName is the single published artifact's filename. build runs inside a
// private scratch directory and returns the finished primary artifact. The
// artifact can live in an external compiler cache; publication copies it into
// scratch before atomically renaming the complete entry.
//
// Exactly one process builds a given finalDir: callers take a per-digest advisory
// lock (released automatically on process death, so a crash never wedges the
// cache), re-check the cache under the lock, then build into private scratch and
// publish with a single atomic rename. Readers therefore never observe a
// half-written entry, distinct Pig instances share one built artifact instead of
// each rebuilding it, and only {canonicalName, ready.json} is retained -
// compiler intermediates (generated source, Rust target/) are discarded.
func PublishArtifact(ctx context.Context, finalDir, canonicalName, inputDigest, language string, build func(scratch string) (artifactPath string, err error), keepAlso ...string) (PublishedEntry, error) {
	return publishArtifact(ctx, finalDir, canonicalName, inputDigest, language, false, build, keepAlso...)
}

// publishArtifactWithFailureCache replays a compiler failure for the same content address.
// The build callback marks only deterministic compile errors with cacheBuildFailure.
// Setup, cancellation, and I/O errors remain retryable.
func publishArtifactWithFailureCache(ctx context.Context, finalDir, canonicalName, inputDigest, language string, build func(scratch string) (artifactPath string, err error), keepAlso ...string) (PublishedEntry, error) {
	return publishArtifact(ctx, finalDir, canonicalName, inputDigest, language, true, build, keepAlso...)
}

func publishArtifact(ctx context.Context, finalDir, canonicalName, inputDigest, language string, cacheBuildFailures bool, build func(scratch string) (artifactPath string, err error), keepAlso ...string) (PublishedEntry, error) {
	identity := EntryIdentity{InputDigest: inputDigest, Artifact: canonicalName, Language: language}
	if entry, ok := ReusePublishedArtifact(finalDir, identity); ok {
		return entry, nil
	}
	if cacheBuildFailures {
		if err, ok := cachedBuildFailure(finalDir, inputDigest, language); ok {
			return PublishedEntry{}, err
		}
	}

	parent := filepath.Dir(finalDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return PublishedEntry{}, fmt.Errorf("create cell cache: %w", err)
	}
	lockDir := filepath.Join(parent, ".locks")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return PublishedEntry{}, fmt.Errorf("create cell lock dir: %w", err)
	}

	lock := flock.New(filepath.Join(lockDir, inputDigest+".lock"))
	lockCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		lockCtx, cancel = context.WithTimeout(ctx, cellBuildTimeout())
		defer cancel()
	}
	// pig additive (D20): packed cells poll a process-shared build lock.
	locked, err := lock.TryLockContext(lockCtx, 50*time.Millisecond)
	if err != nil || !locked {
		// Timed out or cancelled waiting for the builder. Re-check once: a peer
		// may have finished: rather than starting a duplicate expensive build.
		if art, ok := validCellEntry(finalDir, identity); ok {
			return PublishedEntry{Dir: finalDir, ArtifactPath: art, Reused: true}, nil
		}
		if cacheBuildFailures {
			if cachedErr, ok := cachedBuildFailure(finalDir, inputDigest, language); ok {
				return PublishedEntry{}, cachedErr
			}
		}
		if err != nil {
			return PublishedEntry{}, fmt.Errorf("wait for cell build lock: %w", err)
		}
		return PublishedEntry{}, fmt.Errorf("timed out waiting for another process to build cell %s", filepath.Base(finalDir))
	}
	defer func() { _ = lock.Unlock() }()

	// A peer may have published while we waited for the lock.
	if art, ok := validCellEntry(finalDir, identity); ok {
		_ = TouchUsage(finalDir, time.Now())
		return PublishedEntry{Dir: finalDir, ArtifactPath: art, Reused: true}, nil
	}
	if cacheBuildFailures {
		if err, ok := cachedBuildFailure(finalDir, inputDigest, language); ok {
			return PublishedEntry{}, err
		}
	}

	// We are committed to an expensive build, so this is a cheap place (off the
	// cache-hit hot path) to reclaim scratch directories abandoned by a builder
	// that was killed mid-build. The age gate is far longer than any real build,
	// so this can never remove a concurrent build's live scratch.
	gcStaleScratch(parent)

	// Build in private scratch that is a sibling of finalDir, so the publish
	// rename stays on one filesystem (atomic, never EXDEV) and no other process
	// can observe or collide with the in-progress build.
	scratch, err := os.MkdirTemp(parent, ".build-*")
	if err != nil {
		return PublishedEntry{}, fmt.Errorf("create build scratch: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()

	artifactPath, err := build(scratch)
	if err != nil {
		var failure *cacheableBuildFailure
		if cacheBuildFailures && errors.As(err, &failure) {
			if cacheErr := publishCellFailure(finalDir, inputDigest, language, failure.Error()); cacheErr != nil {
				return PublishedEntry{}, errors.Join(err, fmt.Errorf("cache build failure: %w", cacheErr))
			}
		}
		return PublishedEntry{}, err
	}

	// Reduce the entry to the canonical artifact plus any extra names the caller
	// must keep (e.g. a Node launcher's sibling runtime tree); compiler
	// intermediates (generated source, Rust target/) are discarded.
	canonical := filepath.Join(scratch, canonicalName)
	if artifactPath != canonical {
		if err := stageArtifact(scratch, artifactPath, canonical); err != nil {
			return PublishedEntry{}, fmt.Errorf("stage cell artifact: %w", err)
		}
	}
	if err := pruneExcept(scratch, canonicalName, keepAlso...); err != nil {
		return PublishedEntry{}, err
	}
	if err := os.Chmod(canonical, 0o755); err != nil {
		return PublishedEntry{}, err
	}
	digest, size, err := hashFile(canonical)
	if err != nil {
		return PublishedEntry{}, err
	}
	meta, err := json.Marshal(cellEntryMeta{
		InputDigest:    inputDigest,
		ArtifactDigest: digest,
		Size:           size,
		Artifact:       canonicalName,
		Language:       language,
		Target:         runtime.GOOS + "/" + runtime.GOARCH,
		Created:        time.Now().Unix(),
	})
	if err != nil {
		return PublishedEntry{}, err
	}
	if err := os.WriteFile(filepath.Join(scratch, cellReadyFile), meta, 0o644); err != nil {
		return PublishedEntry{}, err
	}

	// Publish atomically. A leftover invalid entry (e.g. legacy, no marker) is
	// removed first; we hold the lock, so no peer is mid-publish here.
	if _, err := os.Stat(finalDir); err == nil {
		if err := os.RemoveAll(finalDir); err != nil {
			return PublishedEntry{}, fmt.Errorf("replace stale cell entry: %w", err)
		}
	}
	if err := os.Rename(scratch, finalDir); err != nil {
		if art, ok := validCellEntry(finalDir, identity); ok {
			return PublishedEntry{Dir: finalDir, ArtifactPath: art, Reused: true}, nil
		}
		return PublishedEntry{}, fmt.Errorf("publish cell entry: %w", err)
	}
	_ = TouchUsage(finalDir, time.Now())
	return PublishedEntry{Dir: finalDir, ArtifactPath: filepath.Join(finalDir, canonicalName), Reused: false}, nil
}

// staleScratchAge is how long an abandoned build scratch directory must be
// untouched before gcStaleScratch reclaims it. It is far longer than any real
// compile, so a live build's scratch (whose mtime advances as files are written)
// is never a candidate.
const staleScratchAge = 24 * time.Hour

// gcStaleScratch best-effort removes .build-* scratch directories under parent
// that a killed builder left behind. It never touches lock files (removing a
// held flock file would break mutual exclusion) and ignores errors.
func gcStaleScratch(parent string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleScratchAge)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ".build-") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(parent, e.Name()))
	}
}

func stageArtifact(scratch, source, destination string) error {
	relative, err := filepath.Rel(scratch, source)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return os.Rename(source, destination)
	}
	return copyArtifact(source, destination)
}

func copyArtifact(source, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, input.Close()) }()

	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	_, err = io.Copy(output, input)
	return err
}

// pruneExcept removes every top-level entry of dir except keep and any name in
// also. ready.json is written after pruning, so it is never a candidate here.
func pruneExcept(dir, keep string, also ...string) error {
	keepSet := make(map[string]struct{}, 1+len(also))
	keepSet[keep] = struct{}{}
	for _, k := range also {
		keepSet[k] = struct{}{}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, ok := keepSet[e.Name()]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// hashFile streams a file through SHA-256, returning the hex digest and byte size
// without loading the whole file into memory.
func cacheBuildEnvironment(workingDirectory string) []string {
	environment := slices.DeleteFunc(append([]string(nil), os.Environ()...), func(value string) bool {
		return strings.HasPrefix(value, "GIT_DIR=") || strings.HasPrefix(value, "GIT_WORK_TREE=") || strings.HasPrefix(value, "PWD=")
	})
	return append(environment, "PWD="+workingDirectory)
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// ValidEntry reports whether a cache directory contains a complete artifact
// published for want on this platform.
func ValidEntry(entryDir string, want EntryIdentity) (string, bool) {
	return validCellEntry(entryDir, want)
}

// ArtifactIdentity returns the immutable digest recorded when an entry was published.
func ArtifactIdentity(entryDir string) (string, bool) {
	meta, valid := readReadyMetadata(entryDir)
	if !valid || meta.ArtifactDigest == "" {
		return "", false
	}
	return meta.ArtifactDigest, true
}
