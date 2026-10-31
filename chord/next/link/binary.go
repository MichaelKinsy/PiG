package link

import (
	"fmt"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/link/binary.ts. The binary codec (2b): a kind byte (its high bit set when an origin
// follows as a varint), then the kind's fields as unsigned varints, length-prefixed UTF-8 strings, raw incarnation (16
// bytes) and fingerprint (8 bytes), parts as length-prefixed bytes whose prefix also says whether the part is deflated
// (decoded as views of the message, never parsed or inflated here), and byte segments only beside payloads marked as
// carrying bytes. No outer length: links deliver whole messages.
//
// The writer and reader signal a failure with a panic of codecError, recovered at Encode and Decode (the way
// encoding/json does), so the field code reads as upstream's does.

// Kind bytes.
const (
	kindHello = iota
	kindHold
	kindRelease
	kindCall
	kindAbort
	kindBound
	kindState
	kindFailed
	kindResult
	kindError
	kindGone
	kindRetry
	kindLoading
	kindAck
	kindClosed
	kindPing
	kindPong
	kindRevoke
	kindCount
)

const (
	// originFlag is the kind byte's flag for an origin.
	originFlag = 0x80
	// payloadPresent and payloadSegments are a payload's flags: present, and carrying byte segments.
	payloadPresent  = 1
	payloadSegments = 2
	// A hold's optional fields, one bit each.
	holdPrincipal   = 1
	holdIncarnation = 2
	holdChain       = 4
	holdLimit       = 8
	// deflatedFlag: a part's length prefix is its length doubled, plus one when the part is deflated.
	deflatedFlag = 1
)

// codecError is a failure of the codec's own, carried by panic through the field code.
type codecError struct{ err error }

func fail(format string, args ...any) { panic(codecError{fmt.Errorf(format, args...)}) }

func failWith(err error) { panic(codecError{err}) }

// recoverCodec turns a codecError panic into *err; any other panic goes on.
func recoverCodec(err *error) {
	if caught := recover(); caught != nil {
		if failure, ok := caught.(codecError); ok {
			*err = failure.err
			return
		}
		panic(caught)
	}
}

type binaryCodec struct{}

// BinaryCodec is the messages as bytes; the default codec of every link.
var BinaryCodec Codec = binaryCodec{}

func (binaryCodec) Name() string { return "binary" }

func (binaryCodec) Encode(message Message) (encoded []byte, err error) {
	defer recoverCodec(&err)
	writer := newWriter()
	kind := kindOf(message)
	if origin := OriginOf(message); origin.Ok {
		writer.byte(kind | originFlag)
		writer.varint(origin.Value)
	} else {
		writer.byte(kind)
	}
	encodeFields(message, writer)
	return writer.finish(), nil
}

func (binaryCodec) Decode(bytes []byte) (message Message, err error) {
	defer recoverCodec(&err)
	reader := &reader{bytes: bytes}
	first := reader.byte()
	hasOrigin := first&originFlag != 0
	var origin int
	if hasOrigin {
		origin = reader.varint()
	}
	kind := int(first &^ originFlag)
	if kind >= kindCount {
		fail("binary: an unknown kind")
	}
	message = decodeFields(kind, reader)
	reader.end()
	// A fresh message; the message schema refuses an origin of 0.
	if hasOrigin && !setOrigin(message, origin) {
		fail("binary: an origin on a kind without one")
	}
	return message, nil
}

func kindOf(message Message) int {
	switch message.(type) {
	case *HelloMessage:
		return kindHello
	case *HoldMessage:
		return kindHold
	case *ReleaseMessage:
		return kindRelease
	case *CallMessage:
		return kindCall
	case *AbortMessage:
		return kindAbort
	case *BoundMessage:
		return kindBound
	case *StateMessage:
		return kindState
	case *FailedMessage:
		return kindFailed
	case *ResultMessage:
		return kindResult
	case *ErrorMessage:
		return kindError
	case *GoneMessage:
		return kindGone
	case *RetryMessage:
		return kindRetry
	case *LoadingMessage:
		return kindLoading
	case *AckMessage:
		return kindAck
	case *ClosedMessage:
		return kindClosed
	case *PingMessage:
		return kindPing
	case *PongMessage:
		return kindPong
	case *RevokeMessage:
		return kindRevoke
	}
	fail("binary: an unknown message type %T", message)
	return 0
}

// setOrigin sets a decoded message's origin in place; false for a kind without one.
func setOrigin(message Message, origin int) bool {
	switch typed := message.(type) {
	case *HoldMessage:
		typed.Origin = Some(origin)
	case *ReleaseMessage:
		typed.Origin = Some(origin)
	case *CallMessage:
		typed.Origin = Some(origin)
	case *AbortMessage:
		typed.Origin = Some(origin)
	case *RetryMessage:
		typed.Origin = Some(origin)
	case *BoundMessage:
		typed.Origin = Some(origin)
	case *StateMessage:
		typed.Origin = Some(origin)
	case *AckMessage:
		typed.Origin = Some(origin)
	case *ClosedMessage:
		typed.Origin = origin
	case *FailedMessage:
		typed.Origin = Some(origin)
	case *LoadingMessage:
		typed.Origin = Some(origin)
	case *ResultMessage:
		typed.Origin = Some(origin)
	case *ErrorMessage:
		typed.Origin = Some(origin)
	case *GoneMessage:
		typed.Origin = Some(origin)
	default:
		return false
	}
	return true
}

func encodeFields(message Message, writer *writer) {
	switch typed := message.(type) {
	case *HelloMessage:
		writer.varint(typed.Protocol)
		writer.raw(hex(stripDashes(typed.Incarnation), 16))
		writer.varint(typed.Calls)
	case *HoldMessage:
		writer.varint(typed.Hold)
		writer.string(typed.Type)
		writer.varint(typed.Version)
		writer.raw(hex(typed.Fingerprint, 8))
		writer.part(typed.Params)
		writer.string(typed.Address.Kind)
		writer.json(typed.Address.Params)
		var flags byte
		if typed.Principal != nil {
			flags |= holdPrincipal
		}
		if typed.Incarnation != "" {
			flags |= holdIncarnation
		}
		if typed.Chain != nil {
			flags |= holdChain
		}
		if typed.Limit.Ok {
			flags |= holdLimit
		}
		writer.byte(int(flags))
		if typed.Principal != nil {
			writer.json(typed.Principal)
		}
		if typed.Incarnation != "" {
			writer.raw(hex(stripDashes(typed.Incarnation), 16))
		}
		if typed.Chain != nil {
			writer.ids(typed.Chain)
		}
		if typed.Limit.Ok {
			writer.varint(typed.Limit.Value)
		}
	case *ReleaseMessage:
		writer.varint(typed.Hold)
	case *LoadingMessage:
		writer.varint(typed.Hold)
	case *RetryMessage:
		writer.varint(typed.Hold)
		writer.varint(typed.Hops)
	case *CallMessage:
		writer.varint(typed.Call)
		writer.varint(typed.Hold)
		writer.varint(typed.Method)
		writer.payload(typed.Args, typed.Segments)
	case *AbortMessage:
		writer.varint(typed.Call)
	case *BoundMessage:
		writer.varint(len(typed.Holds))
		for _, entry := range typed.Holds {
			writer.varint(entry.Hold)
			writer.varint(entry.Variant)
			writer.varint(len(entry.Members))
			for _, member := range entry.Members {
				writer.varint(member.Member)
				writer.part(member.Value)
			}
			writer.failed(entry.Failed)
		}
	case *StateMessage:
		writer.varint(len(typed.Holds))
		for _, entry := range typed.Holds {
			writer.varint(entry.Hold)
			writer.varint(len(entry.Members))
			for _, member := range entry.Members {
				writer.varint(member.Member)
				writer.part(member.Ops)
			}
			writer.failedAndRetired(entry.Failed, entry.Retired)
		}
	case *FailedMessage:
		writer.varint(typed.Hold)
		writer.byte(causeKindByte(typed.Cause.Kind))
		writer.json(typed.Cause.Error)
		// 0 when absent, else one more than the delay.
		if typed.RetryIn.Ok {
			writer.varint(typed.RetryIn.Value + 1)
		} else {
			writer.varint(0)
		}
	case *ResultMessage:
		writer.varint(typed.Call)
		writer.payload(typed.Value, typed.Segments)
	case *ErrorMessage:
		writer.varint(typed.Call)
		writer.json(typed.Error)
	case *GoneMessage:
		writer.varint(typed.Hold)
		writer.json(typed.Error)
	case *RevokeMessage:
		writer.json(typed.Revocation)
	case *AckMessage, *ClosedMessage, *PingMessage, *PongMessage:
	}
}

func causeKindByte(kind string) int {
	for index, each := range CauseKinds {
		if each == kind {
			return index
		}
	}
	return -1
}

func stripDashes(text string) string {
	out := make([]byte, 0, len(text))
	for index := range len(text) {
		if text[index] != '-' {
			out = append(out, text[index])
		}
	}
	return string(out)
}

// decodeFields reads the kind's fields structurally; the envelope's types are checked by the message schema afterwards.
func decodeFields(kind int, reader *reader) Message {
	switch kind {
	case kindHello:
		protocol := reader.varint()
		incarnation := uuid(reader.raw(16))
		return &HelloMessage{Protocol: protocol, Incarnation: incarnation, Calls: reader.varint()}
	case kindHold:
		message := &HoldMessage{Hold: reader.varint()}
		message.Type = reader.string()
		message.Version = reader.varint()
		message.Fingerprint = hexText(reader.raw(8))
		message.Params = reader.part()
		message.Address = WireAddress{Kind: reader.string()}
		message.Address.Params = reader.json()
		flags := reader.byte()
		if flags > holdPrincipal|holdIncarnation|holdChain|holdLimit {
			fail("binary: unknown hold flags")
		}
		if flags&holdPrincipal != 0 {
			message.Principal = reader.object("a principal")
		}
		if flags&holdIncarnation != 0 {
			message.Incarnation = uuid(reader.raw(16))
		}
		if flags&holdChain != 0 {
			message.Chain = reader.ids()
		}
		if flags&holdLimit != 0 {
			message.Limit = Some(reader.varint())
		}
		return message
	case kindRelease:
		return &ReleaseMessage{Hold: reader.varint()}
	case kindLoading:
		return &LoadingMessage{Hold: reader.varint()}
	case kindRetry:
		hold := reader.varint()
		return &RetryMessage{Hold: hold, Hops: reader.varint()}
	case kindCall:
		message := &CallMessage{Call: reader.varint()}
		message.Hold = reader.varint()
		message.Method = reader.varint()
		message.Args, message.Segments = reader.payload()
		return message
	case kindAbort:
		return &AbortMessage{Call: reader.varint()}
	case kindBound:
		count := reader.count()
		holds := make([]BoundEntry, count)
		for index := range holds {
			entry := &holds[index]
			entry.Hold = reader.varint()
			entry.Variant = reader.varint()
			if members := reader.count(); members > 0 {
				entry.Members = make([]BoundMember, members)
				for at := range entry.Members {
					entry.Members[at] = BoundMember{Member: reader.varint(), Value: reader.part()}
				}
			}
			entry.Failed = reader.failed()
		}
		return &BoundMessage{Holds: holds}
	case kindState:
		count := reader.count()
		holds := make([]StateEntry, count)
		for index := range holds {
			entry := &holds[index]
			entry.Hold = reader.varint()
			if members := reader.count(); members > 0 {
				entry.Members = make([]StateMember, members)
				for at := range entry.Members {
					entry.Members[at] = StateMember{Member: reader.varint(), Ops: reader.part()}
				}
			}
			// The failure count times two, plus one when retirements follow.
			flags := reader.varint()
			entry.Failed = reader.failedEntries(flags / 2)
			if flags%2 == 0 {
				entry.Retired = NoRetired
			} else {
				retired := make([]int, reader.count())
				for at := range retired {
					retired[at] = reader.varint()
				}
				entry.Retired = retired
			}
		}
		return &StateMessage{Holds: holds}
	case kindFailed:
		message := &FailedMessage{Hold: reader.varint()}
		cause := reader.byte()
		if cause >= len(CauseKinds) {
			fail("binary: an unknown failure kind")
		}
		message.Cause = WireCause{Kind: CauseKinds[cause], Error: reader.object("an error")}
		if retryIn := reader.varint(); retryIn != 0 {
			message.RetryIn = Some(retryIn - 1)
		}
		return message
	case kindResult:
		message := &ResultMessage{Call: reader.varint()}
		message.Value, message.Segments = reader.payload()
		return message
	case kindError:
		message := &ErrorMessage{Call: reader.varint()}
		message.Error = reader.object("an error")
		return message
	case kindGone:
		message := &GoneMessage{Hold: reader.varint()}
		message.Error = reader.object("an error")
		return message
	case kindRevoke:
		return &RevokeMessage{Revocation: reader.json()}
	case kindAck:
		return &AckMessage{}
	case kindPing:
		return &PingMessage{}
	case kindPong:
		return &PongMessage{}
	default: // kindClosed
		// Its origin is the kind byte's; 0 without one, which the message schema refuses.
		return &ClosedMessage{}
	}
}

const (
	// shortText: up to this many UTF-16 units a string takes at most 126 bytes, so its length is one varint byte.
	shortText = 42
	// longText: from this many UTF-16 units a string is encoded in place (below it, encoding on its own and copying is
	// cheaper).
	longText = 4096
	// lengthSlot is the fixed length prefix of a long string: five varint bytes, up to 32 GiB (16 GiB for a part).
	lengthSlot = 5
	// spareRoom is the room left after a large chunk for the fields that follow it.
	spareRoom = 4096
)

// writer writes a message into one growing buffer, right-sized at the end.
type writer struct {
	// buffer is large enough for most small messages (a state change, a call), small enough to zero cheaply.
	buffer []byte
	length int
}

func newWriter() *writer { return &writer{buffer: make([]byte, 256)} }

func (w *writer) byte(value int) {
	w.reserve(1)
	w.buffer[w.length] = byte(value)
	w.length++
}

// varint is an unsigned LEB128 varint of a safe integer.
func (w *writer) varint(value int) {
	w.reserve(8)
	rest := uint64(value)
	for rest >= 0x80 {
		w.buffer[w.length] = byte(rest%0x80) | 0x80
		w.length++
		rest /= 0x80
	}
	w.buffer[w.length] = byte(rest)
	w.length++
}

func (w *writer) raw(bytes []byte) {
	w.reserve(len(bytes))
	copy(w.buffer[w.length:], bytes)
	w.length += len(bytes)
}

// string is a length-prefixed UTF-8 string (text).
func (w *writer) string(text string) { w.text(text, 1) }

func (w *writer) json(value any) {
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		failWith(err)
	}
	w.text(string(encoded), 1)
}

func (w *writer) ids(ids []string) {
	w.varint(len(ids))
	for _, id := range ids {
		w.raw(hex(stripDashes(id), 16))
	}
}

// part is a part: its text encoded in place, or its bytes (a part received) copied as they are, behind its length
// doubled, plus one when deflated.
func (w *writer) part(part Part) {
	switch typed := part.(type) {
	case Text:
		w.text(string(typed), 2)
	case Bytes:
		w.varint(len(typed) * 2)
		w.raw(typed)
	case *DeflatedPart:
		w.varint(len(typed.Bytes)*2 + deflatedFlag)
		w.raw(typed.Bytes)
	default:
		fail("binary: a part is missing")
	}
}

// units is text's length in UTF-16 units, counted only when the bytes do not settle which side of a threshold it is on.
func units(text string, below, atLeast int) int {
	if len(text) <= below {
		return len(text)
	}
	if len(text) >= 3*atLeast {
		return atLeast
	}
	return jsstring.Length(text)
}

// text is text behind its UTF-8 length times scale (2 for a part's prefix), written straight into the buffer where the
// prefix is known in advance: a short text (its prefix fits one byte: at most 126 bytes, 42 UTF-16 units, divided by
// scale) and a long one (a snapshot's JSON, behind a fixed five-byte prefix, a padded varint, which readers accept).
// Others are measured on their own. No text is encoded twice, and the largest value of a message is never copied.
func (w *writer) text(text string, scale int) {
	if !utf8.ValidString(text) {
		text = wellFormed(text)
	}
	limit := shortText / scale
	count := units(text, limit, longText)
	if count <= limit {
		w.reserve(1 + len(text))
		w.buffer[w.length] = byte(len(text) * scale)
		w.length++
		w.length += copy(w.buffer[w.length:], text)
		return
	}
	if count < longText {
		w.varint(len(text) * scale)
		w.reserve(len(text))
		w.length += copy(w.buffer[w.length:], text)
		return
	}
	w.reserve(lengthSlot + len(text))
	length := len(text) * scale
	for index := range lengthSlot {
		last := byte(0)
		if index < lengthSlot-1 {
			last = 0x80
		}
		w.buffer[w.length+index] = byte(length%0x80) | last
		length /= 0x80
	}
	w.length += lengthSlot
	w.length += copy(w.buffer[w.length:], text)
}

// wellFormed replaces each invalid UTF-8 sequence (a lone UTF-16 surrogate in WTF-8) with U+FFFD, as TextEncoder does.
func wellFormed(text string) string {
	out := make([]byte, 0, len(text))
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		if r == utf8.RuneError && size == 1 {
			// A WTF-8 surrogate is three bytes, one replacement.
			if size = surrogateLength(text[index:]); size == 0 {
				size = 1
			}
			out = utf8.AppendRune(out, utf8.RuneError)
			index += size
			continue
		}
		out = append(out, text[index:index+size]...)
		index += size
	}
	return string(out)
}

func surrogateLength(text string) int {
	if len(text) >= 3 && text[0] == 0xED && text[1] >= 0xA0 && text[1] <= 0xBF && text[2] >= 0x80 && text[2] <= 0xBF {
		return 3
	}
	return 0
}

// payload is a payload: its flags, then its part and, when marked, its segments (count, lengths, raw bytes).
func (w *writer) payload(part Part, segments [][]byte) {
	if part == nil {
		w.varint(0)
		return
	}
	if segments == nil {
		w.varint(payloadPresent)
	} else {
		w.varint(payloadPresent | payloadSegments)
	}
	w.part(part)
	if segments == nil {
		return
	}
	w.varint(len(segments))
	for _, segment := range segments {
		w.varint(len(segment))
	}
	for _, segment := range segments {
		w.raw(segment)
	}
}

func (w *writer) failed(failed []FailedMember) {
	w.varint(len(failed))
	w.failedEntries(failed)
}

// failedAndRetired is a state entry's failures and retirements: the failure count times two, plus one when retirements
// follow, so an entry without retirements costs no byte more than its failures.
func (w *writer) failedAndRetired(failed []FailedMember, retired []int) {
	flag := 0
	if len(retired) > 0 {
		flag = 1
	}
	w.varint(len(failed)*2 + flag)
	w.failedEntries(failed)
	if len(retired) == 0 {
		return
	}
	w.varint(len(retired))
	for _, member := range retired {
		w.varint(member)
	}
}

func (w *writer) failedEntries(failed []FailedMember) {
	for _, entry := range failed {
		w.varint(entry.Member)
		w.json(entry.Error)
	}
}

// finish is the message: a view of the buffer, or a right-sized copy when the spare room is meaningful (at least 64
// bytes and more than an eighth of the message), so a kept message keeps at most that much beyond itself. A large
// message ends within its 4 KiB of room and is not copied.
func (w *writer) finish() []byte {
	message := w.buffer[:w.length]
	spare := len(w.buffer) - w.length
	if spare >= 64 && spare*8 > w.length {
		exact := make([]byte, w.length)
		copy(exact, message)
		return exact
	}
	return message
}

// reserve grows the buffer to fit bytes more: doubled, or for a large chunk to its end plus room for the small fields
// after it.
func (w *writer) reserve(bytes int) {
	if w.length+bytes <= len(w.buffer) {
		return
	}
	grown := make([]byte, max(len(w.buffer)*2, w.length+bytes+spareRoom))
	copy(grown, w.buffer[:w.length])
	w.buffer = grown
}

// reader reads a message's bytes; every read is bounds-checked, segments are views of the message.
type reader struct {
	bytes  []byte
	offset int
}

const maxSafeInteger = 1<<53 - 1

func (r *reader) byte() int {
	if r.offset >= len(r.bytes) {
		fail("binary: the message ends early")
	}
	value := r.bytes[r.offset]
	r.offset++
	return int(value)
}

// varint is an unsigned LEB128 varint of at most 8 bytes, at most a safe integer.
func (r *reader) varint() int {
	var value uint64
	for read := 1; ; read++ {
		if read > 8 {
			fail("binary: a varint does not fit")
		}
		next := r.byte()
		value += uint64(next&0x7f) << (7 * (read - 1))
		if value > maxSafeInteger {
			fail("binary: a varint does not fit")
		}
		if next < 0x80 {
			return int(value)
		}
	}
}

func (r *reader) raw(length int) []byte {
	if length > len(r.bytes)-r.offset {
		fail("binary: the message ends early")
	}
	// A view with its capacity cut: an append to it never writes into the message.
	view := r.bytes[r.offset : r.offset+length : r.offset+length]
	r.offset += length
	return view
}

func (r *reader) string() string {
	text, err := readUtf8(r.raw(r.varint()))
	if err != nil {
		failWith(err)
	}
	return text
}

func (r *reader) json() any {
	value, err := parseJson(r.raw(r.varint()))
	if err != nil {
		failWith(err)
	}
	return value
}

// object is a JSON object field; anything else is no message (the schema asks for an object there).
func (r *reader) object(what string) *delta.JsonObject {
	object, ok := r.json().(*delta.JsonObject)
	if !ok {
		fail("binary: %s is no object", what)
	}
	return object
}

// part is a part as a view of the message, unparsed and not inflated.
func (r *reader) part() Part {
	prefix := r.varint()
	bytes := r.raw(prefix / 2)
	if prefix%2 == deflatedFlag {
		return NewDeflatedPart(bytes)
	}
	return Bytes(bytes)
}

// count is a list's count: each element takes at least a byte, so it cannot pass the message's end.
func (r *reader) count() int {
	count := r.varint()
	if count > len(r.bytes)-r.offset {
		fail("binary: a count does not fit")
	}
	return count
}

// ids is a counted list of incarnations (16 raw bytes each); a present list is never nil.
func (r *reader) ids() []string {
	ids := make([]string, r.count())
	for index := range ids {
		ids[index] = uuid(r.raw(16))
	}
	return ids
}

// payload is a payload part, nil when not present, with its segments when marked as carrying them.
func (r *reader) payload() (Part, [][]byte) {
	flags := r.varint()
	if flags == 0 {
		return nil, nil
	}
	if flags != payloadPresent && flags != payloadPresent|payloadSegments {
		fail("binary: unknown payload flags")
	}
	part := r.part()
	if flags == payloadPresent {
		return part, nil
	}
	lengths := make([]int, r.count())
	for index := range lengths {
		lengths[index] = r.varint()
	}
	segments := make([][]byte, len(lengths))
	for index, length := range lengths {
		segments[index] = r.raw(length)
	}
	return part, segments
}

func (r *reader) failed() []FailedMember { return r.failedEntries(r.varint()) }

// failedEntries is count failed members, as writer.failed writes them after their count.
func (r *reader) failedEntries(count int) []FailedMember {
	if count > len(r.bytes)-r.offset {
		fail("binary: a count does not fit")
	}
	if count == 0 {
		return nil
	}
	// The message schema checks each error's shape.
	failed := make([]FailedMember, count)
	for index := range failed {
		failed[index] = FailedMember{Member: r.varint(), Error: r.object("an error")}
	}
	return failed
}

// end checks the message is consumed exactly.
func (r *reader) end() {
	if r.offset != len(r.bytes) {
		fail("binary: bytes after the message")
	}
}

// hex is length bytes from lowercase hex (an incarnation without dashes, a fingerprint).
func hex(text string, length int) []byte {
	if len(text) != 2*length || !isLowerHex(text) {
		fail("binary: not %d bytes of hex", length)
	}
	bytes := make([]byte, length)
	for index := range bytes {
		bytes[index] = hexDigit(text[2*index])<<4 | hexDigit(text[2*index+1])
	}
	return bytes
}

func hexDigit(char byte) byte {
	if char <= '9' {
		return char - '0'
	}
	return char - 'a' + 10
}

const hexDigits = "0123456789abcdef"

func hexText(bytes []byte) string {
	out := make([]byte, 0, 2*len(bytes))
	for _, value := range bytes {
		out = append(out, hexDigits[value>>4], hexDigits[value&0xf])
	}
	return string(out)
}

// uuid is 16 bytes as a UUID's text.
func uuid(bytes []byte) string {
	text := hexText(bytes)
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}
