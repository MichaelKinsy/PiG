package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// notifyingUI hands every notification to a channel, for a test that waits for a command's answer.
type notifyingUI struct {
	extension.UIContext
	messages chan string
}

func (u *notifyingUI) Notify(message, _ string) { u.messages <- message }

// writeCrashingFactoryModule writes a Go factory with a command "ping" that notifies "pong-<name>" and a command "crash" that starts a goroutine of the
// extension's own, which panics: nothing recovers it, so the shared process ends with a trace that names this extension.
func writeCrashingFactoryModule(t *testing.T, modulePath, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`package ext

import "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	e := sdk.New(%q)
	e.Command("ping", "", func(ctx sdk.Context, _ string) error { ctx.Notify("pong-"+%q, "info"); return nil })
	e.Command("crash", "", func(ctx sdk.Context, _ string) error {
		go func() { panic("goroutine of " + %q) }()
		return nil
	})
	return e
}
`, name, name, name)
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A shared Go process ends because a goroutine one member started panicked. The trace blames that member: it stops, as an isolated extension stops when its
// process ends, and a /reload runs it isolated; the others restart in a shared process. Nothing quarantines the cell, and no other member is reported stopped.
func TestPackedProcessCrashBlamesTheMemberWhoseGoroutineEndedIt(t *testing.T) {
	culpritRoot := writeCrashingFactoryModule(t, "example.com/blame/culprit", "culprit")
	healthyRoot := writeCrashingFactoryModule(t, "example.com/blame/healthy", "healthy")
	configs := []ExtConfig{
		packedFactoryConfig("culprit", culpritRoot, "example.com/blame/culprit", "hc"),
		packedFactoryConfig("healthy", healthyRoot, "example.com/blame/healthy", "hh"),
	}
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	messages := make(chan string, 16)
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(&notifyingUI{UIContext: extension.NoopUIContext, messages: messages})
	h.SetUIBridge(bridge)
	var reportsMu sync.Mutex
	var reports []string
	h.SetCrashHandler(func(name string, _ time.Duration, disabled bool, reason string) {
		reportsMu.Lock()
		reports = append(reports, fmt.Sprintf("%s disabled=%t: %s", name, disabled, reason))
		reportsMu.Unlock()
	})
	ctx, cancel := buildContext(t)
	defer cancel()
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	loaded, errs := h.LoadAll(ctx, configs)
	if len(errs) != 0 || len(loaded) != 2 {
		t.Fatalf("loaded %d, errors %v", len(loaded), errs)
	}
	h.mu.Lock()
	packedKey := h.exts["culprit"].packedCellKey
	sharesCell := packedKey != "" && packedKey == h.exts["healthy"].packedCellKey
	h.mu.Unlock()
	if !sharesCell {
		t.Fatal("the two members did not share one packed cell")
	}
	ping := func(name string) {
		t.Helper()
		for _, ext := range h.Extensions() {
			if ext.Name != name {
				continue
			}
			if err := ext.Commands["ping"].Handler(ctx, ""); err != nil {
				t.Fatalf("%s ping: %v", name, err)
			}
			select {
			case got := <-messages:
				if got != "pong-"+name {
					t.Fatalf("%s answered %q", name, got)
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("%s never answered", name)
			}
			return
		}
		t.Fatalf("%s is not loaded", name)
	}
	ping("culprit")
	ping("healthy")

	var crash extension.Extension
	for _, ext := range loaded {
		if ext.Name == "culprit" {
			crash = ext
		}
	}
	if err := crash.Commands["crash"].Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}

	// The healthy member comes back in a process of a cell of its own; the culprit does not.
	deadline := time.Now().Add(60 * time.Second)
	for {
		h.mu.Lock()
		culprit, healthy := h.exts["culprit"], h.exts["healthy"]
		back := healthy != nil && healthy.ext != nil && healthy.packedProcess != nil && healthy.packedCellKey != packedKey
		stopped := culprit == nil
		h.mu.Unlock()
		if back && stopped {
			break
		}
		if time.Now().After(deadline) {
			reportsMu.Lock()
			defer reportsMu.Unlock()
			t.Fatalf("the healthy member did not restart and the culprit stop; quarantined cells %v, reports %q", h.QuarantinedCells(), reports)
		}
		time.Sleep(50 * time.Millisecond)
	}
	ping("healthy")
	h.mu.Lock()
	fault := h.nativeFaults["culprit"]
	h.mu.Unlock()
	if !strings.Contains(fault, "goroutine of culprit") {
		t.Fatalf("culprit fault = %q, want the panic that ended the process", fault)
	}
	if cells := h.QuarantinedCells(); len(cells) != 0 {
		t.Fatalf("the crash quarantined cells %v", cells)
	}
	reportsMu.Lock()
	for _, report := range reports {
		if strings.HasPrefix(report, "healthy disabled=true") || strings.Contains(report, "healthy stopped") {
			reportsMu.Unlock()
			t.Fatalf("the healthy member was reported stopped: %q", reports)
		}
	}
	blamed := slices.ContainsFunc(reports, func(report string) bool {
		return strings.HasPrefix(report, "culprit disabled=true") && strings.Contains(report, "goroutine of culprit") && strings.Contains(report, "/reload restarts it")
	})
	allReports := slices.Clone(reports)
	reportsMu.Unlock()
	if !blamed {
		t.Fatalf("no report says the culprit stopped for its own goroutine: %q", allReports)
	}

	// A /reload runs the culprit isolated, in a cell of its own.
	if _, err := h.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	ping("culprit")
	ping("healthy")
	h.mu.Lock()
	culpritKey, healthyKey := h.exts["culprit"].packedCellKey, h.exts["healthy"].packedCellKey
	h.mu.Unlock()
	if culpritKey == healthyKey {
		t.Fatalf("the reload put the culprit back beside the members it took down: %q", culpritKey)
	}
}
