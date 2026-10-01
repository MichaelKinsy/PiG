package piglet

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/MichaelKinsy/PiG/internal/bytesize"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// defaultPruneKeep is how many built Piglet Binaries of each Piglet and target
// `pig piglet prune` leaves in place unless told otherwise: the one in use and
// its predecessor.
const defaultPruneKeep = 2

// pruneItem is one built Piglet Binary in the managed store.
type pruneItem struct {
	piglet string
	record managedRecord
	when   time.Time
	size   int64
}

// selectPrunable chooses the built Piglet Binaries to remove. The newest keep of
// each Piglet and target are never removed: one build for several targets
// records one Binary per target, and each target's newest build is the one
// that target runs. Without a limit every other one is removed, oldest first. With
// a limit the others are removed oldest first, and only until the store is
// within it; storeBytes is the current size of the whole artifact store,
// including installs that are never removed.
func selectPrunable(items []pruneItem, keep int, limit *int64, storeBytes int64) []pruneItem {
	newest := slices.Clone(items)
	slices.SortStableFunc(newest, func(a, b pruneItem) int { return b.when.Compare(a.when) })
	type lineage struct{ piglet, target string }
	kept := map[lineage]int{}
	var candidates []pruneItem
	for _, item := range newest {
		key := lineage{item.piglet, item.record.Target}
		if kept[key] < keep {
			kept[key]++
			continue
		}
		candidates = append(candidates, item)
	}
	slices.Reverse(candidates)
	if limit == nil {
		return candidates
	}
	var selected []pruneItem
	for _, item := range candidates {
		if storeBytes <= *limit {
			break
		}
		selected = append(selected, item)
		storeBytes -= item.size
	}
	return selected
}

// pruneRemovals lists the files that go with one built Piglet Binary: its
// artifact and record, and its resolution record unless a Binary that stays uses it.
func pruneRemovals(item pruneItem, staying map[string]bool) []pigletFacetRemoval {
	removals := []pigletFacetRemoval{
		{path: item.record.Path, label: "Piglet Binary record"},
		{path: item.record.ArtifactPath, label: "managed Piglet Binary"},
	}
	if resolution := item.record.ResolutionPath; resolution != "" && !staying[resolution] {
		removals = append(removals, pigletFacetRemoval{path: resolution, label: "Piglet resolution record"})
	}
	return removals
}

// pruneItems separates the built Piglet Binaries that may be removed from
// everything else. A pulled install, and a record or artifact that cannot be
// inspected, is never removed; the resolution records those name stay too.
func pruneItems(records []managedRecord) ([]pruneItem, map[string]bool) {
	var items []pruneItem
	staying := map[string]bool{}
	for _, record := range records {
		info, err := os.Stat(record.Path)
		if record.CurrentPath != "" || err != nil {
			staying[record.ResolutionPath] = true
			continue
		}
		artifact, err := os.Stat(record.ArtifactPath)
		if err != nil {
			staying[record.ResolutionPath] = true
			continue
		}
		items = append(items, pruneItem{piglet: record.Piglet, record: record, when: info.ModTime(), size: artifact.Size()})
	}
	return items, staying
}

// cmdPrune removes old built Piglet Binaries from the managed artifact store.
// Installs pulled from a signed release, whose `current` pointer names them, are
// never touched.
// pig additive (D18): Pi has no Piglet Binaries, so it has nothing to prune.
func cmdPrune(args []string, stdout, stderr io.Writer) int {
	const usage = "Usage: pig piglet prune [--keep <n>] [--max-size <size>] [--dry-run]"
	keep, dryRun := defaultPruneKeep, false
	var limit *int64
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--keep":
			if i+1 >= len(args) {
				_, _ = fmt.Fprintln(stderr, usage)
				return 2
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				_, _ = fmt.Fprintf(stderr, "error: invalid --keep %q\n", args[i])
				return 2
			}
			keep = n
		case "--max-size":
			if i+1 >= len(args) {
				_, _ = fmt.Fprintln(stderr, usage)
				return 2
			}
			i++
			size, err := bytesize.Parse(args[i])
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
				return 2
			}
			limit = &size
		case "-h", "--help":
			_, _ = fmt.Fprintln(stdout, usage)
			return 0
		default:
			_, _ = fmt.Fprintln(stderr, usage)
			return 2
		}
	}

	records, recordErrs := listManagedRecords()
	items, staying := pruneItems(records)
	selected := selectPrunable(items, keep, limit, directorySize(codingagent.PigletArtifactsDir()))
	chosen := map[string]bool{}
	for _, item := range selected {
		chosen[item.record.Path] = true
	}
	for _, item := range items {
		if !chosen[item.record.Path] {
			staying[item.record.ResolutionPath] = true
		}
	}

	var removedBytes int64
	var failures []error
	removedCount := 0
	for _, item := range selected {
		verb := "would remove"
		if !dryRun {
			verb = "removed"
			if err := removePigletFacets(pruneRemovals(item, staying)); err != nil {
				failures = append(failures, fmt.Errorf("%s %s: %w", item.piglet, item.record.ArtifactPath, err))
				continue
			}
		}
		removedCount++
		removedBytes += item.size
		_, _ = fmt.Fprintf(stdout, "%s %s: %s (%d bytes)\n", verb, item.piglet, item.record.ArtifactPath, item.size)
	}
	if !dryRun {
		touched := map[string]struct{}{}
		for _, item := range selected {
			touched[item.piglet] = struct{}{}
		}
		for name := range touched {
			pruneEmptyRecordDirs(filepath.Join(codingagent.PigletRecordsDir(), name))
			pruneEmptyRecordDirs(filepath.Join(codingagent.PigletArtifactsDir(), name))
		}
	}
	summary := fmt.Sprintf("Piglet Binaries: %d removed, %d bytes", removedCount, removedBytes)
	if dryRun {
		summary = fmt.Sprintf("Piglet Binaries: %d would be removed, %d bytes", removedCount, removedBytes)
	}
	_, _ = fmt.Fprintln(stdout, summary)
	for _, err := range recordErrs {
		_, _ = fmt.Fprintf(stderr, "warning: %v\n", err)
	}
	if len(failures) > 0 {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", errors.Join(failures...))
		return 1
	}
	if limit != nil && !dryRun && directorySize(codingagent.PigletArtifactsDir()) > *limit {
		_, _ = fmt.Fprintf(stderr, "error: the store is still over %d bytes: %d built Piglet Binaries are kept by --keep %d or are pulled installs\n", *limit, len(items)-removedCount, keep)
		return 1
	}
	return 0
}

func directorySize(root string) int64 {
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
