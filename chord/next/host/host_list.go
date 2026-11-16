package host

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/src/host/list.ts
//
// A list as a JSON source, read as a reader reads (`Reading`): each `read` opens every page again under its bookmark, for
// the new target, and the list switches in one frame once each has run; bookmarks of another order start it again with a
// first page.
//
// Go forms: Pi's `#loading` promise chain is `tail`, a channel closed when the last queued `more` ended; `#switched` is a
// channel closed (and replaced) on every `show`; Pi's `awaitWithContext` is `chordctx.Receive` (it parks a method handler where it waits); Pi's `void this.read`
// in `#rebound` (list.ts:126) opens at once (`Open`) and runs its load as a task of the host's loop (`spawn`). A tiling's owner is the list (Pi's object literal over the
// tiling): it is given the tiling at each call.

// ListOptions is what `self.list` takes besides its name and sources.
type ListOptions struct {
	// First is the rows of a page: what the first page and each `more` load. A positive integer.
	First int
}

// ListSource is the one source of `self.List`: a named query and its arguments (a JSON value, or a watchable of them).
type ListSource struct {
	Query contracts.NamedQuery[delta.JsonValue, delta.JsonValue]
	Args  any
}

// ListHandle is what `self.List` returns (Pi's `ListHandle`, attempt.ts): the list's value for a state member, and `More` for
// a method.
type ListHandle struct {
	value chord.Watchable[delta.JsonValue]
	more  func(ctx context.Context) error
	// list is the list a live attempt made; nil for a recording's handle.
	list *list
}

// Value is the loaded rows in order, and whether rows may follow them: `{ rows, moreAfter }`.
func (h *ListHandle) Value() chord.Watchable[delta.JsonValue] { return h.value }

// More loads one more page behind the last one; it returns once its rows are in Value, or with ctx's error.
func (h *ListHandle) More(ctx context.Context) error { return h.more(ctx) }

// ListMemory is what an object keeps of a list across its attempts (a reload, a restart): the bookmarks, whether the last
// page was open at its end, and the order they are positions of. It goes with the object. Go form: a lock guards it (a
// tiling's read starts on a goroutine of its own while an adoption writes it; Pi's one thread).
type ListMemory struct {
	mu        sync.Mutex
	signature string
	bookmarks []store.Position
	open      bool
}

// opening is the bookmarks and the openness a tiling of `signature` starts with: the memory's when they are positions of its
// order, otherwise a first page alone, open.
func (m *ListMemory) opening(signature string) ([]store.Position, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.signature == signature {
		return m.bookmarks, m.open
	}
	return nil, true
}

// keep is what the current tiling shows: its order, its bookmarks and whether its last page is open.
func (m *ListMemory) keep(signature string, bookmarks []store.Position, open bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signature, m.bookmarks, m.open = signature, bookmarks, open
}

// list is a list as a JSON source.
type list struct {
	state.FramedSource
	site   store.ReadSite
	first  int
	memory *ListMemory
	rows   store.DecodedRows

	// spawn runs a task of the host's loop (the reader site's Go): Pi's `void` calls and awaited chains of the list.
	spawn func(task func())

	mu sync.Mutex
	// current is the tiling its value is of, and next the one that replaces it once every page of it ran.
	current *tiling
	next    *tiling
	// tail is closed when the last queued `more` ended: the `more` calls in turn, one at a time per list.
	tail chan struct{}
	// switched is closed when the list has another current tiling, or none (a switch, a failure, its end).
	switched chan struct{}
}

var _ tilingOwner = (*list)(nil)
var _ Reading = (*list)(nil)

// closedChannel is a channel that is closed: the tail of a list no `more` is queued on.
func closedChannel() chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func newList(site store.ReadSite, owner state.FramedOwner, first int, memory *ListMemory, spawn func(task func())) *list {
	l := &list{site: site, first: first, memory: memory, spawn: spawn, tail: closedChannel(), switched: make(chan struct{})}
	l.Init(site.Batch, owner)
	return l
}

// Open is the part of Pi's `read` before its first await: it reads `target` from now on, its pages open under the bookmarks
// the list has (when they are positions of its order: another order starts again with a first page), and the previous
// pending tiling closes. It returns the load: the list shows its previous rows until every page of the new tiling ran,
// then switches in one frame; the load returns once every page's load settled. Called inside a section; the load is not.
func (l *list) Open(target store.LiveTarget, ctx context.Context) (load func()) {
	load = func() {}
	// `if (this.detached) return Promise.resolve(); this.#next?.close(); ... this.#next = tiling` (list.ts:57-61): the check of
	// `detached` and the installation of the tiling are one turn.
	l.site.Batch.CheckAct(func() func() {
		if l.Detached() {
			return nil
		}
		return func() {
			tiling := newTiling(l.site, target, l.first, l, ctx, l.spawn)
			l.mu.Lock()
			previous := l.next
			l.next = tiling
			l.mu.Unlock()
			if previous != nil {
				previous.close()
			}
			load = tiling.openPages(l.memory.opening(tiling.signature()))
		}
	})
	return load
}

// Fail is: what it should read cannot be read (its arguments threw, its provider refused them): a failed member with its
// last rows. Its pages close: neither the previous target's pages nor a pending tiling may publish or heal the failure; the
// next `read` does.
func (l *list) Fail(err error, ctx context.Context) {
	if l.Detached() {
		return
	}
	l.closeAll()
	_ = l.site.Batch.Within(func() error { l.FailWith(err); return nil }, ctx)
}

// Detach is: its attempt ended: no commit runs its pages again, reads throw `err`, its watches end.
func (l *list) Detach(err error) {
	l.closeAll()
	l.End(err)
}

// more loads one more page of `first` rows behind the last one; calls run one at a time. A call waits while the list is
// about to show other pages, and loads behind those: while a tiling is pending (new arguments, a changed binding; its
// pages may not have run yet: a table they read is held) and while the provider restarts (its query is closed, its
// successor's not read yet: nothing is read with the old code's statement). The caller's wait ends with `ctx`; the load
// goes on.
func (l *list) more(ctx context.Context) error {
	l.mu.Lock()
	previous := l.tail
	finished := make(chan struct{})
	l.tail = finished
	l.mu.Unlock()
	loaded := make(chan struct{})
	// list.ts:92-99: `this.#loading.then(async () => { while (...) await this.#switched.promise; await this.#current?.more() })`
	// is a task of the loop that waits parked for the `more` before it and for a switch.
	l.spawn(func() {
		defer close(finished)
		defer close(loaded)
		parkedOn(previous)
		for {
			l.mu.Lock()
			current, pending, switched := l.current, l.next, l.switched
			l.mu.Unlock()
			if pending == nil && (current == nil || !current.retired()) {
				if current != nil {
					current.more()
				}
				return
			}
			parkedOn(switched)
		}
	})
	_, err := chordctx.Receive(ctx, loaded)
	return err
}

func (l *list) closeAll() {
	l.mu.Lock()
	current, next := l.current, l.next
	l.next = nil
	l.mu.Unlock()
	if current != nil {
		current.close()
	}
	if next != nil {
		next.close()
	}
	l.show(nil)
}

// show: `tiling` is the current one from now on; the `more` calls waiting for another one go on.
func (l *list) show(tiling *tiling) {
	l.mu.Lock()
	l.current = tiling
	close(l.switched)
	l.switched = make(chan struct{})
	l.mu.Unlock()
}

// rebound: a binding of `tiling`'s query changed: its target is read again as one new tiling under the list's bookmarks,
// and the list switches in one frame once every page of it ran, as for new arguments. A current tiling that a pending one is
// about to replace is left alone: the pending one reads again for itself.
func (l *list) rebound(tiling *tiling) {
	// Pi's `#rebound` checks which tiling is pending or current and `void this.read(...)`s in one turn: a `read` for new
	// arguments that ran between would be replaced by this older target. The decision is made in the section that opens.
	var load func()
	l.site.Batch.CheckAct(func() func() {
		l.mu.Lock()
		pending := l.next
		again := tiling == pending || (tiling == l.current && pending == nil)
		l.mu.Unlock()
		if !again {
			return nil
		}
		// The read opens now, as Pi's `void this.read` runs up to its first await when it is called: the new tiling is the
		// pending one at once. A failed page fails the list, as any read's failure does; nothing awaits the loads.
		return func() { load = l.Open(tiling.target, tiling.context()) }
	})
	if load != nil {
		l.spawn(load)
	}
}

// adopted: a run of `tiling` was adopted, inside a batch. A pending tiling becomes the current one once every page of it
// ran: never a first page alone, never rows of two targets. A failed page of it fails the list at once, and the previous
// target's pages close: they may not heal that failure. The current tiling's run makes the list's value, and its bookmarks
// are what the object keeps.
func (l *list) adopted(tiling *tiling) {
	if l.Detached() {
		return
	}
	l.mu.Lock()
	switch tiling {
	case l.next:
		if tiling.failure() == nil && !tiling.complete() {
			l.mu.Unlock()
			return
		}
		previous := l.current
		l.next = nil
		l.mu.Unlock()
		if previous != nil {
			previous.close()
		}
		l.show(tiling)
		l.rows.Reset()
	case l.current:
		l.mu.Unlock()
	default:
		l.mu.Unlock()
		return
	}
	if failure := tiling.failure(); failure != nil {
		l.FailWith(failure)
		return
	}
	// A page of the current tiling that has not run (its table was held when the list opened): no value yet.
	if !l.Ready() && !tiling.complete() {
		return
	}
	l.memory.keep(tiling.signature(), tiling.bookmarks(), tiling.open())
	rows, err := l.rows.Decode(tiling.rows(), tiling.target, tiling.context())
	if err != nil {
		l.FailWith(err)
		return
	}
	l.Set(delta.JsonObjectOf("rows", rows, "moreAfter", tiling.moreAfter()))
}
