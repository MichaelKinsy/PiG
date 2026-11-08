package surfaces

import (
	"context"
	"sync"

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
// of its mode) and at every change of the registry, the user's pick or the mode, until Stop. Watches end in Stop itself
// (D452).
func FollowActiveTheme(h host.Host, mode string, changed func(Theme), ctx context.Context) (*ThemeFollower, error) {
	ctx, cancel := context.WithCancel(ctx)
	var mu sync.Mutex
	var themes []Theme
	var pick ThemePick
	current := mode
	var stopped bool
	update := func() {
		mu.Lock()
		defer mu.Unlock()
		if !stopped {
			changed(ActiveTheme(themes, pick, current))
		}
	}
	update()
	var stopsMu sync.Mutex
	var stops []func()
	addStop := func(stop func()) { stopsMu.Lock(); stops = append(stops, stop); stopsMu.Unlock() }
	stopAll := func() {
		mu.Lock()
		stopped = true
		mu.Unlock()
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
	go func() {
		watch, err := choice.Member("pick").Watch(ctx)
		if err != nil {
			// The hold ended (the follower stopped) before its first value.
			return
		}
		addStop(func() { _, _ = watch.Stop() })
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
	}()
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
