package subprocess

import (
	"context"
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

// writeCrashingFactoryModule writes a Go factory with these commands: "ping" notifies "pong-<name>"; "handled" panics in its handler, which the SDK recovers
// (and writes a goroutine block for to the log the cell shares); "crash" starts a goroutine of the extension's own that panics, which nothing recovers, so the
// shared process ends with a trace that names this extension; "exit" ends the process with os.Exit(3), which writes no trace.
func writeCrashingFactoryModule(t *testing.T, modulePath, name string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`package ext

import (
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New(%[1]q)
	e.Command("ping", "", func(ctx sdk.Context, _ string) error { ctx.Notify("pong-"+%[1]q, "info"); return nil })
	e.Command("handled", "", func(ctx sdk.Context, _ string) error { panic("handled panic of " + %[1]q) })
	e.Command("crash", "", func(ctx sdk.Context, _ string) error {
		go func() { panic("goroutine of " + %[1]q) }()
		return nil
	})
	e.Command("exit", "", func(ctx sdk.Context, _ string) error { os.Exit(3); return nil })
	return e
}
`, name)
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// crashCell is a packed Go cell of two members, "culprit" and "healthy", loaded in one process.
type crashCell struct {
	t         *testing.T
	h         *Host
	ctx       context.Context
	loaded    []extension.Extension
	packedKey string
	messages  chan string
	mu        sync.Mutex
	reports   []string
}

func loadCrashCell(t *testing.T) *crashCell {
	t.Helper()
	culpritRoot := writeCrashingFactoryModule(t, "example.com/blame/culprit", "culprit")
	healthyRoot := writeCrashingFactoryModule(t, "example.com/blame/healthy", "healthy")
	configs := []ExtConfig{
		packedFactoryConfig("culprit", culpritRoot, "example.com/blame/culprit", "hc"),
		packedFactoryConfig("healthy", healthyRoot, "example.com/blame/healthy", "hh"),
	}
	c := &crashCell{t: t, messages: make(chan string, 16)}
	c.h = NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { c.h.Shutdown("test done") })
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(&notifyingUI{UIContext: extension.NoopUIContext, messages: c.messages})
	c.h.SetUIBridge(bridge)
	c.h.SetCrashHandler(func(name string, _ time.Duration, disabled bool, reason string) {
		c.mu.Lock()
		c.reports = append(c.reports, fmt.Sprintf("%s disabled=%t: %s", name, disabled, reason))
		c.mu.Unlock()
	})
	ctx, cancel := buildContext(t)
	t.Cleanup(cancel)
	c.ctx = ctx
	c.h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	var errs []error
	c.loaded, errs = c.h.LoadAll(ctx, configs)
	if len(errs) != 0 || len(c.loaded) != 2 {
		t.Fatalf("loaded %d, errors %v", len(c.loaded), errs)
	}
	c.h.mu.Lock()
	c.packedKey = c.h.exts["culprit"].packedCellKey
	shares := c.packedKey != "" && c.packedKey == c.h.exts["healthy"].packedCellKey
	c.h.mu.Unlock()
	if !shares {
		t.Fatal("the two members did not share one packed cell")
	}
	c.ping("culprit")
	c.ping("healthy")
	return c
}

func (c *crashCell) ping(name string) {
	c.t.Helper()
	for _, ext := range c.h.Extensions() {
		if ext.Name != name {
			continue
		}
		if err := ext.Commands["ping"].Handler(c.ctx, ""); err != nil {
			c.t.Fatalf("%s ping: %v", name, err)
		}
		select {
		case got := <-c.messages:
			if got != "pong-"+name {
				c.t.Fatalf("%s answered %q", name, got)
			}
		case <-time.After(30 * time.Second):
			c.t.Fatalf("%s never answered", name)
		}
		return
	}
	c.t.Fatalf("%s is not loaded", name)
}

// run starts a command of the member as it was first loaded, and returns the command's error.
func (c *crashCell) run(name, command string) error {
	c.t.Helper()
	for _, ext := range c.loaded {
		if ext.Name == name {
			return ext.Commands[command].Handler(c.ctx, "")
		}
	}
	c.t.Fatalf("%s is not loaded", name)
	return nil
}

func (c *crashCell) reportsCopy() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.reports)
}

// waitFor polls until cond holds, or fails the test with the reports.
func (c *crashCell) waitFor(what string, cond func() bool) {
	c.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			c.t.Fatalf("%s did not happen; quarantined cells %v, reports %q", what, c.h.QuarantinedCells(), c.reportsCopy())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A shared Go process ends because a goroutine one member started panicked. The trace blames that member: it stops, as an isolated extension stops when its
// process ends, and a /reload runs it isolated; the others restart in a shared process. Nothing quarantines the cell, and no other member is reported stopped.
// A panic the other member's handler raised earlier, which the SDK recovered, left a goroutine block in the log they share, and does not decide.
func TestPackedProcessCrashBlamesTheMemberWhoseGoroutineEndedIt(t *testing.T) {
	c := loadCrashCell(t)
	if err := c.run("healthy", "handled"); err == nil {
		t.Fatal("the healthy member's handler panic was not reported as an error")
	}
	c.ping("healthy")
	if err := c.run("culprit", "crash"); err != nil {
		t.Fatal(err)
	}

	// The healthy member comes back in a process of a cell of its own; the culprit does not.
	c.waitFor("the healthy member's restart and the culprit's stop", func() bool {
		c.h.mu.Lock()
		defer c.h.mu.Unlock()
		healthy := c.h.exts["healthy"]
		return healthy != nil && healthy.ext != nil && healthy.packedProcess != nil && healthy.packedCellKey != c.packedKey && c.h.exts["culprit"] == nil
	})
	c.ping("healthy")
	c.h.mu.Lock()
	fault := c.h.nativeFaults["culprit"]
	c.h.mu.Unlock()
	if !strings.Contains(fault, "goroutine of culprit") {
		t.Fatalf("culprit fault = %q, want the panic that ended the process", fault)
	}
	if cells := c.h.QuarantinedCells(); len(cells) != 0 {
		t.Fatalf("the crash quarantined cells %v", cells)
	}
	reports := c.reportsCopy()
	for _, report := range reports {
		if strings.HasPrefix(report, "healthy disabled=true") || strings.Contains(report, "healthy stopped") {
			t.Fatalf("the healthy member was reported stopped: %q", reports)
		}
	}
	if !slices.ContainsFunc(reports, func(report string) bool {
		return strings.HasPrefix(report, "culprit disabled=true") && strings.Contains(report, "goroutine of culprit") && strings.Contains(report, "/reload restarts it")
	}) {
		t.Fatalf("no report says the culprit stopped for its own goroutine: %q", reports)
	}

	// A /reload runs the culprit isolated, in a cell of its own.
	if _, err := c.h.Reload(c.ctx); err != nil {
		t.Fatal(err)
	}
	c.ping("culprit")
	c.ping("healthy")
	c.h.mu.Lock()
	culpritKey, healthyKey := c.h.exts["culprit"].packedCellKey, c.h.exts["healthy"].packedCellKey
	c.h.mu.Unlock()
	if culpritKey == healthyKey {
		t.Fatalf("the reload put the culprit back beside the members it took down: %q", culpritKey)
	}
}

// A shared process that ends with os.Exit writes no trace, and the log holds the block of a panic the SDK recovered earlier: no member is blamed, and the
// whole cell stops until a /reload, as D274 says.
func TestPackedProcessExitStopsTheWholeCell(t *testing.T) {
	c := loadCrashCell(t)
	if err := c.run("healthy", "handled"); err == nil {
		t.Fatal("the healthy member's handler panic was not reported as an error")
	}
	_ = c.run("culprit", "exit")
	c.waitFor("both members' stop", func() bool {
		quarantined := c.h.QuarantinedCells()[c.packedKey] != ""
		c.h.mu.Lock()
		defer c.h.mu.Unlock()
		return quarantined && c.h.exts["culprit"] == nil && c.h.exts["healthy"] == nil
	})
	reports := c.reportsCopy()
	for _, name := range []string{"culprit", "healthy"} {
		if !slices.ContainsFunc(reports, func(report string) bool {
			return strings.HasPrefix(report, name+" disabled=true") && strings.Contains(report, "/reload restarts them")
		}) {
			t.Fatalf("no report says %s stopped with the cell: %q", name, reports)
		}
	}
	c.h.mu.Lock()
	faults := len(c.h.nativeFaults)
	c.h.mu.Unlock()
	if faults != 0 {
		t.Fatalf("an exit without a trace blamed %d members", faults)
	}
}
