package surfaces

import (
	"reflect"
	"runtime"
	"sync"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/surfaces/page-state.ts. Values a client host keeps for its UI by key, beside its plugins,
// so a component of a reloaded plugin finds what it had. Same-host values of any kind, never on the wire; gone with the
// host (D451: the store of a host is dropped by a runtime cleanup when the host is collected, upstream's WeakMap).

// PageState is one key's value in a host's page state. Its functions are bound values.
type PageState[T any] struct {
	state *keptState
}

// Read is the value now.
func (p PageState[T]) Read() T {
	v, _ := p.state.read().(T)
	return v
}

// Set sets the value; a new value (by identity: see sameValue) calls every subscriber.
func (p PageState[T]) Set(value T) { p.state.set(value) }

// Subscribe calls changed after every Set that changed the value, until the returned unsubscribe.
func (p PageState[T]) Subscribe(changed func()) (unsubscribe func()) {
	return p.state.subscribe(changed)
}

// keptState is a value with its subscribers; PageState[T] is its typed view (a key is the app's: whoever names it names
// its type too).
type keptState struct {
	mu          sync.Mutex
	value       any
	next        int
	subscribers []subscriber
}

type subscriber struct {
	id      int
	changed func()
}

func (k *keptState) read() any {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.value
}

func (k *keptState) set(next any) {
	k.mu.Lock()
	if sameValue(k.value, next) {
		k.mu.Unlock()
		return
	}
	k.value = next
	subscribers := append([]subscriber(nil), k.subscribers...)
	k.mu.Unlock()
	for _, each := range subscribers {
		each.changed()
	}
}

func (k *keptState) subscribe(changed func()) func() {
	k.mu.Lock()
	defer k.mu.Unlock()
	id := k.next
	k.next++
	k.subscribers = append(k.subscribers, subscriber{id, changed})
	return func() {
		k.mu.Lock()
		defer k.mu.Unlock()
		for i, each := range k.subscribers {
			if each.id == id {
				k.subscribers = append(k.subscribers[:i:i], k.subscribers[i+1:]...)
				return
			}
		}
	}
}

// sameValue is JavaScript's `===` on two kept values: equal comparable values are the same, and a map, slice, function,
// channel or pointer is the same when it is the one (slices: the same array and length). Other values (structs holding
// such, arrays of them) are never the same, so a set of one notifies.
func sameValue(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb {
		return false
	}
	if ta.Comparable() {
		return comparableEqual(a, b)
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	switch ta.Kind() {
	case reflect.Map, reflect.Func, reflect.Chan:
		return va.Pointer() == vb.Pointer()
	case reflect.Slice:
		return va.Pointer() == vb.Pointer() && va.Len() == vb.Len()
	}
	return false
}

// comparableEqual is a == b for two values of one comparable type; a struct or array holding an interface that holds an
// uncomparable value (a slice) is no `===` of anything: not the same.
func comparableEqual(a, b any) bool {
	// Comparable is true only when == on a and anything else cannot panic (the dynamic type and the interfaces inside it).
	return reflect.ValueOf(a).Comparable() && a == b
}

// pageStates are each client host's page state by key, kept as long as the host.
var pageStates sync.Map // weak.Pointer[byte] to the host's struct (it keeps nothing alive, and never equals a later host's) -> *hostStates

type hostStates struct {
	mu     sync.Mutex
	states map[string]*keptState
}

// pageStateEntry is what the cleanup of a host removes: its key and the entry it was registered with.
type pageStateEntry struct {
	key    weak.Pointer[byte]
	states *hostStates
}

// PageStateOf is the page state of h at key: what was kept there, or initial when the key is new.
func PageStateOf[T any](h host.Host, key string, initial T) PageState[T] {
	// The host is a pointer to its own struct, as CreateHost returns it.
	pointer := (*byte)(reflect.ValueOf(h).UnsafePointer())
	owner := weak.Make(pointer)
	loaded, found := pageStates.Load(owner)
	if !found {
		var stored bool
		fresh := &hostStates{states: map[string]*keptState{}}
		loaded, stored = pageStates.LoadOrStore(owner, fresh)
		if !stored {
			// The entry goes with its host: the cleanup runs after the host is unreachable, and removes only the entry it was
			// made with (D451).
			runtime.AddCleanup(pointer, func(entry pageStateEntry) { pageStates.CompareAndDelete(entry.key, entry.states) }, pageStateEntry{owner, fresh})
		}
	}
	states := loaded.(*hostStates)
	states.mu.Lock()
	defer states.mu.Unlock()
	state, has := states.states[key]
	if !has {
		state = &keptState{value: initial}
		states.states[key] = state
	}
	return PageState[T]{state}
}

// SameValue is JavaScript's `===` on two values: the identity pageState and Optimistic compare by (see sameValue).
func SameValue(a, b any) bool { return sameValue(a, b) }

// languageKey is the page state key of a host's language.
const languageKey = "chord.language"

// HostLanguage is h's active language, kept as its page state: nil until a follower sets it (then `t` renders the first
// catalog). Ports packages/chord/src/surfaces/active-language.ts `hostLanguage`.
func HostLanguage(h host.Host) PageState[*Language] {
	return PageStateOf[*Language](h, languageKey, nil)
}
