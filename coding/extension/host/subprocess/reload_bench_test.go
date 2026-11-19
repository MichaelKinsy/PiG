package subprocess

import (
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// skippedByTreeHash reports whether runtimecell's hashTree does not enter a directory called name: it skips the directories that are hidden
// or named node_modules, target, vendor, dist, build or __pycache__.
func skippedByTreeHash(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "target" || name == "vendor" || name == "dist" || name == "build" || name == "__pycache__"
}

// reloadSDKTree is a copy of the repository's Go SDK that a reload reads, with the files and directories hashTree reads in it.
type reloadSDKTree struct {
	root        string
	files, dirs int
}

// copyReloadSDK copies the repository's Go SDK, the whole of it for the SDK embeds its hidden .gz directory, into a new directory, and adds
// extraFiles data files to the directory the copy's root is, and extraDirs new directories under it with reloadDirFiles data files each, as the
// SDK's own directories hold several. A staged copy is made where the host stages an SDK, <config root>/state/pigsdk/sdk-<hash>, as an installed
// pig's is; any other copy is where a checkout keeps it. hashTree reads every file whatever it is called, so a data file costs a reload what a
// source file does.
func copyReloadSDK(b *testing.B, staged bool, extraFiles, extraDirs int) reloadSDKTree {
	b.Helper()
	src, err := filepath.Abs(benchSDKDir)
	if err != nil {
		b.Fatal(err)
	}
	dir := b.TempDir()
	root := filepath.Join(dir, "sdk")
	if staged {
		root = filepath.Join(dir, "state", "pigsdk", "sdk-copy")
	}
	tree := reloadSDKTree{root: root}
	if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if err := os.MkdirAll(filepath.Join(tree.root, rel), 0o755); err != nil {
				return err
			}
			if rel != "." && skippedByTreeHash(d.Name()) {
				return filepath.SkipDir
			}
			tree.dirs++
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tree.files++
		return os.WriteFile(filepath.Join(tree.root, rel), data, 0o644)
	}); err != nil {
		b.Fatal(err)
	}
	// The SDK embeds its hidden .gz directory, which hashTree does not read: copy it as it is.
	gz := filepath.Join(src, ".gz")
	if err := os.CopyFS(filepath.Join(tree.root, ".gz"), os.DirFS(gz)); err != nil {
		b.Fatal(err)
	}
	data := []byte("a data file of the SDK tree\n")
	for i := range extraFiles {
		if err := os.WriteFile(filepath.Join(tree.root, fmt.Sprintf("reload-data%03d.txt", i)), data, 0o644); err != nil {
			b.Fatal(err)
		}
		tree.files++
	}
	for i := range extraDirs {
		dir := filepath.Join(tree.root, fmt.Sprintf("reload-data-dir%03d", i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		for j := range reloadDirFiles {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("data%02d.txt", j)), data, 0o644); err != nil {
				b.Fatal(err)
			}
		}
		tree.dirs++
		tree.files += reloadDirFiles
	}
	// The files of an SDK that was staged at an earlier start are old when a reload reads them. A file written a moment ago is not trusted yet by
	// the memo of a staged tree (the racy rule of runtimecell's sdkTreeHash), and the reloads counted here are those of a tree long settled.
	old := time.Now().Add(-time.Hour)
	if err := filepath.WalkDir(tree.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		return os.Chtimes(path, old, old)
	}); err != nil {
		b.Fatal(err)
	}
	return tree
}

// reloadAllocs is the mean allocations of a warm Reload() of two packed Go extensions, built against a copy of the SDK, a staged one or a
// checkout's (copyReloadSDK), with extraFiles data files and extraDirs directories added, and the files and directories of that tree. The cell is
// built cold once; reloads warm reloads are then counted, and the host is shut down before the next one is measured.
func reloadAllocs(b *testing.B, staged bool, extraFiles, extraDirs, reloads int) (allocs float64, tree reloadSDKTree) {
	b.Helper()
	tree = copyReloadSDK(b, staged, extraFiles, extraDirs)
	b.Setenv("PIG_SDK_GO_ROOT", tree.root)
	rootA := writePackedFactoryModule(b, "example.com/bench/a", "bench-a", "tool_a")
	rootB := writePackedFactoryModule(b, "example.com/bench/b", "bench-b", "tool_b")
	configs := []ExtConfig{
		packedFactoryConfig("bench-a", rootA, "example.com/bench/a", "ha"),
		packedFactoryConfig("bench-b", rootB, "example.com/bench/b", "hb"),
	}
	h := NewHost(b.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	defer h.Shutdown("bench done")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := h.Reload(ctx); err != nil {
		b.Fatalf("warmup reload: %v", err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range reloads {
		if _, err := h.Reload(ctx); err != nil {
			b.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	// A cell that fails to build runs isolated, and then every reload costs a build: only a reload that reused the one packed cell is counted.
	if rep := h.LastReloadReport(); rep == nil || len(rep.Issues) != 0 || len(rep.Cells) != 1 || rep.Cells[0].Strategy != CellStrategyPackedGo || !rep.Cells[0].Cached {
		b.Fatalf("the last reload did not reuse one packed Go cell: %+v", rep)
	}
	return float64(after.Mallocs-before.Mallocs) / float64(reloads), tree
}

// The three figures of each root of BenchmarkReload_PackedCellWarmCache, which its sub-benchmarks report as allocs/op.
const (
	reloadExtraFiles = 300 // data files added to the SDK's root directory to measure a file
	reloadExtraDirs  = 50  // directories added to measure a directory, each with reloadDirFiles data files
	reloadDirFiles   = 10  // data files in each added directory: the shape of the SDK's own directories
	reloadCount      = 20  // warm reloads counted for each tree
)

// reloadRows are the three figures of a reload against one kind of root.
type reloadRows struct{ fixed, per100Files, per10Dirs float64 }

// reloadFigures holds the rows of the staged root (0) and of the checkout's (1), each measured once per process.
var reloadFigures [2]struct {
	once sync.Once
	rows reloadRows
}

func reloadRowsOf(b *testing.B, staged bool) reloadRows {
	b.Helper()
	i := 0
	if !staged {
		i = 1
	}
	reloadFigures[i].once.Do(func() { reloadFigures[i].rows = measureReloadFigures(b, staged) })
	return reloadFigures[i].rows
}

// measureReloadFigures reads a reload's cost off three trees: the copy of the SDK, the copy with reloadExtraFiles files more in a directory it
// has, and the copy with reloadExtraDirs directories more. A reload looks at every file and directory of the SDK tree for the key of a packed
// cell (runtimecell.sdkTreeHash): a directory listing for each directory and a stat for each file, and for a root that is not staged a read of
// each file as well. So a reload costs a fixed part, a part for each file and a part for each directory, and the SDK gaining files or
// directories moves only its own part. The slopes are measured here, in the same run, and taken out of the reload of the copy as it is: what is
// left is fixed.
//
// A directory is measured in the shape the SDK's directories have, reloadDirFiles files each, so a directory's listing is not counted as a file.
// What the model leaves is small and written here, for the checkout's rows: adding 60 files and 3 directories of ten files to the SDK moved fixed
// by about 14 (1483 to 1486 plain, 1492 to 1503 with them), that is about a fifth of an allocation for each file and 5 for each directory beyond
// the two slopes (a larger listing grows its slices a step more). The rows have the ratchet's tolerance, 6 %: the SDK may gain some files or
// directories before a row leaves it, and past that the row is raised in the change that grows the SDK, with the figures above.
func measureReloadFigures(b *testing.B, staged bool) reloadRows {
	b.Helper()
	base, tree := reloadAllocs(b, staged, 0, 0, reloadCount)
	withFiles, _ := reloadAllocs(b, staged, reloadExtraFiles, 0, reloadCount)
	withDirs, _ := reloadAllocs(b, staged, 0, reloadExtraDirs, reloadCount)
	perFile := (withFiles - base) / reloadExtraFiles
	// Each new directory holds reloadDirFiles files: they cost what files do, and what is left is the directory.
	perDir := (withDirs-base)/reloadExtraDirs - perFile*reloadDirFiles
	return reloadRows{
		fixed:       base - perFile*float64(tree.files) - perDir*float64(tree.dirs),
		per100Files: perFile * 100,
		per10Dirs:   perDir * 10,
	}
}

// BenchmarkReload_PackedCellWarmCache measures the allocations of a Reload() of a packed Go cell that is already built: the production-hot
// path, where most reloads hit the cache. A reload looks at the whole SDK tree, so the total grows with every file and directory the SDK gains.
// What it does with each file depends on the root the SDK is served from (runtimecell.sdkTreeHash): an SDK the host staged, a directory inside
// <config root>/state/pigsdk as an installed pig has it, is checked by its files' sizes and times and no file is read while they are as they
// were; every other root, a source checkout's or a PIG_SDK_GO_ROOT's, is read and hashed in full by every reload. Each sub-benchmark reports as
// its allocs/op (b.ReportMetric, which replaces the testing package's own count) one part of the cost:
//
//   - fixed: the allocations of a reload with the looking at the SDK tree's files and directories taken out;
//   - per-100-sdk-files: the allocations that looking at the tree adds to a reload for each 100 files in it;
//   - per-10-sdk-dirs: the same for each 10 directories in it;
//
// and the same three, as checkout-fixed, checkout-per-100-sdk-files and checkout-per-10-sdk-dirs, for a root that is not staged and reads every
// file of the tree on every reload: what a developer's repository SDK costs.
//
// The figures are measured once per process, over reloadCount warm reloads of each of three copies of the SDK tree (see measureReloadFigures).
//
// Run with:
//
//	go test ./coding/extension/host/subprocess -run=^$ -bench=BenchmarkReload_PackedCellWarmCache -benchtime=5x -benchmem
func BenchmarkReload_PackedCellWarmCache(b *testing.B) {
	for _, row := range []struct {
		name   string
		staged bool
		pick   func(reloadRows) float64
	}{
		{"fixed", true, func(r reloadRows) float64 { return r.fixed }},
		{"per-100-sdk-files", true, func(r reloadRows) float64 { return r.per100Files }},
		{"per-10-sdk-dirs", true, func(r reloadRows) float64 { return r.per10Dirs }},
		{"checkout-fixed", false, func(r reloadRows) float64 { return r.fixed }},
		{"checkout-per-100-sdk-files", false, func(r reloadRows) float64 { return r.per100Files }},
		{"checkout-per-10-sdk-dirs", false, func(r reloadRows) float64 { return r.per10Dirs }},
	} {
		b.Run(row.name, func(b *testing.B) {
			b.ReportMetric(math.Round(row.pick(reloadRowsOf(b, row.staged))), "allocs/op")
		})
	}
}

// BenchmarkPlanCells_Isolated measures planner throughput for a realistic
// extension count when every extension runs in an isolated process.
// PlanCells must stay near-zero-cost: it runs on every Reload() and on every
// validation, and it is not parallel.
func BenchmarkPlanCells_Isolated(b *testing.B) {
	configs := make([]ExtConfig, 0, 24)
	for i := range 24 {
		configs = append(configs, ExtConfig{
			Name:        cellNameForBench(i),
			Enabled:     true,
			Source:      "/tmp/example",
			RuntimeKind: "subprocess",
		})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = PlanCells(configs, nil)
	}
}

func cellNameForBench(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	return "ext-" + string(letters[i%len(letters)]) + "-" + string(letters[(i/26)%len(letters)])
}
