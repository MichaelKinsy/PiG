package tui

import (
	"reflect"
	"slices"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// pig additive (D91): a TuiSurface retains the main region per component,
// so a frame costs what changed, not the transcript's length. A component
// that embeds invalidatable reports each Invalidate to the surface that
// draws it, and a container each change of its children. A frame rebuilds
// only the components it was told about and the few it polls, splices the
// children a container changed into the region, and walks the whole
// document only when the width, theme or hooks changed or a component is
// keyed by its position.

// surfaceMain is the retained main region of a TuiSurface.
type surfaceMain struct {
	// ids holds the state of each mounted component, keyed by component,
	// and containers that of each mounted container. An unmounted one is
	// forgotten, so both stay bounded.
	ids        map[Component]*surfaceComponent
	containers map[*Container]*surfaceContainer
	nextID     int
	// sink receives the invalidations of the components the surface draws,
	// and watch maps each to the component or container it changes.
	sink    *surfaceSink
	watch   map[*invalidatable]surfaceWatch
	drained []*invalidatable
	// frame numbers the walks, whole or of a container's changed children,
	// and pass the frames.
	frame, pass uint64
	// mainGen numbers the inputs every main node is built from: the width,
	// the theme and the hooks. A component built under an older one is
	// rebuilt, and walkedGen is the one the last whole walk ran under.
	mainGen, walkedGen uint64
	mainWidth          int
	mainTheme          *Theme
	// ver numbers the main entries built, so an entry that was not rebuilt
	// is known equal without comparing its node.
	ver uint64
	// walkAlways records that the last walk keyed a component by its
	// position, or met a container another surface draws, so every frame
	// walks the whole document.
	walkAlways bool
	// live holds the mounted components rebuilt every frame, changed those
	// rebuilt this frame, restructured the containers whose children
	// changed, and streaming the message the Streaming hook reported last.
	live         []*surfaceComponent
	changed      []*surfaceComponent
	restructured []*surfaceContainer
	streaming    *AssistantMessageComponent
	// leaves and mounted count the components and containers the last
	// whole walk mounted, visited the components and containers the last
	// walk of any kind visited, and walks the whole walks.
	leaves, mounted, visited int
	walks                    uint64
	// splicing walks a container's changed children: a component mounted
	// outside them is a second mount, which aborts the splice.
	splicing, spliceAborted bool
	// unmounted collects the components and containers a splice takes out.
	unmounted  []*surfaceComponent
	unmountedC []*surfaceContainer
	edits      []surfaceEdit
	main       []surfaceEntry
	mainNext   []surfaceEntry
	window     []surfaceEntry
	// The splice buffers hold a container's changed children, their
	// entries, states and cumulative entry counts.
	spliceKids    []Component
	spliceEntries []surfaceEntry
	spliceStates  []*surfaceComponent
	spliceSubs    []*surfaceContainer
	spliceEnds    []int
}

func newSurfaceMain() surfaceMain {
	return surfaceMain{
		ids:        map[Component]*surfaceComponent{},
		containers: map[*Container]*surfaceContainer{},
		sink:       &surfaceSink{},
		watch:      map[*invalidatable]surfaceWatch{},
		mainGen:    1,
	}
}

// surfaceComponent is the retained state of one mounted component.
type surfaceComponent struct {
	id   string
	comp Component
	// parent is the container the component is mounted in, nil for a
	// document that is a component, and index its position there.
	parent  *surfaceContainer
	index   int
	mounted bool
	// seen is the walk that last mounted the component.
	seen uint64
	// inv is the component's invalidation, which the surface receives, or
	// nil when the surface polls it. dep is a tool card's result component's.
	inv, dep *invalidatable
	// live rebuilds the component every frame: its changes do not all
	// arrive as invalidations. dirty rebuilds it on the next frame, changed
	// records that it is in the frame's changed list, and listed that it is
	// in the live list.
	live, dirty, changed, listed bool
	// gen is the mainGen entries were built under, or zero, and pass the
	// frame that built them.
	gen, pass uint64
	entries   []surfaceEntry
	// partIDs are the ids of an assistant message's parts.
	partIDs []string
	// rendered is a copy of the lines the component last rendered, and lines
	// the node built from them. Unchanged lines reuse the node, which also
	// makes the diff's comparison cheap.
	rendered []string
	lines    frontend.Lines
}

// surfaceContainer is the retained state of one mounted container.
type surfaceContainer struct {
	c       *Container
	parent  *surfaceContainer
	index   int
	mounted bool
	seen    uint64
	inv     *invalidatable
	// changed records that it is in the frame's restructured list.
	changed bool
	// kids are its children at the last walk, states and subs their states
	// as a component or a container (nil for a child keyed by position),
	// and ends[i] the number of main entries kids[:i+1] hold.
	kids   []Component
	states []*surfaceComponent
	subs   []*surfaceContainer
	ends   []int
	// The spare buffers hold the next walk's.
	spareKids   []Component
	spareStates []*surfaceComponent
	spareSubs   []*surfaceContainer
	spareEnds   []int
}

// surfaceWatch is what an invalidation changes: a component, or a
// container's children.
type surfaceWatch struct {
	leaf      *surfaceComponent
	container *surfaceContainer
}

// surfaceEdit replaces the main entries [lo, hi) with entries, those of
// state when it is set.
type surfaceEdit struct {
	lo, hi  int
	entries []surfaceEntry
	state   *surfaceComponent
}

// surfaceSink collects the invalidations of the components one TuiSurface
// draws. Invalidate may run on any goroutine.
type surfaceSink struct {
	mu      sync.Mutex
	pending []*invalidatable
}

// mark queues i once until the surface takes it.
func (s *surfaceSink) mark(i *invalidatable) {
	for {
		queued := i.queued.Load()
		if queued == s {
			return
		}
		if i.queued.CompareAndSwap(queued, s) {
			break
		}
	}
	s.mu.Lock()
	s.pending = append(s.pending, i)
	s.mu.Unlock()
}

// take returns the queued invalidations and queues the next ones in spare.
func (s *surfaceSink) take(spare []*invalidatable) []*invalidatable {
	s.mu.Lock()
	pending := s.pending
	s.pending = spare[:0]
	s.mu.Unlock()
	return pending
}

// surfaceInvalidator is a component whose Invalidate the surface can
// receive: it embeds invalidatable or BaseComponent.
type surfaceInvalidator interface {
	surfaceInvalidation() *invalidatable
}

// surfacePolled is a component whose IsDirty also reports changes that do
// not invalidate it. While SurfaceLive is true the surface rebuilds it every
// frame, as the main-screen renderer renders it every frame. The method is
// exported so components outside this package that override IsDirty can
// report it too.
type surfacePolled interface {
	SurfaceLive() bool
}

// endBefore is the number of main entries sc's first i children hold.
func (sc *surfaceContainer) endBefore(i int) int {
	if i == 0 {
		return 0
	}
	return sc.ends[i-1]
}

// offset is the index of sc's first main entry.
func (sc *surfaceContainer) offset() int {
	n := 0
	for c := sc; c.parent != nil; c = c.parent {
		n += c.parent.endBefore(c.index)
	}
	return n
}

// start is the index of the component's first main entry.
func (s *surfaceComponent) start() int {
	if s.parent == nil {
		return 0
	}
	return s.parent.offset() + s.parent.endBefore(s.index)
}

// within reports whether the component is mounted under sc's children
// [lo, hi).
func (s *surfaceComponent) within(sc *surfaceContainer, lo, hi int) bool {
	parent, index := s.parent, s.index
	for parent != nil {
		if parent == sc {
			return index >= lo && index < hi
		}
		parent, index = parent.parent, parent.index
	}
	return false
}

// grow adds delta to the entries sc's children from index i on hold, and to
// those of the containers that hold sc.
func (sc *surfaceContainer) grow(i, delta int) {
	if delta == 0 {
		return
	}
	for c := sc; c != nil; i, c = c.index, c.parent {
		for j := i; j < len(c.ends); j++ {
			c.ends[j] += delta
		}
	}
}

// updateMain appends the ops that bring the main region to the document at
// width: the ops of the whole region's diff from the last frame's nodes.
func (t *TuiSurface) updateMain(ops []frontend.Op, width int) []frontend.Op {
	if t.document == nil {
		t.forgetMain()
		clear(t.ids)
		clear(t.containers)
		ops, _ = diffSurfaceRegion(ops, frontend.RegionMain, t.main, nil)
		t.main = t.main[:0]
		return ops
	}
	if theme := ActiveTheme(); width != t.mainWidth || theme != t.mainTheme {
		t.mainWidth, t.mainTheme = width, theme
		t.mainGen++
	}
	t.pass++
	t.takeInvalidations()
	t.trackStreaming()
	t.pollLive()
	whole := t.walkAlways || t.walkedGen != t.mainGen || len(t.restructured) > 1
	if !whole {
		ops, whole = t.applyChanges(ops, width)
	}
	if whole {
		ops = t.walkMain(ops, width)
	}
	for _, state := range t.changed {
		state.changed = false
	}
	clear(t.changed)
	t.changed = t.changed[:0]
	for _, sc := range t.restructured {
		sc.changed = false
	}
	clear(t.restructured)
	t.restructured = t.restructured[:0]
	return ops
}

// takeInvalidations marks what the invalidations since the last frame
// changed.
func (t *TuiSurface) takeInvalidations() {
	pending := t.sink.take(t.drained)
	for _, inv := range pending {
		// Clear the mark first, so an invalidation during this frame's
		// rebuild queues the component for the next one.
		inv.queued.CompareAndSwap(t.sink, nil)
		w := t.watch[inv]
		switch {
		case w.container != nil && w.container.mounted && !w.container.changed:
			w.container.changed = true
			t.restructured = append(t.restructured, w.container)
		case w.leaf != nil && w.leaf.mounted:
			w.leaf.dirty = true
			t.markChanged(w.leaf)
		}
	}
	clear(pending)
	t.drained = pending[:0]
}

// trackStreaming rebuilds the messages that started or stopped streaming.
func (t *TuiSurface) trackStreaming() {
	var block *AssistantMessageComponent
	if t.hooks.Streaming != nil {
		block = t.hooks.Streaming()
	}
	if block == t.streaming {
		return
	}
	for _, b := range []*AssistantMessageComponent{t.streaming, block} {
		if b == nil {
			continue
		}
		if state := t.ids[b]; state != nil && state.mounted {
			state.dirty = true
			t.markChanged(state)
		}
	}
	t.streaming = block
}

// pollLive marks the mounted live components changed and drops the others
// from the live list.
func (t *TuiSurface) pollLive() {
	live := t.live[:0]
	for _, state := range t.live {
		if !state.live || !state.mounted {
			state.listed = false
			continue
		}
		live = append(live, state)
		t.markChanged(state)
	}
	clear(t.live[len(live):])
	t.live = live
}

func (t *TuiSurface) markChanged(state *surfaceComponent) {
	if !state.changed {
		state.changed = true
		t.changed = append(t.changed, state)
	}
}

// listLive adds a mounted component to the ones rebuilt every frame.
func (t *TuiSurface) listLive(state *surfaceComponent) {
	if !state.listed {
		state.listed = true
		t.live = append(t.live, state)
	}
}

// applyChanges appends the ops of the changed components and of the one
// container whose children changed, without walking the document. It
// reports true, having sent nothing, when only a whole walk can apply them.
func (t *TuiSurface) applyChanges(ops []frontend.Op, width int) ([]frontend.Op, bool) {
	// The container's children [p, oldHi) became kids.
	var sc *surfaceContainer
	var kids []Component
	p, oldHi := 0, 0
	if len(t.restructured) == 1 {
		sc = t.restructured[0]
		c := sc.c
		c.mu.Lock()
		head, tail := c.takeChildrenLocked()
		n := len(c.children)
		p = min(head, len(sc.kids), n)
		s := min(tail, len(sc.kids)-p, n-p)
		oldHi = len(sc.kids) - s
		t.spliceKids = append(t.spliceKids[:0], c.children[p:n-s]...)
		c.mu.Unlock()
		kids = t.spliceKids
	}
	edits := t.edits[:0]
	moved := false
	for _, state := range t.changed {
		if sc != nil && state.within(sc, p, oldHi) {
			// The walk of the container's changed children rebuilds it.
			continue
		}
		start, count := state.start(), len(state.entries)
		t.build(state, width)
		moved = moved || !sameIDs(t.main[start:start+count], state.entries)
		edits = append(edits, surfaceEdit{lo: start, hi: start + count, entries: state.entries, state: state})
	}
	t.edits = edits
	if sc == nil && !moved {
		// Every node keeps its id and position: send the changed ones.
		slices.SortFunc(edits, func(a, b surfaceEdit) int { return a.lo - b.lo })
		for _, edit := range edits {
			for i, entry := range edit.entries {
				at := edit.lo + i
				if !nodesEqual(t.main[at].node, entry.node) {
					ops = append(ops, frontend.Op{Kind: frontend.Update, Region: frontend.RegionMain, ID: entry.id, Index: at, Node: entry.node})
				}
				t.main[at] = entry
			}
		}
		clear(edits)
		return ops, false
	}
	if sc != nil {
		base := sc.offset()
		lo, hi := base+sc.endBefore(p), base+sc.endBefore(oldHi)
		for i := p; i < oldHi; i++ {
			t.unmount(sc.states[i], sc.subs[i])
		}
		t.frame++
		t.visited = 0
		t.splicing, t.spliceAborted = true, false
		var mid []surfaceEntry
		var states []*surfaceComponent
		var subs []*surfaceContainer
		var ends []int
		mid, states, subs, ends = t.walkKids(t.spliceEntries[:0], sc, kids, p, width, t.spliceStates[:0], t.spliceSubs[:0], t.spliceEnds[:0])
		t.spliceEntries, t.spliceStates, t.spliceSubs, t.spliceEnds = mid, states, subs, ends
		t.splicing = false
		if t.spliceAborted {
			return t.abandonChanges(ops)
		}
		edits = append(edits, surfaceEdit{lo: lo, hi: hi, entries: mid})
		t.edits = edits
		if !sortEdits(edits) {
			return t.abandonChanges(ops)
		}
		// The container now holds kids in place of its children
		// [p, oldHi).
		delta := len(mid) - (hi - lo)
		head := sc.endBefore(p)
		for i := range ends {
			ends[i] += head
		}
		for i := oldHi; i < len(sc.ends); i++ {
			sc.ends[i] += delta
		}
		sc.ends = slices.Replace(sc.ends, p, oldHi, ends...)
		sc.kids = slices.Replace(sc.kids, p, oldHi, kids...)
		sc.states = slices.Replace(sc.states, p, oldHi, states...)
		sc.subs = slices.Replace(sc.subs, p, oldHi, subs...)
		for i := p + len(kids); i < len(sc.kids); i++ {
			if state := sc.states[i]; state != nil {
				state.index = i
			}
			if sub := sc.subs[i]; sub != nil {
				sub.index = i
			}
		}
		if sc.parent != nil {
			sc.parent.grow(sc.index, delta)
		}
		clear(kids)
		clear(states)
		clear(subs)
	} else if !sortEdits(edits) {
		return t.abandonChanges(ops)
	}
	// A rebuilt component whose node count changed moves what follows it.
	for _, edit := range edits {
		if edit.state != nil && edit.state.parent != nil {
			edit.state.parent.grow(edit.state.index, len(edit.entries)-(edit.hi-edit.lo))
		}
	}
	lo, hi := edits[0].lo, edits[len(edits)-1].hi
	window := t.window[:0]
	at := lo
	for _, edit := range edits {
		window = append(window, t.main[at:edit.lo]...)
		window = append(window, edit.entries...)
		at = edit.hi
	}
	window = append(window, t.main[at:hi]...)
	ops, _ = diffSurfaceWindow(ops, frontend.RegionMain, t.main[lo:hi], window, lo)
	t.main = slices.Replace(t.main, lo, hi, window...)
	clear(window)
	t.window = window[:0]
	clear(edits)
	clear(t.spliceEntries)
	t.forgetUnmounted()
	return ops, false
}

// sortEdits orders edits by position and reports false when two insert at
// one index, an order only the tree knows.
func sortEdits(edits []surfaceEdit) bool {
	slices.SortFunc(edits, func(a, b surfaceEdit) int {
		if a.lo != b.lo {
			return a.lo - b.lo
		}
		return a.hi - b.hi
	})
	for i := 1; i < len(edits); i++ {
		if edits[i].lo == edits[i].hi && edits[i-1].lo == edits[i-1].hi && edits[i].lo == edits[i-1].lo {
			return false
		}
	}
	return true
}

// abandonChanges leaves the frame's changes to a whole walk.
func (t *TuiSurface) abandonChanges(ops []frontend.Op) ([]frontend.Op, bool) {
	clear(t.edits)
	t.edits = t.edits[:0]
	clear(t.spliceEntries)
	return ops, true
}

// unmount marks a container's child, and what it holds, unmounted until a
// walk mounts it again.
func (t *TuiSurface) unmount(state *surfaceComponent, sub *surfaceContainer) {
	if state != nil {
		state.mounted = false
		t.unmounted = append(t.unmounted, state)
	}
	if sub != nil {
		sub.mounted = false
		t.unmountedC = append(t.unmountedC, sub)
		for i := range sub.kids {
			t.unmount(sub.states[i], sub.subs[i])
		}
	}
}

// forgetUnmounted forgets the components and containers a splice took out
// and did not mount again.
func (t *TuiSurface) forgetUnmounted() {
	for _, state := range t.unmounted {
		if !state.mounted {
			t.release(state)
			delete(t.ids, state.comp)
		}
	}
	for _, sc := range t.unmountedC {
		if !sc.mounted {
			t.releaseContainer(sc)
			delete(t.containers, sc.c)
		}
	}
	clear(t.unmounted)
	clear(t.unmountedC)
	t.unmounted, t.unmountedC = t.unmounted[:0], t.unmountedC[:0]
}

// walkMain walks the whole document, reuses the nodes of every component
// that is current, and appends the ops that turn the main region into the
// result.
func (t *TuiSurface) walkMain(ops []frontend.Op, width int) []frontend.Op {
	t.frame++
	t.walks++
	t.walkAlways, t.splicing = false, false
	t.walkedGen = t.mainGen
	t.leaves, t.mounted, t.visited = 0, 0, 0
	for _, state := range t.live {
		state.listed = false
	}
	clear(t.live)
	t.live = t.live[:0]
	clear(t.unmounted)
	clear(t.unmountedC)
	t.unmounted, t.unmountedC = t.unmounted[:0], t.unmountedC[:0]
	next := t.mainNext[:0]
	if c, ok := t.document.(*Container); ok {
		next, _ = t.walkContainer(next, c, nil, 0, width)
	} else {
		next, _ = t.walkLeaf(next, t.document, nil, nil, 0, width)
	}
	// Only mounted components keep their state, so the maps stay bounded.
	if t.leaves < len(t.ids) {
		for c, state := range t.ids {
			if state.seen != t.frame {
				state.mounted = false
				t.release(state)
				delete(t.ids, c)
			}
		}
	}
	if t.mounted < len(t.containers) {
		for c, sc := range t.containers {
			if sc.seen != t.frame {
				sc.mounted = false
				t.releaseContainer(sc)
				delete(t.containers, c)
			}
		}
	}
	ops = diffSurfaceMain(ops, t.main, next)
	clear(t.main)
	t.main, t.mainNext = next, t.main[:0]
	return ops
}

// walkKids appends the entries of kids, sc's children from index first on,
// to next, and their states and cumulative entry counts to states, subs and
// ends. A child mounted where it was at the last walk reuses its state
// without a lookup.
func (t *TuiSurface) walkKids(next []surfaceEntry, sc *surfaceContainer, kids []Component, first, width int, states []*surfaceComponent, subs []*surfaceContainer, ends []int) ([]surfaceEntry, []*surfaceComponent, []*surfaceContainer, []int) {
	base := len(next)
	for i, kid := range kids {
		at := first + i
		var state *surfaceComponent
		var sub *surfaceContainer
		if c, ok := kid.(*Container); ok {
			next, sub = t.walkContainer(next, c, sc, at, width)
		} else {
			var hint *surfaceComponent
			if at < len(sc.states) && sc.states[at] != nil && sc.states[at].comp == kid {
				hint = sc.states[at]
			}
			next, state = t.walkLeaf(next, kid, hint, sc, at, width)
		}
		states = append(states, state)
		subs = append(subs, sub)
		ends = append(ends, len(next)-base)
	}
	return next, states, subs, ends
}

// walkContainer appends the entries of c, mounted in parent at index, and
// returns its state, or nil for a second mount, whose components are keyed
// by position.
func (t *TuiSurface) walkContainer(next []surfaceEntry, c *Container, parent *surfaceContainer, index, width int) ([]surfaceEntry, *surfaceContainer) {
	t.visited++
	sc := t.containers[c]
	if sc == nil {
		sc = &surfaceContainer{c: c}
		t.containers[c] = sc
	}
	if sc.seen == t.frame || (t.splicing && sc.mounted) {
		if t.splicing {
			t.spliceAborted = true
			return next, nil
		}
		t.walkAlways = true
		c.mu.RLock()
		kids := slices.Clone(c.children)
		c.mu.RUnlock()
		for _, kid := range kids {
			if nested, ok := kid.(*Container); ok {
				next, _ = t.walkContainer(next, nested, nil, 0, width)
			} else {
				next, _ = t.walkLeaf(next, kid, nil, nil, 0, width)
			}
		}
		return next, nil
	}
	if !t.splicing {
		t.mounted++
	}
	sc.seen, sc.mounted = t.frame, true
	sc.parent, sc.index = parent, index
	if sc.inv == nil {
		sc.inv = t.claim(&c.invalidatable, surfaceWatch{container: sc})
	}
	if sc.inv == nil {
		// Another surface receives the container's changes.
		t.walkAlways = true
	}
	c.mu.Lock()
	sc.spareKids = append(sc.spareKids[:0], c.children...)
	kids := sc.spareKids
	if sc.inv != nil {
		c.takeChildrenLocked()
	}
	c.mu.Unlock()
	var states []*surfaceComponent
	var subs []*surfaceContainer
	var ends []int
	next, states, subs, ends = t.walkKids(next, sc, kids, 0, width, sc.spareStates[:0], sc.spareSubs[:0], sc.spareEnds[:0])
	clear(sc.kids)
	clear(sc.states)
	clear(sc.subs)
	sc.spareKids, sc.spareStates, sc.spareSubs, sc.spareEnds = sc.kids[:0], sc.states[:0], sc.subs[:0], sc.ends[:0]
	sc.kids, sc.states, sc.subs, sc.ends = kids, states, subs, ends
	return next, sc
}

// walkLeaf appends the entries of the component c, mounted in parent at
// index, whose state was hint at the last walk, and returns its state, or
// nil when it is keyed by position.
func (t *TuiSurface) walkLeaf(next []surfaceEntry, c Component, hint *surfaceComponent, parent *surfaceContainer, index, width int) ([]surfaceEntry, *surfaceComponent) {
	t.visited++
	// A component whose dynamic type is not comparable cannot be tracked,
	// and a second mount of the same component needs its own node, so both
	// are keyed by position.
	if hint == nil && reflect.TypeOf(c).Comparable() {
		hint = t.ids[c]
		if hint == nil {
			t.nextID++
			hint = &surfaceComponent{id: "n" + strconv.Itoa(t.nextID), comp: c}
			t.ids[c] = hint
		}
	}
	if hint == nil || hint.seen == t.frame || (t.splicing && hint.mounted) {
		if t.splicing {
			t.spliceAborted = true
			return next, nil
		}
		t.walkAlways = true
		return t.appendNodes(next, "p"+strconv.Itoa(len(next)), nil, c, width), nil
	}
	state := hint
	state.seen, state.mounted = t.frame, true
	state.parent, state.index = parent, index
	if !t.splicing {
		t.leaves++
	}
	if state.pass != t.pass && (state.dirty || state.live || state.gen != t.mainGen) {
		t.build(state, width)
	}
	if state.live {
		t.listLive(state)
	}
	return append(next, state.entries...), state
}

// build rebuilds the nodes of a mounted component.
func (t *TuiSurface) build(state *surfaceComponent, width int) {
	state.dirty = false
	state.gen = t.mainGen
	state.pass = t.pass
	if state.inv == nil {
		if c, ok := state.comp.(surfaceInvalidator); ok {
			state.inv = t.claim(c.surfaceInvalidation(), surfaceWatch{leaf: state})
		}
	}
	entries := t.appendNodes(state.entries[:0], state.id, state, state.comp, width)
	for i := range entries {
		t.ver++
		entries[i].ver = t.ver
	}
	state.entries = entries
	state.live = t.polled(state)
	if state.live && state.mounted {
		t.listLive(state)
	}
}

// polled reports whether the surface must rebuild the component every frame
// because not every change to its nodes reaches it as an invalidation: it
// does not embed invalidatable, another surface draws it, it is a Box, whose
// children do not invalidate it, or it reports itself live. A tool card
// drawn through a definition also depends on its result component.
func (t *TuiSurface) polled(state *surfaceComponent) bool {
	if state.inv == nil {
		return true
	}
	switch c := state.comp.(type) {
	case *Box:
		return true
	case *ToolExecutionComponent:
		var dep *invalidatable
		if c.definition != nil && c.definitionResultComponent != nil {
			result := c.definitionResultComponent
			inv, ok := result.(surfaceInvalidator)
			p, isPolled := result.(surfacePolled)
			_, box := result.(*Box)
			_, container := result.(*Container)
			if !ok || box || container || (isPolled && p.SurfaceLive()) {
				t.releaseDep(state)
				return true
			}
			dep = inv.surfaceInvalidation()
		}
		if dep != state.dep {
			t.releaseDep(state)
			if dep != nil {
				if state.dep = t.claim(dep, surfaceWatch{leaf: state}); state.dep == nil {
					return true
				}
			}
		}
	case surfacePolled:
		return c.SurfaceLive()
	}
	return false
}

// claim makes inv report its invalidations to this surface and returns it,
// or nil when another surface, or another of this surface's components,
// receives them.
func (t *TuiSurface) claim(inv *invalidatable, w surfaceWatch) *invalidatable {
	if _, taken := t.watch[inv]; taken || !inv.sink.CompareAndSwap(nil, t.sink) {
		return nil
	}
	t.watch[inv] = w
	return inv
}

func (t *TuiSurface) unclaim(inv *invalidatable) {
	if inv == nil {
		return
	}
	delete(t.watch, inv)
	inv.sink.CompareAndSwap(t.sink, nil)
	inv.queued.CompareAndSwap(t.sink, nil)
}

func (t *TuiSurface) releaseDep(state *surfaceComponent) {
	t.unclaim(state.dep)
	state.dep = nil
}

func (t *TuiSurface) release(state *surfaceComponent) {
	t.unclaim(state.inv)
	t.releaseDep(state)
	state.inv = nil
}

func (t *TuiSurface) releaseContainer(sc *surfaceContainer) {
	t.unclaim(sc.inv)
	sc.inv = nil
}

// forgetMain stops every mounted component from reporting to the surface
// and makes the next frame walk the whole document and rebuild every node.
func (t *TuiSurface) forgetMain() {
	for _, state := range t.ids {
		t.release(state)
	}
	for _, sc := range t.containers {
		t.releaseContainer(sc)
	}
	clear(t.sink.take(nil))
	t.mainGen++
}

// sameIDs reports whether two runs of entries have the same ids.
func sameIDs(a, b []surfaceEntry) bool {
	return slices.EqualFunc(a, b, func(x, y surfaceEntry) bool { return x.id == y.id })
}
