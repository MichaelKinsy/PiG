package tui_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// The owner services posted applies while workers run: an awaited query may wait for its owner apply before completing. No sleep or guessed event-loop delay substitutes for completion.
func awaitedCompletionEditor(t testing.TB, ctx context.Context) (*tui.Editor, func(), func()) {
	t.Helper()
	e := tui.NewEditor()
	e.SetMaxVisibleLines(7)
	var workers sync.WaitGroup
	applies := make(chan func(), 16)
	finished := make(chan struct{}, 16)
	errors := make(chan error, 16)
	started, consumed := 0, 0
	e.SetAsyncApply(func(apply func()) { applies <- apply })
	e.SetAutocompleteTaskOwner(ctx, func(task func()) {
		started++
		workers.Go(func() {
			task()
			finished <- struct{}{}
		})
	}, func(err error) { errors <- err })
	drain := func(abort <-chan struct{}) {
		t.Helper()
		for consumed < started || len(applies) > 0 || len(errors) > 0 {
			select {
			case apply := <-applies:
				apply()
			case err := <-errors:
				t.Errorf("autocomplete error: %v", err)
			case <-finished:
				consumed++
			case <-abort:
				t.Fatalf("autocomplete did not complete: %v", ctx.Err())
			}
		}
	}
	flush := func() {
		t.Helper()
		if started == consumed {
			t.Fatal("input did not start the awaited autocomplete operation")
		}
		drain(ctx.Done())
	}
	join := func() {
		drain(nil)
		workers.Wait()
	}
	return e, flush, join
}

// packages/tui/test/editor.test.ts:2994. The callback returns an awaited result, not a preloaded menu; CombinedAutocompleteProvider awaits it (autocomplete.ts:364).
func TestUpstreamEditorCompletionAwaitsSlashCommandArguments(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	e, flush, join := awaitedCompletionEditor(t, ctx)
	t.Cleanup(func() { e.AutocompleteCancel(); cancel(); join() })
	e.SetAutocomplete(tui.NewCombinedProvider([]tui.SlashCommand{{
		Name: "load-skills", Description: "Load skills",
		AwaitArgumentCompletions: func(prefix string) ([]tui.AutocompleteItem, error) {
			if strings.HasPrefix(prefix, "s") {
				return []tui.AutocompleteItem{{Value: "skill-a", Label: "skill-a"}}, nil
			}
			return nil, nil
		},
	}}, t.TempDir(), ""))
	e.SetText("/load-skills ")
	e.HandleInput("s")
	flush()
	if !e.AutocompleteOpen() {
		t.Fatal("awaited command argument menu did not open")
	}
	e.HandleInput("\t")
	if e.Text() != "/load-skills skill-a" {
		t.Fatalf("text=%q, want /load-skills skill-a", e.Text())
	}
	if e.AutocompleteOpen() {
		t.Fatal("accepted argument menu remains open")
	}
}

func BenchmarkEditorOwnedArgumentCompletion(b *testing.B) {
	ctx, cancel := context.WithCancel(b.Context())
	e, flush, join := awaitedCompletionEditor(b, ctx)
	b.Cleanup(func() { e.AutocompleteCancel(); cancel(); join() })
	e.SetAutocomplete(tui.NewCombinedProvider([]tui.SlashCommand{{
		Name: "load-skills", Description: "Load skills",
		AwaitArgumentCompletions: func(prefix string) ([]tui.AutocompleteItem, error) {
			if strings.HasPrefix(prefix, "s") {
				return []tui.AutocompleteItem{{Value: "skill-a", Label: "skill-a"}}, nil
			}
			return nil, nil
		},
	}}, b.TempDir(), ""))
	b.ReportAllocs()
	for b.Loop() {
		e.SetText("/load-skills ")
		e.HandleInput("s")
		flush()
		e.HandleInput("\t")
		e.Render(80)
	}
}

// packages/tui/test/editor.test.ts:3019 returns the literal "not-an-array". The Go callback cannot return that shape, so this case enters through the real Node registration boundary. runtime.mjs:2430-2435 validates the awaited result; host.makeCommandArgumentCompletions decodes the resulting null as nil.
func TestUpstreamEditorCompletionIgnoresInvalidArgumentResult(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("the original invalid-result case requires the Node runtime:", err)
	}
	dir := t.TempDir()
	fixture, err := os.ReadFile("../test/parity/testdata/editor-completion-helper/invalid-argument.mjs")
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "commands.mjs")
	if err := os.WriteFile(entry, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "received-prefix.txt")
	// Bound external process/IPC failure, as in the existing host command-completion test.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Setenv("PIG_HOME", t.TempDir())
	host := subprocess.NewHost(t.TempDir())
	e, flush, join := awaitedCompletionEditor(t, ctx)
	t.Cleanup(func() {
		e.AutocompleteCancel()
		cancel()
		host.Shutdown("test done")
		join()
	})
	loaded, errs := host.LoadAll(ctx, []subprocess.ExtConfig{{Name: "commands", Source: entry, Enabled: true}})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", errs)
	}
	complete := loaded[0].Commands["load-skills"].GetArgumentCompletions
	if complete == nil {
		t.Fatal("the Node command did not register its argument callback")
	}
	e.SetAutocomplete(tui.NewCombinedProvider([]tui.SlashCommand{{
		Name: "load-skills", Description: "Load skills",
		AwaitArgumentCompletions: func(prefix string) ([]tui.AutocompleteItem, error) {
			items, err := complete(prefix)
			if err != nil {
				return nil, err
			}
			var out []tui.AutocompleteItem
			for _, item := range items {
				out = append(out, tui.AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description})
			}
			return out, nil
		},
	}}, dir, ""))
	e.SetText("/load-skills ")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("callback ran before typing: marker stat=%v", err)
	}
	e.HandleInput("s")
	flush()
	prefix, err := os.ReadFile(marker)
	if err != nil || string(prefix) != "s" {
		t.Fatalf("Node invocation prefix=%q err=%v, want s", prefix, err)
	}
	if e.AutocompleteOpen() {
		t.Fatal("invalid argument result opened the menu")
	}
	if e.Text() != "/load-skills s" {
		t.Fatalf("text=%q, want /load-skills s", e.Text())
	}
}
