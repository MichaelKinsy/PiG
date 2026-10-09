//go:build !pig_strip_node_extensions

package subprocess

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// A socket close and a process exit are two observations of the same failed member, not two failures to report. Both detector orders must claim the member exactly once.
func TestPackedCrashDetectorsReportMemberOnce(t *testing.T) {
	for _, processFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "socket-first", true: "process-first"}[processFirst], func(t *testing.T) {
			h := NewHost(t.TempDir())
			me := &managedExt{config: ExtConfig{Name: "crashed"}, packedCellKey: "cell", packedProcess: &packedProcessState{key: "cell"}}
			h.exts[me.config.Name] = me
			var notices []string
			h.SetCrashHandler(func(_ string, _ time.Duration, _ bool, reason string) { notices = append(notices, reason) })
			if processFirst {
				h.quarantinePackedCell("cell", "process exited")
				h.disablePackedMember(me, "packed member connection closed")
			} else {
				h.disablePackedMember(me, "packed member connection closed")
				h.quarantinePackedCell("cell", "process exited")
			}
			if len(notices) != 1 {
				t.Fatalf("one failed member produced %d notices: %v", len(notices), notices)
			}
		})
	}
}

func TestCrashingCommandHasOneDiagnostic(t *testing.T) {
	for _, isolation := range []string{"shared-ok", "strict"} {
		t.Run(isolation, func(t *testing.T) {
			privatePackedLogTemp(t)
			entry := filepath.Join(t.TempDir(), "crash.mjs")
			if err := os.WriteFile(entry, []byte(`export default function(pi) { pi.registerCommand("crash", {handler: async () => process.exit(7)}); }`), 0o600); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			var mu sync.Mutex
			var diagnostics []string
			reported := make(chan struct{}, 1)
			h.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
				mu.Lock()
				diagnostics = append(diagnostics, FormatCrashNotice(name, delay, disabled, reason))
				mu.Unlock()
				reported <- struct{}{}
			})
			loaded, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "crash", Source: entry, Isolation: isolation, Enabled: true, SupervisorConfig: SupervisorConfig{MaxCrashes: 1}}})
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			runner := inproc.NewRunner(loaded, t.TempDir())
			runner.AddErrorListener(func(err *extension.ExtensionError) {
				mu.Lock()
				diagnostics = append(diagnostics, err.Error)
				mu.Unlock()
			})
			if !runner.ExecuteCommand(t.Context(), "crash", "") {
				t.Fatal("crash command missing")
			}
			<-reported
			h.Shutdown("after crash")
			mu.Lock()
			defer mu.Unlock()
			if len(diagnostics) != 1 || !strings.Contains(diagnostics[0], "stderr:") {
				t.Fatalf("one command crash produced diagnostics: %v", diagnostics)
			}
		})
	}
}

func TestInvocationErrorKeepsCauseAndOnlyDelegatesTransportDiagnostics(t *testing.T) {
	h := NewHost(t.TempDir())
	transport := &TransportError{Extension: "failed", Operation: "read", Err: io.EOF}
	got := h.invocationError(transport)
	if _, owned := errors.AsType[*invocation.LifecycleError](got); owned || !errors.Is(got, transport) {
		t.Fatalf("without a crash handler, the runner must own the error: %v", got)
	}
	h.SetCrashHandler(func(string, time.Duration, bool, string) {})
	got = h.invocationError(transport)
	if _, owned := errors.AsType[*invocation.LifecycleError](got); !owned || !errors.Is(got, io.EOF) || got.Error() != transport.Error() {
		t.Fatalf("delegated diagnostic lost the invocation failure: %v", got)
	}
	ordinary := errors.New("connection closed")
	got = h.invocationError(ordinary)
	if _, owned := errors.AsType[*invocation.LifecycleError](got); owned || !errors.Is(got, ordinary) {
		t.Fatalf("ordinary error was mistaken for a transport failure: %v", got)
	}
}

func TestCommandErrorsStillReportWithoutLifecycleOwnership(t *testing.T) {
	for _, crash := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-throw", true: "no-crash-handler"}[crash], func(t *testing.T) {
			privatePackedLogTemp(t)
			entry := filepath.Join(t.TempDir(), "fail.mjs")
			body := `throw new Error("connection closed")`
			if crash {
				body = `process.exit(7)`
			}
			if err := os.WriteFile(entry, []byte(`export default function(pi) { pi.registerCommand("fail", {handler: async () => { `+body+`; }}); }`), 0o600); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			if !crash {
				h.SetCrashHandler(func(_ string, _ time.Duration, _ bool, reason string) {
					t.Errorf("throw reported as crash: %s", reason)
				})
			}
			loaded, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "fail", Source: entry, Enabled: true}})
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			runner := inproc.NewRunner(loaded, t.TempDir())
			var diagnostics []string
			runner.AddErrorListener(func(err *extension.ExtensionError) { diagnostics = append(diagnostics, err.Error) })
			if !runner.ExecuteCommand(t.Context(), "fail", "") {
				t.Fatal("missing command")
			}
			h.Shutdown("after command")
			if len(diagnostics) != 1 || (!crash && diagnostics[0] != "connection closed") {
				t.Fatalf("command diagnostics = %v", diagnostics)
			}
		})
	}
}

func TestPackedMemberClosureAfterReplacementDoesNotReportOrUnregister(t *testing.T) {
	h := NewHost(t.TempDir())
	old := &managedExt{config: ExtConfig{Name: "same"}, providerNames: []string{"provider"}, packedCellKey: "old"}
	replacement := &managedExt{config: ExtConfig{Name: "same"}, packedCellKey: "new"}
	h.exts["same"] = replacement
	h.SetCrashHandler(func(_ string, _ time.Duration, _ bool, reason string) { t.Errorf("stale crash: %s", reason) })
	h.SetProviderCallbacks(nil, func(name string) { t.Errorf("stale unregister: %s", name) })
	h.disablePackedMember(old, "connection closed")
	if h.exts["same"] != replacement {
		t.Fatal("stale closure removed replacement")
	}
}
