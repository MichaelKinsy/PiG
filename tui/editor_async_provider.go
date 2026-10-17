package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// AsyncAutocompleteProvider runs a complete provider chain off the editor loop. Provider columns are byte offsets; the native editor converts its UTF-16 positions at this boundary.
type AsyncAutocompleteProvider struct {
	TriggerCharacters           []string
	GetSuggestions              func(context.Context, []string, int, int, bool) (*AutocompleteSuggestions, error)
	ApplyCompletion             func(context.Context, []string, int, int, AutocompleteItem, string) ([]string, int, int, error)
	ShouldTriggerFileCompletion func(context.Context, []string, int, int) (bool, error)
}

// AutocompleteWork runs off-loop and returns a mutation to execute on the owner loop. The host holds subsequent input while input work is pending.
type AutocompleteWork func(context.Context) (func(), error)

// SetAsyncAutocomplete selects a complete provider, not an additive suggestion source. start owns query workers; input owns ordered completion workers.
func (e *Editor) SetAsyncAutocomplete(provider *AsyncAutocompleteProvider, lifetime context.Context, start func(func()), input func(AutocompleteWork), report func(error)) {
	e.AutocompleteCancel()
	e.asyncAutocomplete = provider
	e.autocompleteLifetime = lifetime
	e.startAutocompleteTask = start
	e.startAutocompleteInput = input
	e.autocompleteError = report
}

// AsyncSuggestionPlanner captures the awaited branch of a local provider without executing that branch on the owner loop.
type AsyncSuggestionPlanner interface {
	SuggestionTask([]string, int, int, bool) (string, func(context.Context) ([]AutocompleteItem, error), bool)
}

// SetAutocompleteTaskOwner binds the lifetime, joined worker launcher and owner-loop error reporter for native queries.
func (e *Editor) SetAutocompleteTaskOwner(ctx context.Context, start func(func()), report func(error)) {
	e.AutocompleteCancel()
	e.autocompleteLifetime = ctx
	e.startAutocompleteTask = start
	e.autocompleteError = report
}

// AutocompleteProvider returns the local base captured by extension wrapper factories.
func (e *Editor) AutocompleteProvider() AutocompleteProvider { return e.autocomplete }

// SetAutocompleteChanged binds the owner's provider rebuild operation.
func (e *Editor) SetAutocompleteChanged(changed func(AutocompleteProvider)) {
	e.autocompleteChanged = changed
}

// AcceptAutocomplete reports whether an accepted slash-name completion submits. Remote completion waits off-loop and calls done only after applying on the owner loop.
func (e *Editor) AcceptAutocomplete(done func(bool)) {
	if e.asyncAutocomplete == nil {
		done(e.AutocompleteAccept())
		return
	}
	e.acceptAsyncAutocomplete(done)
}

func (e *Editor) acceptAsyncAutocomplete(done func(bool)) {
	if len(e.autocompleteItems) == 0 || e.cursor != e.autocompleteQueryCursor {
		if done != nil {
			done(false)
		}
		return
	}
	provider := e.asyncAutocomplete
	lines, cursor := slices.Clone(e.lines), e.cursor
	queryLines, queryRow, queryCol := e.autocompleteView()
	queryLines = slices.Clone(queryLines)
	item, prefix := e.autocompleteItems[e.autocompleteCursor], e.autocompletePrefix
	e.AutocompleteCancel()
	e.saveHistory()
	e.lastAction = ""
	request := e.autocompleteRequestID
	e.startAutocompleteInput(func(ctx context.Context) (func(), error) {
		result, line, col, err := provider.ApplyCompletion(ctx, queryLines, queryRow, queryCol, item, prefix)
		if err != nil {
			return nil, err
		}
		if err := validAutocompleteCompletion(result, line, col); err != nil {
			return nil, err
		}
		return func() {
			if request != e.autocompleteRequestID || e.cursor != cursor || !slices.Equal(e.lines, lines) {
				if done != nil {
					done(false)
				}
				return
			}
			e.applyAutocompleteState(result, line, col)
			e.Invalidate()
			if e.OnChange != nil {
				e.OnChange(e.Text())
			}
			if done != nil {
				done(strings.HasPrefix(prefix, "/"))
			}
		}, nil
	})
}

func validAutocompleteCompletion(lines []string, line, col int) error {
	if line < 0 || line >= len(lines) || col < 0 || col > len(lines[line]) {
		return errors.New("autocomplete provider returned an invalid cursor")
	}
	return nil
}

// Mirrors utils.ts autocompleteSeparatorRegex, including punctuation in CJK Script_Extensions.
func autocompleteSeparator(r rune) bool {
	return widthx.IsJSSpace(r) || strings.ContainsRune("，．：；！？（）［］｛｝“”‘’…—", r) || (unicode.IsPunct(r) && widthx.IsCJKBreak(string(r)))
}

func autocompleteTriggered(before string, characters []string) bool {
	triggers := []rune{'@', '#'}
	for _, character := range characters {
		r, size := utf8.DecodeRuneInString(character)
		if size > 0 && size == len(character) && r <= 0xffff && r != '/' && !widthx.IsJSSpace(r) && !slices.Contains(triggers, r) {
			triggers = append(triggers, r)
		}
	}
	previousBoundary := true
	for offset, r := range before {
		if previousBoundary && slices.Contains(triggers, r) {
			suffix := before[offset+utf8.RuneLen(r):]
			if r == '@' && strings.HasPrefix(suffix, "\"") && !strings.Contains(suffix[1:], "\"") {
				return true
			}
			if strings.IndexFunc(suffix, autocompleteSeparator) < 0 {
				return true
			}
		}
		previousBoundary = nextAutocompleteBoundary(previousBoundary, r)
	}
	return false
}

// nextAutocompleteBoundary reports whether a trigger token may start after r, given whether one could start before it. A trigger token starts at a boundary, optionally after opening wrappers, e.g. "(@src/foo" or "`@src/foo" (editor.ts autocompleteTokenStartSource).
func nextAutocompleteBoundary(previousBoundary bool, r rune) bool {
	return autocompleteSeparator(r) || (previousBoundary && strings.ContainsRune(autocompleteTokenWrappers, r))
}

// autocompleteTokenWrappers are the opening wrappers that may sit between a boundary and a trigger token.
const autocompleteTokenWrappers = "([{<`"

func (e *Editor) naturalAsyncAutocomplete() (bool, bool) {
	before := jsstring.Slice(e.lines[e.cursor[0]], 0, e.cursor[1])
	triggered := autocompleteTriggered(before, e.asyncAutocomplete.TriggerCharacters)
	slash := e.cursor[0] == 0 && strings.HasPrefix(strings.TrimLeftFunc(before, widthx.IsJSSpace), "/")
	return triggered || slash || e.AutocompleteOpen(), triggered
}

func (e *Editor) requestAsyncAutocomplete(force, explicit bool) {
	e.requestAutocompleteProvider(e.asyncAutocomplete, force, explicit)
}

func (e *Editor) requestAutocompleteProvider(provider *AsyncAutocompleteProvider, force, explicit bool) {
	if e.asyncCancel != nil {
		e.asyncCancel()
	}
	lifetime := e.autocompleteLifetime
	ctx, cancel := context.WithCancel(context.WithoutCancel(lifetime))
	stopped := make(chan struct{})
	stopLifetime := context.AfterFunc(e.autocompleteLifetime, func() { cancel(); close(stopped) })
	if e.autocompleteLifetime.Err() != nil {
		cancel()
	}
	e.asyncCancel = cancel
	lines, cursor, request := slices.Clone(e.lines), e.cursor, e.autocompleteRequestID
	queryLines, queryRow, queryCol := e.autocompleteView()
	queryLines = slices.Clone(queryLines)
	previous := e.autocompleteTaskDone
	finished := make(chan struct{})
	finish := sync.OnceFunc(func() {
		if !stopLifetime() {
			<-stopped
		}
		close(finished)
	})
	e.autocompleteTaskDone = finished
	var timer *time.Timer
	if !explicit && !force && autocompleteTriggered(jsstring.Slice(lines[cursor[0]], 0, cursor[1]), provider.TriggerCharacters) {
		timer = time.NewTimer(attachmentAutocompleteDebounce)
	}
	launch := func() {
		e.startAutocompleteTask(func() {
			defer finish()
			if timer != nil {
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
				}
			}
			// Cancelled queued requests still join predecessors before releasing their successors.
			if previous != nil {
				<-previous
			}
			if ctx.Err() != nil {
				return
			}
			result, err := provider.GetSuggestions(ctx, queryLines, queryRow, queryCol, force)
			if ctx.Err() != nil {
				return
			}
			applied := make(chan struct{})
			e.scheduleAsyncApply(func() {
				defer close(applied)
				if ctx.Err() != nil || request != e.autocompleteRequestID || e.cursor != cursor || !slices.Equal(e.lines, lines) {
					return
				}
				defer e.requestRender()
				e.asyncCancel = nil
				e.autocompleteFromAwaited = e.asyncAutocomplete == nil
				if err != nil {
					e.autocompleteError(err)
					return
				}
				e.setAutocompleteItems(nil)
				e.autocompletePrefix = ""
				e.autocompleteForced = force
				if result != nil && len(result.Items) > 0 {
					e.setAutocompleteItems(result.Items)
					e.autocompletePrefix = result.Prefix
					e.autocompleteQueryCursor = cursor
					e.autocompleteCursor = bestAutocompleteMatchIndex(result.Items, result.Prefix)
					if force && explicit && len(result.Items) == 1 {
						if e.asyncAutocomplete == nil {
							e.AutocompleteAccept()
						} else {
							e.acceptAsyncAutocomplete(nil)
						}
					}
				}
				e.Invalidate()
			})
			select {
			case <-applied:
			case <-lifetime.Done():
			}
		})
	}
	if force && provider.ShouldTriggerFileCompletion != nil {
		e.startAutocompleteInput(func(_ context.Context) (func(), error) {
			should, err := provider.ShouldTriggerFileCompletion(ctx, queryLines, queryRow, queryCol)
			if err != nil || !should {
				finish()
				return nil, err
			}
			return func() {
				if ctx.Err() != nil {
					finish()
					return
				}
				launch()
			}, nil
		})
	} else {
		launch()
	}
}
