package extensionconformance

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/fusepack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/sdkscenarios"
)

// The reload leak test reloads one extension of every SDK, in every realization the SDK has, a hundred times through the host's reload
// (session_shutdown with reason reload, Host.Reload, a new runner, session_start with reason reload: the order Session.Reload runs) and
// requires that the host holds nothing more after the hundredth reload than it held before the first. Ten reloads come first, so the
// baseline holds whatever a host allocates once. Nothing is retried: a row that grows is red and names the resource and its samples.
// PIG_RELOAD_LEAK_RELOADS sets the count of a longer run. The surfaces rows reload extensions that draw (reload_leak_surfaces_test.go).
const (
	reloadLeakWarmup      = 10
	reloadLeakReloads     = 100
	reloadLeakSampleEvery = 10
	// reloadLeakSettle is how long the goroutines of a replaced extension may take to end after the reload returned.
	reloadLeakSettle = 5 * time.Second
	// reloadLeakHeapBytes bounds the live host heap a hundred reloads may add, and reloadLeakHeapSlopeBytes its growth per reload over the
	// samples. A host keeps one report of its latest reload (Host.LastReloadReport) and nothing per generation, so both are noise bounds:
	// a kept extension (its registrations, connection and frame buffers) is tens of kilobytes per reload.
	reloadLeakHeapBytes      = 512 << 10
	reloadLeakHeapSlopeBytes = 1 << 10
	// reloadLeakHeapObjects bounds the live heap objects a hundred reloads may add. Go reports no count of pending timers; a timer or
	// ticker that was not stopped is live heap with its callback, so twenty objects per reload is the bound that holds them.
	reloadLeakHeapObjects = 2000
	// reloadLeakMarker names the environment variable that marks the processes a row started, so that one whose parent ended is still found.
	reloadLeakMarker = "PIG_RELOAD_LEAK_ROW"
)

// reloadLeakRow is one SDK in one realization.
type reloadLeakRow struct {
	sdk         sdkscenarios.SDK
	realization string
	// strategy is the cell strategy the host must report; it is empty for a fused extension, which has no cell.
	strategy subprocess.CellStrategy
	// command is a command every extension of the row registers and answers without a UI.
	command string
	configs func(t *testing.T, root, dir string) []subprocess.ExtConfig
	// parallel marks a row that reloads on every P of the process and leaves the heap bounds, which hold on one P only, to its one-P twin.
	parallel bool
	// afterLoad runs once after the first load and before the warm-up reloads.
	afterLoad func(t *testing.T)
	// invalidated marks a row whose every reload follows a reload that failed: the host holds the stale message when the reload starts, as after the Host.Invalidate a mode calls when its reload fails, and the reload ends that staleness.
	invalidated bool
	// surfaces is the kind of a row whose extensions draw, "views", "keys" or "surfaces" (both): after every reload the row draws what that kind names (reload_leak_surfaces_test.go).
	surfaces string
}

func reloadLeakRows() []reloadLeakRow {
	var rows []reloadLeakRow
	for _, s := range sdkscenarios.SDKs {
		rows = append(rows, reloadLeakRow{sdk: s, realization: "isolated", strategy: subprocess.CellStrategyIsolated, command: "noop", configs: func(t *testing.T, root, dir string) []subprocess.ExtConfig {
			writeSpec(t, filepath.Join(dir, scenarioFile), sdkscenarios.Spec{Commands: []sdkscenarios.Command{{Name: "noop", Description: "Return at once", Op: sdkscenarios.CommandNoop}}})
			// The driver registers under this name, and a directory selected directly must carry its extension's name.
			return []subprocess.ExtConfig{scenarioDriverConfig(t, root, s, "scenario-driver-"+string(s), "strict")}
		}})
		if s == sdkscenarios.Node {
			rows = append(rows, reloadLeakRow{sdk: s, realization: "packed", strategy: subprocess.CellStrategyPackedNode, command: "noop", configs: func(t *testing.T, _, dir string) []subprocess.ExtConfig {
				if _, err := exec.LookPath("node"); err != nil {
					t.Fatalf("node is required for SDK conformance: %v", err)
				}
				var configs []subprocess.ExtConfig
				for _, name := range []string{"reload-leak-node-a", "reload-leak-node-b"} {
					path := filepath.Join(dir, name+".mjs")
					if err := os.WriteFile(path, []byte("export default function (pi) {\n  pi.registerCommand(\"noop\", { description: \"Return at once\", handler: async () => {} });\n}\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					configs = append(configs, subprocess.ExtConfig{Name: name, Source: path, Enabled: true})
				}
				return configs
			}})
			continue
		}
		rows = append(rows, reloadLeakRow{sdk: s, realization: "packed", strategy: subprocess.CellStrategy("packed-" + string(s)), command: "flags", configs: func(t *testing.T, root, _ string) []subprocess.ExtConfig {
			return []subprocess.ExtConfig{packedFlagFactory(t, root, string(s), "z-first", true), packedFlagFactory(t, root, string(s), "a-second", false)}
		}})
		if s == sdkscenarios.Go {
			rows = append(rows, reloadLeakRow{sdk: s, realization: "fused", command: "noop", configs: func(*testing.T, string, string) []subprocess.ExtConfig {
				return []subprocess.ExtConfig{{Name: reloadLeakFusedName, Enabled: true}}
			}})
			// A member of a Piglet Binary built with `build.fusedReload: source` (D31): it loads fused, its source is edited, and the
			// first warm-up reload switches it to the subprocess built from that source. The baseline is taken after the switch, so
			// the stopped fused copy is counted once; TestFusedMemberSwitchLeavesNothingRunning requires that a host after the switch
			// holds the goroutines of one that loaded the subprocess copy directly. The member draws nothing.
			var hybridSource string
			rows = append(rows, reloadLeakRow{sdk: s, realization: "fused-then-subprocess", strategy: subprocess.CellStrategyIsolated, command: "noop", configs: func(t *testing.T, _, dir string) []subprocess.ExtConfig {
				hybridSource = filepath.Join(dir, reloadLeakHybridName)
				if err := os.Mkdir(hybridSource, 0o700); err != nil {
					t.Fatal(err)
				}
				cfg := hybridConfig(t, hybridSource, reloadLeakHybridName, "v0")
				reloadLeakHybridDigest = hybridDigest(t, hybridSource)
				return []subprocess.ExtConfig{cfg}
			}, afterLoad: func(t *testing.T) { hybridEdit(t, hybridSource, reloadLeakHybridName, "v1") }})
			rows = append(rows, reloadLeakSurfaceRows()...)
		}
	}
	// The two rows whose generations each left a goroutine behind run again on every P, so the reload's own scheduling across Ps stays
	// under the bounds that do not depend on the runtime's caches: the lone Node extension (the connection loop that waited for the
	// process) and the Go cell (the watcher of a replaced state).
	for _, row := range slices.Clone(rows) {
		if row.sdk == sdkscenarios.Node && row.realization == "isolated" || row.sdk == sdkscenarios.Go && row.realization == "packed" {
			row.realization, row.parallel = row.realization+"-parallel", true
			rows = append(rows, row)
		}
	}
	// A reload that failed leaves the host invalidated, and the next reload ends that (Host.Invalidate, Host.Reload). These rows run again with every reload preceded by the invalidation: it leaves nothing behind either.
	for _, row := range slices.Clone(rows) {
		if (row.sdk == sdkscenarios.Node || row.sdk == sdkscenarios.Go) && row.realization == "isolated" || row.sdk == sdkscenarios.Node && row.realization == "packed" {
			row.realization, row.invalidated = row.realization+"-invalidated", true
			rows = append(rows, row)
		}
	}
	return rows
}

// reloadLeakFusedName is the extension reloadLeakFused serves in this process.
const reloadLeakFusedName = "reload-leak-fused"

// reloadLeakHybridName is the member of the fused-then-subprocess row, and reloadLeakHybridDigest the digest baked for it.
const reloadLeakHybridName = "reload-leak-hybrid"

var reloadLeakHybridDigest string

// reloadLeakFused is the fused resolver of the fused rows: every load runs the factory again in this process, as a Piglet Binary's generated registry does.
type reloadLeakFused struct{}

// BakedSourceDigest is the digest of the source the hybrid member was fused from; the fused row's member has none.
func (reloadLeakFused) BakedSourceDigest(name string) (string, bool) {
	return reloadLeakHybridDigest, name == reloadLeakHybridName
}

func (reloadLeakFused) FusedServe(cfg subprocess.ExtConfig) (func(net.Conn) error, bool) {
	if cfg.Name == reloadLeakHybridName {
		return func(conn net.Conn) error {
			return sdk.ServeFactory(conn, func() *sdk.Extension { return hybridFusedCopy(reloadLeakHybridName) })
		}, true
	}
	if cfg.Name != reloadLeakFusedName {
		return nil, false
	}
	return func(conn net.Conn) error {
		e := sdk.New(reloadLeakFusedName)
		e.Command("noop", "Return at once", func(sdk.Context, string) error { return nil })
		return e.RunWithConn(conn)
	}, true
}

// reloadLeakSample is what the host holds after a number of reloads.
type reloadLeakSample struct {
	reloads     int
	goroutines  int
	heapBytes   uint64
	heapObjects uint64
	fds         int
	processes   int
	zombies     int
	// rssBytes is the resident memory of the row's processes: a host keeps an extension's process over a reload and runs the factory in it again.
	rssBytes       int64
	inotify        int
	inotifyWatches int
	files          int
}

// reloadLeakProcesses returns the live processes below this one or carrying marker in their environment, by pid with the command name, and
// the number of ended children nobody waited for.
func reloadLeakProcesses(marker string) (live map[int]string, zombies int, rss int64) {
	live = map[int]string{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return live, 0, 0
	}
	self := os.Getpid()
	parent := map[int]int{}
	state := map[int]string{}
	name := map[int]string{}
	resident := map[int]int64{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			continue
		}
		// The command name is parenthesized and may hold spaces: the fields follow the last ")".
		head, rest, ok := strings.Cut(string(stat), ") ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 22 {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		pages, _ := strconv.ParseInt(fields[21], 10, 64)
		parent[pid], state[pid], resident[pid] = ppid, fields[0], pages*int64(os.Getpagesize())
		_, name[pid], _ = strings.Cut(head, "(")
	}
	needle := []byte(marker + "\x00")
	for pid := range parent {
		below := false
		for p, hops := pid, 0; hops < 64 && p != 0; hops++ {
			if p = parent[p]; p == self {
				below = true
				break
			}
		}
		if state[pid] == "Z" {
			if below {
				zombies++
			}
			continue
		}
		if !below {
			environ, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
			if err != nil || !bytes.Contains(append(environ, 0), needle) {
				continue
			}
		}
		live[pid] = name[pid]
		rss += resident[pid]
	}
	return live, zombies, rss
}

// reloadLeakDescriptors counts this process's open descriptors, its inotify instances and their watches.
func reloadLeakDescriptors(t *testing.T) (fds, inotify, watches int) {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err != nil {
			// The descriptor that listed the directory is closed by now.
			continue
		}
		fds++
		if target != "anon_inode:inotify" {
			continue
		}
		inotify++
		info, err := os.ReadFile(filepath.Join("/proc/self/fdinfo", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		watches += strings.Count(string(info), "inotify wd:")
	}
	return fds, inotify, watches
}

// reloadLeakFiles lists every file, directory and socket below roots.
func reloadLeakFiles(t *testing.T, roots []string) []string {
	t.Helper()
	var files []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
			if err != nil {
				// A file removed while the tree is walked is not there.
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(files)
	return files
}

// reloadLeakStacks returns the stacks at least atLeast goroutines stand in, most first: what a row that keeps goroutines kept.
func reloadLeakStacks(atLeast int) string {
	var profile bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&profile, 1); err != nil {
		return err.Error()
	}
	var out []string
	// The profile opens with one line that counts the goroutines; the stacks follow it, most goroutines first.
	_, stacks, _ := strings.Cut(profile.String(), "\n")
	for block := range strings.SplitSeq(stacks, "\n\n") {
		head, frames, _ := strings.Cut(block, "\n")
		count, _ := strconv.Atoi(strings.TrimSpace(strings.SplitN(head, "@", 2)[0]))
		if count < atLeast {
			continue
		}
		lines := []string{fmt.Sprintf("%d goroutines:", count)}
		for frame := range strings.SplitSeq(frames, "\n") {
			if fields := strings.Fields(frame); len(fields) >= 4 && fields[0] == "#" && len(lines) <= 10 {
				lines = append(lines, "    "+strings.Join(fields[2:], " "))
			}
		}
		out = append(out, strings.Join(lines, "\n"))
	}
	return strings.Join(out, "\n")
}

// reloadLeakSlope is the least-squares growth of the live heap per reload over samples.
func reloadLeakSlope(samples []reloadLeakSample) float64 {
	n := float64(len(samples))
	var sumX, sumY, sumXY, sumXX float64
	for _, s := range samples {
		x, y := float64(s.reloads), float64(s.heapBytes)
		sumX, sumY, sumXY, sumXX = sumX+x, sumY+y, sumXY+x*y, sumXX+x*x
	}
	if d := n*sumXX - sumX*sumX; d != 0 {
		return (n*sumXY - sumX*sumY) / d
	}
	return 0
}

func TestReloadLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("builds subprocess fixtures")
	}
	if runtime.GOOS != "linux" {
		t.Skip("samples descriptors, processes and inotify watches from /proc, which only Linux has")
	}
	reloads := envInt("PIG_RELOAD_LEAK_RELOADS", reloadLeakReloads)
	root := findModuleRoot(t)
	// A Piglet Binary installs the host's parsers and its resolver before any fused factory runs (fusepack.Register). No other test of the
	// package runs while this one does: it is not parallel, and the parallel ones wait for it.
	sdk.SetHostHelpers(fusepack.HostHelpers())
	subprocess.SetFusedResolver(reloadLeakFused{})
	t.Cleanup(func() { subprocess.SetFusedResolver(nil) })
	for _, row := range reloadLeakRows() {
		t.Run(string(row.sdk)+"/"+row.realization, func(t *testing.T) {
			if !row.parallel {
				// The row runs on one P, because the heap bounds compare live heap and the Go runtime keeps live heap per P for the goroutines that
				// ended: their g structs (runtime.malg; a g struct is never freed, and each P keeps up to 64 free ones before half move to the
				// global list) and the sudogs they blocked on (runtime.acquireSudog; 128 per P, and only the central list is dropped at a
				// collection). A goroutine that blocks on one P and is woken on another moves its sudog to the other P's cache, so on more Ps
				// the caches fill for about a thousand reloads, at a rate that follows GOMAXPROCS and what the process ran before, and then
				// stand. Measured on the Go packed row, in bytes per reload: over the first hundred reloads 1064 to 1454 on 16 Ps when the row
				// ran alone (above the slope bound, five runs of five) and 430 to 537 on 4; over each hundred of a thousand reloads on 16 Ps
				// 1217, 560, 391, 400, -43, 386, -77, 110, 49, 31, and on one P 30, 3 and then 0; over each five hundred of five thousand on 16
				// Ps 465, 163, -14, 35, -22, -5, 55, -45, 0, 23, and on one P 6 and then 0. The goroutines stood at 21 at every sample. The
				// exact memory profile between reload 100 and reload 1000 holds 188 more g structs and 440 more sudogs on 16 Ps, none of either
				// on one P, and in both nothing more that the host allocated.
				procs := runtime.GOMAXPROCS(1)
				t.Cleanup(func() { runtime.GOMAXPROCS(procs) })
			}
			dir, temp := t.TempDir(), t.TempDir()
			marker := reloadLeakMarker + "=" + strconv.Itoa(os.Getpid()) + "-" + string(row.sdk) + "-" + row.realization
			t.Setenv(reloadLeakMarker, strings.TrimPrefix(marker, reloadLeakMarker+"="))
			t.Setenv("TMPDIR", temp)
			roots := []string{dir, temp, fixtureRoot}

			configs := row.configs(t, root, dir)
			host := subprocess.NewHost(dir)
			t.Cleanup(func() { host.Shutdown("done") })
			bridge := subprocess.NewUIBridge(func() {})
			recording := newRecordingUI(new([]string), new([]string))
			var ui extension.UIContext = recording
			var surfaces *reloadLeakSurfaces
			if row.surfaces != "" {
				surfaces = newReloadLeakSurfaces(row.surfaces, bridge, recording)
				ui = reloadLeakDialogUI{recording}
			}
			bridge.SetUIContext(ui)
			bridge.SetNotifyFunc(func(string, string) {})
			bridge.SetHostAction("getFlag", func(_, _ string) any { return nil })
			host.SetUIBridge(bridge)
			host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return slices.Clone(configs), nil })
			loaded, errs := host.LoadAll(t.Context(), slices.Clone(configs))
			if len(errs) != 0 || len(loaded) != len(configs) {
				t.Fatalf("load: %d of %d loaded, %v", len(loaded), len(configs), errs)
			}
			runner := inproc.NewRunner(loaded, dir)
			bridge.SetUIPromptScope(runner)
			if row.afterLoad != nil {
				row.afterLoad(t)
			}

			// artifacts are the cells of the latest reload: an unchanged source runs the artifact it ran before.
			var artifacts []string
			// The row's own records are sized before the baseline, so the heap the samples compare does not hold their growth.
			durations := make([]time.Duration, 0, max(reloads, reloadLeakWarmup))
			samples := make([]reloadLeakSample, 0, reloads/reloadLeakSampleEvery+2)
			reload := func(n int) {
				t.Helper()
				start := time.Now()
				if _, err := runner.Emit(t.Context(), extension.SessionShutdownEvent{Type: "session_shutdown", Reason: "reload"}); err != nil {
					t.Fatalf("reload %d: session_shutdown: %v", n, err)
				}
				runner.Invalidate("")
				if row.invalidated {
					host.Invalidate("")
				}
				var err error
				if loaded, err = host.Reload(t.Context()); err != nil {
					t.Fatalf("reload %d: %v", n, err)
				}
				report := host.LastReloadReport()
				if len(loaded) != len(configs) || report == nil || len(report.Issues) != 0 || report.Error != "" {
					t.Fatalf("reload %d: %d of %d loaded, report %+v", n, len(loaded), len(configs), report)
				}
				artifacts = artifacts[:0]
				for _, cell := range report.Cells {
					if cell.Strategy != row.strategy {
						t.Fatalf("reload %d: cell %s runs as %s, want %s", n, cell.Key, cell.Strategy, row.strategy)
					}
					artifacts = append(artifacts, cell.Key+" "+cell.Hash+" "+cell.BinaryPath)
				}
				if want := min(len(row.strategy), 1); len(report.Cells) != want {
					t.Fatalf("reload %d: %d cells, want %d: %+v", n, len(report.Cells), want, report.Cells)
				}
				runner = inproc.NewRunner(loaded, dir)
				bridge.SetUIPromptScope(runner)
				if _, err := runner.Emit(t.Context(), extension.SessionStartEvent{Type: "session_start", Reason: "reload"}); err != nil {
					t.Fatalf("reload %d: session_start: %v", n, err)
				}
				durations = append(durations, time.Since(start))
				if surfaces != nil {
					surfaces.draw(t, n, runner, loaded)
				}
			}
			// answer proves that the extensions the host holds after a reload are running: each answers a command.
			answer := func(n int) {
				t.Helper()
				for _, ext := range loaded {
					command, ok := ext.Commands[row.command]
					if !ok {
						t.Fatalf("reload %d: %s registers no command %s", n, ext.Name, row.command)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
					err := command.Handler(ctx, "")
					cancel()
					if err != nil {
						t.Fatalf("reload %d: %s command %s: %v", n, ext.Name, row.command, err)
					}
				}
			}
			var baseline *reloadLeakSample
			var baselineFiles []string
			var baselineProcesses map[int]string
			take := func(n int) (reloadLeakSample, []string, map[int]string) {
				t.Helper()
				sample := reloadLeakSample{reloads: n}
				// The goroutines of the replaced extensions end on their own; the first sample waits until their number stands still.
				deadline := time.Now().Add(reloadLeakSettle)
				for previous := -1; ; {
					runtime.GC()
					sample.goroutines = runtime.NumGoroutine()
					if baseline != nil && sample.goroutines <= baseline.goroutines || baseline == nil && sample.goroutines == previous || time.Now().After(deadline) {
						break
					}
					previous = sample.goroutines
					time.Sleep(100 * time.Millisecond)
				}
				var processes map[int]string
				for {
					processes, sample.zombies, sample.rssBytes = reloadLeakProcesses(marker)
					if baseline != nil && len(processes) <= baseline.processes && sample.zombies == 0 || time.Now().After(deadline) {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				sample.processes = len(processes)
				runtime.GC()
				runtime.GC()
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				sample.heapBytes, sample.heapObjects = stats.HeapAlloc, stats.HeapObjects
				sample.fds, sample.inotify, sample.inotifyWatches = reloadLeakDescriptors(t)
				files := reloadLeakFiles(t, roots)
				sample.files = len(files)
				return sample, files, processes
			}

			for n := 1; n <= reloadLeakWarmup; n++ {
				reload(-n)
			}
			answer(0)
			baselineArtifacts := slices.Clone(artifacts)
			first, files, processes := take(0)
			baseline, baselineFiles, baselineProcesses = &first, files, processes
			if row.strategy != "" && baseline.processes == 0 {
				t.Fatalf("no extension process found below this one or by %s: the row would not see a process that stays", reloadLeakMarker)
			}
			samples = append(samples, first)
			durations = durations[:0]
			var lastFiles []string
			var lastProcesses map[int]string
			for n := 1; n <= reloads; n++ {
				reload(n)
				if !slices.Equal(artifacts, baselineArtifacts) {
					t.Fatalf("reload %d runs another artifact than the baseline for an unchanged source:\n  %s\nbaseline:\n  %s", n, strings.Join(artifacts, "\n  "), strings.Join(baselineArtifacts, "\n  "))
				}
				if n%reloadLeakSampleEvery == 0 || n == reloads {
					answer(n)
					var sample reloadLeakSample
					sample, lastFiles, lastProcesses = take(n)
					samples = append(samples, sample)
				}
			}
			last := samples[len(samples)-1]
			slices.Sort(durations)
			slope := reloadLeakSlope(samples)
			t.Logf("LEAKRSS %s %s extension process resident bytes (reloads:bytes) %s", row.sdk, row.realization, func() string {
				parts := make([]string, len(samples))
				for i, s := range samples {
					parts[i] = fmt.Sprintf("%d:%d", s.reloads, s.rssBytes)
				}
				return strings.Join(parts, " ")
			}())
			t.Logf("LEAKROW %s %s reloads=%d reload-p50=%.1fms reload-p90=%.1fms | goroutines %d->%d | fds %d->%d | processes %d->%d zombies %d | inotify %d->%d watches %d->%d | files %d->%d | heap %d->%d bytes (%+d, slope %+.0f B/reload) | heap objects %d->%d (%+d)",
				row.sdk, row.realization, reloads, percentile(durations, 0.50), percentile(durations, 0.90),
				baseline.goroutines, last.goroutines, baseline.fds, last.fds, baseline.processes, last.processes, last.zombies,
				baseline.inotify, last.inotify, baseline.inotifyWatches, last.inotifyWatches, baseline.files, last.files,
				baseline.heapBytes, last.heapBytes, int64(last.heapBytes)-int64(baseline.heapBytes), slope,
				baseline.heapObjects, last.heapObjects, int64(last.heapObjects)-int64(baseline.heapObjects))
			// A structure that fills flattens over the tenths of a long run; what a reload leaks does not.
			var tenths []string
			for i := 1; i <= 10; i++ {
				from, to := samples[(i-1)*(len(samples)-1)/10], samples[i*(len(samples)-1)/10]
				if to.reloads > from.reloads {
					tenths = append(tenths, fmt.Sprintf("%d:%+d/%d", to.reloads, (int64(to.heapBytes)-int64(from.heapBytes))/int64(to.reloads-from.reloads), to.goroutines))
				}
			}
			t.Logf("LEAKTENTHS %s %s on %d P: live heap bytes per reload and goroutines over each tenth of the run (reloads:bytes/goroutines) %s", row.sdk, row.realization, runtime.GOMAXPROCS(0), strings.Join(tenths, " "))
			series := func(value func(reloadLeakSample) int64) string {
				parts := make([]string, len(samples))
				for i, s := range samples {
					parts[i] = fmt.Sprintf("%d:%d", s.reloads, value(s))
				}
				return strings.Join(parts, " ")
			}
			grew := func(resource string, bound int64, value func(reloadLeakSample) int64, detail string) {
				t.Helper()
				if growth := value(last) - value(*baseline); growth > bound {
					t.Errorf("%s %s: %s grew by %d over %d reloads (bound %d); samples (reloads:value) %s%s", row.sdk, row.realization, resource, growth, reloads, bound, series(value), detail)
				}
			}
			kept := ""
			if growth := last.goroutines - baseline.goroutines; growth > 0 {
				kept = "\n" + reloadLeakStacks(max(2, growth/2))
			}
			grew("goroutines", 0, func(s reloadLeakSample) int64 { return int64(s.goroutines) }, kept)
			grew("open descriptors", 0, func(s reloadLeakSample) int64 { return int64(s.fds) }, "")
			var stayed []string
			for pid, name := range lastProcesses {
				if _, ok := baselineProcesses[pid]; !ok {
					stayed = append(stayed, fmt.Sprintf("%d(%s)", pid, name))
				}
			}
			slices.Sort(stayed)
			grew("processes", 0, func(s reloadLeakSample) int64 { return int64(s.processes) }, fmt.Sprintf("; processes the baseline did not have: %s", strings.Join(stayed, " ")))
			if last.zombies != 0 {
				t.Errorf("%s %s: %d ended child process(es) nobody waited for after %d reloads; samples %s", row.sdk, row.realization, last.zombies, reloads, series(func(s reloadLeakSample) int64 { return int64(s.zombies) }))
			}
			grew("inotify instances", 0, func(s reloadLeakSample) int64 { return int64(s.inotify) }, "")
			grew("inotify watches", 0, func(s reloadLeakSample) int64 { return int64(s.inotifyWatches) }, "")
			var added []string
			for _, file := range lastFiles {
				if _, found := slices.BinarySearch(baselineFiles, file); !found {
					added = append(added, file)
				}
			}
			if len(added) > 12 {
				added = append(added[:12], fmt.Sprintf("... and %d more", len(added)-12))
			}
			grew("files", 0, func(s reloadLeakSample) int64 { return int64(s.files) }, "; files the baseline did not have:\n  "+strings.Join(added, "\n  "))
			if row.parallel {
				return
			}
			grew("live heap bytes", reloadLeakHeapBytes, func(s reloadLeakSample) int64 { return int64(s.heapBytes) }, "")
			if slope > reloadLeakHeapSlopeBytes {
				t.Errorf("%s %s: live heap grows by %.0f bytes per reload (bound %d); samples (reloads:bytes) %s", row.sdk, row.realization, slope, reloadLeakHeapSlopeBytes, series(func(s reloadLeakSample) int64 { return int64(s.heapBytes) }))
			}
			grew("live heap objects", reloadLeakHeapObjects, func(s reloadLeakSample) int64 { return int64(s.heapObjects) }, "")
		})
	}
}
