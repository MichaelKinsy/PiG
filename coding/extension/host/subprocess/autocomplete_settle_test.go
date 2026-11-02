//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

// Every line is a different way for a Node provider chain to end. Pi's editor awaits the chain in the Promise jobs after the terminal
// read (editor.ts startAutocompleteRequest), so what ends inside that microtask checkpoint has opened the menu before the next read and
// what needs a later task (a timer, even a zero one, a socket, a process) has not.
const settleAutocompleteExtension = `export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.addAutocompleteProvider((current) => ({
      async getSuggestions(lines, cursorLine, cursorCol, options) {
        const line = lines[cursorLine] ?? "";
        await current.getSuggestions(lines, cursorLine, cursorCol, options);
        if (line === "microtasks") {
          for (let i = 0; i < 20; i++) await Promise.resolve();
          await new Promise((resolve) => queueMicrotask(resolve));
          await new Promise((resolve) => process.nextTick(resolve));
        }
        if (line === "timeout0") await new Promise((resolve) => setTimeout(resolve, 0));
        if (line === "immediate") await new Promise((resolve) => setImmediate(resolve));
        if (line === "slow") await new Promise((resolve) => setTimeout(resolve, 400));
        if (line === "nested") {
          await current.getSuggestions(lines, cursorLine, cursorCol, options);
          await current.getSuggestions(lines, cursorLine, cursorCol, options);
        }
        return { items: [{ value: line, label: "settled:" + line }], prefix: line };
      },
      applyCompletion: (...args) => current.applyCompletion(...args),
    }));
  });
}
`

// ioAutocompleteUI installs the provider chain over a base that takes 150 ms to answer "hostwait": a wait on the host that no chain announces as I/O.
type ioAutocompleteUI struct {
	*testUIContext
	factories []extension.AutocompleteProviderFactory
}

func (u *ioAutocompleteUI) AddAutocompleteProvider(factory extension.AutocompleteProviderFactory) error {
	base := testAutocompleteBase()
	base.GetSuggestions = func(ctx context.Context, lines []string, _, _ int, _ bool) (*extension.AutocompleteSuggestions, error) {
		switch lines[0] {
		case "hostwait":
			time.Sleep(150 * time.Millisecond)
		case "baseio":
			// The base provider's awaited work (an fd search) is real I/O that only the host can see.
			tui.AutocompleteAwaitsIO(ctx)
			time.Sleep(150 * time.Millisecond)
		}
		return nil, nil
	}
	u.autocompleteMu.Lock()
	defer u.autocompleteMu.Unlock()
	u.factories = append(u.factories, factory)
	provider := base
	for _, factory := range u.factories {
		var err error
		if provider, err = factory(context.Background(), provider); err != nil {
			return err
		}
	}
	u.autocompleteSource = provider
	return nil
}

func TestNodeAutocompleteChainTellsTheHostWhenItAwaitsIO(t *testing.T) {
	shortSockDir(t)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "index.mjs")
			if err := os.WriteFile(source, []byte(settleAutocompleteExtension), 0o600); err != nil {
				t.Fatal(err)
			}
			ui := &ioAutocompleteUI{testUIContext: &testUIContext{UIContext: extension.NoopUIContext}}
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(ui)
			host := NewHost(t.TempDir())
			host.SetUIBridge(bridge)
			t.Cleanup(func() { host.Shutdown("test done") })
			ext, err := host.Load(testbudget.Context(t), ExtConfig{Name: "settle-autocomplete", Source: source, Enabled: true, Isolation: isolation})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ext.Handlers["session_start"][0](map[string]any{"type": "session_start"}); err != nil {
				t.Fatal(err)
			}
			pollUntil(t, testbudget.Wait(t), "autocomplete provider was not registered", func() bool { return ui.autocompleteProvider() != nil })
			provider := ui.autocompleteProvider()
			for _, tc := range []struct {
				line   string
				awaits int32
			}{
				{"fast", 0}, {"microtasks", 0}, {"hostwait", 0}, {"nested", 0},
				{"timeout0", 1}, {"immediate", 1}, {"slow", 1}, {"fast", 0},
			} {
				var awaited atomic.Int32
				ctx := tui.WithAutocompleteUnhold(testbudget.Context(t), func() { awaited.Add(1) })
				got, err := provider.GetSuggestions(ctx, []string{tc.line}, 0, len(tc.line), false)
				if err != nil {
					t.Fatalf("%s: %v", tc.line, err)
				}
				if got == nil || len(got.Items) != 1 || got.Items[0].Label != "settled:"+tc.line {
					t.Fatalf("%s: answer %+v", tc.line, got)
				}
				if n := awaited.Load(); n != tc.awaits {
					t.Fatalf("%s: the chain announced real I/O %d times before its answer, want %d", tc.line, n, tc.awaits)
				}
			}
		})
	}
}

// Pi's providers share one process: the base provider's I/O is the I/O of a chain that another extension's provider wraps. Two members of one
// Node cell call each other's providers directly, so the host cannot tell from the call's parent which request it serves; the call names it.
func TestPackedNodeMembersShareTheBaseProvidersIOAnnouncement(t *testing.T) {
	shortSockDir(t)
	dir := t.TempDir()
	wrapper := `export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.addAutocompleteProvider((current) => ({
      async getSuggestions(lines, cursorLine, cursorCol, options) {
        const result = await current.getSuggestions(lines, cursorLine, cursorCol, options);
        return result ?? { items: [{ value: "x", label: "x" }], prefix: lines[cursorLine] };
      },
      applyCompletion: (...args) => current.applyCompletion(...args),
    }));
  });
}
`
	var sources []string
	for _, name := range []string{"first", "second"} {
		path := filepath.Join(dir, name+".mjs")
		if err := os.WriteFile(path, []byte(wrapper), 0o600); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, path)
	}
	ui := &ioAutocompleteUI{testUIContext: &testUIContext{UIContext: extension.NoopUIContext}}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	host := NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, errs := host.LoadAll(testbudget.Context(t), []ExtConfig{{Name: "first", Source: sources[0], Enabled: true}, {Name: "second", Source: sources[1], Enabled: true}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	for _, ext := range loaded {
		if _, err := ext.Handlers["session_start"][0](map[string]any{"type": "session_start"}); err != nil {
			t.Fatal(err)
		}
	}
	pollUntil(t, testbudget.Wait(t), "both providers were not registered", func() bool {
		ui.autocompleteMu.Lock()
		defer ui.autocompleteMu.Unlock()
		return len(ui.factories) == 2 && ui.autocompleteSource != nil
	})
	provider := ui.autocompleteProvider()
	for _, tc := range []struct {
		line   string
		awaits int32
	}{{"fast", 0}, {"baseio", 1}, {"hostwait", 0}} {
		var awaited atomic.Int32
		ctx := tui.WithAutocompleteUnhold(testbudget.Context(t), func() { awaited.Add(1) })
		if _, err := provider.GetSuggestions(ctx, []string{tc.line}, 0, len(tc.line), false); err != nil {
			t.Fatalf("%s: %v", tc.line, err)
		}
		if n := awaited.Load(); n != tc.awaits {
			t.Fatalf("%s: announced %d times through two packed members, want %d", tc.line, n, tc.awaits)
		}
	}
}
