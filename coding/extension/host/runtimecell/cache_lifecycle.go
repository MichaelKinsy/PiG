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

	"github.com/gofrs/flock"
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
	DryRun         bool
	Rename         func(string, string) error
	RemoveAll      func(string) error
}

type UsageLease struct {
	lock *flock.Flock
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

func AcquireArtifactUsageLease(artifactPath string) (*UsageLease, error) {
	entryDir := filepath.Dir(artifactPath)
	if _, valid := readReadyMetadata(entryDir); !valid {
		return nil, nil
	}
	lease, err := AcquireUsageLease(entryDir)
	if err != nil {
		return nil, err
	}
	if current, valid := readReadyMetadata(entryDir); !valid || filepath.Join(entryDir, current.Artifact) != filepath.Clean(artifactPath) {
		_ = lease.Release()
		return nil, fmt.Errorf("cache artifact %s changed while acquiring its usage lease", artifactPath)
	}
	return lease, nil
}

func AcquireUsageLease(entryDir string) (*UsageLease, error) {
	lockPath := cacheLockPath(entryDir, ".usage.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	lock := flock.New(lockPath)
	if err := lock.RLock(); err != nil {
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

	var gcLock *flock.Flock
	if prune {
		if err := os.MkdirAll(options.CacheRoot, 0o755); err != nil {
			return report, err
		}
		gcLock = flock.New(filepath.Join(options.CacheRoot, ".gc.lock"))
		if _, err := gcLock.TryLockContext(ctx, gcLockRetry); err != nil {
			return report, fmt.Errorf("lock extension cache GC: %w", err)
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
		usageLock := flock.New(lockPath)
		locked, lockErr := usageLock.TryLock()
		if lockErr != nil || !locked {
			report.Skipped++
			continue
		}
		buildLock := flock.New(cacheBuildLockPath(entry.Path))
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

func classifyCacheEntry(path string, options CacheLifecycleOptions) CacheEntry {
	entry := CacheEntry{Path: path, Bytes: cacheDirSize(options.Context, path)}
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
	lock := flock.New(path)
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
