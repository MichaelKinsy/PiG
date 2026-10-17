package codemode

import (
	"encoding/json"
	"fmt"
)

// The wire forms of the messages between the host and a VM, and of the remote protocol that carries them across an
// RPC boundary. They are plain data with the JSON shape of upstream's types, so a Go remote and a Go client agree
// with each other and with Pi's `RemoteCodemodeSandbox` and `serveCodemodeRemote` on the bytes. VmStartData is the
// start data the host hands every launcher; the host decodes the messages into the seam's private forms (vmMessage,
// callResult).

// VmStartData is everything a VM needs to run one script, independent of where it runs.
//
// Ports packages/codemode/src/runtime/protocol.ts (VmStartData).
type VmStartData struct {
	Code    string          `json:"code"`
	Tools   []VmStartTool   `json:"tools"`
	Globals []VmStartGlobal `json:"globals"`
	// MemoryLimitBytes is the QuickJS heap limit; zero means none (`undefined` upstream, which JSON omits).
	MemoryLimitBytes uint32 `json:"memoryLimitBytes,omitempty"`
	// Store is the snapshot for load(): key to JSON text.
	Store map[string]string `json:"store"`
	// Deterministic, when set, runs the VM deterministically (Sandbox.Start): UTC, a virtual clock starting at StartedAt (ms
	// since the epoch) that moves only with delivered results, randomness from Seed, no WeakRef, results applied in seq
	// order, and started and idle messages.
	Deterministic *VmDeterministic `json:"deterministic,omitempty"`
}

// VmDeterministic is the start of a deterministic run: the clock's start and the seed of its randomness.
//
// Ports packages/codemode/src/runtime/protocol.ts (VmStartData.deterministic).
type VmDeterministic struct {
	StartedAt float64 `json:"startedAt"`
	Seed      uint32  `json:"seed"`
}

// VmStartTool is a tool as the prelude lists it: JSName is the identifier the script uses and Description is
// listed in ALL_TOOLS.
//
// Ports packages/codemode/src/runtime/protocol.ts (VmStartData.tools).
type VmStartTool struct {
	Name        string `json:"name"`
	JSName      string `json:"jsName"`
	Description string `json:"description"`
}

// VmStartGlobal is a global as the prelude lists it.
//
// Ports packages/codemode/src/runtime/protocol.ts (VmStartData.globals).
type VmStartGlobal struct {
	Name   string `json:"name"`
	Spread bool   `json:"spread"`
}

// The Type values of WorkerToHostMessage, HostToWorkerMessage and RemoteMessage.
const (
	MessageCall   = "call"
	MessageOutput = "output"
	MessageDone   = "done"
	MessageCrash  = "crash"
	// MessageStarted and MessageIdle are sent by a deterministic VM.
	MessageStarted = "started"
	MessageIdle    = "idle"
	MessageResult  = "result"
	MessageFailure = "failure"
	MessageStop    = "stop"
)

// WorkerToHostMessage is one message from a VM to the host. Type selects the fields that apply, as the discriminant
// of upstream's union does. Tool arguments, results and values cross as JSON strings: the VM passes them into and out
// of QuickJS as strings and never builds structured values itself. A message the host does not recognise (any other
// Type, or a field a type requires but lacks) is carried unchanged, and the host fails the execution as a broken
// bridge.
//
// Ports packages/codemode/src/runtime/protocol.ts (WorkerToHostMessage).
type WorkerToHostMessage struct {
	Type string
	// call: a nested tool call (Target "tool") or a global (Target "global"); Args is nil for undefined.
	ID     int
	Target string
	Name   string
	Args   *string
	// output
	Item *OutputItem
	// done: a script that returned (Ok, with Value and Writes) or threw (Error, a JSON `{ name?, message, stack? }`).
	Ok     bool
	Value  *string
	Writes *string
	Error  *string
	// During, with done in a deterministic run: the result the VM was processing when the script finished and the calls it
	// had issued by then.
	During *During
	// crash: the VM failed outside the script's control, for example a wasm trap.
	Message string
	// started: what the VM is, so a replay can refuse a different one.
	Fingerprint string
	// idle: having processed result Seq (0: the script's first run), the VM has issued Issued calls in total.
	Seq    int
	Issued int
}

// HostToWorkerMessage is the host's answer to a nested call: Payload is the JSON result when Ok, otherwise the
// error message, and nil for undefined.
//
// Ports packages/codemode/src/runtime/protocol.ts (HostToWorkerMessage).
//
// Deterministic mode: Seq numbers the results from 1 in the order the VM applies them, and the VM's clock reads Now (ms
// since the epoch) while it processes this one; Properties, for a rejection, is a JSON object whose properties the
// script's Error gets.
type HostToWorkerMessage struct {
	Type       string   `json:"type"`
	ID         int      `json:"id"`
	Ok         bool     `json:"ok"`
	Payload    *string  `json:"payload,omitempty"`
	Seq        *int     `json:"seq,omitempty"`
	Now        *float64 `json:"now,omitempty"`
	Properties *string  `json:"properties,omitempty"`
}

type wireCall struct {
	Type   string  `json:"type"`
	ID     int     `json:"id"`
	Target string  `json:"target"`
	Name   string  `json:"name"`
	Args   *string `json:"args,omitempty"`
}

type wireOutput struct {
	Type string `json:"type"`
	Item any    `json:"item"`
}

// wireTextItem and wireImageItem are CodemodeOutputItem's two shapes: every field upstream requires is written, also
// when it is empty, which OutputItem's omitempty tags would drop.
type wireTextItem struct {
	Type    OutputItemType `json:"type"`
	Text    string         `json:"text"`
	Console bool           `json:"console,omitempty"`
}

type wireImageItem struct {
	Type     OutputItemType `json:"type"`
	Data     string         `json:"data"`
	MimeType string         `json:"mimeType"`
}

// outputItemToWire is the item of an output message in upstream's shape; an item of another type is carried unchanged.
func outputItemToWire(item *OutputItem) any {
	switch {
	case item == nil:
		return nil
	case item.Type == OutputItemText:
		return wireTextItem{item.Type, item.Text, item.Console}
	case item.Type == OutputItemImage:
		return wireImageItem{item.Type, item.Data, item.MimeType}
	}
	return item
}

type wireDone struct {
	Type   string  `json:"type"`
	Ok     bool    `json:"ok"`
	Value  *string `json:"value,omitempty"`
	Writes *string `json:"writes,omitempty"`
	Error  *string `json:"error,omitempty"`
	During *During `json:"during,omitempty"`
}

type wireStarted struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

type wireIdle struct {
	Type   string `json:"type"`
	Seq    int    `json:"seq"`
	Issued int    `json:"issued"`
}

type wireCrash struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// MarshalJSON writes the fields that apply to Type, in upstream's shape.
func (m WorkerToHostMessage) MarshalJSON() ([]byte, error) {
	var wire any
	switch m.Type {
	case MessageCall:
		wire = wireCall{m.Type, m.ID, m.Target, m.Name, m.Args}
	case MessageOutput:
		wire = wireOutput{m.Type, outputItemToWire(m.Item)}
	case MessageDone:
		wire = wireDone{m.Type, m.Ok, m.Value, m.Writes, m.Error, m.During}
	case MessageCrash:
		wire = wireCrash{m.Type, m.Message}
	case MessageStarted:
		wire = wireStarted{m.Type, m.Fingerprint}
	case MessageIdle:
		wire = wireIdle{m.Type, m.Seq, m.Issued}
	default:
		wire = struct {
			Type string `json:"type"`
		}{m.Type}
	}
	return json.Marshal(wire)
}

// UnmarshalJSON reads every field that is present; Type selects which ones matter.
func (m *WorkerToHostMessage) UnmarshalJSON(data []byte) error {
	var wire struct {
		Type        string          `json:"type"`
		ID          int             `json:"id"`
		Target      string          `json:"target"`
		Name        string          `json:"name"`
		Args        *string         `json:"args"`
		Item        *OutputItem     `json:"item"`
		Ok          bool            `json:"ok"`
		Value       *string         `json:"value"`
		Writes      *string         `json:"writes"`
		Error       json.RawMessage `json:"error"`
		Message     string          `json:"message"`
		During      *During         `json:"during"`
		Fingerprint string          `json:"fingerprint"`
		Seq         int             `json:"seq"`
		Issued      int             `json:"issued"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*m = WorkerToHostMessage{Type: wire.Type, ID: wire.ID, Target: wire.Target, Name: wire.Name, Args: wire.Args, Item: wire.Item,
		Ok: wire.Ok, Value: wire.Value, Writes: wire.Writes, Message: wire.Message, During: wire.During, Fingerprint: wire.Fingerprint, Seq: wire.Seq, Issued: wire.Issued}
	if wire.Type == MessageDone && len(wire.Error) > 0 {
		var text string
		if err := json.Unmarshal(wire.Error, &text); err != nil {
			return fmt.Errorf("done message: error is not a string: %w", err)
		}
		m.Error = &text
	}
	return nil
}

// RemoteMessage is what the remote sends to the client: VM messages (Type "call", "output", "done", "crash"), or a
// failure outside the VM bridge (Type "failure", with Failure set).
//
// Ports packages/codemode/src/runtime/remote.ts (CodemodeRemoteMessage).
type RemoteMessage struct {
	WorkerToHostMessage
	// Failure is the error of a "failure" message.
	Failure *Error
}

// MarshalJSON writes a failure as `{ type: "failure", error: { kind, ... } }` and any other message as the
// WorkerToHostMessage it carries.
func (m RemoteMessage) MarshalJSON() ([]byte, error) {
	if m.Type == MessageFailure {
		return json.Marshal(struct {
			Type  string `json:"type"`
			Error *Error `json:"error"`
		}{m.Type, m.Failure})
	}
	return m.WorkerToHostMessage.MarshalJSON()
}

// UnmarshalJSON is the inverse of MarshalJSON.
func (m *RemoteMessage) UnmarshalJSON(data []byte) error {
	var head struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	if head.Type == MessageFailure {
		var failure Error
		if err := json.Unmarshal(head.Error, &failure); err != nil {
			return fmt.Errorf("failure message: %w", err)
		}
		*m = RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageFailure}, Failure: &failure}
		return nil
	}
	*m = RemoteMessage{}
	return m.WorkerToHostMessage.UnmarshalJSON(data)
}

// RemoteReply is the client's answer to an exchange: the result of the call the exchange carried (Type "result"), or
// "stop" once the client no longer wants the execution. A nil *RemoteReply is upstream's `null`: the exchange
// carried no call.
//
// Ports packages/codemode/src/runtime/remote.ts (CodemodeRemoteReply).
type RemoteReply struct {
	HostToWorkerMessage
}

// MarshalJSON writes a stop as `{ type: "stop" }` and a result as the HostToWorkerMessage it carries.
func (r RemoteReply) MarshalJSON() ([]byte, error) {
	if r.Type == MessageStop {
		return []byte(`{"type":"stop"}`), nil
	}
	return json.Marshal(r.HostToWorkerMessage)
}

// RemoteRequest is what the client sends to start an execution on the remote.
//
// Ports packages/codemode/src/runtime/remote.ts (CodemodeRemoteRequest).
type RemoteRequest struct {
	Data VmStartData `json:"data"`
	// InterruptBudget is the requested interrupt budget; the remote may lower it (see ServeOptions). Zero asks for
	// the remote's default (`undefined` upstream).
	InterruptBudget int `json:"interruptBudget,omitempty"`
}

func derefString(s *string) (string, bool) {
	if s == nil {
		return "", false
	}
	return *s, true
}

func stringPtr(s string, defined bool) *string {
	if !defined {
		return nil
	}
	return &s
}

// messageToWire is the inverse of messageFromWire.
func messageToWire(message vmMessage) WorkerToHostMessage {
	switch message.kind {
	case vmMessageCall:
		target := "global"
		if message.isTool {
			target = "tool"
		}
		return WorkerToHostMessage{Type: MessageCall, ID: message.id, Target: target, Name: message.name, Args: stringPtr(message.args, message.argsDefined)}
	case vmMessageOutput:
		item := message.item
		return WorkerToHostMessage{Type: MessageOutput, Item: &item}
	case vmMessageDone:
		if !message.ok {
			return WorkerToHostMessage{Type: MessageDone, Error: &message.errorText, During: message.during}
		}
		return WorkerToHostMessage{Type: MessageDone, Ok: true, Value: stringPtr(message.value, message.valueDefined), Writes: stringPtr(message.writes, message.writesDefined), During: message.during}
	case vmMessageCrash:
		return WorkerToHostMessage{Type: MessageCrash, Message: message.message}
	case vmMessageStarted:
		return WorkerToHostMessage{Type: MessageStarted, Fingerprint: message.fingerprint}
	case vmMessageIdle:
		return WorkerToHostMessage{Type: MessageIdle, Seq: message.seq, Issued: message.issued}
	}
	return WorkerToHostMessage{}
}

func replyFromWire(reply HostToWorkerMessage) callResult {
	payload, defined := derefString(reply.Payload)
	result := callResult{id: reply.ID, ok: reply.Ok, payload: payload, defined: defined}
	if reply.Seq != nil {
		result.seq = *reply.Seq
	}
	if reply.Now != nil {
		result.now, result.hasNow = *reply.Now, true
	}
	result.properties, result.propertiesDefined = derefString(reply.Properties)
	return result
}
