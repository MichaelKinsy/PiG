//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/mod/modfile"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// observedGeometry runs the named extension's height tool and returns the
// "height=H width=W" text it reports from its own SDK context.
func observedGeometry(ctx context.Context, h *Host, name string) (string, error) {
	h.mu.Lock()
	var ext *extension.Extension
	if me := h.exts[name]; me != nil {
		ext = me.ext
	}
	h.mu.Unlock()
	if ext == nil {
		return "", fmt.Errorf("extension %s is not loaded", name)
	}
	tool, ok := ext.Tools["height"]
	if !ok {
		return "", fmt.Errorf("extension %s has no height tool", name)
	}
	result, err := tool.Definition.Execute(ctx, "geometry-call", json.RawMessage(`{}`), nil)
	if err != nil {
		return "", err
	}
	text := fmt.Sprint(result)
	fields := strings.Fields(text[max(strings.Index(text, "height="), 0):])
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "height=") {
		return "", fmt.Errorf("%s height tool result %q has no geometry", name, text)
	}
	return fields[0] + " " + fields[1], nil
}

// waitGeometry polls until the extension observes want: notifications and
// crash restarts are asynchronous, so an early read may precede delivery.
func waitGeometry(t *testing.T, ctx context.Context, h *Host, name, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var got string
	var err error
	for time.Now().Before(deadline) {
		if got, err = observedGeometry(ctx, h, name); err == nil && got == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s observes %q (err %v), want %q", name, got, err, want)
}

// writeHeightReportingStandalone turns the height-reporting factory into an
// exact standalone executable, which loads through the isolated handshake.
func writeHeightReportingStandalone(t *testing.T, modulePath, extName string) string {
	t.Helper()
	dir := writeHeightReportingModule(t, modulePath, extName)
	src, err := os.ReadFile(filepath.Join(dir, "extension.go"))
	if err != nil {
		t.Fatal(err)
	}
	main := strings.Replace(string(src), "package ext", "package main", 1) + "\nfunc main() {\n\tif err := Extension().Run(); err != nil {\n\t\tpanic(err)\n\t}\n}\n"
	if err := os.Remove(filepath.Join(dir, "extension.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	sdkRoot := filepath.Join(findModuleRoot(t), "extensions", "sdk")
	goMod := fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", modulePath, modfile.AutoQuote(filepath.ToSlash(sdkRoot)))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeHeightReportingNode writes a Node factory whose "height" tool reports
// ctx.height and ctx.width.
func writeHeightReportingNode(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the packed Node geometry fixture: %v", err)
	}
	entry := filepath.Join(t.TempDir(), "index.mjs")
	src := "export default function (pi) {\n" +
		"  pi.registerTool({ name: \"height\", label: \"height\", description: \"report ctx.height\", parameters: { type: \"object\", properties: {} },\n" +
		"    execute: async (_id, _params, _signal, _onUpdate, ctx) => ({ content: [{ type: \"text\", text: `height=${ctx.height} width=${ctx.width}` }] }) });\n" +
		"}\n"
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

// geometryForms names the ready handshakes: a Go factory loads through the
// packed-cell handshake whether packed or isolated, a packed Node factory
// defers its ready payload until every factory in the reload has registered,
// and a standalone loads through the isolated-process handshake.
var geometryForms = []string{"packed", "isolated-factory", "packed-node", "standalone"}

func geometryHost(t *testing.T, form string, names ...string) *Host {
	t.Helper()
	cfgs := make([]ExtConfig, 0, len(names))
	for _, name := range names {
		module := "example.com/" + strings.ReplaceAll(name, "-", "")
		var cfg ExtConfig
		switch form {
		case "packed":
			cfg = packedFactoryConfig(name, writeHeightReportingModule(t, module, name), module, name+"-h1")
		case "isolated-factory":
			cfg = packedFactoryConfig(name, writeHeightReportingModule(t, module, name), module, name+"-h1")
			cfg.Isolation = "isolated"
		case "packed-node":
			cfg = ExtConfig{Name: name, Source: writeHeightReportingNode(t), Enabled: true, RuntimeKind: "subprocess", RuntimeLanguage: "node", EntrypointKind: "factory", SDKName: "pi-node"}
		case "standalone":
			cfg = ExtConfig{Name: name, Source: writeHeightReportingStandalone(t, module, name), Enabled: true, Isolation: "isolated", RuntimeKind: "subprocess", RuntimeLanguage: "go", EntrypointKind: "standalone"}
		default:
			t.Fatalf("unknown form %q", form)
		}
		cfg.SupervisorConfig = SupervisorConfig{MaxCrashes: 5, CrashWindow: time.Minute, InitialDelay: 10 * time.Millisecond, MaxDelay: 10 * time.Millisecond, BackoffFactor: 1}
		cfgs = append(cfgs, cfg)
	}
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return cfgs, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	return h
}

func reloadGeometryHost(t *testing.T, ctx context.Context, h *Host) {
	t.Helper()
	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if issues := h.LastReloadReport().Issues; len(issues) > 0 {
		t.Fatalf("reload issues: %v", issues)
	}
}

// A resize that lands while /reload is handshaking extensions must reach every
// reloaded extension. Interactive mode reads the renderer's height for the
// ready payload, and the renderer stores a new height before its asynchronous
// height_change callback runs. So one extension can be handshaken with the old
// height and a later one with the new height, and the resize broadcast runs
// before the reloaded extensions are visible to it. A host-wide "last
// broadcast" value overwritten by each handshake then suppresses or misses the
// broadcast and leaves the first extension at the old height until the next
// resize.
func TestReloadResizeDuringHandshakeReachesEveryExtension(t *testing.T) {
	for _, form := range geometryForms {
		t.Run(form, func(t *testing.T) {
			h := geometryHost(t, form, "geom-a", "geom-b")
			var calls atomic.Int64
			var height atomic.Int64
			height.Store(40)
			h.SetWidthFunc(func() int { return 100 })
			h.SetHeightFunc(func() int {
				current := int(height.Load())
				if calls.Add(1) == 1 {
					// The terminal grows right after the first handshake read
					// the height. The renderer dispatches its height callback
					// on its own goroutine, which may run before, during, or
					// after the remaining handshakes and the commit.
					height.Store(60)
					go h.NotifyHeight(60)
				}
				return current
			})

			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			reloadGeometryHost(t, ctx, h)
			for _, name := range []string{"geom-a", "geom-b"} {
				waitGeometry(t, ctx, h, name, "height=60 width=100")
			}
		})
	}
}

// One extension's handshake must not mark a resize as already delivered to the
// others. A crashed standalone extension restarts alone while the rest keep
// running; its handshake records the geometry that one connection saw, and a
// later broadcast of that value is still news to every extension that saw
// another.
func TestCrashRestartHandshakeDoesNotSuppressResizeForOthers(t *testing.T) {
	h := geometryHost(t, "standalone", "geom-a", "geom-b")
	var height atomic.Int64
	height.Store(40)
	h.SetWidthFunc(func() int { return 100 })
	h.SetHeightFunc(func() int { return int(height.Load()) })
	h.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
		t.Logf("crash notice %s delay=%v disabled=%v: %s", name, delay, disabled, reason)
	})
	// Wait for the restart's handshake mark rather than calling geom-b: a
	// request sent while it restarts reaches the new process before ready.
	var armed atomic.Bool
	restarted := make(chan struct{}, 1)
	h.SetStartupTrace(func(label string) {
		if armed.Load() && label == "extension.geom-b.handshake-done" {
			select {
			case restarted <- struct{}{}:
			default:
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	reloadGeometryHost(t, ctx, h)
	waitGeometry(t, ctx, h, "geom-a", "height=40 width=100")
	waitGeometry(t, ctx, h, "geom-b", "height=40 width=100")

	// The renderer already reports the new height, but its asynchronous
	// callback has not broadcast it yet when geom-b crashes and restarts.
	height.Store(60)
	armed.Store(true)
	h.mu.Lock()
	crashed := h.exts["geom-b"]
	h.mu.Unlock()
	crashed.procMu.Lock()
	proc := crashed.proc
	crashed.procMu.Unlock()
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-ctx.Done():
		t.Fatal("geom-b did not restart")
	}
	waitGeometry(t, ctx, h, "geom-b", "height=60 width=100")
	h.NotifyHeight(60)

	waitGeometry(t, ctx, h, "geom-a", "height=60 width=100")
}

// The ready handshake and the resize broadcast share the per-extension
// geometry record; run them concurrently under the race detector.
func TestReloadHandshakeRacesResizeBroadcast(t *testing.T) {
	h := geometryHost(t, "packed", "geom-a")
	var height atomic.Int64
	height.Store(40)
	h.SetWidthFunc(func() int { return 100 })
	h.SetHeightFunc(func() int { return int(height.Load()) })

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			h.NotifyHeight(30 + i%20)
			h.NotifyWidth(90 + i%20)
		}
	})
	for range 3 {
		if _, err := h.Reload(ctx); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("reload: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}
