package host

import "github.com/MichaelKinsy/PiG/chord/next/data"

// Ports packages/chord/src/next/host/unbound.ts. The unbound index (dependency model §1, §4): the requests a host decided
// cannot bind yet, by the name they wait for. Before the host's first publication every request made here, and every hold
// arriving on its links, waits here (`unpublished`); after it, those its check left unbound (`not_served`, `version`): a
// reference made here, a hold that arrived over a link and was refused for a name. A publication wakes the requests of the
// names it serves; a request leaves when it ends (released, settled, given up, its link or origin closed, revoked) or binds.

// unboundRequest is a request in the index: a reference made here or a hold that arrived over a link. It must be
// comparable (a pointer): the index tells requests apart.
type unboundRequest interface {
	// principal is whom it acts for: revocation ends the requests of a revoked principal. Nil is Pi's undefined.
	principal() data.Principal
	// wake: the name it waits for became usable (or the host published): it runs its check again, leaving the index first.
	wake()
	// end: it is revoked, or its host closes: it ends with err (a hold sends `gone`), leaving the index.
	end(err *data.ChordError)
}

// unboundSet is Pi's `Set<T>`: insertion order, deletion in constant time, and an item deleted and added again goes last.
type unboundSet[T comparable] struct {
	nodes      map[T]*unboundNode[T]
	head, tail *unboundNode[T]
}

type unboundNode[T comparable] struct {
	item       T
	prev, next *unboundNode[T]
}

func (s *unboundSet[T]) add(item T) {
	if _, ok := s.nodes[item]; ok {
		return
	}
	if s.nodes == nil {
		s.nodes = map[T]*unboundNode[T]{}
	}
	node := &unboundNode[T]{item: item, prev: s.tail}
	if s.tail == nil {
		s.head = node
	} else {
		s.tail.next = node
	}
	s.tail = node
	s.nodes[item] = node
}

// delete removes item and reports whether it was a member.
func (s *unboundSet[T]) delete(item T) bool {
	node, ok := s.nodes[item]
	if !ok {
		return false
	}
	delete(s.nodes, item)
	if node.prev == nil {
		s.head = node.next
	} else {
		node.prev.next = node.next
	}
	if node.next == nil {
		s.tail = node.prev
	} else {
		node.next.prev = node.prev
	}
	return true
}

func (s *unboundSet[T]) size() int { return len(s.nodes) }

// items is the members in order, a copy: Pi's `[...set]`.
func (s *unboundSet[T]) items() []T {
	items := make([]T, 0, len(s.nodes))
	for node := s.head; node != nil; node = node.next {
		items = append(items, node.item)
	}
	return items
}

// unboundIndex is a host's unbound requests by name. It is used inside the host's sections.
type unboundIndex struct {
	byName map[string]*unboundSet[unboundRequest]
	// names is the names of byName in the order Pi's Map keeps them: the order requests wake in.
	names unboundSet[string]
}

func newUnboundIndex() *unboundIndex {
	return &unboundIndex{byName: map[string]*unboundSet[unboundRequest]{}}
}

// add indexes request under name (once; again after a wake).
func (i *unboundIndex) add(name string, request unboundRequest) {
	requests := i.byName[name]
	if requests == nil {
		requests = &unboundSet[unboundRequest]{}
		i.byName[name] = requests
		i.names.add(name)
	}
	requests.add(request)
}

// delete: request ended or binds: it leaves the index.
func (i *unboundIndex) delete(name string, request unboundRequest) {
	requests := i.byName[name]
	if requests == nil || !requests.delete(request) {
		return
	}
	if requests.size() == 0 {
		delete(i.byName, name)
		i.names.delete(name)
	}
}

// all is every indexed request (by any name), in order; the index is emptied.
func (i *unboundIndex) takeAll() []unboundRequest {
	var all []unboundRequest
	for _, name := range i.names.items() {
		all = append(all, i.byName[name].items()...)
	}
	i.byName = map[string]*unboundSet[unboundRequest]{}
	i.names = unboundSet[string]{}
	return all
}

// wakeAll is the host's publication: every request indexed (by any name) runs its check again, out of the index first.
func (i *unboundIndex) wakeAll() {
	for _, request := range i.takeAll() {
		request.wake()
	}
}

// revoke ends, with err, every indexed request acting for a principal `revoked` names.
func (i *unboundIndex) revoke(revoked func(principal data.Principal) bool, err *data.ChordError) {
	names := i.names.items()
	sets := make([]*unboundSet[unboundRequest], len(names))
	for at, name := range names {
		sets[at] = i.byName[name]
	}
	for _, requests := range sets {
		// Each set is read when its turn comes: an end before it may have removed requests from it.
		for _, request := range requests.items() {
			if principal := request.principal(); principal != nil && revoked(principal) {
				request.end(err)
			}
		}
	}
}

// close: the host closed: every indexed request ends with err.
func (i *unboundIndex) close(err *data.ChordError) {
	for _, request := range i.takeAll() {
		request.end(err)
	}
}
