package delta

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// Ports packages/chord/src/delta/diff.ts.
//
// Container identity (JavaScript ===) is the identity of a Go map, or of a Go slice header over one backing array. Array alignment uses it first because the tracker and revision sources share unchanged containers between revisions. A zero-length slice has no usable identity (Go gives zero-size allocations one shared address), so any two empty arrays compare as the same container; Pi treats two separately built empty arrays as distinct, so a batch that moves empty arrays may take a different, equally valid operation shape.

const (
	defaultOverlapScan    = 65_536
	maxDeltaOperations    = 4_096
	maxIdentityCandidates = 200_000
	maxSemanticCells      = 65_536
	snapshotCostThreshold = 65_536
)

// diffState collects one batch. Past maxDeltaOperations it stops emitting and the batch becomes a root replacement.
type diffState struct {
	operations []Op
	overflowed bool
}

func (d *diffState) emit(operation Op) {
	if d.overflowed {
		return
	}
	if len(d.operations) >= maxDeltaOperations {
		d.overflowed = true
		return
	}
	d.operations = append(d.operations, operation)
}

func (d *diffState) emitSet(path Path, value JsonValue) {
	if len(path) == 0 {
		d.emit(Op{"r", value})
		return
	}
	d.emit(Op{"s", path, value})
}

func appendPath(path Path, segment Seg) Path {
	out := make(Path, len(path)+1)
	copy(out, path)
	out[len(path)] = segment
	return out
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// strictEqual is JavaScript ===: equal primitives, or the same container.
func strictEqual(left, right JsonValue) bool {
	leftContainer, rightContainer := isContainer(left), isContainer(right)
	if leftContainer || rightContainer {
		if !leftContainer || !rightContainer {
			return false
		}
		leftID, leftTracked := identityOf(left)
		rightID, rightTracked := identityOf(right)
		return leftTracked == rightTracked && leftID == rightID && sameKind(left, right)
	}
	if leftNumber, ok := number(left); ok {
		rightNumber, ok := number(right)
		return ok && leftNumber == rightNumber
	}
	return left == right
}

func sameKind(left, right JsonValue) bool {
	_, leftArray := left.([]any)
	_, rightArray := right.([]any)
	return leftArray == rightArray
}

// equalJson is deep structural equality; object key order and number representation do not matter.
func equalJson(left, right JsonValue) bool {
	if strictEqual(left, right) {
		return true
	}
	switch typed := left.(type) {
	case []any:
		other, ok := right.([]any)
		if !ok || len(typed) != len(other) {
			return false
		}
		for at := range typed {
			if !equalJson(typed[at], other[at]) {
				return false
			}
		}
		return true
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || len(typed) != len(other) {
			return false
		}
		for key, value := range typed {
			counterpart, exists := other[key]
			if !exists || !equalJson(value, counterpart) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func sameValue(left, right JsonValue) bool { return strictEqual(left, right) || equalJson(left, right) }

// valueKey is a map key with the identity semantics of a JavaScript Map keyed by JSON values.
type valueKey struct {
	kind      byte // 0 primitive, 1 array, 2 object
	container identity
	primitive any
}

func keyOf(value JsonValue) valueKey {
	switch value.(type) {
	case []any:
		id, _ := identityOf(value)
		return valueKey{kind: 1, container: id}
	case map[string]any:
		id, _ := identityOf(value)
		return valueKey{kind: 2, container: id}
	}
	if n, ok := number(value); ok {
		return valueKey{primitive: n}
	}
	return valueKey{primitive: value}
}

// permutation returns the order that rearranges before into after, or nil when the arrays are not rearrangements of the same elements (by identity or primitive value).
func permutation(before, after []any) []any {
	if len(before) != len(after) {
		return nil
	}
	type slot struct {
		indices []int
		used    int
	}
	positions := map[valueKey]*slot{}
	for at, value := range before {
		key := keyOf(value)
		entry, exists := positions[key]
		if !exists {
			entry = &slot{}
			positions[key] = entry
		}
		entry.indices = append(entry.indices, at)
	}
	result := make([]any, len(after))
	for at, value := range after {
		entry, exists := positions[keyOf(value)]
		if !exists || entry.used == len(entry.indices) {
			return nil
		}
		result[at] = entry.indices[entry.used]
		entry.used++
	}
	return result
}

// copyItems is a non-nil copy of a slice of elements: an empty batch of items must stay a JSON array.
func copyItems(items []any) []any { return append(make([]any, 0, len(items)), items...) }

func (d *diffState) emitString(before, after string, path Path) {
	if before == after {
		return
	}
	if len(after) > len(before) && strings.HasPrefix(after, before) {
		d.emit(Op{"a", path, after[len(before):]})
		return
	}
	shared := Overlap(before, after, defaultOverlapScan)
	if shared == 0 {
		d.emit(Op{"s", path, after})
		return
	}
	d.emit(Op{"t", path, utf16Len(before) - shared})
	if utf16Len(after) > shared {
		d.emit(Op{"a", path, utf16Suffix(after, shared)})
	}
}

type arrayMatch [2]int

type matchCandidate struct{ before, after, previous int }

// lcsMatches is a longest common subsequence under equal, or nil when the table would exceed maxCells.
func lcsMatches(before, after []any, equal func(left, right JsonValue) bool, maxCells int) ([]arrayMatch, bool) {
	if len(before) == 0 || len(after) == 0 {
		return nil, true
	}
	if len(before) > maxCells/len(after) || len(before) >= math.MaxInt32 || len(after) >= math.MaxInt32 {
		return nil, false
	}
	width := len(after) + 1
	lengths := make([]uint32, (len(before)+1)*width)
	for left, b := range slices.Backward(before) {
		for right, a := range slices.Backward(after) {
			at := left*width + right
			if equal(b, a) {
				lengths[at] = lengths[(left+1)*width+right+1] + 1
			} else {
				lengths[at] = max(lengths[(left+1)*width+right], lengths[left*width+right+1])
			}
		}
	}
	var matches []arrayMatch
	left, right := 0, 0
	for left < len(before) && right < len(after) {
		switch {
		case equal(before[left], after[right]) && lengths[left*width+right] == lengths[(left+1)*width+right+1]+1:
			matches = append(matches, arrayMatch{left, right})
			left++
			right++
		case lengths[(left+1)*width+right] >= lengths[left*width+right+1]:
			left++
		default:
			right++
		}
	}
	return matches, true
}

// semanticallyAligned reports whether two elements are the same value or share a container child at the same position, which marks an edited row rather than an unrelated one.
func semanticallyAligned(left, right JsonValue) bool {
	if sameValue(left, right) {
		return true
	}
	switch typed := left.(type) {
	case []any:
		other, ok := right.([]any)
		if !ok || len(typed) != len(other) {
			return false
		}
		for at, value := range typed {
			if isContainer(value) && strictEqual(value, other[at]) {
				return true
			}
		}
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range typed {
			counterpart, exists := other[key]
			if isContainer(value) && exists && strictEqual(value, counterpart) {
				return true
			}
		}
	}
	return false
}

func lowerBound(values []int, value int) int {
	low, high := 0, len(values)
	for low < high {
		middle := (low + high) / 2
		if values[middle] < value {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low
}

// identitySubsequence matches the shorter side as an in-order subsequence of the longer side by identity, or returns false.
func identitySubsequence(before []any, beforeStart, beforeEnd int, after []any, afterStart, afterEnd int) ([]arrayMatch, bool) {
	beforeCount, afterCount := beforeEnd-beforeStart, afterEnd-afterStart
	var matches []arrayMatch
	switch {
	case afterCount < beforeCount:
		beforeIndex := beforeStart
		for afterIndex := afterStart; afterIndex < afterEnd; afterIndex++ {
			for beforeIndex < beforeEnd && !strictEqual(before[beforeIndex], after[afterIndex]) {
				beforeIndex++
			}
			if beforeIndex == beforeEnd {
				return nil, false
			}
			matches = append(matches, arrayMatch{beforeIndex, afterIndex})
			beforeIndex++
		}
		return matches, true
	case beforeCount < afterCount:
		afterIndex := afterStart
		for beforeIndex := beforeStart; beforeIndex < beforeEnd; beforeIndex++ {
			for afterIndex < afterEnd && !strictEqual(before[beforeIndex], after[afterIndex]) {
				afterIndex++
			}
			if afterIndex == afterEnd {
				return nil, false
			}
			matches = append(matches, arrayMatch{beforeIndex, afterIndex})
			afterIndex++
		}
		return matches, true
	default:
		return nil, false
	}
}

func greedyIdentityAnchors(positions map[valueKey][]int, after []any, afterStart, afterEnd int) []arrayMatch {
	var matches []arrayMatch
	previous := -1
	for afterIndex := afterStart; afterIndex < afterEnd; afterIndex++ {
		candidates, exists := positions[keyOf(after[afterIndex])]
		if !exists {
			continue
		}
		at := lowerBound(candidates, previous+1)
		if at >= len(candidates) {
			continue
		}
		matches = append(matches, arrayMatch{candidates[at], afterIndex})
		previous = candidates[at]
	}
	return matches
}

// identityAnchors is a longest increasing subsequence over identity matches.
func identityAnchors(before []any, beforeStart, beforeEnd int, after []any, afterStart, afterEnd int) []arrayMatch {
	positions := map[valueKey][]int{}
	for at := beforeStart; at < beforeEnd; at++ {
		key := keyOf(before[at])
		positions[key] = append(positions[key], at)
	}
	candidateCount := 0
	for at := afterStart; at < afterEnd; at++ {
		candidateCount += len(positions[keyOf(after[at])])
		if candidateCount > maxIdentityCandidates {
			return greedyIdentityAnchors(positions, after, afterStart, afterEnd)
		}
	}
	if candidateCount == 0 {
		return nil
	}
	var candidates []matchCandidate
	var tails, tailValues []int
	for afterIndex := afterStart; afterIndex < afterEnd; afterIndex++ {
		beforePositions, exists := positions[keyOf(after[afterIndex])]
		if !exists {
			continue
		}
		for _, beforeIndex := range slices.Backward(beforePositions) {

			slot := lowerBound(tailValues, beforeIndex)
			previous := -1
			if slot > 0 {
				previous = tails[slot-1]
			}
			candidateIndex := len(candidates)
			candidates = append(candidates, matchCandidate{before: beforeIndex, after: afterIndex, previous: previous})
			if slot == len(tails) {
				tails = append(tails, candidateIndex)
				tailValues = append(tailValues, beforeIndex)
			} else {
				tails[slot] = candidateIndex
				tailValues[slot] = beforeIndex
			}
		}
	}
	var matches []arrayMatch
	candidateIndex := -1
	if len(tails) > 0 {
		candidateIndex = tails[len(tails)-1]
	}
	for candidateIndex >= 0 {
		candidate := candidates[candidateIndex]
		matches = append(matches, arrayMatch{candidate.before, candidate.after})
		candidateIndex = candidate.previous
	}
	slices.Reverse(matches)
	return matches
}

type region struct{ beforeStart, beforeEnd, afterStart, afterEnd, outputStart int }

func (d *diffState) processArrayMatches(before, after []any, path Path, bounds region, matches []arrayMatch) {
	beforeAt, afterAt, outputAt := bounds.beforeStart, bounds.afterStart, bounds.outputStart
	for _, match := range matches {
		if d.overflowed {
			return
		}
		beforeMatch, afterMatch := match[0], match[1]
		d.diffArrayRegion(before, after, path, region{beforeAt, beforeMatch, afterAt, afterMatch, outputAt})
		outputAt += afterMatch - afterAt
		if !sameValue(before[beforeMatch], after[afterMatch]) {
			d.diffValue(before[beforeMatch], after[afterMatch], appendPath(path, outputAt))
		}
		outputAt++
		beforeAt, afterAt = beforeMatch+1, afterMatch+1
	}
	if !d.overflowed {
		d.diffArrayRegion(before, after, path, region{beforeAt, bounds.beforeEnd, afterAt, bounds.afterEnd, outputAt})
	}
}

func (d *diffState) diffArrayRegion(before, after []any, path Path, bounds region) {
	if d.overflowed {
		return
	}
	beforeStart, beforeEnd, afterStart, afterEnd, outputStart := bounds.beforeStart, bounds.beforeEnd, bounds.afterStart, bounds.afterEnd, bounds.outputStart
	for beforeStart < beforeEnd && afterStart < afterEnd && sameValue(before[beforeStart], after[afterStart]) {
		beforeStart++
		afterStart++
		outputStart++
	}
	for beforeStart < beforeEnd && afterStart < afterEnd && sameValue(before[beforeEnd-1], after[afterEnd-1]) {
		beforeEnd--
		afterEnd--
	}
	beforeCount, afterCount := beforeEnd-beforeStart, afterEnd-afterStart
	if beforeCount == 0 && afterCount == 0 {
		return
	}
	if beforeCount == 0 || afterCount == 0 {
		d.emit(Op{"p", path, outputStart, beforeCount, copyItems(after[afterStart:afterEnd])})
		return
	}
	narrowed := region{beforeStart, beforeEnd, afterStart, afterEnd, outputStart}

	if beforeCount == afterCount {
		var positional []arrayMatch
		for offset := range beforeCount {
			if sameValue(before[beforeStart+offset], after[afterStart+offset]) {
				positional = append(positional, arrayMatch{beforeStart + offset, afterStart + offset})
			}
		}
		if len(positional) > 0 {
			d.processArrayMatches(before, after, path, narrowed, positional)
			return
		}
	}
	if subsequence, ok := identitySubsequence(before, beforeStart, beforeEnd, after, afterStart, afterEnd); ok && len(subsequence) > 0 {
		d.processArrayMatches(before, after, path, narrowed, subsequence)
		return
	}
	if identity := identityAnchors(before, beforeStart, beforeEnd, after, afterStart, afterEnd); len(identity) > 0 {
		d.processArrayMatches(before, after, path, narrowed, identity)
		return
	}
	if semantic, ok := lcsMatches(before[beforeStart:beforeEnd], after[afterStart:afterEnd], semanticallyAligned, maxSemanticCells); ok && len(semantic) > 0 {
		absolute := make([]arrayMatch, len(semantic))
		for at, match := range semantic {
			absolute[at] = arrayMatch{beforeStart + match[0], afterStart + match[1]}
		}
		d.processArrayMatches(before, after, path, narrowed, absolute)
		return
	}
	if beforeCount == 1 && afterCount == 1 {
		d.diffValue(before[beforeStart], after[afterStart], appendPath(path, outputStart))
		return
	}
	d.emit(Op{"p", path, outputStart, beforeCount, copyItems(after[afterStart:afterEnd])})
}

func (d *diffState) diffArray(before, after []any, path Path) {
	if equalJson(before, after) {
		return
	}
	if len(before) == len(after) && len(before) > 1 &&
		!sameValue(before[0], after[0]) && !sameValue(before[len(before)-1], after[len(after)-1]) {
		if order := permutation(before, after); order != nil {
			d.emit(Op{"m", path, order})
			return
		}
	}
	d.diffArrayRegion(before, after, path, region{0, len(before), 0, len(after), 0})
}

func (d *diffState) diffObject(before, after map[string]any, path Path) {
	beforeKeys, afterKeys := sortedKeys(before), sortedKeys(after)
	for _, keys := range [2][]string{beforeKeys, afterKeys} {
		for _, key := range keys {
			if ReservedSegments[key] {
				if !equalJson(before, after) {
					d.emitSet(path, after)
				}
				return
			}
		}
	}
	for _, key := range afterKeys {
		if d.overflowed {
			return
		}
		if previous, exists := before[key]; exists {
			d.diffValue(previous, after[key], appendPath(path, key))
		} else {
			d.emitSet(appendPath(path, key), after[key])
		}
	}
	for _, key := range beforeKeys {
		if d.overflowed {
			return
		}
		if _, exists := after[key]; !exists {
			d.emit(Op{"d", appendPath(path, key)})
		}
	}
}

func (d *diffState) diffValue(before, after JsonValue, path Path) {
	if d.overflowed || strictEqual(before, after) {
		return
	}
	if beforeText, ok := before.(string); ok {
		if afterText, ok := after.(string); ok && len(path) > 0 {
			d.emitString(beforeText, afterText, path)
			return
		}
	}
	if beforeArray, ok := before.([]any); ok {
		if afterArray, ok := after.([]any); ok {
			d.diffArray(beforeArray, afterArray, path)
			return
		}
	}
	if beforeObject, ok := before.(map[string]any); ok {
		if afterObject, ok := after.(map[string]any); ok {
			d.diffObject(beforeObject, afterObject, path)
			return
		}
	}
	d.emitSet(path, after)
}

// jsonCost estimates the serialized size of a value, as characters.
func jsonCost(value JsonValue) int {
	switch typed := value.(type) {
	case nil:
		return 4
	case string:
		return utf16Len(typed) + 2
	case bool:
		if typed {
			return 4
		}
		return 5
	case []any:
		cost := 2
		for at, item := range typed {
			cost += jsonCost(item)
			if at > 0 {
				cost++
			}
		}
		return cost
	case map[string]any:
		cost := 2
		first := true
		for key, item := range typed {
			cost += utf16Len(key) + 3 + jsonCost(item)
			if !first {
				cost++
			}
			first = false
		}
		return cost
	}
	if n, ok := number(value); ok {
		return len(strconv.FormatFloat(n, 'g', -1, 64))
	}
	return 0
}

func pathCost(path Path) int {
	cost := 2
	for at, segment := range path {
		if text, ok := segment.(string); ok {
			cost += utf16Len(text) + 2
		} else {
			cost += len(segmentText(segment))
		}
		if at > 0 {
			cost++
		}
	}
	return cost
}

func operationCost(operation Op) int {
	switch operation.Verb() {
	case "r":
		return 6 + jsonCost(operation[1])
	case "s":
		return 7 + pathCost(operation.Path()) + jsonCost(operation[2])
	case "d":
		return 6 + pathCost(operation.Path())
	case "a":
		return 7 + pathCost(operation.Path()) + utf16Len(operation[2].(string)) + 2
	case "t":
		return 7 + pathCost(operation.Path()) + len(segmentText(operation[2]))
	case "p":
		return 10 + pathCost(operation.Path()) + len(segmentText(operation[2])) + len(segmentText(operation[3])) + jsonCost(operation[4])
	default:
		return 7 + pathCost(operation.Path()) + jsonCost(operation[2])
	}
}

// DiffRevisions computes a compact operation batch from two immutable JSON revisions. Operations for object keys visit keys in ascending order. A batch of more than 4096 operations, or one whose estimated size reaches the size of the whole value, becomes a root replacement.
func DiffRevisions(before, after JsonValue) []Op {
	d := &diffState{operations: []Op{}}
	d.diffValue(before, after, Path{})
	if d.overflowed {
		return []Op{{"r", after}}
	}
	if len(d.operations) == 0 || d.operations[0].Verb() == "r" {
		return d.operations
	}
	deltaCost := 2
	for _, operation := range d.operations {
		deltaCost += operationCost(operation) + 1
	}
	if deltaCost < snapshotCostThreshold {
		return d.operations
	}
	if deltaCost >= jsonCost(after)+6 {
		return []Op{{"r", after}}
	}
	return d.operations
}
