package runtimecell

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/execfile"
	"github.com/MichaelKinsy/PiG/internal/filelock"
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
	Cause       string `json:"cause"`
	Log         string `json:"log,omitempty"`
	// Drift is the SDK drift of the failure: a recorded failure keeps the changes the build named.
	Drift   []Drift `json:"drift,omitempty"` //portlint:allow emptydrop PiG-only cache record of a build failure; Pi has no such record and no reader tells absent from empty
	Created int64   `json:"created"`
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

// PublishedEntry is the result of resolving a content-addressed cache entry.
type PublishedEntry struct {
	Dir          string
	ArtifactPath string
	Reused       bool // an existing valid entry satisfied the request without building
	// Replaced is why the entry this call replaced failed its verification (PublishVerifiedArtifact); nil when nothing was replaced, or the replaced entry was not a ready entry that differed.
	Replaced error
}

// cellBuildTimeout bounds how long a caller with no deadline waits for a peer's
// build before giving up rather than blocking a session start forever. The
// expiry is a visible load error.
// pig additive (D20): the shared build cache and its lock have no upstream
// equivalent.
func cellBuildTimeout() time.Duration {
	return BuildTimeout()
}

// BuildTimeout is the time an extension build may take: PIG_CELL_BUILD_TIMEOUT
// as a Go duration, else ten minutes.
// pig additive (D20): the shared build cache and its build budget have no
// upstream equivalent.
func BuildTimeout() time.Duration {
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
	if meta.InputDigest == "" || meta.Language == "" || meta.Message == "" || meta.Cause == "" || meta.Created <= 0 {
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
	return &BuildFailure{Summary: meta.Message, Cause: meta.Cause, Log: meta.Log, Cached: true, Drift: meta.Drift}, true
}

func publishCellFailure(ctx context.Context, finalDir, inputDigest, language string, failure *BuildFailure) error {
	parent := filepath.Dir(finalDir)
	scratch, err := os.MkdirTemp(parent, ".failed-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	data, err := json.Marshal(cellFailureMeta{
		InputDigest: inputDigest,
		Language:    language,
		Message:     failure.Summary,
		Cause:       failure.Cause,
		Log:         failure.Log,
		Drift:       failure.Drift,
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
	// A peer may have published the same deterministic failure while this
	// publisher waited out a transient lock.
	if _, err := publishRename.publish(ctx, scratch, finalDir, func() bool {
		_, ok := cachedBuildFailure(finalDir, inputDigest, language)
		return ok
	}); err != nil {
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
// The check and the record happen under the entry's shared usage lock, which a
// collection holds exclusively while it renames an entry away: the entry is
// either reused and recorded before a collection can take the lock, or the
// collector has it and the entry is not reused.
func ReusePublishedArtifact(finalDir string, want EntryIdentity) (PublishedEntry, bool) {
	return reusePublishedArtifact(finalDir, entryCheck{identity: want})
}

// entryCheck is what makes a published entry usable as it is: it is a ready entry for identity, and verify, when there is one, finds nothing different in it.
type entryCheck struct {
	identity EntryIdentity
	verify   func(entryDir string) error
}

// adopt reports whether the entry at finalDir can be used as it is and returns its artifact path. When the entry is ready but verify finds it different, difference says how; an entry that is not ready is never verified, because it is replaced whatever it holds.
func (c entryCheck) adopt(finalDir string) (artifact string, ok bool, difference error) {
	artifact, ok = validCellEntry(finalDir, c.identity)
	if !ok || c.verify == nil {
		return artifact, ok, nil
	}
	if err := c.verify(finalDir); err != nil {
		return "", false, err
	}
	return artifact, true, nil
}

func reusePublishedArtifact(finalDir string, check entryCheck) (PublishedEntry, bool) {
	if _, ok, _ := check.adopt(finalDir); !ok {
		return PublishedEntry{}, false
	}
	lock, ok := tryLockUsageShared(finalDir)
	if !ok {
		return PublishedEntry{}, false
	}
	defer func() { _ = lock.Unlock() }()
	art, ok := validCellEntry(finalDir, check.identity)
	if !ok {
		return PublishedEntry{}, false
	}
	//portlint:allow clock a use of an entry records the real time, as AcquireUsageLease does; the Now seam dates collections
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
	return publishArtifact(ctx, finalDir, canonicalName, inputDigest, language, false, nil, build, keepAlso...)
}

// PublishVerifiedArtifact is PublishArtifact for an entry that holds more than its canonical artifact, content that
// ready.json does not vouch for. verify finds the difference between the entry directory it is given and what build
// would make, or returns nil; it runs wherever an existing entry would be reused, so a ready entry that differs is not reused.
// Such an entry is replaced under the per-digest lock, as a stale one is: after a verification under the lock, because a
// peer may have replaced it already, the entry is renamed aside and the new one renamed into its place, and the old one is
// deleted. A clean entry is never written. PublishedEntry.Replaced carries the difference verify found in the entry this
// call replaced.
func PublishVerifiedArtifact(ctx context.Context, finalDir, canonicalName, inputDigest, language string, verify func(entryDir string) error, build func(scratch string) (artifactPath string, err error), keepAlso ...string) (PublishedEntry, error) {
	return publishArtifact(ctx, finalDir, canonicalName, inputDigest, language, false, verify, build, keepAlso...)
}

// publishArtifactWithFailureCache replays a compiler failure for the same content address.
// The build callback marks only deterministic compile errors with cacheBuildFailure.
// Setup, cancellation, and I/O errors remain retryable.
func publishArtifactWithFailureCache(ctx context.Context, finalDir, canonicalName, inputDigest, language string, build func(scratch string) (artifactPath string, err error), keepAlso ...string) (PublishedEntry, error) {
	return publishArtifact(ctx, finalDir, canonicalName, inputDigest, language, true, nil, build, keepAlso...)
}

func publishArtifact(ctx context.Context, finalDir, canonicalName, inputDigest, language string, cacheBuildFailures bool, verify func(entryDir string) error, build func(scratch string) (artifactPath string, err error), keepAlso ...string) (PublishedEntry, error) {
	check := entryCheck{identity: EntryIdentity{InputDigest: inputDigest, Artifact: canonicalName, Language: language}, verify: verify}
	if entry, ok := reusePublishedArtifact(finalDir, check); ok {
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

	lock := filelock.New(filepath.Join(lockDir, inputDigest+".lock"))
	lockCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		lockCtx, cancel = context.WithTimeout(ctx, cellBuildTimeout())
		defer cancel()
	}
	// TryLockContext never tries a lock once its context is done, so a build whose context ended before it began fails here with the
	// lock free. No holder was waited for, and the error must not say so: a package run that used up its deadline starves every
	// build that starts after it, and a message about a lock sends the reader after a holder that does not exist.
	startedLate := ctx.Err() != nil
	// pig additive (D20): packed cells poll a process-shared build lock.
	locked, err := lock.TryLockContext(lockCtx, 50*time.Millisecond)
	if err != nil || !locked {
		// Timed out or cancelled waiting for the builder. Re-check once: a peer
		// may have finished: rather than starting a duplicate expensive build.
		if art, ok, _ := check.adopt(finalDir); ok {
			return PublishedEntry{Dir: finalDir, ArtifactPath: art, Reused: true}, nil
		}
		if cacheBuildFailures {
			if cachedErr, ok := cachedBuildFailure(finalDir, inputDigest, language); ok {
				return PublishedEntry{}, cachedErr
			}
		}
		if startedLate {
			return PublishedEntry{}, fmt.Errorf("build cell %s: its context had ended before the build began: %w", filepath.Base(finalDir), err)
		}
		if err != nil {
			return PublishedEntry{}, fmt.Errorf("wait for cell build lock: %w", filelock.Explain(parent, err))
		}
		return PublishedEntry{}, fmt.Errorf("timed out waiting for another process to build cell %s", filepath.Base(finalDir))
	}
	defer func() { _ = lock.Unlock() }()

	// A peer may have published (or replaced a differing entry) while we waited for the lock.
	art, ok, replaced := check.adopt(finalDir)
	if ok {
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
	removeReplacedAsides(parent, filepath.Base(finalDir))

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
			record, isReport := errors.AsType[*BuildFailure](failure.err)
			if !isReport {
				summary := oneLine(failure.err.Error())
				record = &BuildFailure{Summary: summary, Cause: summary, cause: failure.err}
			}
			if cacheErr := publishCellFailure(ctx, finalDir, inputDigest, language, record); cacheErr != nil {
				return PublishedEntry{}, errors.Join(err, fmt.Errorf("cache build failure: %w", cacheErr))
			}
			if !isReport {
				return PublishedEntry{}, record
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

	// Publish atomically. A leftover entry the check does not accept (legacy, no marker, or a ready entry whose content differs) is
	// renamed aside, not deleted in place: a peer that reads the entry meanwhile finds it absent and waits for the lock, never a
	// half-deleted tree. We hold the lock, so no peer is mid-publish here.
	// One Lstat tells whether an entry is there: verify, to say how it differs, and the rename aside both act on it only then.
	_, statErr := os.Lstat(finalDir) //perf:ok once per publication, after a build: the lstat that replaces the os.Stat of this place; a start that reuses its entry never reaches it
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return PublishedEntry{}, fmt.Errorf("replace stale cell entry: %w", statErr)
	}
	if statErr == nil {
		if replaced == nil && verify != nil {
			replaced = verify(finalDir)
		}
		aside, err := moveEntryAside(ctx, finalDir)
		if err != nil {
			return PublishedEntry{}, fmt.Errorf("replace stale cell entry: %w", err)
		}
		defer func() { _ = os.RemoveAll(aside) }()
	}
	var peerArtifact string
	adopted, err := publishRename.publish(ctx, scratch, finalDir, func() bool {
		art, ok, _ := check.adopt(finalDir)
		peerArtifact = art
		return ok
	})
	if err != nil {
		return PublishedEntry{}, fmt.Errorf("publish cell entry: %w", err)
	}
	if adopted {
		return PublishedEntry{Dir: finalDir, ArtifactPath: peerArtifact, Reused: true, Replaced: replaced}, nil
	}
	_ = TouchUsage(finalDir, time.Now())
	return PublishedEntry{Dir: finalDir, ArtifactPath: filepath.Join(finalDir, canonicalName), Reused: false, Replaced: replaced}, nil
}

// replacedEntryPrefix starts the name an entry is renamed to while a new one takes its place: the prefix of a build scratch
// directory, so that gcStaleScratch reclaims what a crash left, then replacedTagLen hex characters that tell two replacements
// apart, then "-" and the entry's own name, which tells entries apart.
const (
	replacedEntryPrefix = ".build-replaced-"
	replacedTagLen      = 16
)

// moveEntryAside renames the entry at finalDir (a folder or a link to one) to a name beside it and returns that name.
func moveEntryAside(ctx context.Context, finalDir string) (string, error) {
	var tag [replacedTagLen / 2]byte
	if _, err := rand.Read(tag[:]); err != nil {
		return "", err
	}
	aside := filepath.Join(filepath.Dir(finalDir), replacedEntryPrefix+hex.EncodeToString(tag[:])+"-"+filepath.Base(finalDir))
	// The same retry as the publishing rename: Windows refuses to rename a folder whose files a process has open, for a moment when a scanner holds one, for as long as a running program does.
	if _, err := publishRename.publish(ctx, finalDir, aside, func() bool { return false }); err != nil {
		return "", err
	}
	return aside, nil
}

// removeReplacedAsides deletes what an earlier replacement of the entry named base left beside it: it holds the entry's
// lock, and a replacement holds it from the rename aside to the delete, so none of these is in use.
func removeReplacedAsides(parent, base string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, entry := range entries {
		tagged, ok := strings.CutPrefix(entry.Name(), replacedEntryPrefix)
		if !ok || len(tagged) <= replacedTagLen+1 || tagged[replacedTagLen] != '-' || tagged[replacedTagLen+1:] != base {
			continue
		}
		_ = os.RemoveAll(filepath.Join(parent, entry.Name()))
	}
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

// copyArtifact copies a compiler's output, which sits outside the scratch directory when CARGO_TARGET_DIR names one. The copy is the runner the host executes once the entry is published, so execfile writes it: a concurrent fork must not leave a child that holds the copy's write descriptor.
func copyArtifact(source, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, input.Close()) }()
	return execfile.Write(destination, os.O_CREATE|os.O_EXCL, 0o600, input)
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
