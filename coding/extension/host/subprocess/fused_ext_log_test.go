//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// loadFusedPanicProbe fuses a Go extension whose context handler panics into a new Host and returns the Host and a runner that emits to it.
func loadFusedPanicProbe(t *testing.T) (*Host, *inproc.Runner) {
	t.Helper()
	ext := sdk.New("fused-log-probe")
	ext.OnEvent("context", func(sdk.Context, map[string]any) (any, error) { panic("fused-probe-boom") })
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test complete") })
	loaded, err := host.LoadInProcess(t.Context(), ExtConfig{Name: "fused-log-probe", Enabled: true}, ext.RunWithConn)
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{*loaded}, t.TempDir())
	t.Cleanup(func() { runner.Invalidate("test complete") })
	return host, runner
}

// failHandler makes the fused extension's handler panic once, as a failing handler does in a session.
func failHandler(t *testing.T, runner *inproc.Runner) {
	t.Helper()
	messages := []extension.AgentMessage{wireMessage(map[string]any{"role": "user", "content": "one", "timestamp": 0})}
	_, _, _ = runner.EmitContextTracked(t.Context(), messages)
}

// A session whose fused extensions never fail puts nothing in the temporary directory, as Pi's does: the Host names the log and the SDK creates it with the first
// diagnostic, whose stack it holds.
func TestFusedExtLogIsCreatedByTheFirstDiagnostic(t *testing.T) {
	tmp := fusedLogTemp(t)
	_, runner := loadFusedPanicProbe(t)
	if names := tempNames(t, tmp); len(names) != 0 {
		t.Fatalf("the temporary directory holds %q after the fused extension loaded, want nothing", names)
	}
	failHandler(t, runner)
	names := tempNames(t, tmp)
	if len(names) != 1 || !strings.HasPrefix(names[0], "pig-ext-fused-") {
		t.Fatalf("the temporary directory holds %q after a handler panicked, want the fused log alone", names)
	}
	data, err := os.ReadFile(filepath.Join(tmp, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fused-probe-boom", "goroutine "} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the fused log lacks %q:\n%s", want, data)
		}
	}
}

// A process that ends through TerminateProcesses (a hung-up terminal, SIGINT, a crash, the end of print and rpc mode) leaves no fused log, as the orderly
// Shutdown leaves none.
func TestFusedExtLogEndsWithTheProcess(t *testing.T) {
	ends := map[string]func(*Host){
		"TerminateProcesses": func(h *Host) { h.TerminateProcesses() },
		"Shutdown":           func(h *Host) { h.Shutdown("quit") },
	}
	for name, end := range ends {
		t.Run(name, func(t *testing.T) {
			tmp := fusedLogTemp(t)
			host, runner := loadFusedPanicProbe(t)
			failHandler(t, runner)
			if names := tempNames(t, tmp); len(names) != 1 {
				t.Fatalf("the temporary directory holds %q after a handler panicked, want the fused log", names)
			}
			end(host)
			if names := tempNames(t, tmp); len(names) != 0 {
				t.Errorf("the temporary directory holds %q after %s, want nothing", names, name)
			}
			if got := os.Getenv(extLogEnvName); got != "" {
				t.Errorf("PIG_EXT_LOG = %q after %s, want it unset", got, name)
			}
		})
	}
}

// An extension process of its own never gets the permission to create its log: the host made the log and reads it, and one that the host removed must not be made
// again by a process that outlives it. A pig that a fused extension starts inherits the host's environment, so the entry is dropped there.
func TestWithExtLogDropsThePermissionToCreate(t *testing.T) {
	got := withExtLog([]string{"A=1", extLogEnvName + "=/old.log", extLogCreateEnvName + "=1"}, "/new.log")
	if want := []string{"A=1", extLogEnvName + "=/new.log"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("withExtLog = %q, want %q", got, want)
	}
}
