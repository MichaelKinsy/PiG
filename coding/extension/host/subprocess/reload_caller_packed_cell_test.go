package subprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's old extension instance outlives the reload its command awaits: `await ctx.reload()` resolves in the handler that called it, and the handler goes on to its next
// line (agent-session.ts:3828-3876: the old runner is invalidated, and the old instance is not stopped). A command that awaits a reload that removes a member of its
// own shared cell goes on as well: the cell's process stays until the command has returned, though the removed member stops at the reload's commit.
func TestPackedGoCommandGoesOnAfterAReloadThatRemovesAMemberOfItsCell(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "resumed")
	callerRoot := writeReloadCallerModule(t, "example.com/reloadcaller/a", "reloadcaller-a", marker)
	siblingRoot := writePackedFactoryModule(t, "example.com/reloadcaller/b", "reloadcaller-b", "tool_b")
	configs := []ExtConfig{
		packedFactoryConfig("reloadcaller-a", callerRoot, "example.com/reloadcaller/a", "ha"),
		packedFactoryConfig("reloadcaller-b", siblingRoot, "example.com/reloadcaller/b", "hb"),
	}
	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return slices.Clone(configs), nil })
	owner, cancelOwner := buildContext(t)
	defer cancelOwner()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(newTestUIContext())
	bridge.SetActions(&HostCallbacks{
		WaitForIdle: func(context.Context) error { return nil },
		Reload: func(context.Context) error {
			_, err := h.Reload(owner)
			return err
		},
	})
	h.SetUIBridge(bridge)
	t.Cleanup(func() { h.Shutdown("test done") })

	if _, err := h.Reload(owner); err != nil {
		t.Fatal(err)
	}
	if names := extensionNames(h); len(names) != 2 {
		t.Fatalf("loaded %v, want both members", names)
	}
	caller := extensionNamed(h, "reloadcaller-a")
	h.mu.Lock()
	oldCaller := h.exts["reloadcaller-a"]
	h.mu.Unlock()
	configs = configs[:1]
	done := make(chan error, 1)
	go func() { done <- caller.Commands["act"].Handler(context.Background(), "") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the command that awaited ctx.reload() failed: %v", err)
		}
	case <-time.After(testTimeout(t, 30*time.Second)):
		t.Fatal("the command that awaited ctx.reload() never returned")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the command did not go on after ctx.reload(): %v", err)
	}
	if names := extensionNames(h); !slices.Equal(names, []string{"reloadcaller-a"}) {
		t.Fatalf("loaded after the reload = %v, want the caller alone", names)
	}
	// The process the old generations shared ends once the command that kept it has returned.
	requireExited(t, oldCaller, "the old generation of the caller")
}

// writeReloadCallerModule writes a Go extension whose command "act" awaits ctx.Reload() and then writes marker.
func writeReloadCallerModule(t testing.TB, modulePath, extName, marker string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`package ext

import (
	"os"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New(%q)
	e.Command("act", "await a reload, then write a marker", func(ctx sdk.Context, _ string) error {
		if err := ctx.Reload(); err != nil {
			return err
		}
		return os.WriteFile(%q, []byte("resumed"), 0o600)
	})
	return e
}
`, extName, marker)
	if err := os.WriteFile(filepath.Join(dir, "ext.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func extensionNamed(h *Host, name string) *extension.Extension {
	for _, ext := range h.Extensions() {
		if ext.Name == name {
			return &ext
		}
	}
	return nil
}
