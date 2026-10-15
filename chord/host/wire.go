package host

// Ports packages/chord/src/wire.ts.
//
// JSON frames between two hosts (or a host and a client). Ids are chosen by the side that creates the hold, call or
// subscription; every frame type has one direction, so ids never collide:
//   consumer → provider: hold, release, call, cancel, sub, unsub
//   provider → consumer: shape, gone, result, state
//   both: hello, ping, pong, bye, root_end, ack; parent → child: invalidate, prepare, commit, abort, retire;
//   child → parent: vote
//
// Frame is the TypeScript union as a sealed interface. An optional member of unknown type (result value, state snap
// and reset, hold principal) carries a presence flag: JSON has no undefined, and upstream tests "snap" in frame.

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/typebox"
)

// ProtocolVersion is the link protocol version, announced in hello. Peers of different versions do not talk: the side
// that notices closes the link with reason `protocol-version <its version>`, so the other side learns it too
// (LinkOptions.OnProtocolMismatch).
const ProtocolVersion = 4

// RootRef names a root at its origin.
type RootRef struct {
	RootID      string
	CodeVersion int
}

// WireError is a ChordError on the wire; Reason is empty when absent.
type WireError struct {
	Code    ChordErrorCode
	Message string
	Reason  string
}

// WireReloadOptions are the reload options a parent sends with prepare; nil fields are absent.
type WireReloadOptions struct {
	Remove         []string
	GateDeadline   *float64
	RetireDeadline *float64
}

// WireReloadReport is a child's prepared reload, sent with its vote (the fields of ReloadReport, without children).
type WireReloadReport struct {
	CodeVersion int
	Previous    int
	Changed     []string
	Removed     []string
	Prepared    []string
	Gated       []string
}

// Frame is one wire frame.
type Frame interface {
	// T is the frame's "t" discriminant.
	T() string
	encode() *chordjson.Object
}

type (
	HelloFrame struct {
		// Protocol is the link protocol version (ProtocolVersion; 0 is absent, Pi's 1). Peers of another version close
		// the link.
		Protocol int
		Name     string
		// Kind is "host" or "client".
		Kind        string
		CodeVersion int
		// Acks: the sender acknowledges what it receives (AckFrame). Flow control applies only when both hellos say so.
		Acks bool
	}
	// AckFrame counts every frame received so far except ack frames, from the link's start, once processed.
	AckFrame  struct{ N int }
	HoldFrame struct {
		ID int
		// At is the target host name.
		At string
		// Token is the consumer's contract token; the provider checks that it covers it.
		Token Description
		Input any
		// Principal is a forwarded principal, honored only on frames from the parent link.
		Principal    any
		HasPrincipal bool
		// Origin is the client connection the hold comes from: an opaque id the server assigned to it, forwarded by
		// relays and honored only on frames from the parent link; present means the hold is a client's (not a reload
		// participant). Empty is absent.
		Origin string
		// NoState is `state: false`: no state for this hold until a sub (a method-only binding).
		NoState bool
		// Again says the consumer already has values (a re-hold after a link loss): its unit is not urgent.
		Again bool
	}
	ReleaseFrame struct{ ID int }
	ShapeFrame   struct {
		ID int
		// Variant is nil for a local contract.
		Variant  *string
		Members  []string
		Contract string
		// State lists the state members whose snapshots follow this frame; the consumer applies the shape once they
		// are here.
		State []string
	}
	GoneFrame struct {
		ID    int
		Error WireError
	}
	CallFrame struct {
		ID     int
		Hold   int
		Member string
		Args   any
		// HasArgs is false when the call carries no args (a method without arguments): JSON has no undefined.
		HasArgs bool
		// Root is nil when the receiving host starts a root (browsers never carry roots).
		Root *RootRef
	}
	ResultFrame struct {
		ID       int
		Value    any
		HasValue bool
		Error    *WireError
	}
	CancelFrame struct{ ID int }
	// SubFrame starts the state of a hold opened with NoState (answered with a unit).
	SubFrame struct{ Hold int }
	// UnsubFrame stops the state of a hold.
	UnsubFrame struct{ Hold int }
	// ResyncFrame sends member M again as a reset (a relay that had to drop what waited for it).
	ResyncFrame struct {
		Hold int
		M    string
	}
	// StateFrame is a state member's frame, addressed by hold and member. As is "snap", "reset", "ops" or "error".
	StateFrame struct {
		Hold int
		M    string
		As   string
		// Value is a snap or reset's value; HasValue false is `undefined`.
		Value    any
		HasValue bool
		// Ops are encoded with the stream's delta encoder; nil is absent.
		Ops   []delta.WireOp
		Error *WireError
	}
	PingFrame struct{ N int }
	PongFrame struct{ N int }
	ByeFrame  struct {
		// Reason is nil when absent.
		Reason *string
	}
	// RootEndFrame: the root ended at its origin, so its leases on objects end (sent on every link the root's calls
	// used).
	RootEndFrame    struct{ RootID string }
	InvalidateFrame struct{ Pattern *chordjson.Object }
	PrepareFrame    struct {
		Reload      int
		CodeVersion int
		Plugins     []string
		Options     WireReloadOptions
	}
	VoteFrame struct {
		Reload int
		Error  *WireError
		Report *WireReloadReport
	}
	CommitFrame struct{ Reload int }
	// RetireFrame: the parent dropped CodeVersion, so roots of it may no longer come from there.
	RetireFrame struct{ CodeVersion int }
	AbortFrame  struct{ Reload int }
)

func (HelloFrame) T() string      { return "hello" }
func (AckFrame) T() string        { return "ack" }
func (HoldFrame) T() string       { return "hold" }
func (ReleaseFrame) T() string    { return "release" }
func (ShapeFrame) T() string      { return "shape" }
func (GoneFrame) T() string       { return "gone" }
func (CallFrame) T() string       { return "call" }
func (ResultFrame) T() string     { return "result" }
func (CancelFrame) T() string     { return "cancel" }
func (SubFrame) T() string        { return "sub" }
func (UnsubFrame) T() string      { return "unsub" }
func (ResyncFrame) T() string     { return "resync" }
func (StateFrame) T() string      { return "state" }
func (PingFrame) T() string       { return "ping" }
func (PongFrame) T() string       { return "pong" }
func (ByeFrame) T() string        { return "bye" }
func (RootEndFrame) T() string    { return "root_end" }
func (InvalidateFrame) T() string { return "invalidate" }
func (PrepareFrame) T() string    { return "prepare" }
func (VoteFrame) T() string       { return "vote" }
func (CommitFrame) T() string     { return "commit" }
func (RetireFrame) T() string     { return "retire" }
func (AbortFrame) T() string      { return "abort" }

func frameObject(t string, pairs ...any) *chordjson.Object {
	return chordjson.ObjectOf(append([]any{"t", t}, pairs...)...)
}

func stringValues(values []string) []any {
	out := make([]any, len(values))
	for index, value := range values {
		out[index] = value
	}
	return out
}

func (err WireError) value() *chordjson.Object {
	out := chordjson.ObjectOf("code", string(err.Code), "message", err.Message)
	if err.Reason != "" {
		out.Set("reason", err.Reason)
	}
	return out
}

func (frame HelloFrame) encode() *chordjson.Object {
	out := frameObject(frame.T())
	if frame.Protocol != 0 {
		out.Set("protocol", float64(frame.Protocol))
	}
	out.Set("name", frame.Name)
	out.Set("kind", frame.Kind)
	out.Set("codeVersion", float64(frame.CodeVersion))
	if frame.Acks {
		out.Set("acks", true)
	}
	return out
}

func (frame AckFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "n", float64(frame.N))
}

func (frame HoldFrame) encode() *chordjson.Object {
	token := frame.Token.wireValue()
	out := frameObject(frame.T(), "id", float64(frame.ID), "at", frame.At, "token", token, "input", frame.Input)
	if frame.HasPrincipal {
		out.Set("principal", frame.Principal)
	}
	if frame.Origin != "" {
		out.Set("origin", frame.Origin)
	}
	if frame.NoState {
		out.Set("state", false)
	}
	if frame.Again {
		out.Set("again", true)
	}
	return out
}

func (frame ReleaseFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "id", float64(frame.ID))
}

func (frame ShapeFrame) encode() *chordjson.Object {
	out := frameObject(frame.T(), "id", float64(frame.ID))
	if frame.Variant != nil {
		out.Set("variant", *frame.Variant)
	}
	out.Set("members", stringValues(frame.Members))
	out.Set("contract", frame.Contract)
	out.Set("state", stringValues(frame.State))
	return out
}

func (frame GoneFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "id", float64(frame.ID), "error", frame.Error.value())
}

func (frame CallFrame) encode() *chordjson.Object {
	out := frameObject(frame.T(), "id", float64(frame.ID), "hold", float64(frame.Hold), "member", frame.Member)
	if frame.HasArgs {
		out.Set("args", frame.Args)
	}
	if frame.Root != nil {
		out.Set("root", chordjson.ObjectOf("rootId", frame.Root.RootID, "codeVersion", float64(frame.Root.CodeVersion)))
	}
	return out
}

func (frame ResultFrame) encode() *chordjson.Object {
	out := frameObject(frame.T(), "id", float64(frame.ID))
	if frame.HasValue {
		out.Set("value", frame.Value)
	}
	if frame.Error != nil {
		out.Set("error", frame.Error.value())
	}
	return out
}

func (frame CancelFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "id", float64(frame.ID))
}

func (frame SubFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "hold", float64(frame.Hold))
}

func (frame UnsubFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "hold", float64(frame.Hold))
}

func (frame ResyncFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "hold", float64(frame.Hold), "m", frame.M)
}

func (frame StateFrame) encode() *chordjson.Object {
	out := frameObject(frame.T(), "hold", float64(frame.Hold), "m", frame.M, "as", frame.As)
	if frame.HasValue {
		out.Set("value", frame.Value)
	}
	if frame.Ops != nil {
		ops := make([]any, len(frame.Ops))
		for index, op := range frame.Ops {
			ops[index] = []any(op)
		}
		out.Set("ops", ops)
	}
	if frame.Error != nil {
		out.Set("error", frame.Error.value())
	}
	return out
}

func (frame PingFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "n", float64(frame.N))
}
func (frame PongFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "n", float64(frame.N))
}

func (frame ByeFrame) encode() *chordjson.Object {
	out := frameObject(frame.T())
	if frame.Reason != nil {
		out.Set("reason", *frame.Reason)
	}
	return out
}

func (frame RootEndFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "rootId", frame.RootID)
}

func (frame InvalidateFrame) encode() *chordjson.Object {
	pattern := frame.Pattern
	if pattern == nil {
		pattern = chordjson.NewObject(0)
	}
	return frameObject(frame.T(), "pattern", pattern)
}

func (frame PrepareFrame) encode() *chordjson.Object {
	options := chordjson.NewObject(3)
	if frame.Options.Remove != nil {
		options.Set("remove", stringValues(frame.Options.Remove))
	}
	if frame.Options.GateDeadline != nil {
		options.Set("gateDeadline", *frame.Options.GateDeadline)
	}
	if frame.Options.RetireDeadline != nil {
		options.Set("retireDeadline", *frame.Options.RetireDeadline)
	}
	return frameObject(frame.T(), "reload", float64(frame.Reload), "codeVersion", float64(frame.CodeVersion), "plugins", stringValues(frame.Plugins), "options", options)
}

func (frame VoteFrame) encode() *chordjson.Object {
	out := frameObject(frame.T(), "reload", float64(frame.Reload))
	if frame.Error != nil {
		out.Set("error", frame.Error.value())
	}
	if report := frame.Report; report != nil {
		out.Set("report", chordjson.ObjectOf(
			"codeVersion", float64(report.CodeVersion), "previous", float64(report.Previous), "changed", stringValues(report.Changed),
			"removed", stringValues(report.Removed), "prepared", stringValues(report.Prepared), "gated", stringValues(report.Gated),
		))
	}
	return out
}

func (frame CommitFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "reload", float64(frame.Reload))
}

func (frame RetireFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "codeVersion", float64(frame.CodeVersion))
}

func (frame AbortFrame) encode() *chordjson.Object {
	return frameObject(frame.T(), "reload", float64(frame.Reload))
}

// EncodeFrame is JSON.stringify(frame).
func EncodeFrame(frame Frame) ([]byte, error) { return json.Marshal(frame.encode()) }

// ToWireError is wire.ts toWireError. As upstream's instanceof, it does not look through a wrapper: an AggregateError
// of ChordErrors is unavailable with the aggregate's message.
func ToWireError(err error) WireError {
	if chordError, ok := err.(*ChordError); ok && chordError != nil { //nolint:errorlint // Pi's instanceof does not unwrap
		return WireError{Code: chordError.Code, Message: chordError.Message, Reason: chordError.Reason}
	}
	return WireError{Code: CodeUnavailable, Message: err.Error()}
}

var wirePrefix = regexp.MustCompile(`^[a-z_]+(/[a-z_]+)?: `)

// FromWireError is wire.ts fromWireError: the ChordError again, without the code prefix the message carries.
func FromWireError(err WireError) *ChordError {
	return NewChordError(err.Code, wirePrefix.ReplaceAllString(err.Message, ""), ChordErrorOptions{Reason: err.Reason})
}

// ─── validation ──────────────────────────────────────────────────────────────

func integer(minimum ...int) Schema {
	schema := Schema{"type": "integer"}
	if len(minimum) > 0 {
		schema["minimum"] = minimum[0]
	}
	return schema
}

func object(required []string, properties ...any) Schema {
	props := Schema{}
	for index := 0; index < len(properties); index += 2 {
		props[properties[index].(string)] = properties[index+1]
	}
	schema := Schema{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = stringValues(required)
	}
	return schema
}

// closed is schema with additionalProperties: false ({ additionalProperties: false } in Type.Object's options).
func closed(schema Schema) Schema {
	schema["additionalProperties"] = false
	return schema
}

func literal(value string) Schema { return Schema{"const": value, "type": "string"} }

// record is Type.Record(Type.String(), value).
func record(value Schema) Schema {
	return Schema{"type": "object", "patternProperties": Schema{"^.*$": value}}
}

func errorCodes() []any {
	codes := make([]any, len(chordErrorCodes))
	for index, code := range chordErrorCodes {
		codes[index] = string(code)
	}
	return codes
}

var (
	wireID         = integer(1)
	wireName       = Schema{"type": "string", "minLength": 1}
	wireString     = Schema{"type": "string"}
	wireUnknown    = Schema{}
	wireStrings    = Schema{"type": "array", "items": wireString}
	wireRoot       = object([]string{"rootId", "codeVersion"}, "rootId", wireName, "codeVersion", integer(1))
	wireErrorShape = object([]string{"code", "message"}, "code", Schema{"enum": errorCodes()}, "message", wireString, "reason", wireString)
	wireReload     = integer(1)
	wireRecord     = record(wireUnknown)
)

// frameSchema is the JSON Schema TypeBox builds for wire.ts FrameSchema, the one declaration of every frame. Payloads
// (hold inputs, call args, results, snapshots) are validated where they are used, against the contract; state ops are
// checked op by op by the delta decoder.
var frameSchema = Schema{"anyOf": []any{
	object([]string{"t", "name", "kind", "codeVersion"}, "t", literal("hello"), "protocol", integer(1), "name", wireName, "kind", Schema{"anyOf": []any{literal("host"), literal("client")}}, "codeVersion", integer(0), "acks", Schema{"type": "boolean"}),
	object([]string{"t", "n"}, "t", literal("ack"), "n", integer(1)),
	object([]string{"t", "id", "at", "token", "input"}, "t", literal("hold"), "id", wireID, "at", wireName,
		"token", descriptionSchema,
		"input", wireUnknown, "principal", wireUnknown, "origin", wireName,
		"state", Schema{"const": false, "type": "boolean"}, "again", Schema{"const": true, "type": "boolean"}),
	object([]string{"t", "id"}, "t", literal("release"), "id", wireID),
	object([]string{"t", "id", "members", "contract", "state"}, "t", literal("shape"), "id", wireID, "variant", wireString, "members", wireStrings, "contract", wireString, "state", Schema{"type": "array", "items": wireName}),
	object([]string{"t", "id", "error"}, "t", literal("gone"), "id", wireID, "error", wireErrorShape),
	object([]string{"t", "id", "hold", "member"}, "t", literal("call"), "id", wireID, "hold", wireID, "member", wireName, "args", wireUnknown, "root", wireRoot),
	object([]string{"t", "id"}, "t", literal("result"), "id", wireID, "value", wireUnknown, "error", wireErrorShape),
	object([]string{"t", "id"}, "t", literal("cancel"), "id", wireID),
	object([]string{"t", "hold"}, "t", literal("sub"), "hold", wireID),
	object([]string{"t", "hold"}, "t", literal("unsub"), "hold", wireID),
	object([]string{"t", "hold", "m"}, "t", literal("resync"), "hold", wireID, "m", wireName),
	object([]string{"t", "hold", "m", "as"}, "t", literal("state"), "hold", wireID, "m", wireName,
		"as", Schema{"anyOf": []any{literal("snap"), literal("reset"), literal("ops"), literal("error")}},
		"value", wireUnknown, "ops", Schema{"type": "array", "items": Schema{"type": "array", "items": wireUnknown}}, "error", wireErrorShape),
	object([]string{"t", "n"}, "t", literal("ping"), "n", integer()),
	object([]string{"t", "n"}, "t", literal("pong"), "n", integer()),
	object([]string{"t"}, "t", literal("bye"), "reason", wireString),
	object([]string{"t", "rootId"}, "t", literal("root_end"), "rootId", wireName),
	object([]string{"t", "pattern"}, "t", literal("invalidate"), "pattern", wireRecord),
	object([]string{"t", "reload", "codeVersion", "plugins", "options"}, "t", literal("prepare"), "reload", wireReload, "codeVersion", integer(1), "plugins", wireStrings,
		"options", object(nil, "remove", wireStrings, "gateDeadline", Schema{"type": "number"}, "retireDeadline", Schema{"type": "number"})),
	object([]string{"t", "reload"}, "t", literal("vote"), "reload", wireReload, "error", wireErrorShape,
		"report", object([]string{"codeVersion", "previous", "changed", "removed", "prepared", "gated"}, "codeVersion", integer(), "previous", integer(), "changed", wireStrings, "removed", wireStrings, "prepared", wireStrings, "gated", wireStrings)),
	object([]string{"t", "reload"}, "t", literal("commit"), "reload", wireReload),
	object([]string{"t", "codeVersion"}, "t", literal("retire"), "codeVersion", integer(1)),
	object([]string{"t", "reload"}, "t", literal("abort"), "reload", wireReload),
}}

// IsFrame is wire.ts isFrame: whether value (decoded JSON) passes structural validation; anything else is a protocol
// violation.
func IsFrame(value any) bool { return typebox.Check(frameSchema, value) }

// DecodeFrame parses and validates one frame. It fails when the data is not JSON or not a frame.
func DecodeFrame(data []byte) (Frame, error) {
	value, err := chordjson.Decode(data)
	if err != nil {
		return nil, err
	}
	if !IsFrame(value) {
		return nil, fmt.Errorf("invalid frame")
	}
	return frameOf(value.(*chordjson.Object))
}

type fields struct{ object *chordjson.Object }

func (f fields) has(key string) bool { return f.object.Has(key) }
func (f fields) value(key string) any {
	value, _ := f.object.Get(key)
	return value
}
func (f fields) str(key string) string { text, _ := f.value(key).(string); return text }

// num reads an integer member. IsFrame admitted only integers there; ids, code versions and counters count up from 0 or 1.
func (f fields) num(key string) int {
	number, _ := f.value(key).(float64)
	//portlint:allow numbers IsFrame admits only integers here, and every peer counts them up from 0 or 1, far below 2^53
	return int(number)
}
func (f fields) optionalString(key string) *string {
	if !f.has(key) {
		return nil
	}
	text := f.str(key)
	return &text
}
func (f fields) strings(key string) []string {
	items, _ := f.value(key).([]any)
	out := make([]string, len(items))
	for index, item := range items {
		out[index], _ = item.(string)
	}
	return out
}
func (f fields) child(key string) fields {
	object, _ := f.value(key).(*chordjson.Object)
	if object == nil {
		object = chordjson.NewObject(0)
	}
	return fields{object}
}
func (f fields) wireError(key string) *WireError {
	if !f.has(key) {
		return nil
	}
	shape := f.child(key)
	return &WireError{Code: ChordErrorCode(shape.str("code")), Message: shape.str("message"), Reason: shape.str("reason")}
}

func frameOf(object *chordjson.Object) (Frame, error) {
	f := fields{object}
	switch f.str("t") {
	case "hello":
		acks, _ := f.value("acks").(bool)
		return HelloFrame{Protocol: f.num("protocol"), Name: f.str("name"), Kind: f.str("kind"), CodeVersion: f.num("codeVersion"), Acks: acks}, nil
	case "ack":
		return AckFrame{N: f.num("n")}, nil
	case "hold":
		raw, err := json.Marshal(f.value("token"))
		if err != nil {
			return nil, err
		}
		var token Description
		//portlint:allow jsonclone a frame is parsed as chordjson values first; the hold token is read into its typed Description (Description.UnmarshalJSON), once per hold frame
		if err := json.Unmarshal(raw, &token); err != nil { //perf:ok once per hold frame (a contract call opening); the token arrives as chordjson values and Description reads only its JSON text
			return nil, err
		}
		noState := f.has("state")
		again, _ := f.value("again").(bool)
		return HoldFrame{ID: f.num("id"), At: f.str("at"), Token: token, Input: f.value("input"), Principal: f.value("principal"), HasPrincipal: f.has("principal"), Origin: f.str("origin"), NoState: noState, Again: again}, nil
	case "release":
		return ReleaseFrame{ID: f.num("id")}, nil
	case "shape":
		return ShapeFrame{ID: f.num("id"), Variant: f.optionalString("variant"), Members: f.strings("members"), Contract: f.str("contract"), State: f.strings("state")}, nil
	case "gone":
		return GoneFrame{ID: f.num("id"), Error: *f.wireError("error")}, nil
	case "call":
		frame := CallFrame{ID: f.num("id"), Hold: f.num("hold"), Member: f.str("member"), Args: f.value("args"), HasArgs: f.has("args")}
		if f.has("root") {
			root := f.child("root")
			frame.Root = &RootRef{RootID: root.str("rootId"), CodeVersion: root.num("codeVersion")}
		}
		return frame, nil
	case "result":
		return ResultFrame{ID: f.num("id"), Value: f.value("value"), HasValue: f.has("value"), Error: f.wireError("error")}, nil
	case "cancel":
		return CancelFrame{ID: f.num("id")}, nil
	case "sub":
		return SubFrame{Hold: f.num("hold")}, nil
	case "unsub":
		return UnsubFrame{Hold: f.num("hold")}, nil
	case "resync":
		return ResyncFrame{Hold: f.num("hold"), M: f.str("m")}, nil
	case "state":
		frame := StateFrame{Hold: f.num("hold"), M: f.str("m"), As: f.str("as"), Value: f.value("value"), HasValue: f.has("value"), Error: f.wireError("error")}
		if f.has("ops") {
			ops, _ := f.value("ops").([]any)
			frame.Ops = make([]delta.WireOp, len(ops))
			for index, op := range ops {
				frame.Ops[index], _ = op.([]any)
			}
		}
		return frame, nil
	case "ping":
		return PingFrame{N: f.num("n")}, nil
	case "pong":
		return PongFrame{N: f.num("n")}, nil
	case "bye":
		return ByeFrame{Reason: f.optionalString("reason")}, nil
	case "root_end":
		return RootEndFrame{RootID: f.str("rootId")}, nil
	case "invalidate":
		return InvalidateFrame{Pattern: f.child("pattern").object}, nil
	case "prepare":
		options := f.child("options")
		frame := PrepareFrame{Reload: f.num("reload"), CodeVersion: f.num("codeVersion"), Plugins: f.strings("plugins")}
		if options.has("remove") {
			frame.Options.Remove = options.strings("remove")
		}
		for key, target := range map[string]**float64{"gateDeadline": &frame.Options.GateDeadline, "retireDeadline": &frame.Options.RetireDeadline} {
			if number, ok := options.value(key).(float64); ok {
				*target = &number
			}
		}
		return frame, nil
	case "vote":
		frame := VoteFrame{Reload: f.num("reload"), Error: f.wireError("error")}
		if f.has("report") {
			report := f.child("report")
			frame.Report = &WireReloadReport{CodeVersion: report.num("codeVersion"), Previous: report.num("previous"), Changed: report.strings("changed"), Removed: report.strings("removed"), Prepared: report.strings("prepared"), Gated: report.strings("gated")}
		}
		return frame, nil
	case "commit":
		return CommitFrame{Reload: f.num("reload")}, nil
	case "retire":
		return RetireFrame{CodeVersion: f.num("codeVersion")}, nil
	case "abort":
		return AbortFrame{Reload: f.num("reload")}, nil
	}
	return nil, fmt.Errorf("invalid frame")
}
