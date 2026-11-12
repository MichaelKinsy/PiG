//go:build unix && !pig_strip_node_extensions

package subprocess

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// A reload keeps the process of an unchanged extension and runs its factory again over a new connection. The generation it replaced must
// leave nothing that waits for that process: these tests hold what the end of the process still has to do, once, however many
// generations ran in it.

// waiterFixture is one way a host keeps a process over a reload.
type waiterFixture struct {
	name string
	// configs writes the extensions and returns their configs; a different edit is a different source.
	configs func(t *testing.T, edit int) []ExtConfig
	// packed is set when the process is a packed cell's, which a watcher goroutine observes.
	packed bool
	// replacedByEdit is set when a reload after a source edit starts another process.
	replacedByEdit bool
}

func waiterFixtures() []waiterFixture {
	node := func(names ...string) func(*testing.T, int) []ExtConfig {
		return func(t *testing.T, _ int) []ExtConfig {
			nodeCellRequireNode(t)
			root := t.TempDir()
			var configs []ExtConfig
			for _, name := range names {
				entry := filepath.Join(root, name+".mjs")
				nodeCellTrivialExtension(t, entry, name)
				cfg := ExtConfig{Name: name, Source: entry, Enabled: true}
				if len(names) == 1 {
					cfg.Isolation = "strict"
				}
				configs = append(configs, cfg)
			}
			return configs
		}
	}
	return []waiterFixture{
		{name: "node isolated", configs: node("waiter-node-lone")},
		{name: "node packed", configs: node("waiter-node-a", "waiter-node-b"), packed: true},
		{name: "go packed", packed: true, replacedByEdit: true, configs: func(t *testing.T, edit int) []ExtConfig {
			var configs []ExtConfig
			for _, name := range []string{"waiter-go-a", "waiter-go-b"} {
				module := "example.com/waiter/" + name
				root := writePackedFactoryModule(t, module, name, fmt.Sprintf("%s_tool_%d", strings.ReplaceAll(name, "-", "_"), edit))
				configs = append(configs, packedFactoryConfig(name, root, module, fmt.Sprintf("%s-%d", name, edit)))
			}
			return configs
		}},
	}
}

// waiterHost is a host over a fixture with its crash reports.
type waiterHost struct {
	host    *Host
	configs []ExtConfig
	mu      sync.Mutex
	reports []string
}

func newWaiterHost(t *testing.T, fixture waiterFixture, retention *RuntimeRetention) *waiterHost {
	t.Helper()
	w := &waiterHost{host: NewHost(t.TempDir()), configs: fixture.configs(t, 0)}
	if retention != nil {
		w.host.SetRuntimeRetention(retention)
	}
	w.host.SetConfigLoader(func() ([]ExtConfig, error) { return slices.Clone(w.configs), nil })
	w.host.SetCrashHandler(func(name string, _ time.Duration, _ bool, reason string) {
		w.mu.Lock()
		w.reports = append(w.reports, name+": "+reason)
		w.mu.Unlock()
	})
	t.Cleanup(func() { w.host.Shutdown("test done") })
	return w
}

// load is the host's first load of its extensions.
func (w *waiterHost) load(t *testing.T) {
	t.Helper()
	// The host binds an extension's lifetime to the context of its load, so that context is the test's.
	loaded, errs := w.host.LoadAll(t.Context(), slices.Clone(w.configs))
	if len(errs) != 0 || len(loaded) != len(w.configs) {
		t.Fatalf("load: %d of %d loaded, %v", len(loaded), len(w.configs), errs)
	}
}

func (w *waiterHost) reload(t *testing.T) {
	t.Helper()
	loaded, err := w.host.Reload(t.Context())
	if err != nil || len(loaded) != len(w.configs) {
		t.Fatalf("reload: %d of %d loaded, %v, issues %q", len(loaded), len(w.configs), err, w.host.LastReloadReport().Issues)
	}
}

func (w *waiterHost) crashReports() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.reports)
}

// processes returns the extension processes the host holds, by pid, each with its stderr log.
func (w *waiterHost) processes() map[int]*processStderrLog {
	out := map[int]*processStderrLog{}
	w.host.mu.Lock()
	var lone []*managedExt
	for state := range w.host.watchedPacked {
		if spawner := state.spawner(); spawner.cmd != nil && spawner.cmd.Process != nil {
			out[spawner.cmd.Process.Pid] = state.stderrLog
		}
	}
	for _, me := range w.host.exts {
		if me.packedProcess == nil {
			lone = append(lone, me)
		}
	}
	w.host.mu.Unlock()
	for _, me := range lone {
		if proc, _ := me.processHandle(); proc != nil {
			out[proc.Pid] = me.stderrLog
		}
	}
	return out
}

// oneProcess returns the host's one extension process and the file of its stderr log.
func (w *waiterHost) oneProcess(t *testing.T) (pid int, logPath string) {
	t.Helper()
	processes := w.processes()
	if len(processes) != 1 {
		t.Fatalf("the host holds %d extension processes, want 1: %v", len(processes), processes)
	}
	for pid, log := range processes {
		if log == nil {
			t.Fatalf("process %d has no stderr log", pid)
		}
		log.mu.Lock()
		path := log.path
		log.mu.Unlock()
		return pid, path
	}
	return 0, ""
}

// processWaiters counts the goroutines that stand in a connection's incoming loop and in a packed process's watcher. A host has one loop
// for each connection it holds and one watcher for each packed process.
func processWaiters(t *testing.T) (loops, watchers int) {
	t.Helper()
	var profile bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&profile, 1); err != nil {
		t.Fatal(err)
	}
	_, stacks, _ := strings.Cut(profile.String(), "\n")
	for block := range strings.SplitSeq(stacks, "\n\n") {
		head, _, _ := strings.Cut(block, "\n")
		count, _ := strconv.Atoi(strings.TrimSpace(strings.SplitN(head, "@", 2)[0]))
		if strings.Contains(block, "subprocess.(*Host).handleIncoming") {
			loops += count
		}
		if strings.Contains(block, "subprocess.(*Host).watchPackedProcess") {
			watchers += count
		}
	}
	return loops, watchers
}

func waiterEventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("not within 20 s: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waiterProcessAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func waiterFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// requireOneWaiterPerProcess requires the loops of the host's connections and the watcher of its process and no more, however many
// generations ran before.
func requireOneWaiterPerProcess(t *testing.T, fixture waiterFixture, w *waiterHost, baseLoops, baseWatchers int) {
	t.Helper()
	wantLoops, wantWatchers := baseLoops+len(w.configs), baseWatchers
	if fixture.packed {
		wantWatchers++
	}
	var loops, watchers int
	deadline := time.Now().Add(5 * time.Second)
	for {
		loops, watchers = processWaiters(t)
		if loops == wantLoops && watchers == wantWatchers || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if loops != wantLoops || watchers != wantWatchers {
		t.Errorf("after the reloads %d goroutines stand in connection loops and %d in process watchers, want %d and %d: one loop for each connection and one watcher for a packed process", loops, watchers, wantLoops, wantWatchers)
	}
}

const waiterReloads = 5

// processDeathReports loads the fixture, reloads it unchanged the given number of times, kills its process and returns what the host
// reported, with the log's path replaced by a fixed word, and whether the log a report names is still there.
func processDeathReports(t *testing.T, fixture waiterFixture, reloads int) (reports []string) {
	t.Helper()
	baseLoops, baseWatchers := processWaiters(t)
	w := newWaiterHost(t, fixture, nil)
	w.load(t)
	pid, logPath := w.oneProcess(t)
	for range reloads {
		w.reload(t)
	}
	if now, _ := w.oneProcess(t); now != pid {
		t.Fatalf("a reload of an unchanged source replaced process %d with %d", pid, now)
	}
	requireOneWaiterPerProcess(t, fixture, w, baseLoops, baseWatchers)
	if !waiterFileExists(logPath) {
		t.Fatalf("the stderr log %s is gone while its process runs", logPath)
	}
	if err := killTestProcess(pid); err != nil {
		t.Fatal(err)
	}
	waiterEventually(t, "a crash report for each extension", func() bool { return len(w.crashReports()) >= len(w.configs) })
	// A further report of the same death would come from a generation that should have ended: give it the time to arrive.
	time.Sleep(1500 * time.Millisecond)
	named := false
	for _, report := range w.crashReports() {
		named = named || strings.Contains(report, logPath)
		reports = append(reports, strings.ReplaceAll(report, logPath, "<log>"))
	}
	slices.Sort(reports)
	if named && !waiterFileExists(logPath) {
		t.Errorf("after %d reloads a crash report names %s, and the file is gone: %q", reloads, logPath, reports)
	}
	if !named {
		waiterEventually(t, "the log no report names is removed", func() bool { return !waiterFileExists(logPath) })
	}
	w.host.Shutdown("test done")
	return reports
}

// TestProcessExitAfterUnchangedReloadsIsReportedOnce: a process that dies after several generations ran in it is reported as one that
// dies with its first generation is: the same notices, each once, and the log they name is still there.
func TestProcessExitAfterUnchangedReloadsIsReportedOnce(t *testing.T) {
	for _, fixture := range waiterFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			first := processDeathReports(t, fixture, 0)
			later := processDeathReports(t, fixture, waiterReloads)
			if !slices.Equal(first, later) {
				t.Errorf("the death of a process after %d unchanged reloads is reported as\n  %s\nand the death of one that was never reloaded as\n  %s", waiterReloads, strings.Join(later, "\n  "), strings.Join(first, "\n  "))
			}
		})
	}
}

// TestParkedProcessExitRemovesItsLogOnce: a process dies between the generation a Host closed when it retired and the generation its
// successor would have started. No extension runs in it then, so nothing is reported; its log is removed, once.
func TestParkedProcessExitRemovesItsLogOnce(t *testing.T) {
	for _, fixture := range waiterFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			baseLoops, baseWatchers := processWaiters(t)
			retention := NewRuntimeRetention()
			t.Cleanup(retention.Close)
			w := newWaiterHost(t, fixture, retention)
			w.load(t)
			pid, logPath := w.oneProcess(t)
			for range waiterReloads {
				w.reload(t)
			}
			w.host.Retain()
			w.host.Shutdown("replaced")
			// The retired Host holds no connection, so it has no loop and no watcher left, while the process it parked runs on.
			waiterEventually(t, "the retired Host's loops and watchers end", func() bool {
				loops, watchers := processWaiters(t)
				return loops == baseLoops && watchers == baseWatchers
			})
			time.Sleep(300 * time.Millisecond)
			if !waiterProcessAlive(pid) {
				t.Fatalf("process %d did not outlive the Host that parked it", pid)
			}
			if !waiterFileExists(logPath) {
				t.Fatalf("the stderr log %s is gone while its parked process runs", logPath)
			}

			if err := killTestProcess(pid); err != nil {
				t.Fatal(err)
			}
			waiterEventually(t, "the log of the dead parked process is removed", func() bool { return !waiterFileExists(logPath) })
			// A second removal would take a file that is written here in the first one's place.
			if err := os.WriteFile(logPath, []byte("not the log"), 0o600); err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
			if !waiterFileExists(logPath) {
				t.Errorf("the log %s was removed a second time", logPath)
			}
			_ = os.Remove(logPath)
			if reports := w.crashReports(); len(reports) != 0 {
				t.Errorf("a parked process that died was reported though no extension ran in it: %q", reports)
			}

			// The successor finds the parked process dead and starts its own.
			next := newWaiterHost(t, fixture, retention)
			next.configs = w.configs
			next.load(t)
			if now, _ := next.oneProcess(t); now == pid {
				t.Fatalf("the successor runs in the dead process %d", pid)
			}
		})
	}
}

// TestSourceEditEndsTheReplacedProcessWaiter: a reload after a source edit starts another process; the one it replaces ends, with its
// watcher and its log.
func TestSourceEditEndsTheReplacedProcessWaiter(t *testing.T) {
	for _, fixture := range waiterFixtures() {
		if !fixture.replacedByEdit {
			continue
		}
		t.Run(fixture.name, func(t *testing.T) {
			baseLoops, baseWatchers := processWaiters(t)
			w := newWaiterHost(t, fixture, nil)
			w.load(t)
			pid, logPath := w.oneProcess(t)
			for range waiterReloads {
				w.reload(t)
			}
			w.configs = fixture.configs(t, 1)
			w.reload(t)
			waiterEventually(t, "the replaced process ends", func() bool { return !waiterProcessAlive(pid) })
			waiterEventually(t, "the host holds the one new process", func() bool {
				processes := w.processes()
				_, old := processes[pid]
				return len(processes) == 1 && !old
			})
			requireOneWaiterPerProcess(t, fixture, w, baseLoops, baseWatchers)
			waiterEventually(t, "the log of the replaced process is removed", func() bool { return !waiterFileExists(logPath) })
			if reports := w.crashReports(); len(reports) != 0 {
				t.Errorf("a process a reload replaced was reported as a crash: %q", reports)
			}
		})
	}
}
