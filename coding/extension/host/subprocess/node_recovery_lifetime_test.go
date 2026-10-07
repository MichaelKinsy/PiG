package subprocess

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestNodeShutdownCancelsInitialAdmission(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	marker, entry := filepath.Join(root, "started"), filepath.Join(root, "blocked.mjs")
	source := `import {writeFileSync} from "node:fs"; export default async function() { writeFileSync(` + strconv.Quote(marker) + `,"started"); await new Promise(()=>{}); }`
	if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	loadDone := make(chan struct{})
	go func() {
		h.LoadAll(t.Context(), []ExtConfig{{Name: "blocked", Source: entry, Enabled: true}})
		close(loadDone)
	}()
	pollUntil(t, 5*time.Second, "initial factory did not start", func() bool { _, err := os.Stat(marker); return err == nil })
	done := make(chan struct{})
	go func() { h.Shutdown("cancel admission"); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not cancel initial admission")
	}
	<-loadDone
}

func TestNodeRecoveryShutdownCancelsBlockedFactory(t *testing.T) {
	t.Parallel()
	h, configs, _ := nodeRecoveryFixture(t, 2)
	marker := filepath.Join(t.TempDir(), "restarted")
	source := `import {writeFileSync} from "node:fs";
export default async function() { writeFileSync(` + strconv.Quote(marker) + `,"started"); await new Promise(()=>{}); }
`
	if err := os.WriteFile(configs[0].Source, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	pid := pidOf(h.exts["member0"])
	h.mu.Unlock()
	if err := killTestProcess(pid); err != nil {
		t.Fatal(err)
	}
	// Recovery waits the supervisor backoff and restages the cell first, so the wait for the factory is a test-budget hang bound, as in waitRecovered.
	pollUntil(t, testbudget.Wait(t), "recovery factory did not start", func() bool { _, err := os.Stat(marker); return err == nil })
	done := make(chan struct{})
	go func() { h.Shutdown("cancel recovery"); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not cancel and drain blocked recovery")
	}
	if h.ExtensionCount() != 0 {
		t.Fatal("late recovery published after Shutdown")
	}
}

func TestNodeStaleRecoveryDoesNotReplaceReloadedGeneration(t *testing.T) {
	h, configs, _ := nodeRecoveryFixture(t, 2)
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	h.mu.Lock()
	old := h.exts["member0"].packedProcess
	h.mu.Unlock()
	if _, err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	current := h.exts["member0"].packedProcess
	h.mu.Unlock()
	h.recoverNodeProcess(old, "late old-generation failure")
	h.disablePackedMember(old.members[0], "late old connection")
	h.mu.Lock()
	after := h.exts["member0"].packedProcess
	h.mu.Unlock()
	if after != current {
		t.Fatal("stale crash replaced the current process")
	}
	if got := recoveryBus(t, h, "member0"); got != "member0,member1 | member0+member1" {
		t.Fatal(got)
	}
}

func TestNodeRecoveryKeepsOriginalOwnerCancellation(t *testing.T) {
	t.Parallel()
	// A connection's owner can end without Host.Shutdown, e.g. a mode replacement.
	nodeCellRequireNode(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	root := t.TempDir()
	entry := filepath.Join(root, "owner.mjs")
	nodeCellTrivialExtension(t, entry, "owner")
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	if _, errs := h.LoadAll(ctx, []ExtConfig{{Name: "owner", Source: entry, Enabled: true}}); len(errs) > 0 {
		t.Fatal(errs)
	}
	h.mu.Lock()
	pid := pidOf(h.exts["owner"])
	h.mu.Unlock()
	if err := killTestProcess(pid); err != nil {
		t.Fatal(err)
	}
	waitRecovered(t, h, []string{"owner"}, pid)
	h.mu.Lock()
	recovered := h.exts["owner"].packedProcess
	h.mu.Unlock()
	cancel()
	select {
	case <-recovered.startWait():
	case <-time.After(5 * time.Second):
		t.Fatal("recovered process outlived its original owner")
	}
}
