package hljs

import (
	"fmt"
	"slices"
)

// Pi's loadAllHighlightLanguages registers every highlight.js language, and most sessions highlight code in a few of them. The full snapshot (grammars_all.json.gz) is therefore not materialized when LoadAllLanguages runs. The first lookup that misses the eager languages inflates it once and indexes it: the byte offset of every grammar node, and each language's registration (name, aliases, root node), which is all registration order, the alias table and Languages need. A language's grammar objects are decoded when it is first looked up, together with the nodes they reach that are not decoded yet; auto-detection over every language decodes the rest.
//
// The snapshot's grammars are almost disjoint (one node is reachable from two languages) and the objects they share with the eager languages are the eager nodes, so a language decoded alone is the object graph decoding the whole snapshot builds, and the registry keeps one object for a shared node as highlight.js does.
//
// The eager languages the full snapshot registers again keep their eager grammar objects: the two are the same grammar (TestDeferredHighlightMatchesEagerLoad highlights every corpus sample of them through both).

// deferredGrammars is the inflated full snapshot.
type deferredGrammars struct {
	decoder snapshotDecoder
	// first is the number of the snapshot's first node; nodeAt[i] is the offset in decoder.data of node first+i.
	first  int
	nodeAt []int
	// roots maps each language not decoded yet to its root node.
	roots map[string]int
}

// indexDeferred inflates and indexes the full snapshot and registers its languages: names in registration order, aliases, and each language's root node in r.deferred. It does nothing before LoadAllLanguages or once indexed.
func (r *Registry) indexDeferred() {
	if r.indexed || !r.allLoaded {
		return
	}
	r.indexed = true
	raw, err := gunzip(allGrammars)
	if err != nil {
		panic("hljs: grammar snapshot: " + err.Error())
	}
	g := &deferredGrammars{decoder: snapshotDecoder{data: raw, keys: map[string]string{}}, roots: map[string]int{}}
	d := &g.decoder
	var registrations []snapshotRegistration
	d.object(func(key []byte) {
		switch string(key) {
		case "firstNode":
			g.first = int(d.number())
		case "nodes":
			d.array(func() {
				g.nodeAt = append(g.nodeAt, d.pos)
				d.skip()
			})
		case "registrations":
			d.array(func() {
				var registration snapshotRegistration
				d.expect('[')
				registration.name = d.string()
				d.expect(',')
				registration.node = int(d.number())
				d.expect(']')
				registrations = append(registrations, registration)
			})
		default:
			d.skip()
		}
	})
	d.space()
	if d.pos != len(d.data) {
		d.fail("data after the snapshot")
	}
	if g.first != len(r.nodes) {
		panic(fmt.Sprintf("hljs: full grammar snapshot starts at node %d, the eager snapshot has %d", g.first, len(r.nodes)))
	}
	r.nodes = slices.Grow(r.nodes, len(g.nodeAt))[:g.first+len(g.nodeAt)]
	for _, registration := range registrations {
		if _, registered := r.languages[registration.name]; !registered {
			if _, pending := g.roots[registration.name]; !pending {
				r.order = append(r.order, registration.name)
			}
			g.roots[registration.name] = registration.node
		}
		d.pos = g.nodeAt[registration.node-g.first]
		for _, prop := range d.node().Props {
			if prop.key == "aliases" {
				for _, alias := range decodeStrings(prop.value) {
					r.aliases[toLowerCase(alias)] = registration.name
				}
			}
		}
	}
	r.deferred = g
}

// lookupDeferred is the part of getLanguage after the eager tables: the name, or the alias's language, from the full snapshot.
func (r *Registry) lookupDeferred(name string) *Mode {
	if language := r.registeredDeferred(name); language != nil {
		return language
	}
	if alias, ok := r.aliases[name]; ok {
		return r.registeredDeferred(alias)
	}
	return nil
}

// registeredDeferred is the registered language of exactly that name, decoded from the full snapshot when it is not decoded yet.
func (r *Registry) registeredDeferred(name string) *Mode {
	if language := r.languages[name]; language != nil {
		return language
	}
	if r.deferred == nil {
		return nil
	}
	if _, ok := r.deferred.roots[name]; !ok {
		return nil
	}
	r.decodeDeferred([]string{name})
	return r.languages[name]
}

// registeredLanguage is highlight.js's languages[name]: the language registered under exactly that name, with no alias lookup.
func (r *Registry) registeredLanguage(name string) *Mode {
	if language := r.languages[name]; language != nil {
		return language
	}
	if !r.allLoaded {
		return nil
	}
	r.indexDeferred()
	return r.registeredDeferred(name)
}

// decodeAllDeferred decodes every language of the full snapshot that is not decoded yet. Nothing is deferred afterwards.
func (r *Registry) decodeAllDeferred() {
	r.indexDeferred()
	if r.deferred == nil {
		return
	}
	names := make([]string, 0, len(r.deferred.roots))
	for _, name := range r.order {
		if _, ok := r.deferred.roots[name]; ok {
			names = append(names, name)
		}
	}
	r.decodeDeferred(names)
}

// decodeDeferred decodes the named languages from the full snapshot and registers them: their root nodes and every node those reach that is not decoded yet, in one batch. The snapshot's inflated bytes are released with its last language.
func (r *Registry) decodeDeferred(names []string) {
	g := r.deferred
	d := &g.decoder
	var ids []int
	var nodes []snapshotNode
	queued := make([]bool, len(g.nodeAt))
	var queue []int
	push := func(id int) {
		if id < g.first || r.nodes[id] != nil || queued[id-g.first] {
			return
		}
		queued[id-g.first] = true
		queue = append(queue, id)
	}
	for _, name := range names {
		push(g.roots[name])
	}
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		d.pos = g.nodeAt[id-g.first]
		node := d.node()
		ids = append(ids, id)
		nodes = append(nodes, node)
		for _, prop := range node.Props {
			forEachRef(prop.value, push)
		}
	}
	r.materialize(ids, nodes)
	for _, name := range names {
		r.setLanguage(name, r.nodes[g.roots[name]].(*Mode))
		delete(g.roots, name)
	}
	if len(g.roots) == 0 {
		r.deferred = nil
	}
}

// forEachRef calls f with the node number of every reference in value, an array's items included.
func forEachRef(value snapshotValue, f func(node int)) {
	if value.kind == valueRef {
		f(int(value.number))
	}
	for _, item := range value.list {
		forEachRef(item, f)
	}
}
