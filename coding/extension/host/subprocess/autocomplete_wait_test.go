package subprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
func loadAutocompleteFixture(t *testing.T, notify func(msg, level string)) *extension.AutocompleteProvider {
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
	got, err := source.GetSuggestions(testbudget.Context(t), []string{"slow"}, 0, len("slow"), false)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Items) != 1 || got.Items[0].Value != "slow-item" {
		t.Fatalf("suggestions = %+v, want the slow provider's item", got)
	}
}

// Provider rejection and cancellation return to the caller which owns popup/error presentation.
func TestAutocompleteProviderErrorIsReturned(t *testing.T) {
	source := loadAutocompleteFixture(t, nil)
	if got, err := source.GetSuggestions(testbudget.Context(t), []string{"boom"}, 0, len("boom"), false); got != nil || err == nil || !strings.Contains(err.Error(), "provider exploded") {
		t.Fatalf("failed provider returned %+v, %v", got, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if got, err := source.GetSuggestions(cancelled, []string{"slow"}, 0, len("slow"), false); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled query returned %+v, %v", got, err)
	}
}
