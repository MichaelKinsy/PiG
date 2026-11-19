package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (autocompleteProviderWrappers).

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
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
	//portlint:allow utf16units byteColumn is a Go byte offset taken from ApplyCompletion; this loop converts it to the UTF-16 column Pi reports
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

// reportAutocompleteRebuildError reports the failure of a provider rebuild nobody awaits. An installed editor that refused the provider
// (Pi: the throw of `this.editor.setAutocompleteProvider?.(provider)`, which the extension runner shows) is an extension error and the session
// goes on: the default editor holds the rebuilt provider, and the extension's editor keeps the one it had. Any other failure is a provider that
// failed, which Pi does not catch either. It runs on the owner loop, once per rebuild, so one failed sync shows one error.
func (m *InteractiveMode) reportAutocompleteRebuildError(err error) {
	if refused, ok := errors.AsType[*extension.EditorSyncError](err); ok {
		m.queueExtensionError(&extension.ExtensionError{ExtensionPath: refused.ExtensionPath, Event: "setAutocompleteProvider", Error: refused.Error(), Stack: extension.ErrorStack(err)})
		return
	}
	m.reportAutocompleteError(err)
}

// autocompleteChangedCallback is the provider rebuild the editor runs when its provider is replaced. The rebuild answers on the channel
// setAutocompleteAwaiting left, when one was left.
func (m *InteractiveMode) autocompleteChangedCallback() func(tui.AutocompleteProvider) {
	return func(base tui.AutocompleteProvider) {
		result := m.autocompleteRebuildResult
		m.autocompleteRebuildResult = nil
		m.rebuildAutocompleteWrappers(base, result)
	}
}

// setAutocompleteAwaiting is editor.SetAutocomplete for a caller that Pi lets see the failure of setupAutocompleteProvider (a throw of its own call
// stack): the rebuild's outcome comes back on the returned channel, which is nil when no rebuild ran. It runs on the owner loop and never waits.
func (m *InteractiveMode) setAutocompleteAwaiting(provider tui.AutocompleteProvider) <-chan error {
	result := make(chan error, 1)
	m.autocompleteRebuildResult = result
	m.editor.SetAutocomplete(provider)
	if m.autocompleteRebuildResult == nil {
		return result
	}
	m.autocompleteRebuildResult = nil
	return nil
}

// setAutocompleteUncaught is editor.SetAutocomplete for a caller whose throw Pi leaves uncaught (the settings selector's change handler, which runs
// inside the terminal's input handler): a rebuild failure ends the session as an uncaught exception, as the same throw ends Pi's.
func (m *InteractiveMode) setAutocompleteUncaught(provider tui.AutocompleteProvider) {
	result := m.setAutocompleteAwaiting(provider)
	if result == nil {
		return
	}
	ctx := m.autocompleteContext()
	m.backgroundTasks.Go(func() {
		if err := awaitAutocompleteRebuild(ctx, result); err != nil {
			_ = m.postToMain(ctx, func() { m.reportAutocompleteError(err) })
		}
	})
}

// awaitAutocompleteRebuild is the wait of a caller whose own failure the rebuild is. A cancelled rebuild is no failure.
func awaitAutocompleteRebuild(ctx context.Context, result <-chan error) error {
	if result == nil {
		return nil
	}
	select {
	case err := <-result:
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
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
		if query.AwaitsIO() {
			// The wrapper chain waits on the base provider's awaited work: it has left Pi's microtask checkpoint.
			tui.AutocompleteAwaitsIO(ctx)
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
		before = jsTrim(jsstring.Slice(before, 0, col))
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

// holdAutocompleteInput holds the next terminal read for one request of the extension provider chain. Pi opens the menu of a chain that
// needs no I/O in the Promise jobs after the read that typed its text, before the next read, and PiG's chain is a round trip through the
// extension runtime that the next read would overtake. The hold ends when the request ends (end: its answer was published, the editor
// cancelled it, or the runtime's connection ended) or when its chain announces that it awaits real I/O (the returned context's
// tui.AutocompleteAwaitsIO), which is when Pi's next read would have come first. A synchronous provider that blocks holds the read until it
// returns, as it holds Pi's whole loop. It runs on the owner loop.
func (m *InteractiveMode) holdAutocompleteInput(ctx context.Context) (context.Context, func()) {
	released := make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	gate := released
	if previous := m.autocompletePending; previous != nil {
		select {
		case <-previous:
		default:
			// An earlier hold (a completion apply, a provider rebuild) still gates input: this one opens after it.
			gate = make(chan struct{})
			lifetime := m.autocompleteContext()
			m.backgroundTasks.Go(func() {
				defer close(gate)
				for _, wait := range []<-chan struct{}{previous, released} {
					select {
					case <-wait:
					case <-lifetime.Done():
						return
					}
				}
			})
		}
	}
	m.autocompletePending = gate
	return tui.WithAutocompleteUnhold(ctx, release), release
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
					m.reportAutocompleteRebuildError(err)
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
	m.autocompleteSetUp = true
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
			m.editor.SetAutocompleteInputHold(m.holdAutocompleteInput)
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

// awaitInstalledEditor returns once the editor a session_start handler installed is in place. Pi's emit is an awaited call in one process, so the editor
// is installed when the bind goes on to setupAutocompleteProvider; here the install is a notice that the owner loop applies after the emit returned, and a
// provider rebuilt before it ran would never reach that editor. It runs off the owner loop, which serves the install meanwhile.
func (m *InteractiveMode) awaitInstalledEditor(ctx context.Context) error {
	if m.onOwnerLoop() {
		return nil
	}
	if bridge, ok := m.opts.SubprocessUIBridge.(interface{ AwaitEditorInstalled(context.Context) error }); ok {
		return bridge.AwaitEditorInstalled(ctx)
	}
	return nil
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
	m.editor.SetAutocompleteInputHold(nil)
	m.editor.SetAutocompleteTaskOwner(m.autocompleteContext(), m.backgroundTasks.Go, m.reportAutocompleteError)
}

// currentAutocompleteProvider is Pi's `this.autocompleteProvider`: the provider chain the editors share, or nil until the first setupAutocompleteProvider has
// run. A caller that is on the owner loop gets the rebuilt chain, or the base of the editor's provider when the chain is not built yet. Owner loop only.
func (m *InteractiveMode) currentAutocompleteProvider() *extension.AutocompleteProvider {
	if !m.autocompleteSetUp {
		return nil
	}
	if m.autocompleteProvider != nil {
		return m.autocompleteProvider
	}
	if base := m.editor.AutocompleteProvider(); base != nil {
		return m.baseAutocompleteProvider(base)
	}
	return nil
}

// AutocompleteProvider captures the active chain for a subprocess editor. Both editors use that same provider instance.
func (u *ExtUIContext) AutocompleteProvider(ctx context.Context) (*extension.AutocompleteProvider, error) {
	var provider *extension.AutocompleteProvider
	err := u.m.autocompleteOnMain(ctx, func() { provider = u.m.currentAutocompleteProvider() })
	return provider, err
}

// AutocompletePending reports that the first setupAutocompleteProvider has not run, so that Pi has no provider to hand an editor installed now.
func (u *ExtUIContext) AutocompletePending(ctx context.Context) (bool, error) {
	pending := false
	err := u.m.autocompleteOnMain(ctx, func() { pending = !u.m.autocompleteSetUp })
	return pending, err
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
	done := (<-chan error)(result)
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
		u.m.editor.SetAutocompleteChanged(u.m.autocompleteChangedCallback())
		if u.m.editor.AutocompleteProvider() == nil {
			// Pi's addAutocompleteProvider runs setupAutocompleteProvider, which builds the base provider itself (interactive-mode.ts:2800-2801, :854). Before the
			// bind's first setup the editor holds none, and the chain must not stand on nil: the editor takes the base that setup would install, and the change it
			// reports rebuilds the chain over it.
			done = u.m.setAutocompleteAwaiting(u.m.buildAutocompleteProvider())
			return
		}
		u.m.rebuildAutocompleteWrappers(u.m.editor.AutocompleteProvider(), result)
	}); err != nil {
		return err
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
