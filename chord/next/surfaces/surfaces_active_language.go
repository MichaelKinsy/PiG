package surfaces

import (
	"context"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports packages/chord/src/surfaces/active-language.ts (`hostLanguage` is in surfaces_page_state.go). The active
// language of a client: the `Translations` registry on the client's host (served there by the kit), the user's
// `LocaleChoice` (held on the server; until it binds, or when the app serves none, no pick) and the device's languages,
// combined. The host keeps it as page state, where `useMessages` and `TuiScope.messages` read and follow it; each change
// also calls the client's writer. Reports are passed on once each.

// LanguageFollower is a client's active language as it follows it: the device's languages can change (a browser's), and
// it ends.
type LanguageFollower struct {
	// SetDevice says the device's languages changed: the locale is resolved again.
	SetDevice func(preferred []string)
	Stop      func()
}

// FollowActiveLanguage follows the active language on h for the device's preferred languages: sets the host's language
// (and calls changed with it) at once and at every change of the registry, the user's pick or the device's languages that
// changes the locale or the catalogs, until Stop. report gets each distinct report once. Watches end in Stop itself, not
// through ctx's cancellation (D452).
func FollowActiveLanguage(h host.Host, preferred []string, changed func(*Language), report func(MessagesReport), ctx context.Context) (*LanguageFollower, error) {
	ctx, cancel := context.WithCancel(ctx)
	state := HostLanguage(h)
	once := reportOnce(report)
	var mu sync.Mutex
	translations := NoTranslations
	var pick *string
	device := preferred
	var stopped bool
	update := func() {
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			return
		}
		locale := ResolveLocale(pick, device, translations.Locales)
		if current := state.Read(); current != nil && current.Locale == locale && current.Translations == translations {
			return
		}
		language, err := CreateLanguage(locale, translations, once)
		if err != nil {
			// A locale some catalog offers is a canonical tag (ComposeTranslations checked it).
			panic(fmt.Sprintf("surfaces: resolved locale %q: %v", locale, err))
		}
		state.Set(language)
		changed(language)
	}
	update()
	var stops []func()
	var stopsMu sync.Mutex
	addStop := func(stop func()) { stopsMu.Lock(); stops = append(stops, stop); stopsMu.Unlock() }
	fail := func(err error) (*LanguageFollower, error) {
		cancel()
		stopsMu.Lock()
		for _, stop := range stops {
			stop()
		}
		stopsMu.Unlock()
		return nil, err
	}

	registry, err := h.Registry(ctx, registries.Registry[any, any](Translations), host.GetOptions{})
	if err != nil {
		return fail(err)
	}
	mu.Lock()
	translations = registry.Value().(*TranslationIndex)
	mu.Unlock()
	update()
	registryWatch, err := registry.Watch(ctx)
	if err != nil {
		return fail(err)
	}
	addStop(func() { _, _ = registryWatch.Stop() })
	registryWatch.Start(func(_ context.Context, call chord.NativeWatchCall[any]) error {
		if call.Kind == chord.WatchCallValue {
			mu.Lock()
			translations = call.Value.(*TranslationIndex)
			mu.Unlock()
		}
		update()
		return nil
	})

	choice, err := h.Hold(ctx, LocaleChoice, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return fail(err)
	}
	addStop(choice.Release)
	// The pick follows once its hold binds; the hold ending (the follower stopped) before its first value is no error.
	go func() {
		watch, err := choice.Member("pick").Watch(ctx)
		if err != nil {
			return
		}
		addStop(func() { _, _ = watch.Stop() })
		watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
			if call.Kind == chord.WatchCallValue {
				mu.Lock()
				if text, ok := call.Value.(string); ok {
					pick = &text
				} else {
					pick = nil
				}
				mu.Unlock()
			}
			update()
			return nil
		})
	}()
	return &LanguageFollower{
		SetDevice: func(next []string) {
			mu.Lock()
			device = next
			mu.Unlock()
			update()
		},
		Stop: func() {
			mu.Lock()
			stopped = true
			mu.Unlock()
			cancel()
			stopsMu.Lock()
			defer stopsMu.Unlock()
			for _, stop := range stops {
				stop()
			}
		},
	}, nil
}

// reportOnce is report, called once per distinct report (by kind, namespace, locale and key).
func reportOnce(report func(MessagesReport)) func(MessagesReport) {
	var mu sync.Mutex
	seen := map[string]struct{}{}
	return func(each MessagesReport) {
		id := fmt.Sprintf("%s|%s|%s|%s", each.Kind, each.Namespace, each.Locale, each.Key)
		mu.Lock()
		_, dup := seen[id]
		seen[id] = struct{}{}
		mu.Unlock()
		if !dup {
			report(each)
		}
	}
}
