package protocol

// Ports packages/protocol/src/protocol.ts.
// Ports packages/protocol/src/codec.ts.

import (
	"errors"
	"math"
	"reflect"
	"regexp"
)

// ProtocolVersion is the exact pinned upstream protocol version, not a PiG-owned format version.
const ProtocolVersion = 8

// ProtocolErrorCode is the opaque routing error code; any non-empty string is valid.
type ProtocolErrorCode = string

// ProtocolError is the bounded routing error exposed on the wire.
type ProtocolError struct{ Code, Message string }

// ProtocolValidationError reports an invalid envelope or framed message.
type ProtocolValidationError struct{ Message string }

func (err *ProtocolValidationError) Error() string { return err.Message }

// RpcTarget fences a call to a logical server or a live Session attachment.
type RpcTarget interface{ targetObject() Object }
type ServerTarget struct{ ServerId string }
type SessionTarget struct{ ServerId, SessionId, AttachmentId string }

func (target ServerTarget) targetObject() Object { return Object{{"serverId", target.ServerId}} }
func (target SessionTarget) targetObject() Object {
	return Object{{"serverId", target.ServerId}, {"sessionId", target.SessionId}, {"attachmentId", target.AttachmentId}}
}

// ClientMessage is the closed set of client wire envelopes.
type ClientMessage interface{ clientObject() Object }
type ClientHello struct{ Version float64 }
type RequestEnvelope struct {
	Id     string
	Target RpcTarget
	Call   any
}
type CancelEnvelope struct {
	Id     string
	Target RpcTarget
}

func (message ClientHello) clientObject() Object {
	return Object{{"type", "hello"}, {"version", message.Version}}
}
func (message RequestEnvelope) clientObject() Object {
	return Object{{"type", "request"}, {"id", message.Id}, {"target", targetObject(message.Target)}, {"call", message.Call}}
}
func (message CancelEnvelope) clientObject() Object {
	return Object{{"type", "cancel"}, {"id", message.Id}, {"target", targetObject(message.Target)}}
}
func targetObject(target RpcTarget) any {
	if nilProtocolValue(target) {
		return nil
	}
	return target.targetObject()
}

// ServerMessage is the closed set of server wire envelopes.
type ServerMessage interface{ serverObject() Object }
type ServerHello struct {
	Version  float64
	ServerId string
}
type ServerHelloError struct{ Error ProtocolError }

// ResponseEnvelope distinguishes omitted results from explicit null. Failed responses carry Error and cannot carry a result.
type ResponseEnvelope struct {
	Id        string
	Ok        bool
	HasResult bool
	Result    any
	Error     *ProtocolError
}
type ServiceEventEnvelope struct {
	SubscriptionId string
	Update         any
}
type AttachmentEnvelope struct{ Attachment *SessionTarget }

func (message ServerHello) serverObject() Object {
	return Object{{"type", "hello"}, {"version", message.Version}, {"serverId", message.ServerId}}
}
func (message ServerHelloError) serverObject() Object {
	return Object{{"type", "hello_error"}, {"error", errorObject(message.Error)}}
}
func (message ResponseEnvelope) serverObject() Object {
	object := Object{{"type", "response"}, {"id", message.Id}, {"ok", message.Ok}}
	if message.HasResult {
		object = append(object, Property{"result", message.Result})
	}
	if message.Error != nil {
		object = append(object, Property{"error", errorObject(*message.Error)})
	}
	return object
}
func (message ServiceEventEnvelope) serverObject() Object {
	return Object{{"type", "service_update"}, {"subscriptionId", message.SubscriptionId}, {"update", message.Update}}
}
func (message AttachmentEnvelope) serverObject() Object {
	var attachment any
	if message.Attachment != nil {
		attachment = message.Attachment.targetObject()
	}
	return Object{{"type", "attachment"}, {"attachment", attachment}}
}
func errorObject(err ProtocolError) Object {
	return Object{{"code", err.Code}, {"message", err.Message}}
}

// ServerId is a logical server identity: a lowercase canonical UUIDv4 (IsServerId).
type ServerId = string

var canonicalServerId = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// IsServerId requires a lowercase canonical UUIDv4.
func IsServerId(value string) bool { return canonicalServerId.MatchString(value) }

// IsSupportedProtocolVersion accepts only pinned integer version 8.
func IsSupportedProtocolVersion(value float64) bool { return value == ProtocolVersion }

func ownString(object Object, key string, nonempty bool) (string, bool) {
	value, present := object.Get(key)
	text, ok := value.(string)
	return text, present && ok && (!nonempty || text != "")
}
func exactKeys(object Object, required []string, optional ...string) bool {
	for _, key := range required {
		if _, ok := object.Get(key); !ok {
			return false
		}
	}
	for _, property := range object {
		key, ok := property.Key.(string)
		if !ok {
			return false
		}
		allowed := false
		for _, name := range required {
			allowed = allowed || name == key
		}
		for _, name := range optional {
			allowed = allowed || name == key
		}
		if !allowed {
			return false
		}
	}
	return true
}
func parseTarget(value any) (RpcTarget, bool) {
	object, ok := value.(Object)
	if !ok {
		return nil, false
	}
	serverId, ok := ownString(object, "serverId", true)
	if !ok || !IsServerId(serverId) {
		return nil, false
	}
	if exactKeys(object, []string{"serverId"}) {
		return ServerTarget{ServerId: serverId}, true
	}
	if !exactKeys(object, []string{"serverId", "sessionId", "attachmentId"}) {
		return nil, false
	}
	sessionId, sessionOK := ownString(object, "sessionId", true)
	attachmentId, attachmentOK := ownString(object, "attachmentId", true)
	return SessionTarget{ServerId: serverId, SessionId: sessionId, AttachmentId: attachmentId}, sessionOK && attachmentOK
}
func parseError(value any) (ProtocolError, bool) {
	object, ok := value.(Object)
	if !ok || !exactKeys(object, []string{"code", "message"}) {
		return ProtocolError{}, false
	}
	code, codeOK := ownString(object, "code", true)
	message, messageOK := ownString(object, "message", false)
	return ProtocolError{code, message}, codeOK && messageOK
}

// ParseClientMessage validates an ordered Object without parsing JSON strings or interpreting the call payload.
func ParseClientMessage(value any) (ClientMessage, error) {
	invalid := &ProtocolValidationError{Message: "Invalid client protocol message"}
	object, ok := value.(Object)
	if !ok || !isProtocolJSON(object, map[uintptr]bool{}, 0) {
		return nil, invalid
	}
	kind, _ := ownString(object, "type", true)
	switch kind {
	case "hello":
		if !exactKeys(object, []string{"type", "version"}) {
			return nil, invalid
		}
		value, _ := object.Get("version")
		version, ok := cborNumber(value)
		if !ok || version < 0 || math.Trunc(version) != version {
			return nil, invalid
		}
		return ClientHello{Version: version}, nil
	case "request", "cancel":
		keys := []string{"type", "id", "target"}
		if kind == "request" {
			keys = append(keys, "call")
		}
		if !exactKeys(object, keys) {
			return nil, invalid
		}
		id, ok := ownString(object, "id", true)
		if !ok {
			return nil, invalid
		}
		value, _ := object.Get("target")
		target, ok := parseTarget(value)
		if !ok {
			return nil, invalid
		}
		if kind == "cancel" {
			return CancelEnvelope{Id: id, Target: target}, nil
		}
		call, _ := object.Get("call")
		return RequestEnvelope{Id: id, Target: target, Call: call}, nil
	default:
		return nil, invalid
	}
}

// ParseServerMessage validates a server envelope while preserving absent/null result and attachment states.
func ParseServerMessage(value any) (ServerMessage, error) {
	invalid := &ProtocolValidationError{Message: "Invalid server protocol message"}
	object, ok := value.(Object)
	if !ok || !isProtocolJSON(object, map[uintptr]bool{}, 0) {
		return nil, invalid
	}
	kind, _ := ownString(object, "type", true)
	switch kind {
	case "hello":
		if !exactKeys(object, []string{"type", "version", "serverId"}) {
			return nil, invalid
		}
		value, _ := object.Get("version")
		version, ok := cborNumber(value)
		serverId, idOK := ownString(object, "serverId", true)
		if !ok || !IsSupportedProtocolVersion(version) || !idOK || !IsServerId(serverId) {
			return nil, invalid
		}
		return ServerHello{Version: version, ServerId: serverId}, nil
	case "hello_error":
		if !exactKeys(object, []string{"type", "error"}) {
			return nil, invalid
		}
		value, _ := object.Get("error")
		failure, ok := parseError(value)
		if !ok {
			return nil, invalid
		}
		return ServerHelloError{Error: failure}, nil
	case "response":
		id, ok := ownString(object, "id", true)
		value, _ := object.Get("ok")
		accepted, boolOK := value.(bool)
		if !ok || !boolOK {
			return nil, invalid
		}
		if accepted {
			if !exactKeys(object, []string{"type", "id", "ok"}, "result") {
				return nil, invalid
			}
			result, present := object.Get("result")
			return ResponseEnvelope{Id: id, Ok: true, HasResult: present, Result: result}, nil
		}
		if !exactKeys(object, []string{"type", "id", "ok", "error"}) {
			return nil, invalid
		}
		value, _ = object.Get("error")
		failure, ok := parseError(value)
		if !ok {
			return nil, invalid
		}
		return ResponseEnvelope{Id: id, Ok: false, Error: &failure}, nil
	case "service_update":
		if !exactKeys(object, []string{"type", "subscriptionId", "update"}) {
			return nil, invalid
		}
		id, ok := ownString(object, "subscriptionId", true)
		if !ok {
			return nil, invalid
		}
		update, _ := object.Get("update")
		return ServiceEventEnvelope{SubscriptionId: id, Update: update}, nil
	case "attachment":
		if !exactKeys(object, []string{"type", "attachment"}) {
			return nil, invalid
		}
		value, _ := object.Get("attachment")
		if value == nil {
			return AttachmentEnvelope{}, nil
		}
		target, ok := parseTarget(value)
		if !ok {
			return nil, invalid
		}
		session, ok := target.(SessionTarget)
		if !ok {
			return nil, invalid
		}
		return AttachmentEnvelope{Attachment: &session}, nil
	default:
		return nil, invalid
	}
}

// isProtocolJSON follows packages/chord/src/json.ts:4-62. CBOR byte strings, undefined members, symbols, cycles, and non-finite numbers are not opaque JSON payloads.
func isProtocolJSON(value any, ancestors map[uintptr]bool, depth int) bool {
	if depth > 512 {
		return false
	}
	if number, ok := cborNumber(value); ok {
		return !math.IsNaN(number) && !math.IsInf(number, 0)
	}
	switch value := value.(type) {
	case nil, string, bool:
		return true
	case []any:
		identity := reflect.ValueOf(value).Pointer()
		if ancestors[identity] {
			return false
		}
		ancestors[identity] = true
		defer delete(ancestors, identity)
		for _, item := range value {
			if !isProtocolJSON(item, ancestors, depth+1) {
				return false
			}
		}
		return true
	case Object:
		identity := reflect.ValueOf(value).Pointer()
		if ancestors[identity] {
			return false
		}
		ancestors[identity] = true
		defer delete(ancestors, identity)
		for _, property := range value {
			if _, ok := property.Key.(string); !ok {
				return false
			}
			if !isProtocolJSON(property.Value, ancestors, depth+1) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// EncodeClientMessage validates and encodes one complete CBOR frame.
func EncodeClientMessage(message ClientMessage, options FrameDecoderOptions) ([]byte, error) {
	if nilProtocolValue(message) {
		return nil, &ProtocolValidationError{Message: "Invalid client protocol message"}
	}
	object := message.clientObject()
	if _, err := ParseClientMessage(object); err != nil {
		return nil, err
	}
	return encodeProtocolObject(object, "client", options)
}

// EncodeServerMessage validates and encodes one complete CBOR frame.
func EncodeServerMessage(message ServerMessage, options FrameDecoderOptions) ([]byte, error) {
	if nilProtocolValue(message) {
		return nil, &ProtocolValidationError{Message: "Invalid server protocol message"}
	}
	object := message.serverObject()
	if _, err := ParseServerMessage(object); err != nil {
		return nil, err
	}
	return encodeProtocolObject(object, "server", options)
}
func nilProtocolValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}

func encodeProtocolObject(object Object, kind string, options FrameDecoderOptions) ([]byte, error) {
	payload, err := EncodeCbor(object, CborOptions{MaxByteLength: options.MaxFrameLength})
	if err == nil {
		payload, err = EncodeFrame(payload)
	}
	if err != nil {
		return nil, &ProtocolValidationError{Message: "Unable to encode " + kind + " protocol message: " + boundedCodecError(err)}
	}
	return payload, nil
}
func boundedCodecError(err error) string {
	message := err.Error()
	if len(message) > 500 {
		return message[:497] + "..."
	}
	return message
}

type validatedMessageDecoder[T any] struct {
	failed         bool
	frames         *FrameDecoder
	kind           string
	maxFrameLength *float64
	parse          func(any) (T, error)
}

func newValidatedDecoder[T any](kind string, parse func(any) (T, error), options FrameDecoderOptions) (*validatedMessageDecoder[T], error) {
	frames, err := NewFrameDecoder(options)
	if err != nil {
		return nil, err
	}
	var limit *float64
	if options.MaxFrameLength != nil {
		limit = new(*options.MaxFrameLength)
	}
	return &validatedMessageDecoder[T]{frames: frames, kind: kind, maxFrameLength: limit, parse: parse}, nil
}
func (decoder *validatedMessageDecoder[T]) push(chunk []byte) ([]T, error) {
	if decoder.failed {
		return nil, &ProtocolValidationError{Message: decoder.kind + " message decoder has failed"}
	}
	messages := []T{}
	frames, err := decoder.frames.Push(chunk)
	if err == nil {
		for _, frame := range frames {
			var value any
			value, err = DecodeCbor(frame, CborOptions{MaxByteLength: decoder.maxFrameLength})
			if err != nil {
				break
			}
			var message T
			message, err = decoder.parse(value)
			if err != nil {
				break
			}
			messages = append(messages, message)
		}
	}
	if err != nil {
		decoder.failed = true
		if failure, ok := errors.AsType[*ProtocolValidationError](err); ok {
			return nil, failure
		}
		return nil, &ProtocolValidationError{Message: "Invalid " + decoder.kind + " protocol frame: " + boundedCodecError(err)}
	}
	return messages, nil
}
func (decoder *validatedMessageDecoder[T]) end() error {
	if decoder.failed {
		return &ProtocolValidationError{Message: decoder.kind + " message decoder has failed"}
	}
	if err := decoder.frames.End(); err != nil {
		decoder.failed = true
		return &ProtocolValidationError{Message: "Invalid " + decoder.kind + " protocol framing: " + boundedCodecError(err)}
	}
	return nil
}

// ClientMessageDecoder incrementally validates framed client messages and permanently fails after a malformed frame.
type ClientMessageDecoder struct {
	decoder *validatedMessageDecoder[ClientMessage]
}

func NewClientMessageDecoder(options FrameDecoderOptions) (*ClientMessageDecoder, error) {
	decoder, err := newValidatedDecoder("client", ParseClientMessage, options)
	if err != nil {
		return nil, err
	}
	return &ClientMessageDecoder{decoder}, nil
}
func (decoder *ClientMessageDecoder) Push(chunk []byte) ([]ClientMessage, error) {
	return decoder.decoder.push(chunk)
}
func (decoder *ClientMessageDecoder) End() error { return decoder.decoder.end() }

// ServerMessageDecoder incrementally validates framed server messages and permanently fails after a malformed frame.
type ServerMessageDecoder struct {
	decoder *validatedMessageDecoder[ServerMessage]
}

func NewServerMessageDecoder(options FrameDecoderOptions) (*ServerMessageDecoder, error) {
	decoder, err := newValidatedDecoder("server", ParseServerMessage, options)
	if err != nil {
		return nil, err
	}
	return &ServerMessageDecoder{decoder}, nil
}
func (decoder *ServerMessageDecoder) Push(chunk []byte) ([]ServerMessage, error) {
	return decoder.decoder.push(chunk)
}
func (decoder *ServerMessageDecoder) End() error { return decoder.decoder.end() }
