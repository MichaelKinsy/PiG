package surfaces

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

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
// changes the locale or the catalogs, until Stop. report gets each distinct report once. Stop ends its watches; so does the
// cancellation of ctx, which the follower's own pick watch may meet before it starts (it then follows nothing).
func FollowActiveLanguage(h host.Host, preferred []string, changed func(*Language), report func(MessagesReport), ctx context.Context) (*LanguageFollower, error) {
	ctx, cancel := context.WithCancel(ctx)
	state := HostLanguage(h)
	once := reportOnce(report)
	var mu sync.Mutex
	translations := NoTranslations
	var pick *string
	device := preferred
	// gate orders Stop against the pick watch's start: a watch is started only while the follower runs.
	var gate sync.Mutex
	var stopped atomic.Bool
	var current *Language
	var out outbox
	update := func() {
		mu.Lock()
		if stopped.Load() {
			mu.Unlock()
			return
		}
		locale := ResolveLocale(pick, device, translations.Locales)
		if current != nil && current.Locale == locale && current.Translations == translations {
			mu.Unlock()
			return
		}
		language, err := CreateLanguage(locale, translations, once)
		if err != nil {
			mu.Unlock()
			// A locale some catalog offers is a canonical tag (ComposeTranslations checked it).
			panic(fmt.Sprintf("surfaces: resolved locale %q: %v", locale, err))
		}
		current = language
		mu.Unlock()
		out.run(func() {
			if stopped.Load() {
				return
			}
			state.Set(language)
			changed(language)
		})
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
	// The pick follows once its hold binds, from the microtask that opens its watch in the turn that binds the hold (Pi:
	// `choice.pick.watch(ctx).then(start, () => {})`, not awaited): a change of the pick after the bind is a frame of the
	// watch. The hold ending (the follower stopped) before its first value is no error.
	choice.Member("pick").WatchThen(ctx, func(watch chord.WatchHandle[delta.JsonValue], err error) {
		if err != nil {
			return
		}
		// Stop marks the follower stopped under gate before it ends the watches: one started here is stopped by it, and a
		// follower stopped already starts nothing (Pi: `watch(ctx).then(start, () => {})`).
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
		})
	})
	return &LanguageFollower{
		SetDevice: func(next []string) {
			mu.Lock()
			device = next
			mu.Unlock()
			update()
		},
		Stop: func() {
			gate.Lock()
			stopped.Store(true)
			gate.Unlock()
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
