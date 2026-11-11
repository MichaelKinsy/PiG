package transcript

// A viewed conversation's transcript on a client (the TUI; the web has its own): Durable's `ConversationTranscript` over the window the conversation's
// `Chat.view` holds. Every value of the view is merged in, so entries that leave the window stay; older entries and the entries of a gap page in through
// `Chat.entries`. One store per runtime and conversation, kept a while after nobody views it, so switching back finds its pages loaded.
//
// Go forced differences: the loads block (the caller runs them on a goroutine of its own), the listeners run on whichever goroutine changed the pages
// (post to the owner loop), and a store follows its view with one goroutine that its stop ends. Upstream's device-kept transcript (`seed`, `keptWindow`)
// and `loadThrough` belong to the web client and are not here.
//
// pi: apps/p2/src/web/history.ts

import (
	"context"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// Gap is a gap of the transcript: entries between After and UpTo are not loaded.
type Gap = harness.TranscriptGap

// Pages is what a client has of a conversation, and which loads run. A value is never changed: every change is a new one.
type Pages struct {
	// View is the view with the client's whole transcript as Items.
	View *harness.TranscriptView
	// LoadingEarlier is whether the page before the oldest entry is loading.
	LoadingEarlier bool
	// Filling are the gaps being filled, by GapKey.
	Filling map[string]bool
}

// GapKey is a gap's stable key, also its transcript row's.
func GapKey(gap Gap) string { return "gap-" + strconv.FormatInt(int64(gap.After), 10) }

// TranscriptSource is what the store follows: the conversation's view and its entry pages (the current `Chat` reference's). View is compared with ==
// (a store follows another reference's view when it differs): use a comparable value, such as a struct of a pointer.
type TranscriptSource struct {
	View    chord.Watchable[delta.JsonValue]
	Entries harness.EntryLoader
}

// pageSize is the entries per `Chat.entries` page the transcript asks for (pages are also cut at about a megabyte).
const pageSize = 100

// TranscriptStore is a conversation's transcript, following its view while someone subscribes. A value that does not come as operations (the first, a
// reconnect's) is checked first: a transcript holding entries newer than the view's newest is gone from the server (restored from an older backup) and starts
// over. Otherwise the transcript grows: a view past what it has adds a gap.
type TranscriptStore struct {
	mu         sync.Mutex
	source     TranscriptSource
	transcript *harness.ConversationTranscript
	listeners  map[int]func()
	nextID     int
	stop       func()
	value      *Pages
	// latest is the last view merged into transcript, which a load's result is brought up to (see current).
	latest harness.ConversationView
}

// NewTranscriptStore is a store following source once someone subscribes.
func NewTranscriptStore(source TranscriptSource) *TranscriptStore {
	store := &TranscriptStore{source: source, listeners: map[int]func(){}}
	store.transcript = store.fresh()
	return store
}

// Value is what the store has now, nil until the first view arrived.
func (s *TranscriptStore) Value() *Pages {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

// Following is whether someone follows the view.
func (s *TranscriptStore) Following() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stop != nil
}

// Follow follows source from now on (a new reference to the same conversation).
func (s *TranscriptStore) Follow(source TranscriptSource) {
	s.mu.Lock()
	changed := source.View != s.source.View
	s.source = source
	stop := s.stop
	if !changed || stop == nil {
		s.mu.Unlock()
		return
	}
	s.stop = nil
	s.mu.Unlock()
	stop()
	s.mu.Lock()
	s.startLocked()
	s.mu.Unlock()
}

// Subscribe follows the view while subscribed; listener hears every change of Value. It returns the unsubscribe.
func (s *TranscriptStore) Subscribe(listener func()) (unsubscribe func()) {
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.listeners[id] = listener
	if s.stop == nil {
		s.startLocked()
	}
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.listeners, id)
			var stop func()
			if len(s.listeners) == 0 {
				stop, s.stop = s.stop, nil
			}
			s.mu.Unlock()
			if stop != nil {
				stop()
			}
		})
	}
}

// LoadEarlier loads the page before the oldest entry; nothing while one loads or when the transcript starts there.
func (s *TranscriptStore) LoadEarlier() error {
	s.mu.Lock()
	pages, transcript := s.value, s.transcript
	if pages == nil || pages.LoadingEarlier || len(pages.View.Items) == 0 {
		s.mu.Unlock()
		return nil
	}
	if _, earlier := pages.View.Items[0].(harness.TranscriptEarlier); !earlier {
		s.mu.Unlock()
		return nil
	}
	loading := *pages
	loading.LoadingEarlier = true
	listeners := s.setLocked(&loading)
	s.mu.Unlock()
	notify(listeners)
	_, err := transcript.LoadEarlier(context.Background())
	s.mu.Lock()
	if transcript == s.transcript && s.value != nil {
		next := *s.value
		next.LoadingEarlier = false
		if err == nil {
			next.View = s.currentLocked()
		}
		listeners = s.setLocked(&next)
	} else {
		listeners = nil
	}
	s.mu.Unlock()
	notify(listeners)
	return err
}

// currentLocked is the transcript's view now: the pages a load added and every view merged so far. A load returns the view as of its own end, and the goroutine
// that follows the view may merge a newer one before the result reaches the store (Pi's store is one event loop, where nothing comes in between): the older view
// set over the newer one would hide the entries of the latter until another view arrived, and a finished answer is the last one. Merging the latest view again
// adds nothing and returns the transcript's current items.
func (s *TranscriptStore) currentLocked() *harness.TranscriptView {
	return s.transcript.Merge(s.latest)
}

// FillGap loads the next page of gap; nothing while it loads.
func (s *TranscriptStore) FillGap(gap Gap) error {
	key := GapKey(gap)
	s.mu.Lock()
	pages, transcript := s.value, s.transcript
	if pages == nil || pages.Filling[key] {
		s.mu.Unlock()
		return nil
	}
	loading := *pages
	loading.Filling = withFilling(pages.Filling, key, true)
	listeners := s.setLocked(&loading)
	s.mu.Unlock()
	notify(listeners)
	_, err := transcript.FillGap(context.Background(), gap)
	s.mu.Lock()
	if transcript == s.transcript && s.value != nil {
		next := *s.value
		next.Filling = withFilling(s.value.Filling, key, false)
		if err == nil {
			next.View = s.currentLocked()
		}
		listeners = s.setLocked(&next)
	} else {
		listeners = nil
	}
	s.mu.Unlock()
	notify(listeners)
	return err
}

func withFilling(filling map[string]bool, key string, on bool) map[string]bool {
	next := make(map[string]bool, len(filling)+1)
	for each := range filling {
		if each != key {
			next[each] = true
		}
	}
	if on {
		next[key] = true
	}
	return next
}

func (s *TranscriptStore) fresh() *harness.ConversationTranscript {
	size := pageSize
	transcript, err := harness.NewConversationTranscript(func(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		s.mu.Lock()
		load := s.source.Entries
		s.mu.Unlock()
		return load(ctx, query, limit)
	}, &harness.ConversationTranscriptOptions{PageSize: &size})
	if err != nil {
		panic(err)
	}
	return transcript
}

// startLocked follows the view on a goroutine of its own, until the stop it stores.
func (s *TranscriptStore) startLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	var done sync.WaitGroup
	done.Add(1)
	var handleMu sync.Mutex
	var handle chord.WatchHandle[delta.JsonValue]
	s.stop = func() {
		cancel()
		handleMu.Lock()
		current := handle
		handleMu.Unlock()
		if current != nil {
			_, _ = current.Stop()
		}
		done.Wait()
	}
	source := s.source.View
	go func() {
		defer done.Done()
		watch, err := source.Watch(ctx)
		if err != nil {
			return
		}
		handleMu.Lock()
		handle = watch
		handleMu.Unlock()
		if ctx.Err() != nil {
			_, _ = watch.Stop()
			return
		}
		var last delta.JsonValue
		apply := func(view delta.JsonValue, ops bool) {
			if view == nil || surfaces.SameValue(view, last) {
				last = view
				return
			}
			last = view
			s.apply(ctx, view, ops)
		}
		apply(watch.Value(), false)
		// The stop (a switch of conversation) may reach the watch between the check above and its start: that only means the follow ended.
		chord.StartUnlessEnded(func() {
			watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
				if call.Kind == chord.WatchCallValue && ctx.Err() == nil {
					// A change as operations, unless the whole value was replaced (`r`: the first, a reconnect's).
					replaced := len(call.Ops) == 1 && delta.Verb(call.Ops[0]) == "r"
					apply(call.Value, !replaced)
				}
				return nil
			})
		})
	}()
}

// apply merges one value of the view; ops says it came as a change of the last one, not whole.
func (s *TranscriptStore) apply(ctx context.Context, value delta.JsonValue, ops bool) {
	view, err := durable.FromJsonValue[harness.ConversationView](value)
	if err != nil {
		return
	}
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	if !ops && !s.continues(view) {
		s.transcript = s.fresh()
		s.value = nil
	}
	s.latest = view
	merged := s.transcript.Merge(view)
	var listeners []func()
	if s.value == nil || merged != s.value.View {
		next := Pages{View: merged, Filling: map[string]bool{}}
		if s.value != nil {
			next.LoadingEarlier, next.Filling = s.value.LoadingEarlier, s.value.Filling
		}
		listeners = s.setLocked(&next)
	}
	s.mu.Unlock()
	notify(listeners)
}

// continues is whether view, not received as operations, may be merged into what is held (see the type).
func (s *TranscriptStore) continues(view harness.ConversationView) bool {
	held, ok := s.transcript.Newest()
	if !ok {
		return true
	}
	var newest durable.EntryId
	switch {
	case len(view.Entries) > 0:
		newest = view.Entries[len(view.Entries)-1].Id
	case view.Before != nil:
		newest = *view.Before
	default:
		return false
	}
	return held <= newest
}

// setLocked is the new value and the listeners to tell, after the lock is released.
func (s *TranscriptStore) setLocked(value *Pages) []func() {
	s.value = value
	listeners := make([]func(), 0, len(s.listeners))
	for _, listener := range s.listeners {
		listeners = append(listeners, listener)
	}
	return listeners
}

func notify(listeners []func()) {
	for _, listener := range listeners {
		listener()
	}
}

// retained is how many conversations keep their pages while nobody views them.
const retained = 4

// Stores are a runtime's transcript stores by conversation (`session#conversation`), the most recently used last.
type Stores struct {
	mu    sync.Mutex
	order []string
	byKey map[string]*TranscriptStore
}

var (
	storesMu sync.Mutex
	stores   = map[any]*Stores{}
)

// StoresOf is the stores of owner (a runtime, a page): they go with it.
func StoresOf[T any](owner *T) *Stores {
	key := weak.Make(owner)
	storesMu.Lock()
	defer storesMu.Unlock()
	if found, ok := stores[key]; ok {
		return found
	}
	created := &Stores{byKey: map[string]*TranscriptStore{}}
	stores[key] = created
	runtime.AddCleanup(owner, func(key any) {
		storesMu.Lock()
		delete(stores, key)
		storesMu.Unlock()
	}, any(key))
	return created
}

// Of is the one transcript store of conversation key, following source (its current reference's).
func (s *Stores) Of(key string, source TranscriptSource) *TranscriptStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, found := s.byKey[key]
	if found {
		s.order = slices.DeleteFunc(s.order, func(each string) bool { return each == key })
		store.Follow(source)
	} else {
		store = NewTranscriptStore(source)
		s.byKey[key] = store
	}
	s.order = append(s.order, key)
	for _, other := range slices.Clone(s.order) {
		if len(s.byKey) <= retained {
			break
		}
		if candidate := s.byKey[other]; candidate != store && !candidate.Following() {
			delete(s.byKey, other)
			s.order = slices.DeleteFunc(s.order, func(each string) bool { return each == other })
		}
	}
	return store
}
