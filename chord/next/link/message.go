package link

import (
	"context"
	"errors"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/link/message.ts. Messages (§22, R1, R4, 2b): the one Message every link carries, a
// sealed interface of one struct per kind in place of upstream's TypeBox tagged union, checked after every decode by
// CheckMessage (the schema's constraints, field by field), and the codecs that turn it into bytes.
//
// The schema's optional fields: an absent part is a nil Part, an absent segments list a nil slice, an absent
// incarnation "" (the schema's pattern refuses the empty string, so no value is lost), an absent principal a nil
// object, and an absent origin, limit or retryIn a zero Optional. `chain` and `through` are absent when nil and a
// present empty list when non-nil.

// Optional is an optional number: its zero value is absent.
type Optional[T any] struct {
	Value T
	Ok    bool
}

// Some is a present optional.
func Some[T any](value T) Optional[T] { return Optional[T]{Value: value, Ok: true} }

// Message is a protocol message. Kind is its tag.
type Message interface{ Kind() string }

// OriginMessage is a message that may carry an origin (every kind but hello, revoke, ping and pong): a relay's number
// for one downstream link (and the origin that link's messages carried), never reused on a link; absent for the
// sender's own work. Holds, calls, windows and acks are keyed by (origin, id), and replies carry the origin of what
// they answer.
type OriginMessage interface {
	Message
	origin() Optional[int]
	withOrigin(origin Optional[int]) OriginMessage
}

// OriginOf is the origin of a message; absent for a kind without one.
func OriginOf(message Message) Optional[int] {
	if carrying, ok := message.(OriginMessage); ok {
		return carrying.origin()
	}
	return Optional[int]{}
}

// WithOrigin is a copy of message with its origin set (a relay's forward); nil origin removes it (a `closed` message,
// whose origin is required, gets 0).
func WithOrigin(message OriginMessage, origin Optional[int]) OriginMessage {
	return message.withOrigin(origin)
}

// ProviderMessage is a message for a provider side: the direction from the consumer (`closed` is the link's).
type ProviderMessage interface {
	OriginMessage
	providerMessage()
}

// ConsumerMessage is a message for a consumer side: the direction from the provider.
type ConsumerMessage interface {
	OriginMessage
	consumerMessage()
}

// CHAIN is the longest activation chain a hold carries (Q5): a longer one keeps its last entries.
const CHAIN = 32

// CauseKinds are the kinds of a `failed` message's cause, in their codes' order (the binary codec's byte, the text
// codec's letter).
var CauseKinds = [3]string{"transient", "broken", "waiting"}

// NoRetired is the `retired` list of a state entry without retirements, shared (no allocation per message).
var NoRetired = []int{}

// WireAddress is an address as a hold names it: its host kind and params (JSON), checked against the kind where it is
// routed.
type WireAddress struct {
	Kind   string
	Params any
}

// WireCause is a failure cause's error in the wire error format (a ChordError, checked by L1's envelope check, or a
// plugin's). `waiting`: not the object's fault (no provider, no fitting version, a dependency failed); an event wakes
// it.
type WireCause struct {
	Kind  string
	Error WireError
}

// FailedMember is a member of a hold that failed, by index, as `bound` and `state` list it.
type FailedMember struct {
	Member int
	Error  WireError
}

// BoundMember is one member's value in a BoundEntry.
type BoundMember struct {
	Member int
	Value  Part
}

// BoundEntry is one hold's entry of a `bound` message.
type BoundEntry struct {
	Hold, Variant int
	Members       []BoundMember
	Failed        []FailedMember
}

// StateMember is one member's ops in a StateEntry.
type StateMember struct {
	Member int
	Ops    Part
}

// StateEntry is one hold's entry of a `state` message. Retired is the members whose document was retired since the
// hold's last message (decision "One retirement mechanism"); a `bound` snapshot carries no flag. Entries without
// retirements share NoRetired.
type StateEntry struct {
	Hold    int
	Members []StateMember
	Failed  []FailedMember
	Retired []int
}

// HelloMessage opens a link. Calls is how many concurrent calls this side accepts (the call window).
type HelloMessage struct {
	Protocol    int
	Incarnation string
	Calls       int
}

// HoldMessage holds an object. Principal is whom the hold acts for and Incarnation the origin's, read only on a parent
// link; Chain is the instance ids of the activations waiting for this hold (Q5), read only on a trusted link; Limit
// is the decoded limit per part of the link the hold was admitted on (Q2), read only on a parent link. Address is
// what the caller computed with the type's `hostAddress`: it routes the hold.
type HoldMessage struct {
	Origin      Optional[int]
	Hold        int
	Type        string
	Version     int
	Fingerprint string
	Params      Part
	Address     WireAddress
	Principal   *delta.JsonObject
	Incarnation string
	Chain       []string
	Limit       Optional[int]
}

// ReleaseMessage releases a hold.
type ReleaseMessage struct {
	Origin Optional[int]
	Hold   int
}

// CallMessage calls a method. Args is absent for a method without arguments; Segments are the byte segments of a
// payload whose method's schema has a `bytes()` leaf, referenced from its JSON by `{"$bytes": i}`.
type CallMessage struct {
	Origin   Optional[int]
	Call     int
	Hold     int
	Method   int
	Args     Part
	Segments [][]byte
}

// AbortMessage aborts a call.
type AbortMessage struct {
	Origin Optional[int]
	Call   int
}

// RetryMessage asks the provider for an attempt of a failed hold's object now (`ref.retry()`); Operation names the one
// retry it belongs to (a uuidv7) and Through the instance ids of the objects it passed through (with Operation only),
// so an object it reaches twice (a diamond, a ring) handles it once. Relays forward both.
type RetryMessage struct {
	Origin    Optional[int]
	Hold      int
	Operation string
	Through   []string
}

// BoundMessage binds several holds at once (first bindings, re-holds), applied in one batch.
type BoundMessage struct {
	Origin Optional[int]
	Holds  []BoundEntry
}

// StateMessage carries the state changes of several holds.
type StateMessage struct {
	Origin Optional[int]
	Holds  []StateEntry
}

// AckMessage acknowledges one applied `bound` or `state` message of its origin (the window).
type AckMessage struct{ Origin Optional[int] }

// ClosedMessage says a relay's downstream link closed: the origin's holds are released and its calls aborted. Its
// origin is required (0 is what a decode without one gives, which CheckMessage refuses).
type ClosedMessage struct{ Origin int }

// RevokeMessage is a revocation from the parent, applied with this host's own `revokedBy` (no origin).
type RevokeMessage struct{ Revocation any }

// PingMessage is the heartbeat's ping.
type PingMessage struct{}

// PongMessage answers a ping.
type PongMessage struct{}

// FailedMessage says a hold's object failed. RetryIn is the rest of the object's backoff window in milliseconds: when
// it retries by itself (absent: broken, waiting).
type FailedMessage struct {
	Origin  Optional[int]
	Hold    int
	Cause   WireCause
	RetryIn Optional[int]
}

// LoadingMessage says the failed object of a hold started a new attempt.
type LoadingMessage struct {
	Origin Optional[int]
	Hold   int
}

// ResultMessage is a call's result. Value is absent for `null`.
type ResultMessage struct {
	Origin   Optional[int]
	Call     int
	Value    Part
	Segments [][]byte
}

// ErrorMessage is a call's error.
type ErrorMessage struct {
	Origin Optional[int]
	Call   int
	Error  WireError
}

// GoneMessage says a hold is gone.
type GoneMessage struct {
	Origin Optional[int]
	Hold   int
	Error  WireError
}

func (*HelloMessage) Kind() string   { return "hello" }
func (*HoldMessage) Kind() string    { return "hold" }
func (*ReleaseMessage) Kind() string { return "release" }
func (*CallMessage) Kind() string    { return "call" }
func (*AbortMessage) Kind() string   { return "abort" }
func (*RetryMessage) Kind() string   { return "retry" }
func (*BoundMessage) Kind() string   { return "bound" }
func (*StateMessage) Kind() string   { return "state" }
func (*AckMessage) Kind() string     { return "ack" }
func (*ClosedMessage) Kind() string  { return "closed" }
func (*RevokeMessage) Kind() string  { return "revoke" }
func (*PingMessage) Kind() string    { return "ping" }
func (*PongMessage) Kind() string    { return "pong" }
func (*FailedMessage) Kind() string  { return "failed" }
func (*LoadingMessage) Kind() string { return "loading" }
func (*ResultMessage) Kind() string  { return "result" }
func (*ErrorMessage) Kind() string   { return "error" }
func (*GoneMessage) Kind() string    { return "gone" }

func (message *HoldMessage) origin() Optional[int]    { return message.Origin }
func (message *ReleaseMessage) origin() Optional[int] { return message.Origin }
func (message *CallMessage) origin() Optional[int]    { return message.Origin }
func (message *AbortMessage) origin() Optional[int]   { return message.Origin }
func (message *RetryMessage) origin() Optional[int]   { return message.Origin }
func (message *BoundMessage) origin() Optional[int]   { return message.Origin }
func (message *StateMessage) origin() Optional[int]   { return message.Origin }
func (message *AckMessage) origin() Optional[int]     { return message.Origin }
func (message *ClosedMessage) origin() Optional[int]  { return Some(message.Origin) }
func (message *FailedMessage) origin() Optional[int]  { return message.Origin }
func (message *LoadingMessage) origin() Optional[int] { return message.Origin }
func (message *ResultMessage) origin() Optional[int]  { return message.Origin }
func (message *ErrorMessage) origin() Optional[int]   { return message.Origin }
func (message *GoneMessage) origin() Optional[int]    { return message.Origin }

func (message *HoldMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (message *ReleaseMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (message *CallMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (message *AbortMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (message *RetryMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (message *BoundMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (message *StateMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (message *AckMessage) withOrigin(origin Optional[int]) OriginMessage {
	return &AckMessage{Origin: origin}
}

func (message *ClosedMessage) withOrigin(origin Optional[int]) OriginMessage {
	return &ClosedMessage{Origin: origin.Value}
}

func (message *FailedMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (message *LoadingMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (message *ResultMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (message *ErrorMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (message *GoneMessage) withOrigin(origin Optional[int]) OriginMessage {
	copied := *message
	copied.Origin = origin
	return &copied
}

func (*HoldMessage) providerMessage()    {}
func (*ReleaseMessage) providerMessage() {}
func (*CallMessage) providerMessage()    {}
func (*AbortMessage) providerMessage()   {}
func (*RetryMessage) providerMessage()   {}
func (*AckMessage) providerMessage()     {}

func (*BoundMessage) consumerMessage()   {}
func (*StateMessage) consumerMessage()   {}
func (*FailedMessage) consumerMessage()  {}
func (*LoadingMessage) consumerMessage() {}
func (*ResultMessage) consumerMessage()  {}
func (*ErrorMessage) consumerMessage()   {}
func (*GoneMessage) consumerMessage()    {}

// Codec is how a link turns messages into bytes and back; both ends of a link use the same one.
type Codec interface {
	Name() string
	// Encode returns an error for a message that cannot be written (a part that is no UTF-8 in a text codec).
	Encode(message Message) ([]byte, error)
	// Decode returns an error for bytes that are no message of this codec (a protocol error); the envelope is checked
	// afterwards by CheckMessage.
	Decode(bytes []byte) (Message, error)
}

// errSchema is the error of a message failing the message schema.
var errSchema = errors.New("message: fails the message schema")

// CheckMessage checks a decoded message's envelope (the wire is a trust boundary, after any codec): kinds, ids and
// indexes, lists and error shapes; not the parts. It returns an error on a mismatch (a protocol error). Counted in
// `chord.link.checks`.
func CheckMessage(message Message, ctx context.Context) (Message, error) {
	chordctx.Telemetry(ctx).Metrics().Counter("chord.link.checks", telemetry.InstrumentOptions{}).Add(1, nil)
	if !conforms(message) {
		return nil, errSchema
	}
	return message, nil
}

// conforms is the `MESSAGE` schema: id and index are integers from 0, an origin from 1, an incarnation a lowercase
// uuid, a fingerprint 16 lowercase hex digits, a chain at most CHAIN incarnations, errors in the wire error format.
func conforms(message Message) bool {
	switch typed := message.(type) {
	case *HelloMessage:
		return typed.Protocol >= 0 && isUUID(typed.Incarnation) && typed.Calls >= 1
	case *HoldMessage:
		return validOrigin(typed.Origin) && typed.Hold >= 0 && typed.Version >= 1 && isHex16(typed.Fingerprint) && typed.Params != nil &&
			(typed.Principal == nil || isString(typed.Principal.Value("kind"))) &&
			(typed.Incarnation == "" || isUUID(typed.Incarnation)) && validIDs(typed.Chain) &&
			(!typed.Limit.Ok || typed.Limit.Value >= 0)
	case *ReleaseMessage:
		return validOrigin(typed.Origin) && typed.Hold >= 0
	case *CallMessage:
		return validOrigin(typed.Origin) && typed.Call >= 0 && typed.Hold >= 0 && typed.Method >= 0
	case *AbortMessage:
		return validOrigin(typed.Origin) && typed.Call >= 0
	case *RetryMessage:
		return validOrigin(typed.Origin) && typed.Hold >= 0 && (typed.Operation == "" || isUUID(typed.Operation)) && validIDs(typed.Through)
	case *BoundMessage:
		if !validOrigin(typed.Origin) {
			return false
		}
		for _, entry := range typed.Holds {
			if entry.Hold < 0 || entry.Variant < 0 || !validFailed(entry.Failed) {
				return false
			}
			for _, member := range entry.Members {
				if member.Member < 0 || member.Value == nil {
					return false
				}
			}
		}
		return true
	case *StateMessage:
		if !validOrigin(typed.Origin) {
			return false
		}
		for _, entry := range typed.Holds {
			if entry.Hold < 0 || !validFailed(entry.Failed) {
				return false
			}
			for _, member := range entry.Members {
				if member.Member < 0 || member.Ops == nil {
					return false
				}
			}
			for _, index := range entry.Retired {
				if index < 0 {
					return false
				}
			}
		}
		return true
	case *AckMessage:
		return validOrigin(typed.Origin)
	case *ClosedMessage:
		return typed.Origin >= 1
	case *RevokeMessage, *PingMessage, *PongMessage:
		return true
	case *FailedMessage:
		return validOrigin(typed.Origin) && typed.Hold >= 0 && isCauseKind(typed.Cause.Kind) && validWireError(typed.Cause.Error) &&
			(!typed.RetryIn.Ok || typed.RetryIn.Value >= 0)
	case *LoadingMessage:
		return validOrigin(typed.Origin) && typed.Hold >= 0
	case *ResultMessage:
		return validOrigin(typed.Origin) && typed.Call >= 0
	case *ErrorMessage:
		return validOrigin(typed.Origin) && typed.Call >= 0 && validWireError(typed.Error)
	case *GoneMessage:
		return validOrigin(typed.Origin) && typed.Hold >= 0 && validWireError(typed.Error)
	}
	return false
}

func validOrigin(origin Optional[int]) bool { return !origin.Ok || origin.Value >= 1 }

func validIDs(ids []string) bool {
	if len(ids) > CHAIN {
		return false
	}
	for _, id := range ids {
		if !isUUID(id) {
			return false
		}
	}
	return true
}

func validFailed(failed []FailedMember) bool {
	for _, member := range failed {
		if member.Member < 0 || !validWireError(member.Error) {
			return false
		}
	}
	return true
}

func isCauseKind(kind string) bool {
	for _, each := range CauseKinds {
		if each == kind {
			return true
		}
	}
	return false
}

func isString(value any) bool {
	_, ok := value.(string)
	return ok
}

// validWireError is the `wireError` schema: `{kind: "chord", error}` or `{kind: "plugin", code, message, details?}`,
// closed objects.
func validWireError(wire WireError) bool {
	if wire == nil {
		return false
	}
	switch wire.Value("kind") {
	case "chord":
		return wire.Len() == 2 && wire.Has("error")
	case "plugin":
		_, hasDetails := wire.Get("details")
		want := 3
		if hasDetails {
			want = 4
		}
		return wire.Len() == want && isString(wire.Value("code")) && isString(wire.Value("message"))
	}
	return false
}

func isLowerHex(text string) bool {
	for index := range len(text) {
		char := text[index]
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func isHex16(text string) bool { return len(text) == 16 && isLowerHex(text) }

// isUUID is the incarnation pattern `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`.
func isUUID(text string) bool {
	if len(text) != 36 || text[8] != '-' || text[13] != '-' || text[18] != '-' || text[23] != '-' {
		return false
	}
	return isLowerHex(text[:8]) && isLowerHex(text[9:13]) && isLowerHex(text[14:18]) && isLowerHex(text[19:23]) && isLowerHex(text[24:])
}
