package tui_test

// test/p2/completion.test.ts, the TUI provider: `@` asks the index, Tab lists the directory, `/` goes to commands, outside the cwd hints. (The chat editor test of
// the file is in tui/clienttest; the prefix helpers' test is runtime/completion_test.go.)
//
// Go forced difference (see completion.go): the provider answers what needs no call to the index from GetSuggestions and gives the rest as a task the editor runs
// off its loop, so `suggest` below is what the editor does with the two.

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	fileindex "github.com/MichaelKinsy/PiG/apps/p2/plugins/file-index"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

type fileSource struct {
	mu    sync.Mutex
	calls []string
}

func (f *fileSource) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fileSource) Complete(_ context.Context, query string, limit int) ([]p2runtime.IndexedPath, bool, error) {
	f.record("complete:" + query)
	var found []p2runtime.IndexedPath
	for _, item := range fileindex.RankFiles(fileindex.WithDirectories([]string{"src/app.ts", "src/main.ts", "README.md"}), query, limit, fileindex.KindAny) {
		found = append(found, p2runtime.IndexedPath{Path: item.Path, Dir: item.Dir})
	}
	return found, true, nil
}

func (f *fileSource) List(_ context.Context, path string) ([]p2runtime.DirectoryEntry, bool, error) {
	f.record("list:" + path)
	return []p2runtime.DirectoryEntry{{Name: "app.ts"}}, false, nil
}

// slashCompletion is `getSuggestions: async () => ({ items: [{ value: "model", label: "model" }], prefix: "/mo" })` and an apply that appends "!".
type slashCompletion struct{}

func (slashCompletion) GetSuggestions(context.Context, []string, int, int, pitui.AutocompleteSuggestionOptions) *pitui.AutocompleteSuggestions {
	return &pitui.AutocompleteSuggestions{Items: []pitui.AutocompleteItem{{Value: "model", Label: "model"}}, Prefix: "/mo"}
}

func (slashCompletion) ApplyCompletion(lines []string, _, _ int, _ pitui.AutocompleteItem, _ string) ([]string, int, int) {
	return []string{lines[0] + "!"}, 0, 0
}

// suggest is the provider's suggestions as the editor takes them (editor_autocomplete_request.go): the task when the provider plans one, else the answer at once.
func suggest(t *testing.T, provider *tui.FileCompletionProvider, lines []string, col int, force bool) *pitui.AutocompleteSuggestions {
	t.Helper()
	if prefix, task, ok := provider.SuggestionTask(lines, 0, col, force); ok {
		found, err := task(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(found) == 0 {
			return nil
		}
		return &pitui.AutocompleteSuggestions{Items: found, Prefix: prefix}
	}
	return provider.GetSuggestions(context.Background(), lines, 0, col, pitui.AutocompleteSuggestionOptions{Force: force})
}

func TestTheTUIProviderAsksTheIndexTabListsTheDirectorySlashGoesToCommandsOutsideTheCwdHints(t *testing.T) {
	files := &fileSource{}
	var mu sync.Mutex
	var hints []string
	provider := tui.NewFileCompletionProvider(slashCompletion{}, func() tui.FileSource { return files }, func(text string) {
		mu.Lock()
		hints = append(hints, text)
		mu.Unlock()
	}, nil)
	at := suggest(t, provider, []string{"fix @app"}, 8, false)
	if at == nil || len(at.Items) == 0 || at.Items[0] != (pitui.AutocompleteItem{Value: "@src/app.ts", Label: "app.ts", Description: "src/app.ts"}) {
		t.Fatalf("@app = %+v", at)
	}
	if at.Prefix != "@app" {
		t.Fatalf("prefix %q", at.Prefix)
	}
	mu.Lock()
	partial := slices.ContainsFunc(hints, func(hint string) bool { return strings.Contains(hint, "partial") })
	mu.Unlock()
	if !partial {
		t.Fatalf("a truncated index is surfaced: %v", hints)
	}
	applied, row, col := provider.ApplyCompletion([]string{"fix @app"}, 0, 8, at.Items[0], "@app")
	if !slices.Equal(applied, []string{"fix @src/app.ts "}) || row != 0 || col != 16 {
		t.Fatalf("applied %q %d %d", applied, row, col)
	}
	tab := suggest(t, provider, []string{"open src/a"}, 10, true)
	if tab == nil || tab.Prefix != "src/a" || len(tab.Items) != 1 || tab.Items[0] != (pitui.AutocompleteItem{Value: "src/app.ts", Label: "app.ts"}) {
		t.Fatalf("tab = %+v", tab)
	}
	slash := suggest(t, provider, []string{"/mo"}, 3, false)
	if slash == nil || slash.Prefix != "/mo" || len(slash.Items) != 1 || slash.Items[0] != (pitui.AutocompleteItem{Value: "model", Label: "model"}) {
		t.Fatalf("slash = %+v", slash)
	}
	if lines, _, _ := provider.ApplyCompletion([]string{"/mo"}, 0, 3, pitui.AutocompleteItem{Value: "model", Label: "model"}, "/mo"); !slices.Equal(lines, []string{"/mo!"}) {
		t.Fatalf("slash applied %q", lines)
	}
	if got := suggest(t, provider, []string{"see @~/x"}, 8, false); got != nil {
		t.Fatalf("@~/x = %+v", got)
	}
	if got := suggest(t, provider, []string{"cat ../x"}, 8, true); got != nil {
		t.Fatalf("../x = %+v", got)
	}
	mu.Lock()
	outOfScope := 0
	for _, hint := range hints {
		if hint == tui.OutOfScopeHint {
			outOfScope++
		}
	}
	mu.Unlock()
	if outOfScope != 2 {
		t.Fatalf("out-of-scope hints = %d (%v)", outOfScope, hints)
	}
	if provider.ShouldTriggerFileCompletion([]string{"/mod"}, 0, 4) {
		t.Fatal("a command name takes no file completion")
	}
	if !provider.ShouldTriggerFileCompletion([]string{"/model src"}, 0, 10) {
		t.Fatal("a command's argument does")
	}
	if want := []string{"complete:app", "list:src"}; !slices.Equal(files.calls, want) {
		t.Fatalf("calls = %v, want %v", files.calls, want)
	}
}
