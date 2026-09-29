package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The mock applyCompletion in editor.test.ts:21-37 replaces exactly the prefix.
// Provider offsets are bytes; Editor.GetCursor remains in upstream UTF-16 units.
type completionUpstreamProvider struct {
	query    func(string, bool) *AutocompleteSuggestions
	triggers []string
	apply    func([]string, int, int, AutocompleteItem, string) ([]string, int, int)
}

func (p *completionUpstreamProvider) GetSuggestions(lines []string, row, col int) *AutocompleteSuggestions {
	return p.query(lines[row][:col], false)
}
func (p *completionUpstreamProvider) GetSuggestionsForce(lines []string, row, col int) *AutocompleteSuggestions {
	return p.query(lines[row][:col], true)
}
func (p *completionUpstreamProvider) TriggerCharacters() []string { return p.triggers }
func (p *completionUpstreamProvider) ApplyCompletion(lines []string, row, col int, item AutocompleteItem, prefix string) ([]string, int, int) {
	if p.apply != nil {
		return p.apply(lines, row, col, item, prefix)
	}
	out := slices.Clone(lines)
	out[row] = lines[row][:col-len(prefix)] + item.Value + lines[row][col:]
	return out, row, col - len(prefix) + len(item.Value)
}

func completionUpstreamItems(prefix string, values ...string) *AutocompleteSuggestions {
	items := make([]AutocompleteItem, len(values))
	for i, value := range values {
		items[i] = AutocompleteItem{Value: value, Label: value}
	}
	return &AutocompleteSuggestions{Prefix: prefix, Items: items}
}

// Every flush drains the real Editor's owner queue, not a fabricated synchronous
// provider result. Time advances only in the synctest bubble.
func completionUpstreamEditor(t *testing.T) (*Editor, func()) {
	t.Helper()
	e := NewEditor()
	e.SetMaxVisibleLines(7) // editor.test.ts:16-17: 24-row terminal.
	tasks := make(chan func(), 256)
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	e.SetAsyncApply(func(f func()) { tasks <- f })
	e.SetAutocompleteTaskOwner(ctx, workers.Go, func(err error) { t.Errorf("autocomplete error: %v", err) })
	flush := func() {
		for {
			synctest.Wait()
			select {
			case f := <-tasks:
				f()
			default:
				return
			}
		}
	}
	t.Cleanup(func() {
		e.AutocompleteCancel()
		cancel()
		flush()
		workers.Wait()
	})
	return e, flush
}
func completionUpstreamTick(flush func(), ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
	flush()
}
func completionUpstreamType(e *Editor, text string) {
	for _, ch := range text {
		e.HandleInput(string(ch))
	}
}
func completionUpstreamText(t *testing.T, e *Editor, want string) {
	t.Helper()
	if got := e.Text(); got != want {
		t.Fatalf("text=%q, want %q", got, want)
	}
}
func completionUpstreamOpen(t *testing.T, e *Editor, want bool) {
	t.Helper()
	if got := e.AutocompleteOpen(); got != want {
		t.Fatalf("autocomplete open=%v, want %v; text=%q", got, want, e.Text())
	}
}
func completionUpstreamRequests(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("requests=%q, want %q", got, want)
	}
}
func completionUpstreamCursorAtEnd(t *testing.T, e *Editor) {
	t.Helper()
	if got := e.GetCursor(); got.Line != 0 || got.Col != jsstring.Length(e.Text()) {
		t.Fatalf("cursor=%+v, want UTF-16 end of %q", got, e.Text())
	}
}

func TestUpstreamEditorCompletionUndoAndForce(t *testing.T) {
	for _, tc := range []struct {
		line               int
		name, typed, value string
		forceOnly          bool
	}{
		// packages/tui/test/editor.test.ts:2096
		{2096, "undoes autocomplete", "di", "dist/", false},
		// packages/tui/test/editor.test.ts:2338
		{2338, "auto-applies single force-file suggestion without showing menu", "Work", "Workspace/", true},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.line, tc.name), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, flush := completionUpstreamEditor(t)
				e.SetAutocomplete(&completionUpstreamProvider{query: func(prefix string, force bool) *AutocompleteSuggestions {
					if prefix == tc.typed && (!tc.forceOnly || force) {
						return completionUpstreamItems(prefix, tc.value)
					}
					return nil
				}})
				completionUpstreamType(e, tc.typed)
				completionUpstreamText(t, e, tc.typed)
				e.HandleInput("\t")
				flush()
				completionUpstreamText(t, e, tc.value)
				completionUpstreamOpen(t, e, false)
				e.HandleInput(kittyUndo)
				completionUpstreamText(t, e, tc.typed)
			})
		})
	}
}

// packages/tui/test/editor.test.ts:2135: every punctuation × trigger row, including the 19+1 ms boundary twice.
func TestUpstreamEditorCompletionCJKSymbolDebounce(t *testing.T) {
	beforeValues := []string{"查看，", "\u3000"}
	for _, ch := range "，．：；！？（）［］｛｝“”‘’…—。、「」『』《》【】" {
		beforeValues = append(beforeValues, string(ch))
	}
	for _, before := range beforeValues {
		for _, trigger := range []string{"@", "#", "$", "-"} {
			t.Run(before+trigger, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					e, flush := completionUpstreamEditor(t)
					var requests []string
					e.SetAutocomplete(&completionUpstreamProvider{triggers: []string{"$", "-"}, query: func(prefix string, _ bool) *AutocompleteSuggestions { requests = append(requests, prefix); return nil }})
					e.SetText(before)
					e.HandleInput(trigger)
					completionUpstreamTick(flush, 19)
					completionUpstreamRequests(t, requests, nil)
					completionUpstreamTick(flush, 1)
					completionUpstreamRequests(t, requests, []string{before + trigger})
					e.HandleInput("r")
					e.HandleInput("e")
					completionUpstreamTick(flush, 19)
					if len(requests) != 1 {
						t.Fatalf("requests=%q, want one call", requests)
					}
					completionUpstreamTick(flush, 1)
					completionUpstreamRequests(t, requests, []string{before + trigger, before + trigger + "re"})
				})
			})
		}
	}
}

// packages/tui/test/editor.test.ts:2212
func TestUpstreamEditorCompletionCJKPathOnlyOnTab(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		type request struct {
			text  string
			force bool
		}
		var requests []request
		e.SetAutocomplete(&completionUpstreamProvider{query: func(prefix string, force bool) *AutocompleteSuggestions {
			requests = append(requests, request{prefix, force})
			return nil
		}})
		text := "查看，/path/"
		completionUpstreamType(e, text)
		completionUpstreamTick(flush, 20)
		if len(requests) != 0 {
			t.Fatalf("unsolicited requests=%v", requests)
		}
		e.HandleInput("\t")
		flush()
		if !slices.Equal(requests, []request{{text, true}}) {
			t.Fatalf("requests=%v", requests)
		}
	})
}

// packages/tui/test/editor.test.ts:2233
func TestUpstreamEditorCompletionChinesePathPrefixes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := t.TempDir()
		if err := os.Mkdir(filepath.Join(base, "文档"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "文档", "说明.md"), []byte("text"), 0o600); err != nil {
			t.Fatal(err)
		}
		e, flush := completionUpstreamEditor(t)
		e.SetAutocomplete(NewCombinedProvider(nil, base, ""))
		for _, separator := range []string{" ", "\t", "\u3000", "\u00a0", "，", "。"} {
			e.SetText("查看" + separator)
			before := e.Text()
			e.HandleInput("文")
			e.HandleInput("\t")
			flush()
			completionUpstreamText(t, e, before+"文档/")
			e.HandleInput("说")
			e.HandleInput("\t")
			flush()
			completionUpstreamText(t, e, before+"文档/说明.md")
			completionUpstreamCursorAtEnd(t, e)
		}
	})
}

// packages/tui/test/editor.test.ts:2255
func TestUpstreamEditorCompletionEndsUnquotedContext(t *testing.T) {
	for _, separator := range []string{" ", "\u3000", "，", "。"} {
		for _, trigger := range []string{"@", "#", "$"} {
			t.Run(separator+trigger, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					e, flush := completionUpstreamEditor(t)
					var requests []string
					prefix := trigger + "src"
					e.SetAutocomplete(&completionUpstreamProvider{triggers: []string{"$"}, query: func(text string, _ bool) *AutocompleteSuggestions {
						requests = append(requests, text)
						if text == prefix {
							return &AutocompleteSuggestions{Prefix: prefix, Items: []AutocompleteItem{{Value: prefix + "/", Label: "src/"}}}
						}
						return nil
					}})
					e.SetText(trigger + "sr")
					e.HandleInput("c")
					completionUpstreamTick(flush, 20)
					completionUpstreamOpen(t, e, true)
					e.HandleInput(separator)
					flush()
					completionUpstreamRequests(t, requests, []string{prefix, prefix + separator})
					completionUpstreamOpen(t, e, false)
					e.HandleInput("文")
					completionUpstreamTick(flush, 20)
					completionUpstreamRequests(t, requests, []string{prefix, prefix + separator})
				})
			})
		}
	}
}

// packages/tui/test/editor.test.ts:2288
func TestUpstreamEditorCompletionRetriggersCJKDirectoriesAndDeletion(t *testing.T) {
	for _, directory := range []string{"文档", "我的 文档", "资料，归档"} {
		t.Run(directory, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				initial, directoryValue, fileValue, filePrefix := "@文", "@"+directory+"/", "@"+directory+"/说明.md", "@"+directory+"/说"
				if directory != "文档" {
					initial = `@"` + jsstring.Slice(directory, 0, 2)
					directoryValue = `@"` + directory + `/"`
					fileValue = `@"` + directory + `/说明.md"`
					filePrefix = `@"` + directory + `/说`
				}
				e, flush := completionUpstreamEditor(t)
				e.SetAutocomplete(&completionUpstreamProvider{apply: (&CombinedProvider{}).ApplyCompletion, query: func(before string, _ bool) *AutocompleteSuggestions {
					start := strings.Index(before, "@")
					if start < 0 {
						// Upstream slice(-1) cannot match either @-prefixed branch when @ is absent.
						return nil
					}
					prefix := before[start:]
					if prefix == initial {
						return &AutocompleteSuggestions{Prefix: prefix, Items: []AutocompleteItem{{Value: directoryValue, Label: directory + "/"}}}
					}
					if prefix == filePrefix {
						return &AutocompleteSuggestions{Prefix: prefix, Items: []AutocompleteItem{{Value: fileValue, Label: "说明.md"}}}
					}
					return nil
				}})
				e.SetText("查看：" + initial)
				e.HandleInput("\t")
				flush()
				completionUpstreamText(t, e, "查看："+directoryValue)
				completionUpstreamOpen(t, e, false)
				e.HandleInput("说")
				completionUpstreamTick(flush, 20)
				completionUpstreamOpen(t, e, true)
				for _, deletion := range []string{"\x7f", "\x1b[3~"} {
					e.HandleInput("错")
					completionUpstreamTick(flush, 20)
					completionUpstreamOpen(t, e, false)
					if deletion == "\x1b[3~" {
						e.HandleInput("\x1b[D")
					}
					e.HandleInput(deletion)
					completionUpstreamTick(flush, 20)
					completionUpstreamOpen(t, e, true)
				}
				e.HandleInput("\t")
				completionUpstreamText(t, e, "查看："+fileValue+" ")
				completionUpstreamCursorAtEnd(t, e)
			})
		})
	}
}

// packages/tui/test/editor.test.ts:2379
func TestUpstreamEditorCompletionForceMultiple(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		e.SetAutocomplete(&completionUpstreamProvider{query: func(prefix string, force bool) *AutocompleteSuggestions {
			if force && prefix == "src" {
				return completionUpstreamItems("src", "src/", "src.txt")
			}
			return nil
		}})
		completionUpstreamType(e, "src")
		completionUpstreamText(t, e, "src")
		e.HandleInput("\t")
		flush()
		completionUpstreamText(t, e, "src")
		completionUpstreamOpen(t, e, true)
		e.HandleInput("\t")
		completionUpstreamText(t, e, "src/")
		completionUpstreamOpen(t, e, false)
	})
}

// packages/tui/test/editor.test.ts:2423
func TestUpstreamEditorCompletionKeepsForceMode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		e.SetAutocomplete(&completionUpstreamProvider{query: func(prefix string, force bool) *AutocompleteSuggestions {
			if !force && !strings.Contains(prefix, "/") && !strings.HasPrefix(prefix, ".") {
				return nil
			}
			var values []string
			for _, v := range []string{"readme.md", "package.json", "src/", "dist/"} {
				if strings.HasPrefix(strings.ToLower(v), strings.ToLower(prefix)) {
					values = append(values, v)
				}
			}
			if len(values) > 0 {
				return completionUpstreamItems(prefix, values...)
			}
			return nil
		}})
		e.HandleInput("\t")
		flush()
		completionUpstreamOpen(t, e, true)
		for _, text := range []string{"r", "re"} {
			e.HandleInput(text[len(text)-1:])
			flush()
			completionUpstreamText(t, e, text)
			completionUpstreamOpen(t, e, true)
		}
		e.HandleInput("\t")
		completionUpstreamText(t, e, "readme.md")
		completionUpstreamOpen(t, e, false)
	})
}

func TestUpstreamEditorCompletionDebouncesWhileTyping(t *testing.T) {
	for _, tc := range []struct {
		line                      int
		name, input, value, label string
		triggers                  []string
		checkClosed               bool
	}{
		// packages/tui/test/editor.test.ts:2475
		{2475, "debounces @ autocomplete while typing", "@mai", "@main.ts", "main.ts", nil, true},
		// packages/tui/test/editor.test.ts:2567
		{2567, "debounces # autocomplete while typing", "#298", "#2983", "#2983", nil, true},
		// packages/tui/test/editor.test.ts:2600
		{2600, "debounces custom triggerCharacters autocomplete while typing", "$sk", "$skill-name", "skill-name", []string{"$"}, false},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.line, tc.name), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, flush := completionUpstreamEditor(t)
				calls := 0
				e.SetAutocomplete(&completionUpstreamProvider{triggers: tc.triggers, query: func(prefix string, _ bool) *AutocompleteSuggestions {
					calls++
					return &AutocompleteSuggestions{Prefix: prefix, Items: []AutocompleteItem{{Value: tc.value, Label: tc.label}}}
				}})
				completionUpstreamType(e, tc.input)
				if calls != 0 {
					t.Fatalf("calls=%d before debounce", calls)
				}
				if tc.checkClosed {
					completionUpstreamOpen(t, e, false)
				}
				completionUpstreamTick(flush, 50)
				if calls != 1 {
					t.Fatalf("calls=%d, want 1", calls)
				}
				completionUpstreamOpen(t, e, true)
			})
		})
	}
}

// packages/tui/test/editor.test.ts:2508
func TestUpstreamEditorCompletionRequeriesOnCursorMove(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		e.SetAutocomplete(&completionUpstreamProvider{query: func(before string, _ bool) *AutocompleteSuggestions {
			if !strings.HasPrefix(before, "/") {
				return nil
			}
			if _, after, ok := strings.Cut(before, " "); ok {
				return completionUpstreamItems(after, "repo", "message", "help")
			}
			return completionUpstreamItems(before, "cmd")
		}})
		for _, ch := range "/cmd " {
			e.HandleInput(string(ch))
			flush()
		}
		completionUpstreamText(t, e, "/cmd ")
		completionUpstreamOpen(t, e, true)
		if !strings.Contains(stripANSI(strings.Join(e.Render(80), "\n")), "repo") {
			t.Fatal("argument menu should be visible at /cmd ")
		}
		e.HandleInput("\x1b[D")
		flush()
		after := stripANSI(strings.Join(e.Render(80), "\n"))
		for _, stale := range []string{"repo", "message"} {
			if strings.Contains(after, stale) {
				t.Fatalf("stale %s menu survives cursor move: %q", stale, after)
			}
		}
	})
}

// completionAbortProvider uses the editor's owned deferred-query capability for
// the pending @ request. It does not install the old additive provider fanout.
type completionAbortProvider struct{ aborts int }

func (*completionAbortProvider) GetSuggestions([]string, int, int) *AutocompleteSuggestions {
	return nil
}
func (*completionAbortProvider) ApplyCompletion(lines []string, row, col int, item AutocompleteItem, prefix string) ([]string, int, int) {
	return (&completionUpstreamProvider{}).ApplyCompletion(lines, row, col, item, prefix)
}
func (p *completionAbortProvider) FileSearchTask([]string, int, int) (string, func(context.Context) []AutocompleteItem, bool) {
	return "@main", func(ctx context.Context) []AutocompleteItem {
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return []AutocompleteItem{{Value: "@main.ts", Label: "main.ts"}}
		case <-ctx.Done():
			p.aborts++
			return nil
		}
	}, true
}

// packages/tui/test/editor.test.ts:2652. The 250/50/500 ms times are the original
// inputs, advanced by synctest; the owner drains every cancelled request at cleanup.
func TestUpstreamEditorCompletionAbortsActiveRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		p := &completionAbortProvider{}
		e.SetAutocomplete(p)
		completionUpstreamType(e, "@mai")
		flush()
		completionUpstreamTick(flush, 250)
		e.HandleInput("n")
		flush()
		completionUpstreamTick(flush, 50)
		if p.aborts != 1 {
			t.Fatalf("aborts=%d, want 1", p.aborts)
		}
	})
}

// packages/tui/test/editor.test.ts:2689
func TestUpstreamEditorCompletionBackspaceSlashToEmpty(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		e.SetAutocomplete(&completionUpstreamProvider{query: func(prefix string, _ bool) *AutocompleteSuggestions {
			if !strings.HasPrefix(prefix, "/") {
				return nil
			}
			commands := []AutocompleteItem{{Value: "/model", Label: "model", Description: "Change model"}, {Value: "/help", Label: "help", Description: "Show help"}}
			var items []AutocompleteItem
			for _, c := range commands {
				if strings.HasPrefix(c.Value, prefix[1:]) {
					items = append(items, c)
				}
			}
			if len(items) > 0 {
				return &AutocompleteSuggestions{Prefix: prefix, Items: items}
			}
			return nil
		}})
		e.HandleInput("/")
		flush()
		completionUpstreamText(t, e, "/")
		completionUpstreamOpen(t, e, true)
		e.HandleInput("\x7f")
		flush()
		completionUpstreamText(t, e, "")
		completionUpstreamOpen(t, e, false)
	})
}

func TestUpstreamEditorCompletionArgumentSelection(t *testing.T) {
	for _, tc := range []struct {
		line                       int
		name, command, typed, want string
		values                     []string
		filter, checkText          bool
	}{
		// packages/tui/test/editor.test.ts:2729
		{2729, "applies exact typed slash-argument value on Enter even when first item is highlighted", "argtest", "two", "two", []string{"one", "two", "three"}, true, true},
		// packages/tui/test/editor.test.ts:2785
		{2785, "selects first prefix match on Enter when typed arg is not exact match", "argtest", "t", "two", []string{"two", "three", "twelve"}, true, false},
		// packages/tui/test/editor.test.ts:2836
		{2836, "highlights unique prefix match as user types (before full exact match)", "argtest", "tw", "two", []string{"one", "two", "three"}, false, true},
		// packages/tui/test/editor.test.ts:2885
		{2885, "selects first prefix match when multiple items match", "argtest", "t", "two", []string{"one", "two", "three"}, false, false},
		// packages/tui/test/editor.test.ts:2931
		{2931, "works for built-in-style command argument completion path (model-like)", "model", "gpt-4o-mini", "gpt-4o-mini", []string{"gpt-4o", "gpt-4o-mini", "claude-sonnet"}, true, true},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.line, tc.name), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, flush := completionUpstreamEditor(t)
				e.SetAutocomplete(&completionUpstreamProvider{query: func(before string, _ bool) *AutocompleteSuggestions {
					prefix, ok := strings.CutPrefix(before, "/"+tc.command+" ")
					if !ok || prefix == "" || strings.ContainsAny(prefix, " \t\r\n") {
						return nil
					}
					var values []string
					for _, value := range tc.values {
						if !tc.filter || strings.HasPrefix(value, prefix) {
							values = append(values, value)
						}
					}
					if len(values) > 0 {
						return completionUpstreamItems(prefix, values...)
					}
					return nil
				}})
				completionUpstreamType(e, "/"+tc.command+" "+tc.typed)
				if tc.checkText {
					completionUpstreamText(t, e, "/"+tc.command+" "+tc.typed)
				}
				flush()
				completionUpstreamOpen(t, e, true)
				e.HandleInput("\r")
				completionUpstreamText(t, e, "/"+tc.command+" "+tc.want)
			})
		})
	}
}

// packages/tui/test/editor.test.ts:3042
func TestUpstreamEditorCompletionCommandWithoutArgumentCompleter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e, flush := completionUpstreamEditor(t)
		e.SetAutocomplete(NewCombinedProvider([]SlashCommand{{Name: "help", Description: "Show help"}, {Name: "model", Description: "Switch model", GetArgumentCompletions: func(string) []AutocompleteItem {
			return []AutocompleteItem{{Value: "claude-opus", Label: "claude-opus"}}
		}}}, t.TempDir(), ""))
		completionUpstreamType(e, "/he")
		flush()
		completionUpstreamOpen(t, e, true)
		e.HandleInput("\t")
		completionUpstreamText(t, e, "/help ")
		completionUpstreamOpen(t, e, false)
	})
}
