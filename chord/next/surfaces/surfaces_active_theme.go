package surfaces

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports packages/chord/src/surfaces/active-theme.ts. The active theme of a client (slice 11 §4b): the `Themes` registry
// on the client's host, the user's `ThemeChoice` (until it binds, or when the app serves none, the first theme of each
// mode) and the client's mode, combined; each change calls the client's writer.

// ThemeFollower is a client's active theme as it follows it: its mode can change (a page's colour scheme), and it ends.
type ThemeFollower struct {
	// SetMode says the client's mode changed: the active theme is chosen again.
	SetMode func(mode string)
	Stop    func()
}

// FollowActiveTheme follows the active theme on h for mode, calling changed with it at once (the fallback or the first
// of its mode) and at every change of the registry, the user's pick or the mode, until Stop. Stop ends its watches; so does the
// cancellation of ctx (a pick watch that ended before its start follows nothing).
func FollowActiveTheme(h host.Host, mode string, changed func(Theme), ctx context.Context) (*ThemeFollower, error) {
	ctx, cancel := context.WithCancel(ctx)
	var mu sync.Mutex
	var themes []Theme
	var pick ThemePick
	current := mode
	// gate orders Stop against the pick watch's start: a watch is started only while the follower runs.
	var gate sync.Mutex
	var stopped atomic.Bool
	var out outbox
	update := func() {
		mu.Lock()
		if stopped.Load() {
			mu.Unlock()
			return
		}
		theme := ActiveTheme(themes, pick, current)
		mu.Unlock()
		out.run(func() {
			if !stopped.Load() {
				changed(theme)
			}
		})
	}
	update()
	var stopsMu sync.Mutex
	var stops []func()
	addStop := func(stop func()) { stopsMu.Lock(); stops = append(stops, stop); stopsMu.Unlock() }
	stopAll := func() {
		gate.Lock()
		stopped.Store(true)
		gate.Unlock()
		cancel()
		stopsMu.Lock()
		defer stopsMu.Unlock()
		for _, stop := range stops {
			stop()
		}
	}
	registry, err := h.Registry(ctx, registries.Registry[any, any](Themes), host.GetOptions{})
	if err != nil {
		stopAll()
		return nil, err
	}
	mu.Lock()
	themes, _ = registry.Value().([]Theme)
	mu.Unlock()
	update()
	registryWatch, err := registry.Watch(ctx)
	if err != nil {
		stopAll()
		return nil, err
	}
	addStop(func() { _, _ = registryWatch.Stop() })
	registryWatch.Start(func(_ context.Context, call chord.NativeWatchCall[any]) error {
		if call.Kind == chord.WatchCallValue {
			mu.Lock()
			themes, _ = call.Value.([]Theme)
			mu.Unlock()
		}
		update()
		return nil
	})
	choice, err := h.Hold(ctx, ThemeChoice, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		stopAll()
		return nil, err
	}
	addStop(choice.Release)
	// The pick follows once its hold binds, from the microtask that opens its watch in the turn that binds the hold (Pi:
	// `choice.pick.watch(ctx).then(start, () => {})`, not awaited): a change of the pick after the bind is a frame of the
	// watch. The hold ending (the follower stopped) before its first value is no error.
	choice.Member("pick").WatchThen(ctx, func(watch chord.WatchHandle[delta.JsonValue], err error) {
		if err != nil {
			return
		}
		gate.Lock()
		defer gate.Unlock()
		if stopped.Load() {
			// Nothing here may block (it is a microtask of the host's loop): Stop's cancel() aborts this watch, and the hold's release ends it.
			return
		}
		addStop(func() { _, _ = watch.Stop() })
		// The caller may cancel the ctx it passed instead of calling Stop: the watch has ended then, and there is nothing to follow
		// (Pi: `watch(ctx).then(start, () => {})` ignores it).
		chord.StartUnlessEnded(func() {
			watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
				if call.Kind == chord.WatchCallValue {
					if value, ok := call.Value.(*delta.JsonObject); ok {
						mu.Lock()
						pick = ThemePick{Dark: idOf(value.Value("dark")), Light: idOf(value.Value("light"))}
						mu.Unlock()
					}
				}
				update()
				return nil
			})
		})
	})
	return &ThemeFollower{
		SetMode: func(next string) {
			mu.Lock()
			current = next
			mu.Unlock()
			update()
		},
		Stop: stopAll,
	}, nil
}

func idOf(value any) *string {
	if text, ok := value.(string); ok {
		return &text
	}
	return nil
}
