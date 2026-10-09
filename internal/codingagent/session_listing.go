// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"cmp"
	"context"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// SessionListProgress receives completed-file counts. A nil partial means no snapshot was published; a non-nil empty slice is an empty published snapshot.
type SessionListProgress func(loaded, total int, partial []SessionInfo)

// SessionListOptions supplies cancellation and progress for session discovery.
type SessionListOptions struct {
	Context    context.Context
	OnProgress SessionListProgress
}

func sessionListOptions(options []SessionListOptions) SessionListOptions {
	var selected SessionListOptions
	if len(options) > 0 {
		selected = options[0]
	}
	if selected.Context == nil {
		selected.Context = context.Background()
	}
	return selected
}

// Ports packages/coding-agent/src/core/session-manager.ts (listSessionsFromDir and buildSessionInfosWithConcurrency).
func listSessionsInDirWithOptions(dir string, options SessionListOptions) ([]SessionInfo, error) {
	if err := options.Context.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if options.Context.Err() != nil {
			return nil, options.Context.Err()
		}
		return []SessionInfo{}, nil
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := options.Context.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	slices.SortStableFunc(files, func(a, b string) int { return localeCompare(filepath.Base(b), filepath.Base(a)) })
	return summarizeSessionFilesWithOptions(files, options, 10, false)
}

func listSessionsAcrossRootWithOptions(root string, options SessionListOptions) ([]SessionInfo, error) {
	if err := options.Context.Err(); err != nil {
		return nil, err
	}
	directories, err := os.ReadDir(root)
	if err != nil {
		if options.Context.Err() != nil {
			return nil, options.Context.Err()
		}
		return []SessionInfo{}, nil
	}
	var files []string
	for _, directory := range directories {
		if err := options.Context.Err(); err != nil {
			return nil, err
		}
		path := filepath.Join(root, directory.Name())
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
				files = append(files, filepath.Join(path, entry.Name()))
			}
		}
	}
	// Pi: candidates sorted by `mtimeMs` descending (a file that cannot be stat'ed sorts last), then by basename `localeCompare` descending.
	mtimes := make(map[string]float64, len(files))
	for _, file := range files {
		if err := options.Context.Err(); err != nil {
			return nil, err
		}
		mtimes[file] = math.Inf(-1)
		if info, err := os.Stat(file); err == nil {
			mod := info.ModTime()
			mtimes[file] = float64(mod.Unix())*1000 + float64(mod.Nanosecond())/1e6
		}
	}
	slices.SortStableFunc(files, func(a, b string) int {
		if c := cmp.Compare(mtimes[b], mtimes[a]); c != 0 {
			return c
		}
		return localeCompare(filepath.Base(b), filepath.Base(a))
	})
	return summarizeSessionFilesWithOptions(files, options, 100, true)
}

type sessionListResult struct {
	index int
	info  SessionInfo
	valid bool
}

func summarizeSessionFilesWithOptions(files []string, options SessionListOptions, publishInterval int, waitForFirst bool) ([]SessionInfo, error) {
	ctx, cancel := context.WithCancel(options.Context)
	if err := ctx.Err(); err != nil {
		cancel()
		return nil, err
	}
	if len(files) == 0 {
		cancel()
		return []SessionInfo{}, nil
	}
	results := make(chan sessionListResult, len(files))
	var next atomic.Int64
	var workers sync.WaitGroup
	for range min(maxConcurrentSessionInfoLoads, len(files)) {
		workers.Go(func() {
			for {
				if ctx.Err() != nil {
					return
				}
				index := int(next.Add(1)) - 1
				if index >= len(files) {
					return
				}
				info, err := summarizeSessionFileContext(ctx, files[index])
				results <- sessionListResult{index: index, info: info, valid: err == nil}
			}
		})
	}
	joined := make(chan struct{})
	go func() { workers.Wait(); close(results); close(joined) }()
	defer func() { cancel(); <-joined }()
	byIndex := make([]*SessionInfo, len(files))
	ordered := func() []SessionInfo {
		infos := make([]SessionInfo, 0, len(files))
		for _, info := range byIndex {
			if info != nil {
				infos = append(infos, *info)
			}
		}
		slices.SortStableFunc(infos, compareSessionModifiedDesc)
		return infos
	}
	loaded := 0
	firstLoaded := false
	for result := range results {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		loaded++
		if result.index == 0 {
			firstLoaded = true
		}
		if result.valid {
			info := result.info
			byIndex[result.index] = &info
		}
		if options.OnProgress != nil {
			publish := loaded == 1 || loaded%publishInterval == 0 || loaded == len(files)
			if waitForFirst {
				publish = firstLoaded && (result.index == 0 || loaded%publishInterval == 0 || loaded == len(files))
			}
			var partial []SessionInfo
			if publish {
				partial = ordered()
			}
			options.OnProgress(loaded, len(files), partial)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ordered(), nil
}

var sessionFileCollator = collate.New(language.Und)

// localeCompare is String.prototype.localeCompare under the root locale, which orders Pi's session files.
func localeCompare(a, b string) int { return sessionFileCollator.CompareString(a, b) }

// compareSessionModifiedDesc is sortSessionInfos of Pi: modification time in milliseconds, newest first; equal times keep their order under a stable sort.
func compareSessionModifiedDesc(a, b SessionInfo) int {
	return cmp.Compare(b.Modified.UnixMilli(), a.Modified.UnixMilli())
}
