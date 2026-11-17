//go:build unix && !pig_strip_node_extensions

package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	// death is how the host reports the end of the fixture's process (deathNotices).
	death string
}

// The ways a host reports a process that was killed.
const (
	// deathRestarts: an extension alone in its process starts again.
	deathRestarts = "restarts"
	// deathRestartsCell: the members of a Node cell start again together.
	deathRestartsCell = "restarts the cell"
	// deathStopsCell: the members of a Go, Python or Rust cell stop until a /reload.
	deathStopsCell = "stops the cell"
)

// deathNotices is what the host reports when the fixture's process is killed: one notice for each extension that ran in it, in load order, as
// docs/runtime-cell-reload.md has it. The path of the process's log reads <log>.
func (f waiterFixture) deathNotices(configs []ExtConfig) []string {
	names := make([]string, len(configs))
	for i, cfg := range configs {
		names[i] = cfg.Name
	}
	sorted := slices.Sorted(slices.Values(names))
	notices := make([]string, len(names))
	for i, name := range names {
		switch f.death {
		case deathRestarts:
			notices[i] = name + " restarts: (stderr: <log>)"
		case deathRestartsCell:
			notices[i] = name + " restarts: packed process exited: signal: killed; restart packed group; interrupted callbacks are not replayed (stderr: <log>)"
		case deathStopsCell:
			notices[i] = name + " stopped: packed process exited: signal: killed; " + strings.Join(sorted, ", ") + " stopped; /reload restarts them (stderr: <log>)"
		}
	}
	return notices
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
		{name: "node isolated", death: deathRestarts, configs: node("waiter-node-lone")},
		{name: "node packed", death: deathRestartsCell, configs: node("waiter-node-b", "waiter-node-a"), packed: true},
		{name: "go packed", death: deathStopsCell, packed: true, replacedByEdit: true, configs: func(t *testing.T, edit int) []ExtConfig {
			var configs []ExtConfig
			for _, name := range []string{"waiter-go-b", "waiter-go-a"} {
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
	w.host.SetCrashHandler(func(name string, _ time.Duration, disabled bool, reason string) {
		outcome := "restarts"
		if disabled {
			outcome = "stopped"
		}
		w.mu.Lock()
		w.reports = append(w.reports, name+" "+outcome+": "+reason)
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

// waiters counts what the host owns that waits for its processes: the read loops of its connections and the watchers of its packed processes
// (Host.loops, Host.runningWatchers). A host has one loop for each connection it holds and one watcher for each packed process. The
// goroutines of the process's profile are not counted: they take in the tails of the goroutines of every host a test ended before, which leave
// the profile after the signals of their end.
func (w *waiterHost) waiters() (loops, watchers int) {
	return w.host.loopCount(), int(w.host.runningWatchers.Load())
}

// requireDrained ends the host and requires that nothing of it runs: Shutdown joins the loops and the watchers, so both counts read 0 when it returns.
func (w *waiterHost) requireDrained(t *testing.T) {
	t.Helper()
	w.host.Shutdown("test done")
	if loops, watchers := w.waiters(); loops != 0 || watchers != 0 {
		t.Errorf("after Shutdown %d connection loops and %d process watchers of the host still run, want 0 and 0", loops, watchers)
	}
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
func requireOneWaiterPerProcess(t *testing.T, fixture waiterFixture, w *waiterHost) {
	t.Helper()
	wantLoops, wantWatchers := len(w.configs), 0
	if fixture.packed {
		wantWatchers = 1
	}
	var loops, watchers int
	deadline := time.Now().Add(5 * time.Second)
	for {
		loops, watchers = w.waiters()
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
// reported, in the order it reported it and with the log's path replaced by a fixed word, beside what it is to report. The end of the process
// reaches the host as the closed connection of each extension in it and, for a packed process, as the reaped process: order, when it is not
// nil, is the order the host sees them in.
func processDeathReports(t *testing.T, fixture waiterFixture, reloads int, order *deathOrder) (reports, want []string) {
	t.Helper()
	w := newWaiterHost(t, fixture, nil)
	observers := newDeathObservers(t, w, order)
	w.load(t)
	pid, logPath := w.oneProcess(t)
	for range reloads {
		w.reload(t)
	}
	if now, _ := w.oneProcess(t); now != pid {
		t.Fatalf("a reload of an unchanged source replaced process %d with %d", pid, now)
	}
	requireOneWaiterPerProcess(t, fixture, w)
	if !waiterFileExists(logPath) {
		t.Fatalf("the stderr log %s is gone while its process runs", logPath)
	}
	observers.armed.Store(true)
	if err := killTestProcess(pid); err != nil {
		t.Fatal(err)
	}
	// The loops and the watcher counted above are all that waits for the process. Once each has acted on its end, nothing is left that could
	// report it: a further report would come from a generation that should have ended, and that generation's loop or watcher is counted.
	watchers := 0
	if fixture.packed {
		watchers = 1
	}
	observers.await(t, len(w.configs), watchers)
	named := false
	for _, report := range w.crashReports() {
		named = named || strings.Contains(report, logPath)
		reports = append(reports, strings.ReplaceAll(report, logPath, "<log>"))
	}
	if named && !waiterFileExists(logPath) {
		t.Errorf("after %d reloads a crash report names %s, and the file is gone: %q", reloads, logPath, reports)
	}
	if !named {
		waiterEventually(t, "the log no report names is removed", func() bool { return !waiterFileExists(logPath) })
	}
	w.requireDrained(t)
	return reports, fixture.deathNotices(w.configs)
}

// TestProcessExitAfterUnchangedReloadsIsReportedOnce: a process that dies after several generations ran in it is reported as one that
// dies with its first generation is: one notice for each extension, in load order, and the log they name is still there.
func TestProcessExitAfterUnchangedReloadsIsReportedOnce(t *testing.T) {
	for _, fixture := range waiterFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			for _, reloads := range []int{0, waiterReloads} {
				if got, want := processDeathReports(t, fixture, reloads, nil); !slices.Equal(got, want) {
					t.Errorf("the death of a process after %d unchanged reloads is reported as\n  %s\nwant\n  %s", reloads, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
				}
			}
		})
	}
}

// TestParkedProcessExitRemovesItsLogOnce: a process dies between the generation a Host closed when it retired and the generation its
// successor would have started. No extension runs in it then, so nothing is reported; its log is removed, once.
func TestParkedProcessExitRemovesItsLogOnce(t *testing.T) {
	for _, fixture := range waiterFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
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
				loops, watchers := w.waiters()
				return loops == 0 && watchers == 0
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
			next.requireDrained(t)
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
			requireOneWaiterPerProcess(t, fixture, w)
			waiterEventually(t, "the log of the replaced process is removed", func() bool { return !waiterFileExists(logPath) })
			if reports := w.crashReports(); len(reports) != 0 {
				t.Errorf("a process a reload replaced was reported as a crash: %q", reports)
			}
			w.requireDrained(t)
		})
	}
}
