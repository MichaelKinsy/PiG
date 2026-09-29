package codingagent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

type autocompleteRegistration struct {
	factory  extension.AutocompleteProviderFactory
	lifetime context.Context
	stop     func() bool
}

func (m *InteractiveMode) pruneAutocompleteFactories() {
	m.autocompleteFactories = slices.DeleteFunc(m.autocompleteFactories, func(reg autocompleteRegistration) bool {
		if reg.lifetime.Err() == nil {
			return false
		}
		if reg.stop != nil {
			reg.stop()
		}
		return true
	})
}

func utf16Column(text string, byteColumn int) int {
	column := 0
	for _, r := range text[:min(max(byteColumn, 0), len(text))] {
		column++
		if r > 0xffff {
			column++
		}
	}
	return column
}
func byteColumn(text string, column int) int {
	units := 0
	for offset, r := range text {
		if units >= column {
			return offset
		}
		units++
		if r > 0xffff {
			units++
		}
	}
	return len(text)
}
func autocompleteLine(lines []string, line int) string {
	if line < 0 || line >= len(lines) {
		return ""
	}
	return lines[line]
}

func extensionSuggestions(result *tui.AutocompleteSuggestions) *extension.AutocompleteSuggestions {
	if result == nil {
		return nil
	}
	out := &extension.AutocompleteSuggestions{Prefix: result.Prefix, Items: make([]extension.AutocompleteItem, len(result.Items))}
	for i, item := range result.Items {
		out.Items[i] = extension.AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description}
	}
	return out
}
func tuiSuggestions(result *extension.AutocompleteSuggestions) *tui.AutocompleteSuggestions {
	if result == nil {
		return nil
	}
	out := &tui.AutocompleteSuggestions{Prefix: result.Prefix, Items: make([]tui.AutocompleteItem, len(result.Items))}
	for i, item := range result.Items {
		out.Items[i] = tui.AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description}
	}
	return out
}

func (m *InteractiveMode) reportAutocompleteError(err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		panic(err)
	}
}

func (m *InteractiveMode) autocompleteContext() context.Context {
	if m.backgroundCtx != nil {
		return m.backgroundCtx
	}
	return context.Background()
}

// autocompleteOnMain obtains a provider snapshot without doing provider IPC or filesystem work on the owner loop.
func (m *InteractiveMode) autocompleteOnMain(ctx context.Context, fn func()) error {
	if m.runCtx == nil {
		fn()
		return nil
	}
	done := make(chan struct{})
	if err := m.postToMain(ctx, func() {
		if ctx.Err() == nil {
			fn()
		}
		close(done)
	}); err != nil {
		return err
	}
	select {
	case <-done:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *InteractiveMode) baseAutocompleteProvider(base tui.AutocompleteProvider) *extension.AutocompleteProvider {
	provider := &extension.AutocompleteProvider{}
	provider.GetSuggestions = func(ctx context.Context, lines []string, line, col int, force bool) (*extension.AutocompleteSuggestions, error) {
		col = byteColumn(autocompleteLine(lines, line), col)
		var query *tui.RemoteSuggestionQuery
		if err := m.autocompleteOnMain(ctx, func() { query = tui.NewAutocompleteQuery(base, lines, line, col, force) }); err != nil {
			return nil, err
		}
		result, err := query.RunResult(ctx)
		return extensionSuggestions(result), err
	}
	provider.ApplyCompletion = func(ctx context.Context, lines []string, line, col int, item extension.AutocompleteItem, prefix string) (extension.AutocompleteCompletion, error) {
		col = byteColumn(autocompleteLine(lines, line), col)
		var result []string
		var resultLine, resultCol int
		err := m.autocompleteOnMain(ctx, func() {
			result, resultLine, resultCol = base.ApplyCompletion(lines, line, col, tui.AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description}, prefix)
		})
		return extension.AutocompleteCompletion{Lines: result, CursorLine: resultLine, CursorCol: utf16Column(autocompleteLine(result, resultLine), resultCol)}, err
	}
	// Pi CombinedAutocompleteProvider.shouldTriggerFileCompletion only rejects the slash-command-name context.
	provider.ShouldTriggerFileCompletion = func(_ context.Context, lines []string, line, col int) (bool, error) {
		before := autocompleteLine(lines, line)
		before = jsTrim(before[:byteColumn(before, col)])
		return !strings.HasPrefix(before, "/") || strings.Contains(before, " "), nil
	}
	return provider
}

func (m *InteractiveMode) asyncAutocompleteProvider(provider *extension.AutocompleteProvider) *tui.AsyncAutocompleteProvider {
	adapted := &tui.AsyncAutocompleteProvider{TriggerCharacters: slices.Clone(provider.TriggerCharacters)}
	adapted.GetSuggestions = func(ctx context.Context, lines []string, line, col int, force bool) (*tui.AutocompleteSuggestions, error) {
		result, err := provider.GetSuggestions(ctx, lines, line, utf16Column(autocompleteLine(lines, line), col), force)
		return tuiSuggestions(result), err
	}
	adapted.ApplyCompletion = func(ctx context.Context, lines []string, line, col int, item tui.AutocompleteItem, prefix string) ([]string, int, int, error) {
		result, err := provider.ApplyCompletion(ctx, lines, line, utf16Column(autocompleteLine(lines, line), col), extension.AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description}, prefix)
		if err != nil {
			return nil, 0, 0, err
		}
		text := autocompleteLine(result.Lines, result.CursorLine)
		if result.CursorCol < 0 || result.CursorCol > utf16Column(text, len(text)) || !utf8.ValidString(text) {
			return nil, 0, 0, errors.New("autocomplete provider returned an invalid cursor")
		}
		return result.Lines, result.CursorLine, byteColumn(text, result.CursorCol), nil
	}
	if provider.ShouldTriggerFileCompletion != nil {
		adapted.ShouldTriggerFileCompletion = func(ctx context.Context, lines []string, line, col int) (bool, error) {
			return provider.ShouldTriggerFileCompletion(ctx, lines, line, utf16Column(autocompleteLine(lines, line), col))
		}
	}
	return adapted
}

// startAutocompleteWork owns ordered input/factory work. The input pump waits before delivering the next chunk, while render and UI tasks remain live.
func (m *InteractiveMode) startAutocompleteWork(work tui.AutocompleteWork, result chan<- error, after func(context.Context) error) {
	previous := m.autocompletePending
	done := make(chan struct{})
	finish := sync.OnceFunc(func() { close(done) })
	m.autocompletePending = done
	ctx := m.autocompleteContext()
	m.backgroundTasks.Go(func() {
		defer finish()
		var err error
		if previous != nil {
			select {
			case <-previous:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		var apply func()
		if err == nil {
			apply, err = work(ctx)
		}
		postErr := m.autocompleteOnMain(ctx, func() {
			if after == nil {
				if m.autocompletePending == done {
					m.autocompletePending = nil
				}
				finish()
			}
			if err != nil {
				if after == nil && result == nil {
					m.reportAutocompleteError(err)
				}
			} else if apply != nil {
				apply()
			}
			if m.tuiInst != nil && after == nil {
				m.tuiInst.RequestRender()
			}
		})
		if postErr != nil {
			err = postErr
		}
		if after != nil {
			if err == nil {
				err = after(ctx)
			}
			if clearErr := m.autocompleteOnMain(ctx, func() {
				if m.autocompletePending == done {
					m.autocompletePending = nil
				}
				if err != nil && result == nil {
					m.reportAutocompleteError(err)
				}
			}); clearErr != nil {
				err = clearErr
			}
		}
		if result != nil {
			result <- err
		}
	})
}

func (m *InteractiveMode) rebuildAutocompleteWrappers(base tui.AutocompleteProvider, result chan<- error) {
	m.pruneAutocompleteFactories()
	factories := make([]extension.AutocompleteProviderFactory, len(m.autocompleteFactories))
	for i, reg := range m.autocompleteFactories {
		factories[i] = reg.factory
	}
	if m.autocompleteEpoch == nil {
		m.autocompleteEpoch, m.autocompleteEpochCancel = context.WithCancel(m.autocompleteContext())
	}
	epoch := m.autocompleteEpoch
	var provider *extension.AutocompleteProvider
	m.startAutocompleteWork(func(_ context.Context) (func(), error) {
		var err error
		provider, err = extension.SetupAutocompleteProvider(epoch, m.baseAutocompleteProvider(base), factories)
		if err != nil {
			if epoch.Err() != nil {
				return nil, epoch.Err()
			}
			return nil, err
		}
		var adapted *tui.AsyncAutocompleteProvider
		if len(factories) > 0 {
			adapted = m.asyncAutocompleteProvider(provider)
		}
		return func() {
			if epoch.Err() != nil {
				return
			}
			m.autocompleteProvider = provider
			m.editor.SetAsyncAutocomplete(adapted, epoch, m.backgroundTasks.Go, func(work tui.AutocompleteWork) { m.startAutocompleteWork(work, nil, nil) }, m.reportAutocompleteError)
		}, nil
	}, result, func(ctx context.Context) error {
		if epoch.Err() != nil {
			return epoch.Err()
		}
		if bridge, ok := m.opts.SubprocessUIBridge.(interface {
			SyncAutocomplete(context.Context, *extension.AutocompleteProvider) error
		}); ok {
			return bridge.SyncAutocomplete(ctx, provider)
		}
		return nil
	})
}

// resetAutocompleteWrappers releases the old generation before resource reload and restores the local provider.
func (m *InteractiveMode) resetAutocompleteWrappers() {
	if m.autocompleteEpochCancel != nil {
		m.autocompleteEpochCancel()
	}
	m.autocompleteEpoch = nil
	m.autocompleteEpochCancel = nil
	for _, reg := range m.autocompleteFactories {
		if reg.stop != nil {
			reg.stop()
		}
	}
	m.autocompleteFactories = nil
	m.autocompleteProvider = nil
	m.editor.SetAutocompleteChanged(nil)
	m.editor.SetAsyncAutocomplete(nil, nil, nil, nil, nil)
	m.editor.SetAutocompleteTaskOwner(m.autocompleteContext(), m.backgroundTasks.Go, m.reportAutocompleteError)
}

// AutocompleteProvider captures the active chain for a subprocess editor. Both editors use that same provider instance.
func (u *ExtUIContext) AutocompleteProvider(ctx context.Context) (*extension.AutocompleteProvider, error) {
	var provider *extension.AutocompleteProvider
	err := u.m.autocompleteOnMain(ctx, func() {
		provider = u.m.autocompleteProvider
		if provider == nil && u.m.editor.AutocompleteProvider() != nil {
			provider = u.m.baseAutocompleteProvider(u.m.editor.AutocompleteProvider())
		}
	})
	return provider, err
}

func (u *ExtUIContext) AddAutocompleteProvider(factory extension.AutocompleteProviderFactory) error {
	if u.m == nil {
		return nil
	}
	return u.AddAutocompleteProviderWithLifetime(u.m.autocompleteContext(), factory)
}

// AddAutocompleteProviderWithLifetime drops registrations when their owning extension connection ends.
func (u *ExtUIContext) AddAutocompleteProviderWithLifetime(lifetime context.Context, factory extension.AutocompleteProviderFactory) error {
	if u.m == nil || u.m.editor == nil {
		return nil
	}
	if factory == nil {
		return errors.New("autocomplete factory is missing")
	}
	result := make(chan error, 1)
	ctx := u.m.autocompleteContext()
	if err := u.m.autocompleteOnMain(ctx, func() {
		if lifetime == nil {
			lifetime = ctx
		}
		stop := context.AfterFunc(lifetime, func() {
			u.m.remoteEditorEvents.post(u.m, func() {
				before := len(u.m.autocompleteFactories)
				u.m.pruneAutocompleteFactories()
				if before != len(u.m.autocompleteFactories) {
					if len(u.m.autocompleteFactories) == 0 && u.m.remoteEditor == nil {
						u.m.resetAutocompleteWrappers()
					} else {
						u.m.rebuildAutocompleteWrappers(u.m.editor.AutocompleteProvider(), nil)
					}
				}
			})
		})
		u.m.autocompleteFactories = append(u.m.autocompleteFactories, autocompleteRegistration{factory: factory, lifetime: lifetime, stop: stop})
		u.m.editor.SetAutocompleteChanged(func(base tui.AutocompleteProvider) { u.m.rebuildAutocompleteWrappers(base, nil) })
		u.m.rebuildAutocompleteWrappers(u.m.editor.AutocompleteProvider(), result)
	}); err != nil {
		return err
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
