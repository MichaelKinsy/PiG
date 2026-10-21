package runtimecell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/internal/filelock"
)

const (
	usageFile         = "usage.json"
	defaultRetention  = 30 * 24 * time.Hour
	defaultCrashGrace = 24 * time.Hour
)

type CacheClass string

const (
	CacheActive           CacheClass = "active"
	CacheCurrent          CacheClass = "current"
	CacheBuilding         CacheClass = "building"
	CacheInactiveRetained CacheClass = "inactive-retained"
	CacheInactiveExpired  CacheClass = "inactive-expired"
	CacheInvalid          CacheClass = "incomplete/invalid"
)

type UsageMetadata struct {
	LastUsed int64 `json:"lastUsed"`
}

type CacheEntry struct {
	Path     string     `json:"path"`
	Class    CacheClass `json:"class"`
	Bytes    int64      `json:"bytes"`
	LastUsed int64      `json:"lastUsed,omitempty"`
	Reason   string     `json:"reason,omitempty"`
	// Failure marks a recorded build failure, not a built artifact.
	Failure bool `json:"failure,omitempty"`
	// use is what the collection read of the entry's ready marker and usage sidecar when it classified the entry. A
	// removal compares it with a second read under the entry's exclusive lock: a difference means a peer reused or
	// republished the entry after the classification.
	use entryUse
}

type CacheReport struct {
	Entries      []CacheEntry       `json:"entries"`
	ByClass      map[CacheClass]int `json:"byClass"`
	TotalBytes   int64              `json:"totalBytes"`
	RemovedBytes int64              `json:"removedBytes"`
	Removed      int                `json:"removed"`
	// Skipped counts entries a collection left in place because they were in use: a usage lease or build lock
	// held, or a file the file system reported busy. A skipped entry is not an error; the next collection retries it.
	Skipped        int      `json:"skipped,omitempty"`
	ProtectedBytes int64    `json:"protectedBytes"`
	LimitSatisfied bool     `json:"limitSatisfied"`
	Errors         []string `json:"errors,omitempty"`
}

type CacheLifecycleOptions struct {
	// Context stops the collection between entries and inside an entry's tree. Nil means no cancellation.
	Context          context.Context
	CacheRoot        string
	Current          map[string]struct{}
	LiveFingerprints map[string]struct{}
	Now              func() time.Time
	Retention        time.Duration
	CrashGrace       time.Duration
	MaxSize          *int64
	// RemoveFailures removes every recorded build failure, so the next start
	// compiles those cells again. A failure whose build is in progress stays.
	RemoveFailures bool
	// Unsized skips reading the size of every file in every entry: Bytes, TotalBytes, ProtectedBytes and RemovedBytes stay zero. A caller that reads none of them sets it, because the sizes cost one stat per cached file. MaxSize needs the sizes and overrides it.
	Unsized   bool
	DryRun    bool
	Rename    func(string, string) error
	RemoveAll func(string) error
}

type UsageLease struct {
	lock filelock.Lock
}

func (l *UsageLease) Release() error {
	if l == nil || l.lock == nil {
		return nil
	}
	return l.lock.Unlock()
}

func MarkArtifactUsed(artifactPath string, now time.Time) error {
	entryDir := filepath.Dir(artifactPath)
	if _, valid := readReadyMetadata(entryDir); !valid {
		return nil
	}
	return TouchUsage(entryDir, now)
}

// AcquireArtifactUsageLease takes the shared usage lease on the cache entry that holds artifactPath, so a collection leaves the
// entry alone until the lease is released. A path outside the governed cache (a prebuilt binary, a source script) has no entry, and
// the lease is nil. An artifact that a cache entry held and that is gone is an error, not a nil lease: another pig's collection
// removed the entry after this process resolved it, and a nil lease would let the caller's spawn fail with a bare missing file while
// the use of the entry went untracked.
func AcquireArtifactUsageLease(artifactPath string) (*UsageLease, error) {
	entryDir := filepath.Dir(artifactPath)
	if _, valid := readReadyMetadata(entryDir); !valid {
		if cacheEntryRemoved(entryDir, artifactPath) {
			return nil, errCacheEntryRemoved(artifactPath)
		}
		return nil, nil
	}
	lock, err := lockUsageShared(entryDir)
	if err != nil {
		return nil, err
	}
	// A collection cannot remove the entry while the lease is held, so what this read finds stays.
	if current, valid := readReadyMetadata(entryDir); !valid || filepath.Join(entryDir, current.Artifact) != filepath.Clean(artifactPath) {
		_ = lock.Unlock()
		if cacheEntryRemoved(entryDir, artifactPath) {
			return nil, errCacheEntryRemoved(artifactPath)
		}
		return nil, fmt.Errorf("cache artifact %s changed while acquiring its usage lease", artifactPath)
	}
	//portlint:allow clock a lease records the real time of a use, as AcquireUsageLease does; the Now seam dates collections
	if err := TouchUsage(entryDir, time.Now()); err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	return &UsageLease{lock: lock}, nil
}

func errCacheEntryRemoved(artifactPath string) error {
	return fmt.Errorf("cache artifact %s no longer exists: another pig's cache collection removed its entry after this process resolved it; start the extension again to rebuild it", artifactPath)
}

// cacheEntryRemoved reports whether artifactPath was a file of a governed cache entry that is gone: it does not exist and its entry
// directory sits beside the cache's .locks directory. A path outside the cache has no such directory beside it.
func cacheEntryRemoved(entryDir, artifactPath string) bool {
	if _, err := os.Lstat(artifactPath); !os.IsNotExist(err) { //perf:ok cold: runs only for a lease whose entry is not valid
		return false
	}
	info, err := os.Stat(filepath.Dir(cacheLockPath(entryDir, ""))) //perf:ok cold: runs only for a lease whose entry is not valid
	return err == nil && info.IsDir()
}

// lockUsageShared takes the shared usage lock of entryDir, waiting for a collection that holds it exclusively while it renames the
// entry away.
func lockUsageShared(entryDir string) (filelock.Lock, error) {
	lock, err := newUsageLock(entryDir)
	if err != nil {
		return nil, err
	}
	if err := lock.RLock(); err != nil {
		return nil, filelock.Explain(filepath.Dir(entryDir), err)
	}
	return lock, nil
}

// tryLockUsageShared is lockUsageShared without the wait: it reports false when a collection holds the lock or it cannot be taken.
func tryLockUsageShared(entryDir string) (filelock.Lock, bool) {
	lock, err := newUsageLock(entryDir)
	if err != nil {
		return nil, false
	}
	if locked, err := lock.TryRLock(); err != nil || !locked {
		return nil, false
	}
	return lock, true
}

func newUsageLock(entryDir string) (filelock.Lock, error) {
	lockPath := cacheLockPath(entryDir, ".usage.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	return filelock.New(lockPath), nil
}

func AcquireUsageLease(entryDir string) (*UsageLease, error) {
	lock, err := lockUsageShared(entryDir)
	if err != nil {
		return nil, err
	}
	if err := TouchUsage(entryDir, time.Now()); err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	return &UsageLease{lock: lock}, nil
}

func TouchUsage(entryDir string, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	if data, err := os.ReadFile(filepath.Join(entryDir, usageFile)); err == nil {
		var current UsageMetadata
		if json.Unmarshal(data, &current) == nil && current.LastUsed > 0 {
			last := time.Unix(current.LastUsed, 0)
			if last.After(now) || now.Sub(last) < time.Hour {
				return nil
			}
		}
	}
	data, err := json.Marshal(UsageMetadata{LastUsed: now.Unix()})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(entryDir, ".usage-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(entryDir, usageFile))
}

func InspectCache(options CacheLifecycleOptions) (CacheReport, error) {
	return inspectOrPruneCache(options, false)
}

func PruneCaches(options CacheLifecycleOptions) (CacheReport, error) {
	return inspectOrPruneCache(options, true)
}

func inspectOrPruneCache(options CacheLifecycleOptions, prune bool) (CacheReport, error) {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Retention <= 0 {
		options.Retention = defaultRetention
	}
	if options.CrashGrace <= 0 {
		options.CrashGrace = defaultCrashGrace
	}
	if options.Rename == nil {
		options.Rename = os.Rename
	}
	if options.Context == nil {
		options.Context = context.Background()
	}
	ctx := options.Context
	if options.RemoveAll == nil {
		options.RemoveAll = func(path string) error { return removeTreeContext(ctx, path, os.Remove) }
	}
	report := CacheReport{Entries: make([]CacheEntry, 0), ByClass: make(map[CacheClass]int), LimitSatisfied: true}
	if options.CacheRoot == "" {
		return report, errors.New("cache root is required")
	}

	var gcLock filelock.Lock
	if prune {
		if err := os.MkdirAll(options.CacheRoot, 0o755); err != nil {
			return report, err
		}
		gcLock = filelock.New(filepath.Join(options.CacheRoot, ".gc.lock"))
		if _, err := gcLock.TryLockContext(ctx, gcLockRetry); err != nil {
			return report, fmt.Errorf("lock extension cache GC: %w", filelock.Explain(options.CacheRoot, err))
		}
		defer func() { _ = gcLock.Unlock() }()
	}

	paths, err := cacheEntryPaths(options.CacheRoot)
	if err != nil {
		return report, err
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		entry := classifyCacheEntry(path, options)
		report.Entries = append(report.Entries, entry)
		report.ByClass[entry.Class]++
		report.TotalBytes += entry.Bytes
		if entry.Class == CacheActive || entry.Class == CacheCurrent || entry.Class == CacheBuilding {
			report.ProtectedBytes += entry.Bytes
		}
	}

	remove := make(map[string]bool)
	for _, entry := range report.Entries {
		if entry.Class == CacheInactiveExpired || entry.Class == CacheInvalid && (entry.Reason == "past crash grace" || entry.Reason == "managed tombstone") {
			remove[entry.Path] = true
		}
	}
	if options.RemoveFailures {
		for _, entry := range report.Entries {
			if entry.Failure && entry.Class != CacheActive && entry.Class != CacheBuilding {
				remove[entry.Path] = true
			}
		}
	}
	if options.MaxSize != nil {
		remaining := report.TotalBytes
		for _, entry := range report.Entries {
			if remove[entry.Path] {
				remaining -= entry.Bytes
			}
		}
		candidates := slices.Clone(report.Entries)
		slices.SortFunc(candidates, func(a, b CacheEntry) int {
			if a.LastUsed < b.LastUsed {
				return -1
			}
			if a.LastUsed > b.LastUsed {
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
		for _, entry := range candidates {
			if remaining <= *options.MaxSize {
				break
			}
			if remove[entry.Path] || entry.Class != CacheInactiveRetained {
				continue
			}
			remove[entry.Path] = true
			remaining -= entry.Bytes
		}
		report.LimitSatisfied = remaining <= *options.MaxSize
	}

	if !prune {
		return report, nil
	}
	for _, entry := range report.Entries {
		if !remove[entry.Path] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if options.DryRun {
			report.Removed++
			report.RemovedBytes += entry.Bytes
			continue
		}
		if strings.HasPrefix(filepath.Base(entry.Path), ".tombstone-") {
			if err := options.RemoveAll(entry.Path); err != nil {
				report.recordFailure(ctx, fmt.Sprintf("remove tombstone %s: %v", entry.Path, err), err)
				continue
			}
			report.Removed++
			report.RemovedBytes += entry.Bytes
			continue
		}
		lockPath := cacheLockPath(entry.Path, ".usage.lock")
		if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("prepare lock for %s: %v", entry.Path, err))
			continue
		}
		usageLock := filelock.New(lockPath)
		locked, lockErr := usageLock.TryLock()
		if lockErr != nil || !locked {
			report.Skipped++
			continue
		}
		buildLock := filelock.New(cacheBuildLockPath(entry.Path))
		buildLocked, buildLockErr := buildLock.TryLock()
		if buildLockErr != nil || !buildLocked {
			_ = usageLock.Unlock()
			if buildLockErr != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("protect %s: build lock: %v", entry.Path, buildLockErr))
			} else {
				report.Skipped++
			}
			continue
		}
		// A peer that reused the entry after the classification read it rewrote the usage record but holds no lease yet, so only
		// this read, under both locks, shows the entry is wanted. The peer's own reuse is held off by the lock, so it either finds
		// the entry before the rename or finds it gone and rebuilds.
		if readEntryUse(entry.Path) != entry.use {
			_ = buildLock.Unlock()
			_ = usageLock.Unlock()
			report.Skipped++
			continue
		}
		tombstone := filepath.Join(filepath.Dir(entry.Path), fmt.Sprintf(".tombstone-%s-%d", filepath.Base(entry.Path), tombstoneSequence.Add(1)))
		if err := options.Rename(entry.Path, tombstone); err != nil {
			_ = buildLock.Unlock()
			_ = usageLock.Unlock()
			report.recordFailure(ctx, fmt.Sprintf("rename %s: %v", entry.Path, err), err)
			continue
		}
		_ = buildLock.Unlock()
		_ = usageLock.Unlock()
		if err := options.RemoveAll(tombstone); err != nil {
			report.recordFailure(ctx, fmt.Sprintf("remove tombstone %s: %v", tombstone, err), err)
			continue
		}
		report.Removed++
		report.RemovedBytes += entry.Bytes
	}
	if options.MaxSize != nil && !options.DryRun {
		report.LimitSatisfied = report.TotalBytes-report.RemovedBytes <= *options.MaxSize
	}
	// A cancellation during the last entry, whose failure may then have counted as busy or whose classification was cut
	// short, means the collection did not finish.
	if err := ctx.Err(); err != nil {
		return report, err
	}
	return report, nil
}

var tombstoneSequence atomic.Uint64

// gcLockRetry is how often a collection waiting for another collector's lock retries it while its context is live.
// pig additive (D20): packed-runtime cache collections poll the shared collector lock like the cell build lock.
const gcLockRetry = 50 * time.Millisecond

// recordFailure counts a removal that failed only because the file system reported busy files as a skipped
// entry, and any other failure as an error. A cancelled collection records neither: the caller returns the
// context error.
func (r *CacheReport) recordFailure(ctx context.Context, message string, err error) {
	switch {
	case ctx.Err() != nil && errors.Is(err, ctx.Err()):
	case isBusyError(err):
		r.Skipped++
	default:
		r.Errors = append(r.Errors, message)
	}
}

// isBusyError reports whether every failure in err is a file the file system reports in use: EBUSY or ETXTBSY,
// a sharing or lock violation on Windows, or any failure on an NFS `.nfs*` file, which NFS creates in place of
// deleting a file that is open elsewhere. It follows the whole wrap chain, so a wrapped join with one failure
// that is not busy is not busy.
func isBusyError(err error) bool {
	for err != nil {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			children := joined.Unwrap()
			for _, child := range children {
				if !isBusyError(child) {
					return false
				}
			}
			return len(children) > 0
		}
		// The walk inspects one wrap level at a time. errors.As would descend into joined failures and accept a busy
		// leaf beside one that is not busy.
		if pathErr, ok := err.(*os.PathError); ok && strings.HasPrefix(filepath.Base(pathErr.Path), ".nfs") { //nolint:errorlint // one wrap level per step; errors.As descends into joins.
			return true
		}
		if errno, ok := err.(syscall.Errno); ok { //nolint:errorlint // one wrap level per step; errors.As descends into joins.
			// ERROR_SHARING_VIOLATION and ERROR_LOCK_VIOLATION: another process holds the file open.
			return errno == syscall.EBUSY || errno == syscall.ETXTBSY || runtime.GOOS == "windows" && (errno == 32 || errno == 33)
		}
		err = errors.Unwrap(err)
	}
	return false
}

// removeTreeContext removes path and everything under it, stopping when ctx is cancelled. A failure to remove
// one file does not stop the rest, as os.RemoveAll does not; the directories that still hold a failed file are
// not reported again, so the result names only the files that could not be removed.
func removeTreeContext(ctx context.Context, path string, remove func(string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		if err := remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if err := removeTreeContext(ctx, filepath.Join(path, entry.Name()), remove); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if err := remove(path); err != nil && !os.IsNotExist(err) {
		return heldBySillyRenames(path, err)
	}
	return nil
}

// heldBySillyRenames explains a directory that could not be removed. NFS removes a file that a process on this
// client holds open by renaming it to `.nfs*` and reports success, so the directory still holds that file and its
// removal fails with ENOTEMPTY. When every remaining entry is such a file, the result names them, which
// isBusyError counts as busy; otherwise it is err.
func heldBySillyRenames(dir string, err error) error {
	entries, readErr := os.ReadDir(dir)
	if readErr != nil || len(entries) == 0 {
		return err
	}
	held := make([]error, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".nfs") {
			return err
		}
		held = append(held, &os.PathError{Op: "remove", Path: filepath.Join(dir, entry.Name()), Err: err})
	}
	return errors.Join(held...)
}

// HasCacheEntries reports whether PruneCaches and InspectCache would classify at least one entry under cacheRoot.
func HasCacheEntries(cacheRoot string) (bool, error) {
	paths, err := cacheEntryPaths(cacheRoot)
	return len(paths) > 0, err
}

func cacheEntryPaths(cacheRoot string) ([]string, error) {
	var paths []string
	extRoot := filepath.Join(cacheRoot, "ext")
	if entries, err := os.ReadDir(extRoot); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && (!strings.HasPrefix(entry.Name(), ".") || strings.HasPrefix(entry.Name(), ".tombstone-")) {
				paths = append(paths, filepath.Join(extRoot, entry.Name()))
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	cellsRoot := filepath.Join(cacheRoot, "cells")
	if languages, err := os.ReadDir(cellsRoot); err == nil {
		for _, language := range languages {
			if !language.IsDir() || strings.HasPrefix(language.Name(), ".") {
				continue
			}
			languageRoot := filepath.Join(cellsRoot, language.Name())
			entries, readErr := os.ReadDir(languageRoot)
			if readErr != nil {
				return nil, readErr
			}
			for _, entry := range entries {
				if entry.IsDir() && (!strings.HasPrefix(entry.Name(), ".") || strings.HasPrefix(entry.Name(), ".tombstone-")) {
					paths = append(paths, filepath.Join(languageRoot, entry.Name()))
				}
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	slices.Sort(paths)
	return paths, nil
}

// entryUse is the raw content of an entry's ready marker and usage sidecar. A peer's reuse rewrites the sidecar and a republication
// replaces the marker, so two reads that differ show the entry was used between them.
type entryUse struct{ ready, usage string }

func readEntryUse(path string) entryUse {
	ready, _ := os.ReadFile(filepath.Join(path, cellReadyFile))
	usage, _ := os.ReadFile(filepath.Join(path, usageFile))
	return entryUse{ready: string(ready), usage: string(usage)}
}

func classifyCacheEntry(path string, options CacheLifecycleOptions) CacheEntry {
	// Read before anything the classification decides from, so a use recorded after any of its reads differs from the stamp.
	entry := CacheEntry{Path: path, use: readEntryUse(path)}
	if !options.Unsized || options.MaxSize != nil {
		entry.Bytes = cacheDirSize(options.Context, path)
	}
	if _, built := readReadyMetadata(path); !built {
		_, entry.Failure = readCellFailureMetadata(path)
	}
	if strings.HasPrefix(filepath.Base(path), ".tombstone-") {
		entry.Class = CacheInvalid
		entry.Reason = "managed tombstone"
		return entry
	}
	if lockHeld(cacheLockPath(path, ".usage.lock")) {
		entry.Class = CacheActive
		return entry
	}
	if _, current := options.Current[filepath.Clean(path)]; current {
		entry.Class = CacheCurrent
		return entry
	}
	if lockHeld(cacheBuildLockPath(path)) || strings.HasPrefix(filepath.Base(path), ".build-") {
		entry.Class = CacheBuilding
		return entry
	}

	created := int64(0)
	if meta, valid := readReadyMetadata(path); valid {
		created = meta.Created
	} else if failure, failed := readCellFailureMetadata(path); failed {
		created = failure.Created
		entry.Reason = "cached build failure"
	} else {
		info, _ := os.Stat(path)
		if info != nil && !info.ModTime().After(options.Now().Add(-options.CrashGrace)) {
			entry.Reason = "past crash grace"
		} else {
			entry.Reason = "within crash grace"
		}
		entry.Class = CacheInvalid
		return entry
	}
	entry.LastUsed = created
	if data, err := os.ReadFile(filepath.Join(path, usageFile)); err == nil {
		var usage UsageMetadata
		if json.Unmarshal(data, &usage) == nil && usage.LastUsed > 0 {
			entry.LastUsed = usage.LastUsed
		} else {
			entry.LastUsed = options.Now().Unix()
			entry.Reason = "malformed usage metadata retained conservatively"
		}
	} else {
		entry.LastUsed = options.Now().Unix()
		entry.Reason = "missing usage metadata retained conservatively"
	}
	now := options.Now().Unix()
	if entry.LastUsed > now {
		entry.LastUsed = now
	}
	if fingerprint, err := os.ReadFile(filepath.Join(path, ".sdk-fingerprint")); err == nil && len(options.LiveFingerprints) > 0 {
		if _, live := options.LiveFingerprints[strings.TrimSpace(string(fingerprint))]; !live {
			entry.Class = CacheInactiveExpired
			entry.Reason = "obsolete SDK fingerprint"
			return entry
		}
	}
	if !time.Unix(entry.LastUsed, 0).Before(options.Now().Add(-options.Retention)) {
		entry.Class = CacheInactiveRetained
		return entry
	}
	entry.Class = CacheInactiveExpired
	entry.Reason = "retention expired"
	return entry
}

func readReadyMetadata(path string) (cellEntryMeta, bool) {
	data, err := os.ReadFile(filepath.Join(path, cellReadyFile))
	if err != nil {
		return cellEntryMeta{}, false
	}
	var meta cellEntryMeta
	if json.Unmarshal(data, &meta) != nil || meta.Created <= 0 || !entryBoundToDigest(path, meta.InputDigest) || !plainEntryName(meta.Artifact) {
		return cellEntryMeta{}, false
	}
	// Lstat, so a symlink can never stand in for a published artifact.
	info, err := os.Lstat(filepath.Join(path, meta.Artifact))
	if err != nil || !info.Mode().IsRegular() || info.Size() != meta.Size {
		return cellEntryMeta{}, false
	}
	return meta, true
}

// entryBoundToDigest reports whether an entry directory is named by the input
// digest its metadata declares: packed cells use <digest>, source builds use
// <name>-<digest>.
func entryBoundToDigest(entryDir, inputDigest string) bool {
	base := filepath.Base(entryDir)
	return inputDigest != "" && (base == inputDigest || strings.HasSuffix(base, "-"+inputDigest))
}

// plainEntryName reports whether name is one path element inside an entry.
func plainEntryName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`) && filepath.Base(name) == name
}

func cacheLockPath(entryDir, suffix string) string {
	return filepath.Join(filepath.Dir(entryDir), ".locks", filepath.Base(entryDir)+suffix)
}

func cacheBuildLockPath(entryDir string) string {
	name := filepath.Base(entryDir)
	if filepath.Base(filepath.Dir(entryDir)) == "ext" {
		if split := strings.LastIndexByte(name, '-'); split >= 0 && split+1 < len(name) {
			name = name[split+1:]
		}
	}
	return filepath.Join(filepath.Dir(entryDir), ".locks", name+".lock")
}

func lockHeld(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	lock := filelock.New(path)
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return true
	}
	_ = lock.Unlock()
	return false
}

func cacheDirSize(ctx context.Context, root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, infoErr := entry.Info(); infoErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
