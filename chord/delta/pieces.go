package delta

import "slices"

// The array overlay of tracker.ts (Pi 1.2.0 :19-54, :768-1066, :1475-1539, :1886-1977, :2094-2144): a draft array is a
// sequence of pieces, each a run of base elements or of one insertion's elements, held in a treap ordered by position
// and sized by element count. An edit splits and joins pieces, so an insert or removal anywhere costs O(log pieces),
// and the plan of a change whose retained base elements keep their order is computed from the pieces alone.

// insertSource is tracker.ts InsertSource: the elements of one insertion. A piece of it names a run of these entries.
type insertSource struct{ entries []*entry }

// piece is tracker.ts Piece: length elements from start, stepping by step (1 or -1), taken from the base array when
// source is nil and from source's entries otherwise.
type piece struct {
	source *insertSource
	start  int
	length int
	step   int
}

func (p piece) isBase() bool { return p.source == nil }

// sourceIndex is the index in the piece's source of the element offset elements into the piece.
func (p piece) sourceIndex(offset int) int { return p.start + p.step*offset }

// pieceNode is tracker.ts PieceNode.
type pieceNode struct {
	piece       piece
	left, right *pieceNode
	priority    uint32
	elements    int
}

// pieceLocation is tracker.ts PieceLocation: where a piece lies in the array and the range of source indices it covers.
type pieceLocation struct {
	piece        piece
	logicalStart int
	minimum      int
	maximum      int
}

// arrayOverlay is tracker.ts ArrayOverlay. A base element has an entry only once overwritten or read as a container
// (tracker.ts keeps baseOverrides and the nodes of read containers apart; an entry holds both).
type arrayOverlay struct {
	root            *pieceNode
	pieces          []piece // in-order pieces; nil when stale
	baseEntries     baseEntrySet
	structural      bool
	generation      int
	seed            uint32
	located         bool // baseLocations and insertLocations are current
	baseLocations   []pieceLocation
	insertLocations map[*insertSource][]pieceLocation
}

// arrayOverlay returns the node's overlay, made on first use with one base piece covering the base array.
func (n *node) arrayOverlay() *arrayOverlay {
	if n.array != nil {
		return n.array
	}
	overlay := &arrayOverlay{seed: 0x9e3779b9}
	if length := len(n.base.([]any)); length > 0 {
		overlay.root = overlay.newPieceNode(piece{start: 0, length: length, step: 1})
	}
	n.array = overlay
	return overlay
}

// arrayLength is the array's present length.
func (n *node) arrayLength() int {
	if n.array == nil {
		return len(n.base.([]any))
	}
	return n.array.root.count()
}

func (overlay *arrayOverlay) nextPriority() uint32 {
	value := overlay.seed
	value ^= value << 13
	value ^= value >> 17
	value ^= value << 5
	overlay.seed = value
	return value
}

func (node *pieceNode) count() int {
	if node == nil {
		return 0
	}
	return node.elements
}

func (node *pieceNode) update() {
	node.elements = node.left.count() + node.piece.length + node.right.count()
}

func (overlay *arrayOverlay) newPieceNode(p piece) *pieceNode {
	return &pieceNode{piece: p, priority: overlay.nextPriority(), elements: p.length}
}

func mergePieceTrees(left, right *pieceNode) *pieceNode {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if left.priority >= right.priority {
		left.right = mergePieceTrees(left.right, right)
		left.update()
		return left
	}
	right.left = mergePieceTrees(left, right.left)
	right.update()
	return right
}

// split divides root into the first index elements and the rest, splitting the piece that straddles index.
func (overlay *arrayOverlay) split(root *pieceNode, index int) (*pieceNode, *pieceNode) {
	if root == nil {
		return nil, nil
	}
	leftLength := root.left.count()
	if index < leftLength {
		left, right := overlay.split(root.left, index)
		root.left = right
		root.update()
		return left, root
	}
	pieceEnd := leftLength + root.piece.length
	if index > pieceEnd {
		left, right := overlay.split(root.right, index-pieceEnd)
		root.right = left
		root.update()
		return root, right
	}
	if index == leftLength {
		left := root.left
		root.left = nil
		root.update()
		return left, root
	}
	if index == pieceEnd {
		right := root.right
		root.right = nil
		root.update()
		return root, right
	}
	offset := index - leftLength
	first := root.piece
	first.length = offset
	second := root.piece
	second.start = root.piece.sourceIndex(offset)
	second.length = root.piece.length - offset
	return mergePieceTrees(root.left, overlay.newPieceNode(first)), mergePieceTrees(overlay.newPieceNode(second), root.right)
}

func leftmostPieceNode(node *pieceNode) *pieceNode {
	for node.left != nil {
		node = node.left
	}
	return node
}

func rightmostPieceNode(node *pieceNode) *pieceNode {
	for node.right != nil {
		node = node.right
	}
	return node
}

// mergeablePieces is tracker.ts mergeablePieces: right continues left's run of the same source.
func mergeablePieces(left, right piece) bool {
	if left.source != right.source {
		return false
	}
	if left.length == 1 && right.length == 1 {
		return right.start-left.start == 1 || left.start-right.start == 1
	}
	return left.step == right.step && left.sourceIndex(left.length) == right.start
}

// joinNormalized joins two trees, merging the pieces that meet when one continues the other.
func (overlay *arrayOverlay) joinNormalized(left, right *pieceNode) *pieceNode {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	leftPiece := rightmostPieceNode(left).piece
	rightPiece := leftmostPieceNode(right).piece
	if !mergeablePieces(leftPiece, rightPiece) {
		return mergePieceTrees(left, right)
	}
	leftRest, _ := overlay.split(left, left.count()-leftPiece.length)
	_, rightRest := overlay.split(right, rightPiece.length)
	step := leftPiece.step
	if leftPiece.length == 1 {
		step = rightPiece.start - leftPiece.start
	}
	combined := piece{source: leftPiece.source, start: leftPiece.start, length: leftPiece.length + rightPiece.length, step: step}
	return overlay.joinNormalized(overlay.joinNormalized(leftRest, overlay.newPieceNode(combined)), rightRest)
}

func flattenPieceTree(node *pieceNode, output []piece) []piece {
	if node == nil {
		return output
	}
	output = flattenPieceTree(node.left, output)
	output = append(output, node.piece)
	return flattenPieceTree(node.right, output)
}

// piecesOf is tracker.ts piecesOf: the pieces in order, cached until the next edit.
func (overlay *arrayOverlay) piecesOf() []piece {
	if overlay.pieces == nil {
		overlay.pieces = flattenPieceTree(overlay.root, []piece{})
	}
	return overlay.pieces
}

func (overlay *arrayOverlay) treeFromPieces(pieces []piece) *pieceNode {
	pieces = mergePieces(pieces)
	var root *pieceNode
	for _, p := range pieces {
		root = mergePieceTrees(root, overlay.newPieceNode(p))
	}
	return root
}

// replaceAllPieces is tracker.ts replaceAllPieces.
func (overlay *arrayOverlay) replaceAllPieces(pieces []piece) {
	overlay.root = overlay.treeFromPieces(pieces)
	overlay.invalidate()
}

// locate is tracker.ts locatePiece: the piece holding the element at index and the element's offset in it.
func (overlay *arrayOverlay) locate(index int) (piece, int) {
	node := overlay.root
	for node != nil {
		leftLength := node.left.count()
		switch {
		case index < leftLength:
			node = node.left
		case index >= leftLength+node.piece.length:
			index -= leftLength + node.piece.length
			node = node.right
		default:
			return node.piece, index - leftLength
		}
	}
	panic("array overlay index is out of range")
}

func extendRightmostPiece(node *pieceNode, amount int) {
	if node.right != nil {
		extendRightmostPiece(node.right, amount)
	} else {
		node.piece.length += amount
	}
	node.update()
}

// invalidate is tracker.ts invalidatePieceCaches.
func (overlay *arrayOverlay) invalidate() {
	overlay.pieces = nil
	overlay.located = false
	overlay.baseLocations = nil
	overlay.insertLocations = nil
}

// markStructural records an edit that moved, removed or inserted elements.
func (n *node) markStructural() {
	n.array.invalidate()
	n.array.structural = true
	n.array.generation++
	n.markChanged()
}

// replacePieceRange is tracker.ts replacePieceRange: remove elements from index and put the inserted pieces there.
// Appending one insertion after an insertion that ends its source extends that piece in place.
func (n *node) replacePieceRange(index, remove int, inserted []piece) {
	if remove == 0 && len(inserted) == 0 {
		return
	}
	overlay := n.arrayOverlay()
	if remove == 0 && index == overlay.root.count() && len(inserted) == 1 && overlay.root != nil {
		addition := inserted[0]
		tail := rightmostPieceNode(overlay.root).piece
		if !addition.isBase() && !tail.isBase() && tail.step == 1 && tail.start+tail.length == len(tail.source.entries) {
			for offset := range addition.length {
				e := addition.source.entries[addition.sourceIndex(offset)]
				e.source, e.index = tail.source, len(tail.source.entries)
				tail.source.entries = append(tail.source.entries, e)
			}
			extendRightmostPiece(overlay.root, addition.length)
			n.markStructural()
			return
		}
	}
	left, rest := overlay.split(overlay.root, index)
	_, right := overlay.split(rest, remove)
	middle := overlay.treeFromPieces(inserted)
	overlay.root = overlay.joinNormalized(overlay.joinNormalized(left, middle), right)
	n.markStructural()
}

// mergePieces is tracker.ts mergePieces: neighbors that continue one run become one piece.
func mergePieces(pieces []piece) []piece {
	for index := 1; index < len(pieces); {
		left, right := &pieces[index-1], pieces[index]
		sameSource := left.source == right.source
		switch {
		case sameSource && left.length == 1 && right.length == 1 && (right.start-left.start == 1 || left.start-right.start == 1):
			left.step = right.start - left.start
			left.length = 2
			pieces = slices.Delete(pieces, index, index+1)
		case sameSource && left.step == right.step && left.sourceIndex(left.length) == right.start:
			left.length += right.length
			pieces = slices.Delete(pieces, index, index+1)
		default:
			index++
		}
	}
	return pieces
}

// insertPiece is tracker.ts insertPiece: one insertion of stored values, as zero or one piece.
func (n *node) insertPiece(stored []any) []piece {
	if len(stored) == 0 {
		return nil
	}
	source := &insertSource{entries: make([]*entry, len(stored))}
	for index, value := range stored {
		e := &entry{source: source, index: index, set: true}
		e.value = n.wrap(value, "", e)
		source.entries[index] = e
	}
	return []piece{{source: source, start: 0, length: len(stored), step: 1}}
}

// slot is the stored value at a source index of a piece: a primitive, a base container that has no node yet, or a
// *node. It makes no entry.
func (n *node) slot(p piece, index int) any {
	if !p.isBase() {
		return n.entryValue(p.source.entries[index])
	}
	if e := n.array.baseEntries.get(index); e != nil {
		return n.entryValue(e)
	}
	return n.base.([]any)[index]
}

// slotEntry is the entry of a source index of a piece, made for a base element that has none.
func (n *node) slotEntry(p piece, index int) *entry {
	if !p.isBase() {
		return p.source.entries[index]
	}
	return n.baseEntry(index)
}

func (n *node) baseEntry(index int) *entry {
	overlay := n.arrayOverlay()
	if e := overlay.baseEntries.get(index); e != nil {
		return e
	}
	e := &entry{index: index}
	overlay.baseEntries.put(index, e, len(n.base.([]any)))
	return e
}

// baseEntrySet holds the entries of base elements by base index: a map while few elements have one, and a slice over the
// base array once at least 256 do and they are a sixteenth of it, as tracker.ts DenseCandidates turns candidate
// indices into a bitmap at 256. Reading every row of a long array then costs a slice store per row, not a map insert.
type baseEntrySet struct {
	sparse map[int]*entry
	dense  []*entry
	count  int
}

func (set *baseEntrySet) get(index int) *entry {
	if set.dense != nil {
		return set.dense[index]
	}
	return set.sparse[index]
}

// put adds the entry of a base element that has none, in an array of baseLength elements.
func (set *baseEntrySet) put(index int, e *entry, baseLength int) {
	set.count++
	if set.dense != nil {
		set.dense[index] = e
		return
	}
	if set.sparse == nil {
		set.sparse = map[int]*entry{}
	}
	set.sparse[index] = e
	if set.count >= 256 && set.count*16 >= baseLength {
		set.dense = make([]*entry, baseLength)
		for index, e := range set.sparse {
			set.dense[index] = e
		}
		set.sparse = nil
	}
}

// all yields the entries with their base indices, in index order once dense and in no order before.
func (set *baseEntrySet) all(yield func(int, *entry) bool) {
	if set.dense != nil {
		for index, e := range set.dense {
			if e != nil && !yield(index, e) {
				return
			}
		}
		return
	}
	for index, e := range set.sparse {
		if !yield(index, e) {
			return
		}
	}
}

// valueAt is the stored value at a position, without making an entry.
func (n *node) valueAt(position int) any {
	if n.array == nil {
		return n.base.([]any)[position]
	}
	p, offset := n.array.locate(position)
	return n.slot(p, p.sourceIndex(offset))
}

// ensureLocations is tracker.ts ensurePieceLocations.
func (overlay *arrayOverlay) ensureLocations() {
	if overlay.located {
		return
	}
	overlay.located = true
	overlay.baseLocations = nil
	overlay.insertLocations = map[*insertSource][]pieceLocation{}
	logicalStart := 0
	for _, p := range overlay.piecesOf() {
		last := p.sourceIndex(p.length - 1)
		location := pieceLocation{piece: p, logicalStart: logicalStart, minimum: min(p.start, last), maximum: max(p.start, last)}
		if p.isBase() {
			overlay.baseLocations = append(overlay.baseLocations, location)
		} else {
			overlay.insertLocations[p.source] = append(overlay.insertLocations[p.source], location)
		}
		logicalStart += p.length
	}
	byMinimum := func(left, right pieceLocation) int { return left.minimum - right.minimum }
	slices.SortFunc(overlay.baseLocations, byMinimum)
	for _, locations := range overlay.insertLocations {
		slices.SortFunc(locations, byMinimum)
	}
}

// findEntryIndex is tracker.ts findEntryIndex: the present position of an entry's element, or -1 once it was removed.
func (overlay *arrayOverlay) findEntryIndex(e *entry) int {
	if e.source == nil && !overlay.structural {
		return e.index
	}
	overlay.ensureLocations()
	locations := overlay.baseLocations
	if e.source != nil {
		locations = overlay.insertLocations[e.source]
	}
	low, high := 0, len(locations)
	for low < high {
		middle := int(uint(low+high) >> 1)
		if locations[middle].minimum <= e.index {
			low = middle + 1
		} else {
			high = middle
		}
	}
	if low == 0 || e.index > locations[low-1].maximum {
		return -1
	}
	location := locations[low-1]
	offset := (e.index - location.piece.start) / location.piece.step
	if offset < 0 || offset >= location.piece.length {
		return -1
	}
	return location.logicalStart + offset
}

// arrayPlan is tracker.ts ArrayPlan: removals from the end as start, length pairs, the permutation of the retained base
// elements (nil when they keep their order), and insertion runs as position, first piece, end piece triples.
type arrayPlan struct {
	removeRuns  []int
	permutation []any
	insertRuns  []int
}

// buildArrayPlan is tracker.ts buildArrayPlan.
func buildArrayPlan(baseLength int, pieces []piece) arrayPlan {
	if plan, ordered := orderedArrayPlan(baseLength, pieces); ordered {
		return plan
	}
	return elementArrayPlan(baseLength, pieces)
}

// orderedArrayPlan is tracker.ts orderedArrayPlan: the plan when the retained base elements keep their order, the
// common case of inserts and removals, computed from the pieces alone. It reports false when base elements moved.
func orderedArrayPlan(baseLength int, pieces []piece) (arrayPlan, bool) {
	// Retained base ranges in piece order, first and last each; ordered means strictly ascending without overlap.
	var ranges []int
	for _, p := range pieces {
		if !p.isBase() || p.length == 0 {
			continue
		}
		if p.step != 1 && p.length > 1 {
			return arrayPlan{}, false
		}
		if len(ranges) > 0 && p.start <= ranges[len(ranges)-1] {
			return arrayPlan{}, false
		}
		ranges = append(ranges, p.start, p.start+p.length-1)
	}
	// Unretained runs from the end backwards: after the last range, between ranges, before the first.
	var removeRuns []int
	end := baseLength
	for index := len(ranges) - 2; index >= -2; index -= 2 {
		start := 0
		if index >= 0 {
			start = ranges[index+1] + 1
		}
		if end > start {
			removeRuns = append(removeRuns, start, end-start)
		}
		if index >= 0 {
			end = ranges[index]
		}
	}
	return arrayPlan{removeRuns: removeRuns, insertRuns: insertRunsOf(pieces)}, true
}

// elementArrayPlan is tracker.ts elementArrayPlan: the plan from a walk over every base element, for pieces that moved
// base elements.
func elementArrayPlan(baseLength int, pieces []piece) arrayPlan {
	retained := make([]bool, baseLength)
	var targetBase []int
	for _, p := range pieces {
		if !p.isBase() {
			continue
		}
		for offset := range p.length {
			index := p.sourceIndex(offset)
			retained[index] = true
			targetBase = append(targetBase, index)
		}
	}
	var removeRuns []int
	for end := baseLength; end > 0; {
		if retained[end-1] {
			end--
			continue
		}
		start := end - 1
		for start > 0 && !retained[start-1] {
			start--
		}
		removeRuns = append(removeRuns, start, end-start)
		end = start
	}
	retainedBase := make([]int, 0, len(targetBase))
	for index, kept := range retained {
		if kept {
			retainedBase = append(retainedBase, index)
		}
	}
	var permutation []any
	if !slices.Equal(targetBase, retainedBase) {
		positions := make(map[int]int, len(retainedBase))
		for position, index := range retainedBase {
			positions[index] = position
		}
		permutation = make([]any, len(targetBase))
		for position, index := range targetBase {
			permutation[position] = positions[index]
		}
	}
	return arrayPlan{removeRuns: removeRuns, permutation: permutation, insertRuns: insertRunsOf(pieces)}
}

// insertRunsOf is tracker.ts insertRunsOf: runs of consecutive insert pieces as position, first piece, end piece.
func insertRunsOf(pieces []piece) []int {
	var insertRuns []int
	logicalIndex := 0
	for pieceIndex := 0; pieceIndex < len(pieces); {
		if pieces[pieceIndex].isBase() {
			logicalIndex += pieces[pieceIndex].length
			pieceIndex++
			continue
		}
		startPiece, startIndex := pieceIndex, logicalIndex
		for pieceIndex < len(pieces) && !pieces[pieceIndex].isBase() {
			logicalIndex += pieces[pieceIndex].length
			pieceIndex++
		}
		insertRuns = append(insertRuns, startIndex, startPiece, pieceIndex)
	}
	return insertRuns
}

// sortToken names one element by its source and source index (tracker.ts sort's order tokens).
type sortToken struct {
	source *insertSource
	index  int
}

// piecesFromOrder is tracker.ts piecesFromSortOrder.
func piecesFromOrder(order []sortToken) []piece {
	var pieces []piece
	for _, token := range order {
		if count := len(pieces); count > 0 && pieces[count-1].source == token.source {
			previous := &pieces[count-1]
			if previous.length == 1 {
				if step := token.index - previous.start; step == 1 || step == -1 {
					previous.step = step
					previous.length = 2
					continue
				}
			} else if previous.sourceIndex(previous.length) == token.index {
				previous.length++
				continue
			}
		}
		pieces = append(pieces, piece{source: token.source, start: token.index, length: 1, step: 1})
	}
	return pieces
}

// appendMergedPiece is tracker.ts appendMergedPiece.
func appendMergedPiece(pieces []piece, p piece) []piece {
	if count := len(pieces); count > 0 && pieces[count-1].source == p.source {
		previous := &pieces[count-1]
		if previous.length == 1 && p.length == 1 {
			if step := p.start - previous.start; step == 1 || step == -1 {
				previous.step = step
				previous.length = 2
				return pieces
			}
		}
		if previous.step == p.step && previous.sourceIndex(previous.length) == p.start {
			previous.length += p.length
			return pieces
		}
	}
	return append(pieces, p)
}

// sameTokenAt is tracker.ts sameSortTokenAt.
func (overlay *arrayOverlay) sameTokenAt(position int, token sortToken) bool {
	p, offset := overlay.locate(position)
	return p.source == token.source && p.sourceIndex(offset) == token.index
}

// deduplicate is tracker.ts deduplicateArrayEntries: an element a sort comparator left in two places becomes an
// independent clone in the later one.
func (n *node) deduplicate() {
	overlay := n.arrayOverlay()
	seen := map[sortToken]bool{}
	var next []piece
	duplicated := false
	for _, p := range overlay.piecesOf() {
		for offset := range p.length {
			token := sortToken{p.source, p.sourceIndex(offset)}
			if !seen[token] {
				seen[token] = true
				next = appendMergedPiece(next, piece{source: p.source, start: token.index, length: 1, step: 1})
				continue
			}
			duplicated = true
			cloned, err := clonePlacementHeld(storedCurrent(n.slot(p, token.index)), n.ctx)
			if err != nil {
				panic(err) // tracked content is strict JSON by construction
			}
			next = appendMergedPiece(next, n.insertPiece([]any{cloned})[0])
		}
	}
	if !duplicated {
		return
	}
	overlay.replaceAllPieces(next)
	n.markStructural()
}
