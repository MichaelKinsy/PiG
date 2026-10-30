package documents

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/next/documents/handles.ts
//
// Document handles (slice 6a §1.2): what `self.state` returns, one per attempt and definition, a thin view of its
// object's slot bound to the attempt's seal token and the definition's version.
//
// Go forced differences: `HandleOf<D>` and the generic parameter `T` are designed out (documents are *JsonObject, a
// draft is *delta.Object); a callback that throws returns an error; `keys` is a KeyIterator (Pi's AsyncIterable).

// SingletonHandle is a singleton document: a watchable that always has a value (`initial()` until written, and after
// a retire).
type SingletonHandle interface {
	// Value is the current value.
	Value() (*JsonObject, error)
	// Watch is a watch holding the current value, then one frame per change.
	Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error)
	// Change is one transaction that drafts this document and passes the draft to fn.
	Change(fn func(draft *delta.Object) (any, error), ctx context.Context) (any, error)
	// Retire is one transaction that retires this document: back to `initial()`.
	Retire(ctx context.Context) error
}

// KeyedHandle is keyed documents: many per object, by string key. Not a watchable itself: each key is, through Watch
// and At; a live collection is a singleton holding it.
type KeyedHandle interface {
	// Get is the document's value, nil when absent (persisted and not in memory: loaded on the object's line).
	Get(key string, ctx context.Context) (*JsonObject, error)
	// Keys is the keys of the present documents, in code point order.
	Keys(ctx context.Context) *KeyIterator
	// Watch is a watch of the document at key, nil when absent. It follows the address: a retirement is one frame with
	// `retired: true` and `initial()`, a later creation a root replacement; it ends with the object.
	Watch(key string, ctx context.Context) (chord.WatchHandle[delta.JsonValue], error)
	// At is, in the factory only, the document at key as a watchable (a state member, a derive source, an `on`
	// source), `initial()` while absent, loaded before the object goes live and pinned while this attempt lives.
	At(key string) (*KeySource, error)
	// Change is one transaction that drafts the document at key and passes the draft to fn.
	Change(key string, fn func(draft *delta.Object) (any, error), ctx context.Context) (any, error)
	// Retire is one transaction that retires the document at key (a no-op when absent).
	Retire(key string, ctx context.Context) error
}

// Singleton is a singleton's handle. Reads go to the slot (allowed after the seal); Change and Retire take the line
// under the attempt's token. Exposed as a member or read by a derive, it resolves to the slot itself.
type Singleton struct {
	Slot  *SingletonSlot
	Token *SealToken
	// Version is the version of the definition it was declared with: the slot refuses it once its values are newer (R15).
	Version int
}

// NewSingleton is the handle of slot for an attempt sealed by token, declared at version.
func NewSingleton(slot *SingletonSlot, token *SealToken, version int) *Singleton {
	return &Singleton{Slot: slot, Token: token, Version: version}
}

// Value is the current value, after the handle's version fence.
func (handle *Singleton) Value() (*JsonObject, error) {
	if err := handle.Slot.definition.AssertVersion(handle.Version); err != nil {
		return nil, err
	}
	return handle.Slot.Object()
}

// Watch is a watch of the document.
func (handle *Singleton) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	var err error
	handle.Slot.owner.Do(func() { err = handle.Slot.definition.AssertVersion(handle.Version) })
	if err != nil {
		return nil, err
	}
	var watch *chord.Watch[delta.JsonValue]
	handle.Slot.owner.Do(func() { watch, err = handle.Slot.OpenWatch(ctx) })
	if err != nil {
		return nil, err
	}
	return watch, nil
}

// Change is one transaction that drafts this document and passes the draft to fn.
func (handle *Singleton) Change(fn func(draft *delta.Object) (any, error), ctx context.Context) (any, error) {
	return handle.Slot.owner.Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(handle)
		if err != nil {
			return nil, err
		}
		return fn(draft)
	}, handle.Token, ctx)
}

// Retire is one transaction that retires this document: back to `initial()`.
func (handle *Singleton) Retire(ctx context.Context) error {
	_, err := handle.Slot.owner.Tx(func(tx *Transaction) (any, error) { return nil, tx.Retire(handle) }, handle.Token, ctx)
	return err
}

// KeyDeclarer is who declares an attempt's `at(key)` pins: its factory, which alone may (the attempt).
type KeyDeclarer interface {
	// At pins key of slot for the attempt and returns its source; fails outside the factory.
	At(slot *KeyedSlot, key string) (*KeySource, error)
}

// Keyed is a keyed handle: reads go to the slot; Change, Retire and cold reads take the line under the attempt's token.
type Keyed struct {
	Slot  *KeyedSlot
	Token *SealToken
	// Version is as Singleton.Version.
	Version  int
	declarer KeyDeclarer
}

// NewKeyed is the handle of slot for an attempt sealed by token, declared at version, whose At pins declarer takes.
func NewKeyed(slot *KeyedSlot, token *SealToken, version int, declarer KeyDeclarer) *Keyed {
	return &Keyed{Slot: slot, Token: token, Version: version, declarer: declarer}
}

func (handle *Keyed) access() Access { return Access{Token: handle.Token, Version: handle.Version} }

// Get is the value at key, nil when absent.
func (handle *Keyed) Get(key string, ctx context.Context) (*JsonObject, error) {
	return handle.Slot.Get(key, handle.access(), ctx)
}

// Keys is the keys of the present documents, in code point order.
func (handle *Keyed) Keys(ctx context.Context) *KeyIterator {
	return handle.Slot.Keys(handle.access(), ctx)
}

// Watch is a watch of the document at key, nil when absent.
func (handle *Keyed) Watch(key string, ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	watch, err := handle.Slot.Watch(key, handle.access(), ctx)
	if err != nil || watch == nil {
		return nil, err
	}
	return watch, nil
}

// At is the document at key as a source, pinned by the attempt's factory.
func (handle *Keyed) At(key string) (*KeySource, error) { return handle.declarer.At(handle.Slot, key) }

// Change is one transaction that drafts the document at key and passes the draft to fn.
func (handle *Keyed) Change(key string, fn func(draft *delta.Object) (any, error), ctx context.Context) (any, error) {
	return handle.Slot.owner.Tx(func(tx *Transaction) (any, error) {
		draft, err := tx.Draft(handle, key)
		if err != nil {
			return nil, err
		}
		return fn(draft)
	}, handle.Token, ctx)
}

// Retire is one transaction that retires the document at key.
func (handle *Keyed) Retire(key string, ctx context.Context) error {
	_, err := handle.Slot.owner.Tx(func(tx *Transaction) (any, error) { return nil, tx.Retire(handle, key) }, handle.Token, ctx)
	return err
}

var (
	_ SingletonHandle = (*Singleton)(nil)
	_ KeyedHandle     = (*Keyed)(nil)
)
