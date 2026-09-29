package extensionconformance

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type autocompleteUI struct {
	*recordingUI
	mu        sync.Mutex
	factories []extension.AutocompleteProviderFactory
	provider  *extension.AutocompleteProvider
}

func autocompleteBase() *extension.AutocompleteProvider {
	return &extension.AutocompleteProvider{
		GetSuggestions: func(_ context.Context, lines []string, _, _ int, force bool) (*extension.AutocompleteSuggestions, error) {
			if lines[0] == "empty" {
				return nil, nil
			}
			if lines[0] == "error" {
				return nil, errors.New("base failed")
			}
			items := []extension.AutocompleteItem{{Value: "keep", Label: "keep"}, {Value: "drop", Label: "drop"}}
			if force {
				items = []extension.AutocompleteItem{{Value: "forced", Label: "forced"}}
			}
			return &extension.AutocompleteSuggestions{Items: items, Prefix: "base-prefix"}, nil
		},
		ApplyCompletion: func(_ context.Context, _ []string, _, _ int, item extension.AutocompleteItem, _ string) (extension.AutocompleteCompletion, error) {
			return extension.AutocompleteCompletion{Lines: []string{item.Value}, CursorLine: 0, CursorCol: len(utf16.Encode([]rune(item.Value)))}, nil
		},
		ShouldTriggerFileCompletion: func(_ context.Context, lines []string, _, _ int) (bool, error) { return lines[0] != "blocked", nil },
	}
}

func (u *autocompleteUI) AddAutocompleteProvider(factory extension.AutocompleteProviderFactory) error {
	u.mu.Lock()
	u.factories = append(u.factories, factory)
	factories := slices.Clone(u.factories)
	u.mu.Unlock()
	provider, err := extension.SetupAutocompleteProvider(context.Background(), autocompleteBase(), factories)
	if err != nil {
		return err
	}
	u.mu.Lock()
	u.provider = provider
	u.mu.Unlock()
	return nil
}
func (u *autocompleteUI) AutocompleteProvider(context.Context) (*extension.AutocompleteProvider, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.provider, nil
}

func autocompleteReferenceCommand(ctx context.Context, _ string) error {
	ui, err := extension.FromContext(ctx).UI()
	if err != nil {
		return err
	}
	for _, tag := range []string{"A", "B"} {
		err := ui.AddAutocompleteProvider(func(_ context.Context, current *extension.AutocompleteProvider) (*extension.AutocompleteProvider, error) {
			ui.Notify("factory:"+tag, "info")
			var calls atomic.Int32
			triggers := []string{"$"}
			if tag == "B" {
				triggers = []string{"#", "$"}
			}
			return &extension.AutocompleteProvider{
				TriggerCharacters: triggers,
				GetSuggestions: func(ctx context.Context, lines []string, line, col int, force bool) (*extension.AutocompleteSuggestions, error) {
					count := calls.Add(1)
					result, err := current.GetSuggestions(ctx, lines, line, col, force)
					if err != nil || result == nil {
						return result, err
					}
					if tag == "A" {
						items := []extension.AutocompleteItem{}
						for _, item := range result.Items {
							if item.Value != "drop" {
								item.Label = fmt.Sprintf("%s:%d", item.Value, count)
								items = append(items, item)
							}
						}
						result.Items = items
					} else {
						result.Items = append(result.Items, extension.AutocompleteItem{Value: "tail", Label: fmt.Sprintf("tail:%d", count)})
					}
					return result, nil
				},
				ApplyCompletion: func(ctx context.Context, lines []string, line, col int, item extension.AutocompleteItem, prefix string) (extension.AutocompleteCompletion, error) {
					result, err := current.ApplyCompletion(ctx, lines, line, col, item, prefix)
					if err != nil {
						return result, err
					}
					result.Lines[result.CursorLine] += "-" + tag
					result.CursorCol += 2
					return result, nil
				},
				ShouldTriggerFileCompletion: func(ctx context.Context, lines []string, line, col int) (bool, error) {
					return current.ShouldTriggerFileCompletion(ctx, lines, line, col)
				},
			}, nil
		})
		if err != nil {
			return err
		}
		ui.Notify("registered:"+tag, "info")
	}
	return nil
}

// Pi interactive-mode.ts:779-794,2562-2565 rebuilds factories immediately, retains each resulting provider across queries, and shares its full get/apply/trigger behavior with both editors.
func TestAutocompleteFactoriesAcrossSDKs(t *testing.T) {
	cases := allHarnessCases()
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			ui := &autocompleteUI{recordingUI: h.ui}
			h.runner.SetUIContext(ui)
			if h.bridge != nil {
				h.bridge.SetUIContext(ui)
			}
			command, ok := findCommand(h.runner, "autocomplete-register")
			if !ok {
				t.Fatal("autocomplete-register is missing")
			}
			cc := h.runner.CreateCommandContext()
			ctx := extension.WithCommandContext(extension.WithContext(t.Context(), cc.Context), cc)
			h.ui.ClearRecorded()
			if err := command.Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			want := []string{"factory:A:info", "registered:A:info", "factory:A:info", "factory:B:info", "registered:B:info"}
			if got := h.ui.Recorded(); !slices.Equal(got, want) {
				t.Fatalf("factory ordering=%v, want %v", got, want)
			}
			provider, _ := ui.AutocompleteProvider(t.Context())
			if provider == nil {
				t.Fatal("no installed provider")
			}
			if !slices.Equal(provider.TriggerCharacters, []string{"$", "#"}) {
				t.Fatalf("triggers=%v", provider.TriggerCharacters)
			}
			for i, input := range []string{"query", "query", "empty", "error", "query"} {
				force := i == 4
				result, err := provider.GetSuggestions(t.Context(), []string{input}, 0, len(input), force)
				if input == "error" {
					if err == nil || !strings.Contains(err.Error(), "base failed") {
						t.Fatalf("error=%v", err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if input == "empty" {
					if result != nil {
						t.Fatalf("absence became %+v", result)
					}
					continue
				}
				value := "keep"
				if force {
					value = "forced"
				}
				want := &extension.AutocompleteSuggestions{Prefix: "base-prefix", Items: []extension.AutocompleteItem{{Value: value, Label: fmt.Sprintf("%s:%d", value, i+1)}, {Value: "tail", Label: fmt.Sprintf("tail:%d", i+1)}}}
				if !reflect.DeepEqual(result, want) {
					t.Fatalf("query %d=%+v, want %+v", i, result, want)
				}
			}
			result, err := provider.ApplyCompletion(t.Context(), []string{"input"}, 0, 5, extension.AutocompleteItem{Value: "🦊", Label: "fox"}, "input")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(result.Lines, []string{"🦊-A-B"}) || result.CursorLine != 0 || result.CursorCol != 6 {
				t.Fatalf("completion=%+v", result)
			}
			for _, input := range []string{"ordinary", "blocked"} {
				got, err := provider.ShouldTriggerFileCompletion(t.Context(), []string{input}, 0, len(input))
				if err != nil {
					t.Fatal(err)
				}
				if got != (input != "blocked") {
					t.Fatalf("trigger %s=%t", input, got)
				}
			}
		})
	}
}
