package fileindex

import (
	"context"
	"errors"
	"slices"
	"strings"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Ports apps/p2/plugins/file-index/listing.ts: the index from the environment's own listing (pi-env's `listFiles`), when
// it has one. Pure over an ExecutionEnv; the worker facet supplies the conversation's environment and the workspace's
// confinement.

// The workspace index's bounds (workspace/environment.ts `FILES_LIMIT`, `FILES_BYTES`, `GIT_TIMEOUT`).
const (
	listEntries   = 50_000
	listBytes     = 4 * 1024 * 1024
	listTimeoutMs = 10_000
)

// inside is whether path is base or below it (canonical absolute paths, `/` separators).
func inside(base, path string) bool {
	if path == base {
		return true
	}
	if base == "/" {
		return strings.HasPrefix(path, "/")
	}
	return strings.HasPrefix(path, base+"/")
}

// Listing is an index of the working directory.
type Listing struct {
	Items     []FileItem
	Truncated bool
}

// ListIndex is the files and directories below root (the canonical working directory) from env.ListFiles: empty and hidden
// directories included; `.git`, `node_modules` and, in a git work tree, what git ignores left out; symbolic links listed
// as files and not followed. Entries in a denied directory (canonical; p2's private data) are left out, as the workspace
// leaves them out. It returns nil when the environment cannot list (an environment without a lister, or a daemon released
// before it: `not_supported`); the caller then uses the workspace's git/walk index. Other failures are returned.
func ListIndex(ctx context.Context, env durableenv.ExecutionEnv, root string, denied []string) (*Listing, error) {
	entries, bytes, timeout := float64(listEntries), float64(listBytes), float64(listTimeoutMs)
	listed, err := env.ListFiles(ctx, root, &durableenv.ListFilesOptions{Exclude: []string{"node_modules"}, MaxEntries: &entries, MaxBytes: &bytes, TimeoutMs: &timeout})
	if err != nil {
		var failure *durableenv.FileError
		if errors.As(err, &failure) && failure.Code == durableenv.FileErrorNotSupported {
			return nil, nil
		}
		return nil, err
	}
	base := strings.TrimRight(root, "/")
	items := make([]FileItem, 0, len(listed.Entries))
	for _, entry := range listed.Entries {
		if len(denied) > 0 && slices.ContainsFunc(denied, func(directory string) bool { return inside(directory, base+"/"+entry.Path) }) {
			continue
		}
		items = append(items, FileItem{Path: entry.Path, Dir: entry.Kind == durableenv.FileKindDirectory})
	}
	return &Listing{Items: items, Truncated: listed.Truncated}, nil
}
