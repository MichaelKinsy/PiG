package sdk

import (
	"bytes"
	"cmp"
	"math"
	"reflect"
	"slices"
	"strconv"
	"unsafe"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A Go handler holds the objects of an event as map[string]any, which has no member order, and the SDK writes a map in byte order. Pi hands its handlers the
// JavaScript objects themselves and writes what they hold with their members in insertion order. An extension that hands back an object it was sent (the
// entries of a turn boundary, a message of the context, the provider request's payload) must therefore not change the order of its members: the host stores
// the entries and sends the messages and the payload, with their tool-call arguments, to the provider as it reads them (the saved session and the prompt-cache
// prefix change with the order).
//
// echoRoots captures, before the handler runs, the objects of the members of an event that a handler is likely to hand back, so that a handler that drops,
// reorders or replaces a list still leaves them identifiable by identity. sourceOrder is the json.KeyOrder of those objects and of every object below them:
// it reads the member order the host wrote them in from the event's own bytes, only when the reply writes an object.

// echoMembers names, for each event whose result the host reads as data, the members of the event that a handler returns.
var echoMembers = map[string][]string{
	"turn_end":                {"entries"},
	"agent_before_settle":     {"entries"},
	"context":                 {"messages"},
	"context_with_system":     {"messages"},
	"before_provider_request": {"payload"},
	"tool_result":             {"content", "details", "structuredContent"},
	"message_end":             {"message"},
}

// echoRoots are the lists and objects of one event that are written in their member order when a handler hands them back. No event has more than three.
type echoRoots struct {
	list [3]echoRoot
	n    int
}

// echoRoot is one member of the event: the list or the object it held.
type echoRoot struct {
	member string
	value  any
}

// captureEchoRoots returns the roots of data, the decoded event, or nil for an event no handler hands objects back from. contextMessages, when not nil, is the
// copy of the context's message list the caller already made.
func captureEchoRoots(event string, data map[string]any, contextMessages []any) *echoRoots {
	members := echoMembers[event]
	if members == nil {
		return nil
	}
	var roots *echoRoots
	for _, member := range members {
		var value any
		switch held := data[member].(type) {
		case []any:
			if member == "messages" && contextMessages != nil {
				value = contextMessages
			} else {
				value = slices.Clone(held)
			}
		case map[string]any:
			if held == nil {
				continue
			}
			value = held
		default:
			continue
		}
		if roots == nil {
			roots = &echoRoots{}
		}
		roots.list[roots.n] = echoRoot{member, value}
		roots.n++
	}
	return roots
}

// reply is result, written with the member order of the objects the event held. A nil receiver or result is returned as it is.
func (r *echoRoots) reply(result any, src []byte) any {
	if r == nil || result == nil || len(src) == 0 {
		return result
	}
	return orderedReply{result, &sourceOrder{src: src, roots: r.list[:r.n]}}
}

// sourceOrder holds the bytes of an event payload and the roots of its objects. It reads nothing until it is asked the order of an object: a reply that holds
// no object of the event, or no object with two members, never asks.
type sourceOrder struct {
	src   []byte
	roots []echoRoot
	// pending are the roots no walk has read yet, set when the first question is asked.
	pending []pendingRoot
	started bool
	// ranges are the objects whose order is known, as ranges of arena.
	ranges map[uintptr]keyRange
	arena  []string
	// members is the stack of the keys of the objects a walk is in.
	members []member
}

// pendingRoot is a list or an object of the event and the offset of its text in the payload.
type pendingRoot struct {
	value any
	at    int
}

// keyRange is the order of one object: a range of the arena. It holds the object so its address is not reused while the order is in use.
type keyRange struct {
	lo, hi int
	object map[string]any
}

type member struct{ key string }

func mapPointer(m map[string]any) uintptr { return reflect.ValueOf(m).Pointer() }

// Keys implements json.KeyOrder.
func (o *sourceOrder) Keys(m uintptr) ([]string, bool) {
	// An object the reply holds may sit inside an element that is not itself in the reply (a rebuilt message around its old content), so the first question
	// reads every element, and the walk costs no more than the reply's own encoding of them.
	if !o.started {
		o.started = true
		for i, at := range memberStarts(o.src, o.roots) {
			if at >= 0 {
				o.pending = append(o.pending, pendingRoot{o.roots[i].value, at})
			}
		}
	}
	if len(o.pending) > 0 {
		pending := o.pending
		o.pending = nil
		for _, p := range pending {
			o.walk(p.value, p.at)
		}
	}
	r, ok := o.ranges[m]
	if !ok {
		return nil, false
	}
	return o.arena[r.lo:r.hi], true
}

// walk follows value, which was decoded from the JSON text at src[at], through the objects below it, and returns the offset after that text. It records the
// member order of every object it meets (an object is a *map[string]any); a value the handler replaced with another kind of value is skipped over as text.
// The handler may have changed the objects since: a member that is gone is not followed, a member the text does not hold is written after the others, and an
// array the handler shortened or reordered in place is matched to the text by the string members of its elements.
func (o *sourceOrder) walk(value any, at int) int {
	if at >= len(o.src) {
		return at
	}
	switch typed := value.(type) {
	case map[string]any:
		if typed != nil && o.src[at] == '{' {
			return o.walkObject(typed, at)
		}
	case []any:
		if o.src[at] == '[' {
			return o.walkArray(typed, at)
		}
	}
	end, ok := scanValue(o.src, at)
	if !ok {
		return len(o.src)
	}
	return end
}

func (o *sourceOrder) walkObject(object map[string]any, at int) int {
	pointer := mapPointer(object)
	_, done := o.ranges[pointer]
	base := len(o.members)
	defer func() { o.members = o.members[:base] }()
	var seen map[string]struct{}
	i := skipSpace(o.src, at+1)
	for i < len(o.src) && o.src[i] != '}' {
		keyEnd, ok := scanString(o.src, i)
		if !ok {
			return len(o.src)
		}
		key, ok := decodeKey(o.src[i:keyEnd])
		if !ok {
			return len(o.src)
		}
		i = skipSpace(o.src, keyEnd)
		if i >= len(o.src) || o.src[i] != ':' {
			return len(o.src)
		}
		i = skipSpace(o.src, i+1)
		// A repeated key keeps the place of its first occurrence, as JSON.parse keeps it.
		if len(o.members)-base == repeatedKeyScan && seen == nil {
			seen = make(map[string]struct{}, 2*repeatedKeyScan)
			for _, m := range o.members[base:] {
				seen[m.key] = struct{}{}
			}
		}
		repeated := false
		if seen != nil {
			_, repeated = seen[key]
			seen[key] = struct{}{}
		} else {
			repeated = slices.ContainsFunc(o.members[base:], func(m member) bool { return m.key == key })
		}
		if !repeated {
			o.members = append(o.members, member{key})
		}
		if child, present := object[key]; present && !done {
			i = o.walk(child, i)
		} else if end, ok := scanValue(o.src, i); ok {
			i = end
		} else {
			return len(o.src)
		}
		i = skipSpace(o.src, i)
		if i < len(o.src) && o.src[i] == ',' {
			i = skipSpace(o.src, i+1)
		}
	}
	if !done {
		if o.ranges == nil {
			o.ranges = make(map[uintptr]keyRange)
		}
		lo := len(o.arena)
		for _, m := range o.members[base:] {
			o.arena = append(o.arena, m.key)
		}
		o.ranges[pointer] = keyRange{lo, len(o.arena), object}
	}
	return min(i+1, len(o.src))
}

// repeatedKeyScan is how many keys an object may hold before a repeated key is looked up in a set and not by scanning the keys read so far.
const repeatedKeyScan = 16

func (o *sourceOrder) walkArray(array []any, at int) int {
	i := skipSpace(o.src, at+1)
	n := 0
	for ; n < len(array) && i < len(o.src) && o.src[i] != ']'; n++ {
		if !o.agrees(array[n], i) {
			// The handler moved or removed elements of the array in place: match the rest by what they are, not by where they stand.
			return o.walkMovedElements(array, n, at)
		}
		i = o.walk(array[n], i)
		i = skipSpace(o.src, i)
		if i < len(o.src) && o.src[i] == ',' {
			i = skipSpace(o.src, i+1)
		}
	}
	// Elements the text holds and the array no longer does are skipped.
	for i < len(o.src) && o.src[i] != ']' {
		end, ok := scanValue(o.src, i)
		if !ok {
			return len(o.src)
		}
		i = skipSpace(o.src, end)
		if i < len(o.src) && o.src[i] == ',' {
			i = skipSpace(o.src, i+1)
		}
	}
	return min(i+1, len(o.src))
}

// walkMovedElements walks the elements array[from:] of the array whose text starts at src[at], the first from of which stood in their places: each element
// is matched to the first element of the text that no walk has used and that agrees with it.
func (o *sourceOrder) walkMovedElements(array []any, from, at int) int {
	end, ok := scanValue(o.src, at)
	if !ok {
		return len(o.src)
	}
	starts := elementStarts(o.src, at)
	claimed := make([]bool, len(starts))
	for n := 0; n < from && n < len(claimed); n++ {
		claimed[n] = true
	}
	for n := from; n < len(array); n++ {
		for j, start := range starts {
			if !claimed[j] && o.agrees(array[n], start) {
				claimed[j] = true
				o.walk(array[n], start)
				break
			}
		}
	}
	return end
}

// agreesMembers is how many leading string members of an object decide whether it is the object of the text.
const agreesMembers = 4

// agrees reports whether value, an element of an array, can be the one the text at src[at] holds: no string member among the first few of the text differs
// from the same member of value. A value that is not an object, or a text that is not, agrees.
func (o *sourceOrder) agrees(value any, at int) bool {
	object, ok := value.(map[string]any)
	if !ok || at >= len(o.src) || o.src[at] != '{' {
		return true
	}
	i := skipSpace(o.src, at+1)
	for checked := 0; i < len(o.src) && o.src[i] != '}' && checked < agreesMembers; {
		keyEnd, ok := scanString(o.src, i)
		if !ok {
			return true
		}
		key, ok := decodeKey(o.src[i:keyEnd])
		if !ok {
			return true
		}
		i = skipSpace(o.src, keyEnd)
		if i >= len(o.src) || o.src[i] != ':' {
			return true
		}
		i = skipSpace(o.src, i+1)
		end, ok := scanValue(o.src, i)
		if !ok {
			return true
		}
		if o.src[i] == '"' {
			checked++
			literal := o.src[i+1 : end-1]
			if got, present := object[key].(string); present && bytes.IndexByte(literal, '\\') < 0 && got != string(literal) {
				return false
			}
		}
		i = skipSpace(o.src, end)
		if i < len(o.src) && o.src[i] == ',' {
			i = skipSpace(o.src, i+1)
		}
	}
	return true
}

// elementStarts returns the offset of each element of the JSON array that starts at src[at].
func elementStarts(src []byte, at int) []int {
	var starts []int
	i := skipSpace(src, at+1)
	for i < len(src) && src[i] != ']' {
		end, ok := scanValue(src, i)
		if !ok {
			return starts
		}
		starts = append(starts, i)
		i = skipSpace(src, end)
		if i < len(src) && src[i] == ',' {
			i = skipSpace(src, i+1)
		}
	}
	return starts
}

// memberStarts returns, for each root, the offset of the value of its member in the JSON object src, or -1 when src has none.
func memberStarts(src []byte, roots []echoRoot) []int {
	starts := make([]int, len(roots))
	for i := range starts {
		starts[i] = -1
	}
	i := skipSpace(src, 0)
	if i >= len(src) || src[i] != '{' {
		return starts
	}
	found := 0
	i = skipSpace(src, i+1)
	for i < len(src) && src[i] != '}' && found < len(roots) {
		keyEnd, ok := scanString(src, i)
		if !ok {
			return starts
		}
		key := src[i+1 : keyEnd-1]
		i = skipSpace(src, keyEnd)
		if i >= len(src) || src[i] != ':' {
			return starts
		}
		i = skipSpace(src, i+1)
		for n, root := range roots {
			if starts[n] < 0 && string(key) == root.member {
				starts[n] = i
				found++
			}
		}
		if found == len(roots) {
			return starts
		}
		end, ok := scanValue(src, i)
		if !ok {
			return starts
		}
		i = skipSpace(src, end)
		if i < len(src) && src[i] == ',' {
			i = skipSpace(src, i+1)
		}
	}
	return starts
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\n' || b[i] == '\t' || b[i] == '\r') {
		i++
	}
	return i
}

// scanString returns the offset after the string that starts at b[i].
func scanString(b []byte, i int) (int, bool) {
	if i >= len(b) || b[i] != '"' {
		return 0, false
	}
	i++
	// Most strings hold no escape: the first quote ends them.
	if end := bytes.IndexByte(b[i:], '"'); end >= 0 && bytes.IndexByte(b[i:i+end], '\\') < 0 {
		return i + end + 1, true
	}
	for ; i < len(b); i++ {
		switch b[i] {
		case '\\':
			i++
		case '"':
			return i + 1, true
		}
	}
	return 0, false
}

// scanValue returns the offset after the JSON value that starts at b[i].
func scanValue(b []byte, i int) (int, bool) {
	if i >= len(b) {
		return 0, false
	}
	switch b[i] {
	case '"':
		return scanString(b, i)
	case '{', '[':
		depth := 0
		for i < len(b) {
			switch b[i] {
			case '"':
				end, ok := scanString(b, i)
				if !ok {
					return 0, false
				}
				i = end
				continue
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return i + 1, true
				}
			}
			i++
		}
		return 0, false
	}
	for i < len(b) {
		switch b[i] {
		case ',', '}', ']', ' ', '\n', '\t', '\r':
			return i, true
		}
		i++
	}
	return i, true
}

// decodeKey returns the string the JSON string literal holds. A key without an escape aliases the literal's bytes, which an event payload never changes.
func decodeKey(literal []byte) (string, bool) {
	body := literal[1 : len(literal)-1]
	plain := true
	for _, c := range body {
		if c == '\\' {
			plain = false
			break
		}
	}
	if plain {
		if len(body) == 0 {
			return "", true
		}
		//nolint:gosec // G103: an event payload's bytes are never written after the frame is read (socket frames are fresh, fused Args are never written again)
		return unsafe.String(&body[0], len(body)), true
	}
	var key string
	if json.Unmarshal(literal, &key) != nil {
		return "", false
	}
	return key, true
}

// ObjectKeys returns the keys of object in the order Object.keys gives them in Pi. For an object of the event this handler is serving that is the order the
// host wrote its members in, for the members the object still holds, and then the members the handler added, in byte order. A Go map has no order of its own,
// so this is how a Go handler reads the order the host sent; Rust, Python, Lisp and Node objects keep it and need no call. Array-index keys ("0", "1", ...) come first, ascending, as JavaScript
// enumerates them. A map the handler built, or any object of an event whose payload arrived in pieces or whose request has completed, has its keys in
// byte order. A handler that never calls it pays nothing: the order is read from the
// event bytes at the first call.
// pig additive (D273)
func (c Context) ObjectKeys(object map[string]any) []string {
	var known []string
	if c.req != nil && c.req.eventData != nil && len(c.req.Args) > 0 && object != nil {
		c.req.keyMu.Lock()
		if c.req.keyOrder == nil {
			c.req.keyOrder = &sourceOrder{src: c.req.Args, started: true, pending: []pendingRoot{{c.req.eventData, skipSpace(c.req.Args, 0)}}}
		}
		known, _ = c.req.keyOrder.Keys(mapPointer(object))
		known = slices.Clone(known)
		c.req.keyMu.Unlock()
	}
	keys := make([]string, 0, len(object))
	for _, key := range known {
		if _, ok := object[key]; ok {
			keys = append(keys, key)
		}
	}
	if len(keys) < len(object) {
		var rest []string
		for key := range object {
			if !slices.Contains(known, key) {
				rest = append(rest, key)
			}
		}
		slices.Sort(rest)
		keys = append(keys, rest...)
	}
	return arrayIndexKeysFirst(keys)
}

// arrayIndexKeysFirst orders keys as a JavaScript object enumerates them: the array-index keys first, ascending, then the others in the order given.
func arrayIndexKeysFirst(keys []string) []string {
	indexes := 0
	for _, key := range keys {
		if _, ok := arrayIndex(key); ok {
			indexes++
		}
	}
	if indexes == 0 {
		return keys
	}
	ordered := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := arrayIndex(key); ok {
			ordered = append(ordered, key)
		}
	}
	slices.SortStableFunc(ordered, func(a, b string) int {
		x, _ := arrayIndex(a)
		y, _ := arrayIndex(b)
		return cmp.Compare(x, y)
	})
	for _, key := range keys {
		if _, ok := arrayIndex(key); !ok {
			ordered = append(ordered, key)
		}
	}
	return ordered
}

// arrayIndex returns the value of key when it is an array index: a canonical decimal integer below 2^32-1.
func arrayIndex(key string) (uint32, bool) {
	if key == "" || len(key) > 10 || (key[0] == '0' && len(key) > 1) {
		return 0, false
	}
	value, err := strconv.ParseUint(key, 10, 32)
	if err != nil || value == math.MaxUint32 {
		return 0, false
	}
	return uint32(value), true
}
