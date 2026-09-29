package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

type themeWatcherRenderProbe struct {
	tui.Renderer
	invalidations int
	requests      int
	fullRepaints  int
}

func (r *themeWatcherRenderProbe) Invalidate()      { r.invalidations++ }
func (r *themeWatcherRenderProbe) RequestRender()   { r.requests++ }
func (r *themeWatcherRenderProbe) ForceFullRender() { r.fullRepaints++ }

func TestInteractiveThemeWatcherAppliesOnOwnerLoop(t *testing.T) {
	registry, active := tui.ActiveThemeRegistry(), tui.ActiveTheme()
	t.Cleanup(func() { tui.SetThemeRegistry(registry); tui.SetThemeByName(active.Name) })
	agent := t.TempDir()
	dir := filepath.Join(agent, "themes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "tui", "theme_dark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]any
	if err := json.Unmarshal(data, &theme); err != nil {
		t.Fatal(err)
	}
	theme["name"] = "custom-test"
	write := func(accent string) {
		t.Helper()
		theme["colors"].(map[string]any)["accent"] = accent
		data, err := json.Marshal(theme)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "custom-test.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("#123456")
	loaded := tui.NewThemeRegistry()
	if err := loaded.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	tui.SetThemeRegistry(loaded)
	tui.SetThemeByName("custom-test")
	renderer := &themeWatcherRenderProbe{Renderer: tui.New()}
	mode := &InteractiveMode{opts: InteractiveOptions{AgentDir: agent}, uiTaskCh: make(chan func(), 1), tuiInst: renderer}
	watcher := mode.startThemeWatcher(t.Context())
	defer watcher.Close()
	write("#654321")
	var action func()
	select {
	case action = <-mode.uiTaskCh:
	case <-time.After(5 * time.Second):
		t.Fatal("theme reload did not reach owner loop")
	}
	if got := tui.ActiveTheme().Colors()["accent"]; got != "#123456" {
		t.Fatalf("worker mutated UI before dispatch: %q", got)
	}
	action()
	if got := tui.ActiveTheme().Colors()["accent"]; got != "#654321" {
		t.Fatalf("owner did not apply theme: %q", got)
	}
	// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:1039-1043
	if renderer.invalidations != 1 || renderer.requests != 1 || renderer.fullRepaints != 0 {
		t.Fatalf("theme invalidation=%d request=%d destructive repaint=%d; want 1,1,0", renderer.invalidations, renderer.requests, renderer.fullRepaints)
	}
	fmt.Println("THEME_WATCHER_OWNER deferred then applied")
}
