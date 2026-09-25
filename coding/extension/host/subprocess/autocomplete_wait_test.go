package subprocess

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const slowAutocompleteExtension = `export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.addAutocompleteProvider((current) => ({
      async getSuggestions(lines, cursorLine, cursorCol, options) {
        const line = lines[cursorLine] ?? "";
        if (line === "boom") throw new Error("provider exploded");
        if (line !== "slow") return current.getSuggestions(lines, cursorLine, cursorCol, options);
        await new Promise((resolve) => setTimeout(resolve, 400));
        return { items: [{ value: "slow-item", label: "slow-item" }], prefix: "slow" };
      },
      applyCompletion: (...args) => current.applyCompletion(...args),
    }));
  });
}
`

// loadAutocompleteFixture loads a Node extension whose provider answers after
// 400 ms, or throws for the line "boom", and returns its registered source.
func loadAutocompleteFixture(t *testing.T, notify func(msg, level string)) extension.AsyncSuggestionSource {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the autocomplete fixture: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.mjs"), []byte(slowAutocompleteExtension), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeUI := &testUIContext{UIContext: extension.NoopUIContext, onNotify: notify}
	host := NewHost(t.TempDir())
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	host.SetUIBridge(bridge)
	t.Cleanup(func() { host.Shutdown("test done") })
	ext, err := host.Load(testbudget.Context(t), ExtConfig{Name: "slow-autocomplete", Source: filepath.Join(dir, "index.mjs"), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ext.Handlers["session_start"][0](map[string]any{"type": "session_start"}); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, testbudget.Wait(t), "autocomplete provider was not registered", func() bool {
		return fakeUI.autocompleteProvider() != nil
	})
	return fakeUI.autocompleteProvider()
}

// Upstream awaits an async autocomplete provider and only an editor change
// cancels it, so a provider that answers after 400 ms shows its items.
func TestSlowAutocompleteProviderShowsItems(t *testing.T) {
	source := loadAutocompleteFixture(t, nil)
	got := source.Suggest(testbudget.Context(t), []string{"slow"}, 0, len("slow"))
	if got == nil || len(got.Items) != 1 || got.Items[0].Value != "slow-item" {
		t.Fatalf("suggestions = %+v, want the slow provider's item", got)
	}
}

// A provider error reaches the user instead of silently showing nothing, and
// an editor change that cancels the query reports nothing.
func TestAutocompleteProviderErrorIsVisible(t *testing.T) {
	var mu sync.Mutex
	var notices []string
	source := loadAutocompleteFixture(t, func(msg, level string) {
		mu.Lock()
		defer mu.Unlock()
		notices = append(notices, level+":"+msg)
	})
	if got := source.Suggest(testbudget.Context(t), []string{"boom"}, 0, len("boom")); got != nil {
		t.Fatalf("failed provider returned %+v", got)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if got := source.Suggest(cancelled, []string{"slow"}, 0, len("slow")); got != nil {
		t.Fatalf("cancelled query returned %+v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 1 || !strings.HasPrefix(notices[0], "error:") || !strings.Contains(notices[0], "provider exploded") {
		t.Fatalf("notices = %v, want one error naming the provider failure", notices)
	}
}
