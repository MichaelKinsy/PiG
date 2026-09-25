package runtimecell

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
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
}

type CacheReport struct {
	Entries        []CacheEntry       `json:"entries"`
	ByClass        map[CacheClass]int `json:"byClass"`
	TotalBytes     int64              `json:"totalBytes"`
	RemovedBytes   int64              `json:"removedBytes"`
	Removed        int                `json:"removed"`
	ProtectedBytes int64              `json:"protectedBytes"`
	LimitSatisfied bool               `json:"limitSatisfied"`
	Errors         []string           `json:"errors,omitempty"`
}

type CacheLifecycleOptions struct {
	CacheRoot        string
	Current          map[string]struct{}
	LiveFingerprints map[string]struct{}
	Now              func() time.Time
	Retention        time.Duration
	CrashGrace       time.Duration
	MaxSize          *int64
	DryRun           bool
	Rename           func(string, string) error
	RemoveAll        func(string) error
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
	if options.RemoveAll == nil {
		options.RemoveAll = os.RemoveAll
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
		if err := gcLock.Lock(); err != nil {
			return report, fmt.Errorf("lock extension cache GC: %w", err)
		}
		defer func() { _ = gcLock.Unlock() }()
	}

	paths, err := cacheEntryPaths(options.CacheRoot)
	if err != nil {
		return report, err
	}
	for _, path := range paths {
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
		if options.DryRun {
			report.Removed++
			report.RemovedBytes += entry.Bytes
			continue
		}
		if strings.HasPrefix(filepath.Base(entry.Path), ".tombstone-") {
			if err := options.RemoveAll(entry.Path); err != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("remove tombstone %s: %v", entry.Path, err))
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
			report.Errors = append(report.Errors, fmt.Sprintf("protect %s: active usage lease", entry.Path))
			continue
		}
		buildLock := flock.New(cacheBuildLockPath(entry.Path))
		buildLocked, buildLockErr := buildLock.TryLock()
		if buildLockErr != nil || !buildLocked {
			_ = usageLock.Unlock()
			if buildLockErr != nil {
				report.Errors = append(report.Errors, fmt.Sprintf("protect %s: build lock: %v", entry.Path, buildLockErr))
			}
			continue
		}
		tombstone := filepath.Join(filepath.Dir(entry.Path), fmt.Sprintf(".tombstone-%s-%d", filepath.Base(entry.Path), tombstoneSequence.Add(1)))
		if err := options.Rename(entry.Path, tombstone); err != nil {
			_ = buildLock.Unlock()
			_ = usageLock.Unlock()
			report.Errors = append(report.Errors, fmt.Sprintf("rename %s: %v", entry.Path, err))
			continue
		}
		_ = buildLock.Unlock()
		_ = usageLock.Unlock()
		if err := options.RemoveAll(tombstone); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("remove tombstone %s: %v", tombstone, err))
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
	entry := CacheEntry{Path: path, Bytes: cacheDirSize(path)}
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

func cacheDirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
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
