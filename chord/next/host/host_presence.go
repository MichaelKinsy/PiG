package host

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/presence.ts. Presence (§19): `host.references(Type)`, a JSON watchable of the
// references to objects of one type on this host, one entry per reference with the object's params, the reference's
// principal (remote types) and its incarnation.
//
// Forced Go differences. `Presence<C, P>`'s type parameters are type-level (owner class a): Params is the object's
// params as the host holds them (JSON). Pi's `Source` subclass is a struct embedding state.Source (state/doc.go). Its
// entries are the JSON objects the list publishes, compared by identity as Pi's are (a `Set<Presence>`), and Pi's
// `Map<Holder, Presence>` is an insertion-ordered map.

// Presence is one reference to an object of a type: the object's params, the principal it acts as (for a remote type, nil
// otherwise), and its incarnation: the host that made it as its caller sees it (a hold's caller over a link; two tabs are
// two incarnations), the word `ctx.incarnation` uses. The list `host.references` publishes holds each as the JSON object
// {params, principal?, incarnation}.
type Presence struct {
	Params      any
	Principal   data.Principal
	Incarnation string
}

// holderEntry is one entry of a references' map: the holder's entry in the order the holders were first recorded.
type holderEntry struct {
	holder     holder
	entry      *delta.JsonObject
	prev, next *holderEntry
}

// references is the references of one type, made by the host for its first `host.references(Type)` and kept from then
// on: one entry per reference, added when it binds and removed when it ends, in the batch where that happens (derives
// over it recompute there). While watched, a batch's changes are folded into the published list once, with one splice op
// per removal and one for the additions; otherwise the list is built on its next read.
type references struct {
	state.Source
	// entries is Pi's `Map<Holder, Presence>`, in insertion order.
	byHolder    map[holder]*holderEntry
	first, last *holderEntry
	// list is the list as last published or read; unset once it changed while nobody watched it.
	list    []any
	hasList bool
	// added is the entries added since list, in order (only while it is set).
	added []*delta.JsonObject
	// removed is the entries of list removed since.
	removed map[*delta.JsonObject]struct{}
	// ops are the ops folding list forward, for the current batch's frame.
	ops []delta.Op
}

var _ state.SourceImpl = (*references)(nil)

func newReferences(batch *state.Batch) *references {
	r := &references{byHolder: map[holder]*holderEntry{}, removed: map[*delta.JsonObject]struct{}{}}
	r.Init(batch, r)
	return r
}

// Value is the current list of entries (JSON, never failing).
func (r *references) Value() (delta.JsonValue, error) {
	r.fold()
	return r.list, nil
}

// add: holder references object, added in the batch running now or the next one.
func (r *references) add(object *chordObject, holder holder, ctx context.Context) {
	// The adoption has no error to return: the commit's own failures are the batch's to report.
	_ = r.Batch().Within(func() error {
		entry := r.record(object, holder)
		if r.follows() {
			r.added = append(r.added, entry)
		}
		r.Changed()
		return nil
	}, ctx)
}

// remove: holders of object ended, removed in one batch.
func (r *references) remove(holders []holder, ctx context.Context) {
	_ = r.Batch().Within(func() error {
		follows := r.follows()
		for _, holder := range holders {
			node := r.byHolder[holder]
			if node == nil {
				continue
			}
			entry := node.entry
			r.unlink(node)
			if !follows {
				continue
			}
			added := indexOfEntry(r.added, entry)
			if added == -1 {
				r.removed[entry] = struct{}{}
			} else {
				r.added = append(r.added[:added], r.added[added+1:]...)
			}
		}
		r.Changed()
		return nil
	}, ctx)
}

// record records holder without a batch (the list being made) and returns its entry.
func (r *references) record(object *chordObject, holder holder) *delta.JsonObject {
	from := holder.caller()
	entry := delta.NewJsonObject(3)
	entry.Set("params", object.params)
	if object.served.contract.Kind == contracts.KindRemote && from.principal != nil {
		entry.Set("principal", from.principal)
	}
	entry.Set("incarnation", from.incarnation)
	if node := r.byHolder[holder]; node != nil {
		// Map.set of a key already there keeps its place.
		node.entry = entry
		return entry
	}
	node := &holderEntry{holder: holder, entry: entry, prev: r.last}
	if r.last == nil {
		r.first = node
	} else {
		r.last.next = node
	}
	r.last = node
	r.byHolder[holder] = node
	return entry
}

func (r *references) unlink(node *holderEntry) {
	if node.prev == nil {
		r.first = node.next
	} else {
		node.prev.next = node.next
	}
	if node.next == nil {
		r.last = node.prev
	} else {
		node.next.prev = node.prev
	}
	delete(r.byHolder, node.holder)
}

func indexOfEntry(entries []*delta.JsonObject, entry *delta.JsonObject) int {
	for i, each := range entries {
		if each == entry {
			return i
		}
	}
	return -1
}

// Deliver delivers the batch's frame: listed once per change, the batch's first delivery carries all of them.
func (r *references) Deliver(ctx context.Context) error {
	value, _ := r.Value()
	ops := r.ops
	r.ops = nil
	if len(ops) > 0 {
		r.Publish(value, ops, false, ctx)
	}
	return nil
}

// follows reports whether a change is folded into the published list: while watched (a watch read the list first).
// Otherwise the list is dropped and built on its next read.
func (r *references) follows() bool {
	if r.Watched() && r.hasList {
		return true
	}
	r.list, r.hasList = nil, false
	return false
}

// fold folds the changes since list into a new list, once: a removal is one splice op at its index, the additions one
// splice op at the end. It builds the list from the entries when there is none.
func (r *references) fold() {
	if !r.hasList {
		list := []any{}
		for node := r.first; node != nil; node = node.next {
			list = append(list, node.entry)
		}
		r.list, r.hasList = list, true
		r.added = nil
		clear(r.removed)
		r.ops = nil
		return
	}
	if len(r.added) == 0 && len(r.removed) == 0 {
		return
	}
	list := r.list
	if len(r.removed) > 0 {
		kept := make([]any, 0, len(list))
		for _, item := range list {
			entry, _ := item.(*delta.JsonObject)
			if _, removed := r.removed[entry]; removed {
				r.ops = append(r.ops, delta.Op{"p", []any{}, len(kept), 1, []any{}})
			} else {
				kept = append(kept, item)
			}
		}
		clear(r.removed)
		list = kept
	}
	if len(r.added) > 0 {
		items := make([]any, len(r.added))
		for i, entry := range r.added {
			items[i] = entry
		}
		r.ops = append(r.ops, delta.Op{"p", []any{}, len(list), 0, items})
		list = append(append(make([]any, 0, len(list)+len(items)), list...), items...)
		r.added = nil
	}
	r.list = list
}
