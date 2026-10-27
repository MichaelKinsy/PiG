package ai

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Incremental, tolerant parsing of JSON text that arrives in pieces, such as streamed tool-call arguments. It ports Pi's
// StreamingJsonParser (packages/ai/src/utils/streaming-json.ts): Append reads each piece once, so a stream costs time and memory
// linear in its length, and Snapshot returns the value of the text read so far. A snapshot never changes once returned: the parser
// copies the containers that are still open (the open path) into every new snapshot and shares completed containers and strings.
// The rules for incomplete text follow ParseStreamingJson:
//
//   - An unterminated string shows the characters read so far; an unterminated escape is held back until complete.
//   - An unterminated container shows the members and elements read so far.
//   - A member whose key or value has not started is omitted.
//   - An unterminated literal shows its value once it is a prefix of true, false or null; an unterminated number shows its value
//     when the digits read so far form a number (an exponent without digits is ignored).
//
// Malformed text is read leniently, the way repairJson and ParseStreamingJson treat it: raw control characters in strings are
// kept, an invalid escape such as \d stays literal text, missing commas and colons are tolerated, values in invalid positions
// and unknown words are dropped, and text after the root value is ignored. The parser never fails. Callers that need the exact
// value of the finished text parse it once, with parseToolCallArguments.
//
// Cost: copying the open path costs its member and element count. Each Append earns copy credit proportional to its length plus
// a constant; Snapshot spends it and returns the previous snapshot while the open path costs more than the credit. So a document
// that keeps a very large container open shows a preview that lags a little behind the text, and no input makes the total work
// grow faster than the text.

// JSONPath is a path from the root value: member keys (string) and array indices (int).
type JSONPath []any

// JSONChange is one change a piece of text made to the value: "set" when a key, an array item or a finished value appears (a
// new string is set once with its first text, a container empty), "append" when the piece extended an open string. Unterminated
// numbers and literals are not reported until they end. A container nested more than 32 levels deep is reported as one set of
// its whole value when it closes, so paths stay short.
type JSONChange struct {
	Kind  string // "set" or "append"
	Path  JSONPath
	Value any    // set
	Text  string // append
}

const (
	jsonChangeSet    = "set"
	jsonChangeAppend = "append"

	// streamingMaxReportedDepth: changes are reported at most this deep, so a path costs O(1) to build.
	streamingMaxReportedDepth = 32
	// Copy credit per append and per appended character. The floor keeps small documents fresh at every snapshot.
	streamingCreditPerAppend    = 256
	streamingCreditPerCharacter = 8
	// An unterminated number longer than this is not shown until it ends, so Snapshot stays cheap.
	streamingMaxPendingScalarLength = 64
)

type streamingSlotKind uint8

const (
	slotRoot streamingSlotKind = iota + 1
	slotMember
	slotElement
)

// streamingSlot is where a value sits in its parent; nil for a value in an invalid position, which is read but dropped.
type streamingSlot struct {
	kind  streamingSlotKind
	key   string
	index int
}

type streamingPhase uint8

const (
	phaseKey streamingPhase = iota
	phaseColon
	phaseValue
	phaseComma
)

// streamingFrame is an open object or array. Its container is never handed out while open; snapshots get copies.
type streamingFrame struct {
	isArray bool
	object  map[string]any
	keys    []string // an object's keys in the order they were first read
	array   []any
	slot    *streamingSlot
	// path is where the container sits when its changes are reported; nil when it is in an invalid position.
	path          JSONPath
	hasPath       bool
	reportsInside bool
	// size is the member or element count: what a copy of the container costs.
	size   int
	key    string
	hasKey bool
	phase  streamingPhase
}

// streamingOpenChange is a change still being extended by the piece being read.
type streamingOpenChange struct {
	kind  string
	path  JSONPath
	value any
	text  string
}

// streamingOpenString is an unterminated string.
type streamingOpenString struct {
	isKey bool
	slot  *streamingSlot
	text  strings.Builder
	// high is a pending high surrogate read from a \u escape (0 for none): a JavaScript string shows it as a lone unit until its
	// low surrogate arrives.
	high rune
	// escape is the escape read so far: "" for none, otherwise a backslash and up to five more characters.
	escape  string
	path    JSONPath
	hasPath bool
	change  *streamingOpenChange
	// validLen is the length of the prefix of text already found valid UTF-8, which ends between two characters.
	validLen int
}

// StreamingJSONParser is the incremental, tolerant JSON parser described above.
type StreamingJSONParser struct {
	root     any
	hasRoot  bool
	done     bool
	stack    []*streamingFrame
	str      *streamingOpenString
	scalar   string
	scalarSl *streamingSlot
	// pathCost is the sum of size+1 over the open path: what a fresh snapshot copies.
	pathCost int
	credit   int
	dirty    bool

	snapshotValue   any
	snapshotDefined bool
	snapshotOrder   schemaObjectOrder
	snapshotVersion uint64
	snapshotValid   bool
	// invalid is set once a finished string or key is not valid UTF-8 (or WTF-8, for a value); every later snapshot is then not
	// valid.
	invalid bool
	// quiet stops the parser from recording the changes Append returns, for a caller that reads only Snapshot.
	quiet bool

	// order holds the member order of completed containers by path.
	order   schemaObjectOrder
	changes []*streamingOpenChange
}

// StreamingJSONSnapshot is the value of the text read so far.
type StreamingJSONSnapshot struct {
	// Value is nil before a value starts (Defined is false); otherwise a plain JSON value that never changes afterwards.
	Value   any
	Defined bool
	// Order is the member order of the objects in Value that are not in sorted order, by path.
	Order schemaObjectOrder
	// valid is true when every string in Value is valid UTF-8 or WTF-8 and every key is valid UTF-8: Value is then what
	// cloneArgumentsValue returns for it (a number that is not finite is copied as it is), so a holder in this package may keep it as
	// the immutable copy of itself.
	valid bool
	// Version changes whenever Snapshot made a fresh copy.
	Version uint64
}

// Append reads the next piece of text and returns the changes it made, in order. It costs time linear in the length of text.
func (p *StreamingJSONParser) Append(text string) []JSONChange {
	p.credit += streamingCreditPerAppend + streamingCreditPerCharacter*jsstring.Length(text)
	p.changes = p.changes[:0]
	if p.str != nil {
		p.str.change = nil
	}
	index := 0
	for index < len(text) && !p.done {
		switch {
		case p.str != nil:
			index = p.readString(p.str, text, index)
		case p.scalar != "":
			index = p.readScalar(text, index)
		default:
			r, size := utf8.DecodeRuneInString(text[index:])
			if r == utf8.RuneError && size == 1 {
				// A byte that is not UTF-8 starts a scalar word that is dropped when it ends, as an unknown character does.
				p.structural(text[index : index+1])
			} else {
				p.structural(text[index : index+size])
			}
			index += size
		}
	}
	if len(p.changes) == 0 {
		return nil
	}
	changes := make([]JSONChange, len(p.changes))
	for i, change := range p.changes {
		changes[i] = JSONChange{Kind: change.kind, Path: change.path, Value: change.value, Text: change.text}
	}
	return changes
}

// Snapshot returns the value of the text read so far. It returns the previous snapshot (same Version) while the text read since
// did not change the value, or while a fresh copy of the open path is not yet paid for.
func (p *StreamingJSONParser) Snapshot() StreamingJSONSnapshot {
	if p.dirty {
		pending := p.pendingScalar()
		cost := p.pathCost
		if pending != nil {
			cost++
		}
		if cost <= p.credit {
			p.credit -= cost
			p.dirty = false
			p.snapshotValue, p.snapshotDefined, p.snapshotOrder = p.copyOpenPath(pending)
			p.snapshotValid = p.valueIsValid()
			p.snapshotVersion++
		}
	}
	return StreamingJSONSnapshot{Value: p.snapshotValue, Defined: p.snapshotDefined, Order: p.snapshotOrder, valid: p.snapshotValid, Version: p.snapshotVersion}
}

// valueIsValid reports whether the snapshot just copied holds only what cloneArgumentsValue copies as it is: every finished string
// valid UTF-8 or WTF-8 (invalid is sticky once one is not) and the string being read valid so far. A number that is not finite is
// copied as it is too, so it does not count.
func (p *StreamingJSONParser) valueIsValid() bool {
	if p.invalid {
		return false
	}
	return p.str == nil || p.str.isKey || p.str.slot == nil || p.str.valid()
}

// readString reads string characters from start until the string ends or the text does; it returns the next index.
func (p *StreamingJSONParser) readString(open *streamingOpenString, text string, start int) int {
	runStart := start
	for index := start; index < len(text); index++ {
		char := text[index]
		if open.escape != "" {
			if p.continueEscape(open, text[index]) {
				runStart = index + 1
				continue
			}
			// Not part of the escape: read it as a normal string character below.
			runStart = index
		}
		if char == '"' {
			if index > runStart {
				p.appendString(open, text[runStart:index])
			}
			p.endString(open)
			return index + 1
		}
		if char == '\\' {
			if index > runStart {
				p.appendString(open, text[runStart:index])
			}
			open.escape = "\\"
			runStart = index + 1
		}
	}
	if len(text) > runStart {
		p.appendString(open, text[runStart:])
	}
	return len(text)
}

// readScalar reads number or literal characters from start up to the next delimiter; it returns the delimiter's index.
func (p *StreamingJSONParser) readScalar(text string, start int) int {
	end := start
	for end < len(text) && !streamingDelimiter(text[end]) {
		end++
	}
	if end > start {
		p.scalar += text[start:end]
		if p.scalarSl != nil {
			p.dirty = true
		}
	}
	if end < len(text) {
		p.endScalar()
	}
	return end
}

// structural handles one character outside strings.
func (p *StreamingJSONParser) structural(char string) {
	switch char {
	case " ", "\t", "\n", "\r":
	case `"`:
		p.startString()
	case "{":
		p.openContainer(false)
	case "[":
		p.openContainer(true)
	case "}", "]":
		p.closeContainer()
	case ":":
		if frame := p.top(); frame != nil && !frame.isArray && frame.phase == phaseColon {
			frame.phase = phaseValue
		}
	case ",":
		if frame := p.top(); frame != nil {
			if frame.isArray {
				frame.phase = phaseValue
			} else {
				frame.phase = phaseKey
			}
		}
	default:
		p.scalar = char
		p.scalarSl = p.valueSlot()
		if p.scalarSl != nil {
			p.dirty = true
		}
	}
}

func (p *StreamingJSONParser) top() *streamingFrame {
	if len(p.stack) == 0 {
		return nil
	}
	return p.stack[len(p.stack)-1]
}

// valueSlot is where a value starting now goes, or nil when this position takes no value.
func (p *StreamingJSONParser) valueSlot() *streamingSlot {
	frame := p.top()
	if frame == nil {
		if !p.hasRoot {
			return &streamingSlot{kind: slotRoot}
		}
		return nil
	}
	if frame.isArray {
		return &streamingSlot{kind: slotElement, index: len(frame.array)}
	}
	// A missing colon is tolerated: {"a" 1} reads as {"a": 1}.
	if (frame.phase == phaseValue || frame.phase == phaseColon) && frame.hasKey {
		return &streamingSlot{kind: slotMember, key: frame.key}
	}
	return nil
}

func (p *StreamingJSONParser) startString() {
	frame := p.top()
	// A missing comma is tolerated: {"a": 1 "b": 2} reads the second member.
	if frame != nil && !frame.isArray && (frame.phase == phaseKey || frame.phase == phaseComma) {
		p.str = &streamingOpenString{isKey: true}
		return
	}
	slot := p.valueSlot()
	path, hasPath := p.reportedPath(slot)
	open := &streamingOpenString{slot: slot, path: path, hasPath: hasPath}
	p.str = open
	p.place(slot, "")
	if hasPath {
		open.change = p.report(&streamingOpenChange{kind: jsonChangeSet, path: path, value: ""})
	}
}

// reportedPath is the path of a value at slot in the innermost open container, when its changes are reported.
func (p *StreamingJSONParser) reportedPath(slot *streamingSlot) (JSONPath, bool) {
	if p.quiet {
		return nil, false
	}
	if slot == nil {
		return nil, false
	}
	if slot.kind == slotRoot {
		return JSONPath{}, true
	}
	frame := p.top()
	if !frame.reportsInside {
		return nil, false
	}
	path := make(JSONPath, len(frame.path), len(frame.path)+1)
	copy(path, frame.path)
	if slot.kind == slotMember {
		path = append(path, slot.key)
	} else {
		path = append(path, slot.index)
	}
	return path, true
}

func (p *StreamingJSONParser) report(change *streamingOpenChange) *streamingOpenChange {
	p.changes = append(p.changes, change)
	return change
}

// continueEscape continues an escape with char; it returns false when char is not part of it and must be read normally.
func (p *StreamingJSONParser) continueEscape(open *streamingOpenString, char byte) bool {
	if open.escape == "\\" {
		if decoded, ok := streamingSimpleEscape(char); ok {
			open.escape = ""
			p.appendString(open, decoded)
			return true
		}
		if char == 'u' {
			open.escape = "\\u"
			return true
		}
		// Invalid escape: keep the backslash as text and read the character normally, as repairJson does.
		open.escape = ""
		p.appendString(open, "\\")
		return false
	}
	if streamingHexDigit(char) {
		open.escape += string(char)
		if len(open.escape) < 6 {
			return true
		}
		code, _ := strconv.ParseUint(open.escape[2:], 16, 16)
		open.escape = ""
		p.appendUnit(open, rune(code))
		return true
	}
	// \u without four hex digits: keep it as text and read the character normally, as repairJson does.
	literal := open.escape
	open.escape = ""
	p.appendString(open, literal)
	return false
}

// appendUnit appends the UTF-16 unit code of a \u escape, joining a surrogate pair into one character. A surrogate is reported as
// its own unit, so a writer that concatenates the changes as JavaScript strings gets the pair.
func (p *StreamingJSONParser) appendUnit(open *streamingOpenString, code rune) {
	switch {
	case code >= 0xD800 && code <= 0xDBFF:
		p.flushHigh(open)
		open.high = code
		p.placeString(open, wtf8Surrogate(code))
	case code >= 0xDC00 && code <= 0xDFFF && open.high != 0:
		joined := 0x10000 + (open.high-0xD800)<<10 + (code - 0xDC00)
		open.high = 0
		open.text.WriteString(string(joined))
		p.placeString(open, wtf8Surrogate(code))
	case code >= 0xDC00 && code <= 0xDFFF:
		p.flushHigh(open)
		open.text.WriteString(wtf8Surrogate(code))
		p.placeString(open, wtf8Surrogate(code))
	default:
		p.flushHigh(open)
		open.text.WriteString(string(code))
		p.placeString(open, string(code))
	}
}

// flushHigh turns a pending high surrogate that no low surrogate followed into text, as a lone unit.
func (p *StreamingJSONParser) flushHigh(open *streamingOpenString) {
	if open.high == 0 {
		return
	}
	open.text.WriteString(wtf8Surrogate(open.high))
	open.high = 0
}

// wtf8Surrogate is the three-byte form of a lone UTF-16 surrogate, the way a Go string holds a JavaScript string that has one.
func wtf8Surrogate(code rune) string {
	return string([]byte{0xE0 | byte(code>>12), 0x80 | byte(code>>6)&0x3F, 0x80 | byte(code)&0x3F})
}

// current is the text of the string so far, a pending high surrogate included.
func (open *streamingOpenString) current() string {
	if open.high == 0 {
		return open.text.String()
	}
	return open.text.String() + wtf8Surrogate(open.high)
}

func (p *StreamingJSONParser) appendString(open *streamingOpenString, text string) {
	if text == "" {
		return
	}
	p.flushHigh(open)
	open.text.WriteString(text)
	if open.isKey {
		return
	}
	p.placeString(open, text)
}

// placeString stores the string's value and records the change text made; text is "" when only a pending surrogate changed.
func (p *StreamingJSONParser) placeString(open *streamingOpenString, text string) {
	if open.isKey {
		return
	}
	p.place(open.slot, open.current())
	if !open.hasPath || text == "" {
		return
	}
	// Extend this string's newest change while nothing came after it; otherwise report an append.
	change := open.change
	if change != nil && len(p.changes) > 0 && p.changes[len(p.changes)-1] == change {
		if change.kind == jsonChangeSet {
			change.value = jsstring.Concat(change.value.(string), text)
		} else {
			change.text = jsstring.Concat(change.text, text)
		}
		return
	}
	open.change = p.report(&streamingOpenChange{kind: jsonChangeAppend, path: open.path, text: text})
}

func (p *StreamingJSONParser) endString(open *streamingOpenString) {
	p.flushHigh(open)
	p.str = nil
	frame := p.top()
	if open.isKey {
		if frame == nil {
			return
		}
		frame.key, frame.hasKey = open.text.String(), true
		frame.phase = phaseColon
		// A key is checked strictly, as clonePlainObject checks it: a lone surrogate in a key is not valid there.
		if !utf8.ValidString(frame.key) {
			p.invalid = true
		}
		return
	}
	if !open.valid() {
		p.invalid = true
	}
	p.valueEnded(frame)
}

// valid reports whether the string read so far is valid UTF-8, or WTF-8 (the form a lone UTF-16 surrogate takes), as
// isWTF8 reports it. It reads only what follows the prefix an earlier call found valid, so checking a growing string at
// every snapshot costs the new text, not the whole string. A pending high surrogate shows as a lone unit, which is not
// strictly valid until its low surrogate arrives, so that text takes the full check.
func (open *streamingOpenString) valid() bool {
	text := open.current()
	if utf8.ValidString(text[open.validLen:]) {
		open.validLen = len(text)
		return true
	}
	return isWTF8(text)
}

func (p *StreamingJSONParser) endScalar() {
	token, slot := p.scalar, p.scalarSl
	p.scalar, p.scalarSl = "", nil
	if value, ok := streamingScalarValue(token); ok {
		p.place(slot, value)
		if path, has := p.reportedPath(slot); has {
			p.report(&streamingOpenChange{kind: jsonChangeSet, path: path, value: value})
		}
	}
	p.valueEnded(p.top())
}

type streamingPending struct {
	slot  *streamingSlot
	value any
}

// pendingScalar is the value of the unterminated scalar at the end of the text, if it shows.
func (p *StreamingJSONParser) pendingScalar() *streamingPending {
	if p.scalarSl == nil || p.scalar == "" || jsstring.Length(p.scalar) > streamingMaxPendingScalarLength {
		return nil
	}
	value, ok := streamingPendingScalarValue(p.scalar)
	if !ok {
		return nil
	}
	return &streamingPending{slot: p.scalarSl, value: value}
}

func (p *StreamingJSONParser) openContainer(isArray bool) {
	slot := p.valueSlot()
	path, hasPath := p.reportedPath(slot)
	frame := &streamingFrame{isArray: isArray, slot: slot, path: path, hasPath: hasPath, phase: phaseKey}
	frame.reportsInside = hasPath && len(path) < streamingMaxReportedDepth
	if isArray {
		frame.phase = phaseValue
	} else {
		frame.object = map[string]any{}
	}
	if isArray {
		p.place(slot, &frame.array)
	} else {
		p.place(slot, frame.object)
	}
	p.stack = append(p.stack, frame)
	p.pathCost++
	p.dirty = true
	// A fresh container: the working one keeps changing, and a change never does.
	if hasPath {
		if isArray {
			p.report(&streamingOpenChange{kind: jsonChangeSet, path: path, value: []any{}})
		} else {
			p.report(&streamingOpenChange{kind: jsonChangeSet, path: path, value: map[string]any{}})
		}
	}
}

// orderPathOf is the schemaObjectOrder path of frame, which sits above the frames of the stack that are open: the paths of its
// ancestors, read from their slots. It is built when an object closes with keys out of order, never per level while opening, so
// nesting costs time linear in its depth.
func (p *StreamingJSONParser) orderPathOf(frame *streamingFrame) string {
	path := ""
	for _, ancestor := range p.stack {
		path = frameSlotPath(path, ancestor.slot)
	}
	return frameSlotPath(path, frame.slot)
}

// orderPathAt is orderPathOf for the open frame at depth in the stack.
func (p *StreamingJSONParser) orderPathAt(depth int) string {
	path := ""
	for _, ancestor := range p.stack[:depth+1] {
		path = frameSlotPath(path, ancestor.slot)
	}
	return path
}

// frameSlotPath is the path of a container at slot inside a container at parent; "" for a container in an invalid position.
func frameSlotPath(parent string, slot *streamingSlot) string {
	switch {
	case slot == nil, slot.kind == slotRoot:
		return ""
	case slot.kind == slotMember:
		return schemaPath(parent, slot.key)
	}
	return schemaPath(parent, strconv.Itoa(slot.index))
}

func (p *StreamingJSONParser) closeContainer() {
	if len(p.stack) == 0 {
		// A stray closer outside any container is ignored.
		return
	}
	frame := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	p.pathCost -= frame.size + 1
	p.dirty = true
	if frame.slot != nil {
		if frame.isArray {
			// The array was placed by reference while open; completed, it is the slice itself.
			p.replacePlaced(frame.slot, frame.array)
		} else if keys := unsortedKeyOrder(frame.keys); keys != nil {
			if p.order == nil {
				p.order = schemaObjectOrder{}
			}
			p.order[p.orderPathOf(frame)] = keys
		}
	}
	// Too deep to report inside: the whole value, final now that it is closed.
	if frame.hasPath && !frame.reportsInside {
		if frame.isArray {
			p.report(&streamingOpenChange{kind: jsonChangeSet, path: frame.path, value: frame.array})
		} else {
			p.report(&streamingOpenChange{kind: jsonChangeSet, path: frame.path, value: frame.object})
		}
	}
	p.valueEnded(p.top())
}

// valueEnded: a value in frame, or the root value when there is no frame, has ended.
func (p *StreamingJSONParser) valueEnded(frame *streamingFrame) {
	if frame == nil {
		// Text after the root value is ignored, but only once a root value exists.
		if p.hasRoot {
			p.done = true
		}
		return
	}
	frame.phase = phaseComma
}

// place sets the value at slot in the open path; nothing for a value in an invalid position. An array being opened is passed as
// a pointer to the frame's slice, which keeps the placed value current as elements arrive.
func (p *StreamingJSONParser) place(slot *streamingSlot, value any) {
	if slot == nil {
		return
	}
	p.dirty = true
	if slot.kind == slotRoot {
		p.root, p.hasRoot = value, true
		return
	}
	frame := p.top()
	if slot.kind == slotElement {
		if slot.index == len(frame.array) {
			frame.size++
			p.pathCost++
			frame.array = append(frame.array, value)
		} else {
			frame.array[slot.index] = value
		}
		return
	}
	if _, exists := frame.object[slot.key]; !exists {
		frame.size++
		p.pathCost++
		frame.keys = append(frame.keys, slot.key)
	} else if p.order != nil {
		// A repeated key keeps its position but takes the last value, whose own member order replaces the earlier value's.
		p.order.dropSubtree(schemaPath(p.orderPathAt(len(p.stack)-1), slot.key))
	}
	frame.object[slot.key] = value
}

// replacePlaced stores the final slice of a closed array where its placeholder, a pointer to the frame's slice, was placed.
func (p *StreamingJSONParser) replacePlaced(slot *streamingSlot, array []any) {
	switch slot.kind {
	case slotRoot:
		p.root = array
	case slotMember:
		if parent := p.top(); parent != nil {
			parent.object[slot.key] = array
		}
	case slotElement:
		if parent := p.top(); parent != nil && slot.index < len(parent.array) {
			parent.array[slot.index] = array
		}
	}
}

// copyOpenPath is a fresh snapshot: copies of the open containers, innermost first, each holding the copy of its open child.
func (p *StreamingJSONParser) copyOpenPath(pending *streamingPending) (any, bool, schemaObjectOrder) {
	if len(p.stack) == 0 {
		if pending != nil && pending.slot.kind == slotRoot {
			return pending.value, true, p.order
		}
		return p.root, p.hasRoot, p.order
	}
	order := maps.Clone(p.order)
	var child any
	var childSlot *streamingSlot
	for depth, frame := range slices.Backward(p.stack) {

		var copyValue any
		if frame.isArray {
			elements := make([]any, len(frame.array), len(frame.array)+1)
			copy(elements, frame.array)
			if depth == len(p.stack)-1 && pending != nil {
				elements = setSlotArray(elements, pending.slot, pending.value)
			}
			if child != nil && childSlot != nil {
				elements = setSlotArray(elements, childSlot, child)
			}
			copyValue = elements
		} else {
			members := make(map[string]any, len(frame.object)+1)
			maps.Copy(members, frame.object)
			keys := frame.keys
			if depth == len(p.stack)-1 && pending != nil {
				keys = setSlotObject(members, keys, pending.slot, pending.value)
			}
			if child != nil && childSlot != nil {
				keys = setSlotObject(members, keys, childSlot, child)
			}
			if frame.slot != nil {
				// The path is built only when this object's order matters: it costs its depth, which nested objects would pay at every level.
				if unsorted := unsortedKeyOrder(keys); unsorted != nil {
					if order == nil {
						order = schemaObjectOrder{}
					}
					order[p.orderPathAt(depth)] = unsorted
				} else if len(order) > 0 {
					delete(order, p.orderPathAt(depth))
				}
			}
			copyValue = members
		}
		child, childSlot = copyValue, frame.slot
	}
	// The outermost open container is the root unless it sits in an invalid position.
	if childSlot != nil && childSlot.kind == slotRoot {
		return child, true, order
	}
	return p.root, p.hasRoot, p.order
}

func setSlotArray(elements []any, slot *streamingSlot, value any) []any {
	if slot.kind != slotElement {
		return elements
	}
	for len(elements) <= slot.index {
		elements = append(elements, nil)
	}
	elements[slot.index] = value
	return elements
}

func setSlotObject(members map[string]any, keys []string, slot *streamingSlot, value any) []string {
	if slot.kind != slotMember {
		return keys
	}
	if _, exists := members[slot.key]; !exists {
		keys = append(keys[:len(keys):len(keys)], slot.key)
	}
	members[slot.key] = value
	return keys
}

func streamingSimpleEscape(char byte) (string, bool) {
	switch char {
	case '"':
		return `"`, true
	case '\\':
		return `\`, true
	case '/':
		return "/", true
	case 'b':
		return "\b", true
	case 'f':
		return "\f", true
	case 'n':
		return "\n", true
	case 'r':
		return "\r", true
	case 't':
		return "\t", true
	}
	return "", false
}

func streamingDelimiter(char byte) bool {
	switch char {
	case ' ', '\t', '\n', '\r', ',', ':', '{', '}', '[', ']', '"':
		return true
	}
	return false
}

func streamingHexDigit(char byte) bool {
	return (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')
}

// streamingNumber is NUMBER: -?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?, as a complete token.
func streamingNumber(token string) bool {
	i := 0
	if i < len(token) && token[i] == '-' {
		i++
	}
	if i >= len(token) {
		return false
	}
	switch {
	case token[i] == '0':
		i++
	case token[i] >= '1' && token[i] <= '9':
		for i < len(token) && token[i] >= '0' && token[i] <= '9' {
			i++
		}
	default:
		return false
	}
	if i < len(token) && token[i] == '.' {
		i++
		start := i
		for i < len(token) && token[i] >= '0' && token[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	if i < len(token) && (token[i] == 'e' || token[i] == 'E') {
		i++
		if i < len(token) && (token[i] == '+' || token[i] == '-') {
			i++
		}
		start := i
		for i < len(token) && token[i] >= '0' && token[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	return i == len(token)
}

func streamingNumberValue(token string) float64 {
	value, err := strconv.ParseFloat(token, 64)
	if err != nil {
		// Out of range: ParseFloat returns the infinity or zero JavaScript's Number() does.
		return value
	}
	return value
}

// streamingScalarValue is the value of a complete number or literal token; false for anything else, which is dropped.
func streamingScalarValue(token string) (any, bool) {
	switch token {
	case "true":
		return true, true
	case "false":
		return false, true
	case "null":
		return nil, true
	}
	if streamingNumber(token) {
		return streamingNumberValue(token), true
	}
	return nil, false
}

// streamingPendingScalarValue is the value an unterminated number or literal token shows.
func streamingPendingScalarValue(token string) (any, bool) {
	for _, literal := range []string{"true", "false", "null"} {
		if strings.HasPrefix(literal, token) {
			value, _ := streamingScalarValue(literal)
			return value, true
		}
	}
	if streamingNumber(token) {
		return streamingNumberValue(token), true
	}
	// An exponent without digits is ignored.
	if n := len(token); n > 0 {
		mantissa := token
		if token[n-1] == '+' || token[n-1] == '-' {
			mantissa = token[:n-1]
		}
		if len(mantissa) > 0 && (mantissa[len(mantissa)-1] == 'e' || mantissa[len(mantissa)-1] == 'E') {
			mantissa = mantissa[:len(mantissa)-1]
			if streamingNumber(mantissa) {
				return streamingNumberValue(mantissa), true
			}
		}
	}
	return nil, false
}

// ToolCallArgumentsStream is the arguments of one streaming tool call: the raw text, for the one full parse when the call ends, and
// a StreamingJSONParser for the partial arguments in between (Pi's ToolCallArgumentsStream). Providers keep one per call in their own
// stream state, never on the content block, so the raw text never reaches partial messages or transcripts.
type ToolCallArgumentsStream struct {
	text      strings.Builder
	parser    StreamingJSONParser
	version   uint64
	arguments JsonObject
	order     schemaObjectOrder
	// valid is whether arguments are immutable (see immutable).
	valid bool
}

// NewToolCallArgumentsStream returns an empty stream; its partial arguments are the empty object.
func NewToolCallArgumentsStream() *ToolCallArgumentsStream {
	return &ToolCallArgumentsStream{arguments: JsonObject{}, valid: true}
}

// Text is the raw argument text received so far.
func (stream *ToolCallArgumentsStream) Text() string { return stream.text.String() }

// Append adds a piece of argument text and returns the partial arguments: a new object whenever they changed.
func (stream *ToolCallArgumentsStream) Append(delta string) (JsonObject, schemaObjectOrder) {
	stream.text.WriteString(delta)
	// Only the snapshot is read, so the parser need not record the changes each piece made.
	stream.parser.quiet = true
	stream.parser.Append(delta)
	return stream.partial()
}

// Replace takes the provider's complete argument text. When it continues the text so far, it returns the missing piece as delta
// (hasDelta true); otherwise it starts over with text. Either way it returns the partial arguments of text.
func (stream *ToolCallArgumentsStream) Replace(text string) (delta string, hasDelta bool, arguments JsonObject, order schemaObjectOrder) {
	if current := stream.text.String(); strings.HasPrefix(text, current) {
		delta = text[len(current):]
		if delta == "" {
			arguments, order = stream.partial()
		} else {
			arguments, order = stream.Append(delta)
		}
		return delta, true, arguments, order
	}
	stream.text.Reset()
	stream.parser = StreamingJSONParser{}
	stream.version, stream.arguments, stream.order, stream.valid = 0, JsonObject{}, nil, true
	arguments, order = stream.Append(text)
	return "", false, arguments, order
}

// Record adds argument text whose arguments the caller already has, without reading it as a delta.
func (stream *ToolCallArgumentsStream) Record(text string) { stream.text.WriteString(text) }

// Finish is the final arguments: one full parse of the complete text, which reports malformed arguments.
func (stream *ToolCallArgumentsStream) Finish() (JsonObject, schemaObjectOrder, string) {
	return parseToolCallArguments(stream.text.String())
}

func (stream *ToolCallArgumentsStream) partial() (JsonObject, schemaObjectOrder) {
	snapshot := stream.parser.Snapshot()
	if snapshot.Version == stream.version {
		return stream.arguments, stream.order
	}
	stream.version = snapshot.Version
	// Tool arguments are an object; any other partial value shows as no arguments yet.
	if object, ok := snapshot.Value.(map[string]any); ok && snapshot.Defined {
		stream.arguments, stream.order, stream.valid = JsonObject(object), snapshot.Order, snapshot.valid
	} else {
		stream.arguments, stream.order, stream.valid = JsonObject{}, nil, true
	}
	return stream.arguments, stream.order
}

// immutable reports whether the arguments the last Append or Replace returned are the parser's own snapshot and already what
// cloneJSONValue copies them to (valid strings, finite numbers): a holder that never changes them may share them instead of
// copying them again.
func (stream *ToolCallArgumentsStream) immutable() bool { return stream.valid }
