// Ports packages/coding-agent/src/modes/interactive/theme/theme.ts.
package tui

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ThemeWatcher owns native notifications and the reload worker for one interactive mode. Close cancels and joins the worker. Dispatch transfers publication to the UI owner; file reading and color resolution stay on the worker.
type ThemeWatcher struct {
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	dispatch   func(context.Context, func()) error
	directory  string
	mu         sync.Mutex
	changed    chan struct{}
	watcher    *fsnotify.Watcher
	name       string
	generation uint64
	closed     bool
}

// Selection and queued reload publication share a linearization point.
var themeMutationMu sync.Mutex
var activeThemeWatcher atomic.Pointer[ThemeWatcher]
var selectedThemeName atomic.Pointer[string]

// StartThemeWatcher enables custom-theme watching for the selected name. The caller owns Close. A nil dispatcher publishes directly for non-UI callers.
func StartThemeWatcher(ctx context.Context, directory string, dispatch func(context.Context, func()) error) *ThemeWatcher {
	ctx, cancel := context.WithCancel(ctx)
	if dispatch == nil {
		dispatch = func(ctx context.Context, apply func()) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			apply()
			return nil
		}
	}
	w := &ThemeWatcher{ctx: ctx, cancel: cancel, directory: directory, dispatch: dispatch, done: make(chan struct{}), changed: make(chan struct{})}
	themeMutationMu.Lock()
	old := activeThemeWatcher.Swap(w)
	if old != nil {
		old.cancel()
	}
	themeMutationMu.Unlock()
	if old != nil {
		old.Close()
	}
	go w.run()
	themeMutationMu.Lock()
	name := ActiveTheme().Name
	if selected := selectedThemeName.Load(); selected != nil {
		name = *selected
	}
	selectedThemeName.Store(&name)
	w.selectTheme(name)
	themeMutationMu.Unlock()
	return w
}

func noteSelectedTheme(name string, enableWatcher bool) {
	selectedThemeName.Store(&name)
	if enableWatcher {
		if w := activeThemeWatcher.Load(); w != nil {
			w.selectTheme(name)
		}
	}
}

func (w *ThemeWatcher) selectTheme(name string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.ctx.Err() != nil {
		return
	}
	w.name = name
	w.generation++
	if w.watcher != nil {
		_ = w.watcher.Close()
		w.watcher = nil
	}
	// upstream: packages/coding-agent/src/modes/interactive/theme/theme.ts:startThemeWatcher
	if name != "" && name != "dark" && name != "light" && filepath.Base(name) == name {
		if _, err := os.Stat(filepath.Join(w.directory, name+".json")); err == nil {
			if watcher, err := fsnotify.NewWatcher(); err == nil {
				if err := watcher.Add(w.directory); err == nil {
					w.watcher = watcher
				} else {
					_ = watcher.Close()
				}
			}
		}
	}
	// A closed generation pulse wakes every snapshot of the old selection without dropping state changes or blocking the UI owner on the reload worker.
	close(w.changed)
	w.changed = make(chan struct{})
}

func (w *ThemeWatcher) closeNative(watcher *fsnotify.Watcher) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if watcher != nil && w.watcher == watcher {
		_ = watcher.Close()
		w.watcher = nil
	}
}

// Close stops notifications and drains owned reload work. Already queued UI actions check cancellation and generation before publishing.
func (w *ThemeWatcher) Close() {
	if w == nil {
		return
	}
	w.cancel()
	themeMutationMu.Lock()
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		w.generation++
		if w.watcher != nil {
			_ = w.watcher.Close()
			w.watcher = nil
		}
	}
	w.mu.Unlock()
	activeThemeWatcher.CompareAndSwap(w, nil)
	themeMutationMu.Unlock()
	<-w.done
}

func selectedThemeIs(name string) bool {
	selected := selectedThemeName.Load()
	return selected != nil && *selected == name
}

func (w *ThemeWatcher) run() {
	defer close(w.done)
	defer func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.closed = true
		w.generation++
		if w.watcher != nil {
			_ = w.watcher.Close()
			w.watcher = nil
		}
	}()
	var timer *time.Timer
	var timerC <-chan time.Time
	var pendingName string
	var pendingGeneration uint64
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		w.mu.Lock()
		watcher, name, generation, changed := w.watcher, w.name, w.generation, w.changed
		w.mu.Unlock()
		if timerC != nil && pendingGeneration != generation {
			timer.Stop()
			timerC = nil
		}
		var events <-chan fsnotify.Event
		var failures <-chan error
		if watcher != nil {
			events, failures = watcher.Events, watcher.Errors
		}
		select {
		case <-w.ctx.Done():
			return
		case <-changed:
			continue
		case <-failures:
			// The error handler closes notifications without cancelling a reload scheduled by an earlier file event.
			w.closeNative(watcher)
		case event, ok := <-events:
			if !ok {
				w.closeNative(watcher)
				continue
			}
			if !selectedThemeIs(name) || event.Name != "" && filepath.Base(event.Name) != name+".json" {
				continue
			}
			pendingName, pendingGeneration = name, generation
			if timer == nil {
				// upstream: packages/coding-agent/src/modes/interactive/theme/theme.ts:startThemeWatcher
				timer = time.NewTimer(100 * time.Millisecond)
			} else {
				// upstream: packages/coding-agent/src/modes/interactive/theme/theme.ts:startThemeWatcher
				timer.Reset(100 * time.Millisecond)
			}
			timerC = timer.C
		case <-timerC:
			timerC = nil
			w.reload(pendingName, pendingGeneration)
		}
	}
}

func (w *ThemeWatcher) reload(name string, generation uint64) {
	if w.ctx.Err() != nil || !selectedThemeIs(name) {
		return
	}
	// Missing and partially written files retain the last successfully loaded theme.
	theme, err := LoadThemeFile(filepath.Join(w.directory, name+".json"))
	if err != nil {
		return
	}
	indexed := theme.WithColorMode(ColorMode256)
	w.mu.Lock()
	dispatch := w.dispatch
	w.mu.Unlock()
	_ = dispatch(w.ctx, func() {
		themeMutationMu.Lock()
		defer themeMutationMu.Unlock()
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.closed || w.ctx.Err() != nil || w.generation != generation || w.name != name || !selectedThemeIs(name) {
			return
		}
		registry := ActiveThemeRegistry()
		registry.mu.Lock()
		if _, exists := registry.themes[name]; !exists {
			registry.names = append(registry.names, name)
		}
		registry.themes[name] = theme
		if registry.paths == nil {
			registry.paths = make(map[string]string)
		}
		registry.paths[name] = filepath.Join(w.directory, name+".json")
		registry.mu.Unlock()
		if currentColorMode() == ColorMode256 {
			activeTheme.Store(indexed)
		} else {
			activeTheme.Store(theme)
		}
	})
}
