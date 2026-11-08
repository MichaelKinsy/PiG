package tui

// pig additive (D91): a frame costs what changed, not the length of the transcript. A container's render finds the first child whose
// lines may have changed, and every line before it is kept; asking every child of a transcript of thousands whether it is dirty
// made that search the one cost of a streamed delta that grew with the transcript. Here the children the last search found clean
// are not asked again: those of the types below tell their container when they are invalidated, and the others stay polled.

// nestedRender is what a nested container returned when its lines were not the revision the window holds, which the render adopts
// instead of asking the container again.
type nestedRender struct {
	lines   []string
	version uint64
	kept    int
}

// kidInvalidation is the invalidation of ch when ch is of a type whose IsDirty reads that invalidation's flag and nothing else, and
// nil for any other child. The types are listed exactly: a type that embeds one of them may override IsDirty, which a container
// then has to ask on every frame. A card is listed too, while kidQuiet holds.
func kidInvalidation(ch Component) *invalidatable {
	switch v := ch.(type) {
	case *Spacer:
		return &v.invalidatable
	case *Text:
		return &v.invalidatable
	case *UserMessageComponent:
		return &v.invalidatable
	case *AssistantMessageComponent:
		return &v.invalidatable
	case *ToolExecutionComponent:
		return &v.invalidatable
	}
	return nil
}

// kidQuiet reports that nothing but an Invalidate of ch can make its IsDirty true, which holds for every child kidInvalidation
// returns but a tool card whose IsDirty also reads the clock or a renderer's component.
func kidQuiet(ch Component) bool {
	if card, ok := ch.(*ToolExecutionComponent); ok {
		return card.quietForScan()
	}
	return true
}

// quietForScan reports that the card's IsDirty can become true only through an Invalidate of the card: its tool has finished, so no
// shell footer counts the time, and its renderers' components are the card's own text, which an update of the card replaces,
// not a component that reports changes of its own (an extension's, which an extension process updates).
func (c *ToolExecutionComponent) quietForScan() bool {
	if c.State == ToolStateRunning || c.IsPartial || c.definitionDirty.Load() {
		return false
	}
	if c.definition == nil {
		return true
	}
	for _, component := range [...]Component{c.definitionCall, c.definitionResultComponent} {
		if component == nil {
			continue
		}
		if _, reports := component.(rendererDirtyReporter); reports {
			if _, isText := component.(*Text); !isText {
				return false
			}
		}
	}
	return true
}

// kidInvalidated queues the invalidation of a child for the next render. It runs on any goroutine.
func (c *Container) kidInvalidated(i *invalidatable) {
	c.kidMu.Lock()
	c.kidPending = append(c.kidPending, i)
	c.kidMu.Unlock()
}

// takeInvalidatedLocked lowers settled to the first child an invalidation reached since the last render. A child that is no longer
// at the index it was settled at lowers it to the start. c.mu must be held.
func (c *Container) takeInvalidatedLocked() {
	c.kidMu.Lock()
	pending := c.kidPending
	c.kidPending = c.kidSpare[:0]
	c.kidMu.Unlock()
	for _, inv := range pending {
		// The report is cleared before the flag is read, so an Invalidate after this point reports again.
		inv.queuedOwner.Store(false)
		switch {
		case inv.owner.Load() != c:
			// The child left the container since.
		case inv.slot < len(c.children) && kidInvalidation(c.children[inv.slot]) == inv:
			c.settled = min(c.settled, inv.slot)
		default:
			c.settled = 0
		}
	}
	clear(pending)
	c.kidSpare = pending[:0]
}

// childSettledLocked reports whether child i is what the window was built from and needs no render: the same component, a nested
// container whose lines are the revision the window holds, or a cacheable child that is not dirty. A nested container whose
// revision changed leaves its lines in n. c.mu must be held.
func (c *Container) childSettledLocked(i, width int, n *nestedRender) bool {
	ch := c.children[i]
	if i >= len(c.builtKids) || !c.builtSame[i] || c.builtKids[i] != ch {
		return false
	}
	if nested, ok := ch.(*Container); ok {
		lines, version, kept := nested.renderBorrowedSince(width, c.builtVers[i])
		if version != c.builtVers[i] {
			*n = nestedRender{lines: lines, version: version, kept: kept}
			return false
		}
		return true
	}
	dc, ok := ch.(dirtyComponent)
	return ok && childCacheable(ch) && !dc.IsDirty()
}

// scanSettledLocked returns the index of the first child whose lines may have changed since the window was built: one that moved,
// a nested container with new lines (left in n), or a child that renders every frame or reports itself dirty. The children below
// c.settled were clean when the last scan ended: those that report their invalidations are not asked, the others (c.polled) are, and
// the scan from there on asks every child. c.mu must be held.
func (c *Container) scanSettledLocked(width int, n *nestedRender) int {
	c.takeInvalidatedLocked()
	limit := min(c.settled, len(c.children), len(c.builtKids))
	k := 0
	for k < len(c.polled) && c.polled[k] < limit {
		if !c.childSettledLocked(c.polled[k], width, n) {
			c.settled, c.polled = c.polled[k], c.polled[:k]
			return c.settled
		}
		k++
	}
	c.polled = c.polled[:k]
	first := limit
	for first < len(c.children) {
		if !c.childSettledLocked(first, width, n) {
			break
		}
		if !c.trustKidLocked(first, c.children[first]) {
			c.polled = append(c.polled, first)
		}
		first++
	}
	c.settled = first
	return first
}

// trustKidHook, when set by a test, runs in trustKidLocked after the child was read clean and before the container claims it.
var trustKidHook func(Component)

// trustKidLocked reports that the container reads child i's invalidations from now on: the child is of a listed type, quiet, and
// not read by another container. A child listed twice keeps the lower index, which an invalidation lowers settled to, and the scan
// from there reaches the other. c.mu must be held.
func (c *Container) trustKidLocked(i int, ch Component) bool {
	inv := kidInvalidation(ch)
	if inv == nil || !kidQuiet(ch) {
		return false
	}
	if trustKidHook != nil {
		trustKidHook(ch)
	}
	if inv.owner.Load() != c && !inv.owner.CompareAndSwap(nil, c) {
		return false
	}
	// An Invalidate between the scan reading the child clean and the claim above saw no owner and told no one: the child is dirty
	// behind settled, so it stays polled, which the next scan asks.
	if inv.dirty.Load() {
		return false
	}
	if s := inv.slot; s > i || s >= len(c.children) || kidInvalidation(c.children[s]) != inv {
		inv.slot = i
	}
	return true
}

// releaseKidLocked ends the reading of a child's invalidations when it is no longer among the children. c.mu must be held.
func (c *Container) releaseKidLocked(ch Component) {
	inv := kidInvalidation(ch)
	if inv == nil {
		return
	}
	for _, child := range c.children {
		if child == ch {
			return
		}
	}
	inv.owner.CompareAndSwap(c, nil)
}
