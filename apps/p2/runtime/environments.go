package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// Ports apps/p2/src/runtime/environments.ts, the parts plugins on this device or an SSH environment share: this device's
// folders. (`envRevision` is envs' `EnvRevision`; `remotePaths` and `value` are Go's own error returns.)

const (
	listingScan  = 10_000
	listingLimit = 1000
)

// ListLocalDirectory is the folders of path on this device (`~` expands), as an environment's manager lists a remote one:
// symbolic links to directories included, sorted, the first 1,000 shown.
func ListLocalDirectory(path string) (DirectoryListing, error) {
	absolute := NormalizePath(path)
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return DirectoryListing{}, nodeerrno.FromPathError(err)
	}
	scanned := entries[:min(len(entries), listingScan)]
	found := make([]string, len(scanned))
	var wg sync.WaitGroup
	for at, entry := range scanned {
		full := filepath.Join(absolute, entry.Name())
		switch {
		case entry.IsDir():
			found[at] = full
		case entry.Type()&os.ModeSymlink != 0:
			// All links resolve in parallel.
			wg.Add(1)
			//portlint:allow gorecover joined by wg.Wait below; stat of a link cannot panic
			go func() {
				defer wg.Done()
				if info, err := os.Stat(full); err == nil && info.IsDir() {
					found[at] = full
				}
			}()
		}
	}
	wg.Wait()
	directories := make([]string, 0, len(found))
	for _, each := range found {
		if each != "" {
			directories = append(directories, each)
		}
	}
	slices.SortStableFunc(directories, LocaleCompare)
	var parent *string
	if up := filepath.Dir(absolute); up != absolute {
		parent = &up
	}
	return DirectoryListing{
		Path:        absolute,
		Parent:      parent,
		Directories: directories[:min(len(directories), listingLimit)],
		Truncated:   len(entries) > listingScan || len(directories) > listingLimit,
	}, nil
}
