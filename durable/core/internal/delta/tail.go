package delta

// tailOverlay is the overlay of an array whose changes so far touch only its tail: elements overwritten or read as
// containers, a suffix of the base removed, and elements appended. It keeps nothing for an element no one touched, so the
// cost of reading the length, reaching an element or appending to a long array does not grow with the array. Any other
// edit, or a change that makes the array a dense region of overwrites, turns it into the element-per-entry list (n.entries).
//
// The array is the first kept base elements, then the appended ones. touched holds the entry of each kept base element
// that was overwritten or read as a container, ascending by index.
type tailOverlay struct {
	kept     int
	touched  []*entry // sorted by index
	appended []*entry
}

func (t *tailOverlay) length() int { return t.kept + len(t.appended) }

// arrayLen is the array's present length.
func (n *node) arrayLen() int {
	switch {
	case n.entries != nil:
		return len(n.entries)
	case n.tailed:
		return n.tail.length()
	}
	return len(n.base.([]any))
}

// tailOf is the node's tail overlay, made on first use over an untouched array.
func (n *node) tailOf() *tailOverlay {
	if !n.tailed {
		n.tailed = true
		n.tail.kept = len(n.base.([]any))
	}
	return &n.tail
}

// find is the place of the touched element at index in t.touched, and whether there is one.
func (t *tailOverlay) find(index int) (int, bool) {
	low, high := 0, len(t.touched)
	for low < high {
		middle := (low + high) >> 1
		if t.touched[middle].index < index {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low, low < len(t.touched) && t.touched[low].index == index
}

// entryAt is the entry of the element at index, which the caller checked is within the array.
func (n *node) entryAt(index int) *entry {
	if n.entries != nil {
		return n.entries[index]
	}
	t := n.tailOf()
	if index >= t.kept {
		return t.appended[index-t.kept]
	}
	at, found := t.find(index)
	if found {
		return t.touched[at]
	}
	e := n.newEntryAt(index)
	t.touched = append(t.touched, nil)
	copy(t.touched[at+1:], t.touched[at:])
	t.touched[at] = e
	return e
}

// ensureEntries gives the array one entry per element, from its tail overlay when it has one.
func (n *node) ensureEntries() {
	if n.entries != nil {
		return
	}
	t := n.tailOf()
	entries := makeEntries(0, t.length())
	for index := range t.kept {
		if at, found := t.find(index); found {
			entries = append(entries, t.touched[at])
		} else {
			entries = append(entries, n.newEntryAt(index))
		}
	}
	entries = append(entries, t.appended...)
	n.entries = entries
	n.tail, n.tailed = tailOverlay{}, false
}

// removeTail removes the elements from start to the end of an array that has no entry list, storing their present values
// in removed (which has one place for each).
func (n *node) removeTail(start int, removed []any) {
	t := n.tailOf()
	base := n.base.([]any)
	for offset := range removed {
		position := start + offset
		if position >= t.kept {
			e := t.appended[position-t.kept]
			removed[offset] = storedCurrent(n.entryValue(e))
			e.live = false
		} else if at, found := t.find(position); found {
			e := t.touched[at]
			removed[offset] = storedCurrent(n.entryValue(e))
			e.live = false
		} else {
			removed[offset] = base[position]
		}
	}
	if len(removed) > 0 {
		if start < t.kept {
			at, _ := t.find(start)
			t.touched = t.touched[:at]
			t.kept, t.appended = start, nil
		} else {
			t.appended = t.appended[:start-t.kept]
		}
	}
}

// modified reports whether an element read as a container below the tail overlay changed.
func (t *tailOverlay) modified() bool {
	for _, e := range t.touched {
		if !e.set && e.kid != nil && e.kid.modified() {
			return true
		}
	}
	return false
}

// overwrites is how many touched elements were overwritten.
func (t *tailOverlay) overwrites() int {
	count := 0
	for _, e := range t.touched {
		if e.set {
			count++
		}
	}
	return count
}

// emit is emitArray for an array that has a tail overlay: the removal of the base's suffix, the insertion of the
// appended elements, and the overwrite, which is at most one (overwrites keep the order they were first made in, which the
// entry list knows).
func (t *tailOverlay) emit(n *node, path Path, ops *[]Op) {
	base := n.base.([]any)
	if t.kept < len(base) {
		*ops = append(*ops, spliceOp(path, t.kept, len(base)-t.kept, noItems))
	}
	if len(t.appended) > 0 {
		*ops = append(*ops, spliceOp(path, t.kept, 0, insertionItems(t.appended)))
	}
	// The overwrites in the order they were first made: the touched entries, reordered in place by when they were set.
	for _, e := range t.touched {
		if e.set {
			emitChangedValue(ops, childIdx(path, e.index), base[e.index], true, storedCurrent(e.value))
		}
	}
}
