package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// Ports packages/mcp/src/protocol/jsonrpc.ts.

// JSONRPCID is a JSON-RPC request id: a string or a finite number.
type JSONRPCID struct {
	str      string
	num      float64
	isString bool
	set      bool
}

// StringID returns a string id.
func StringID(s string) JSONRPCID { return JSONRPCID{str: s, isString: true, set: true} }

// NumberID returns a numeric id.
func NumberID(n float64) JSONRPCID { return JSONRPCID{num: n, set: true} }

// IsString reports whether the id is a string.
func (id JSONRPCID) IsString() bool { return id.isString }

// IsSet reports whether the id holds a value.
func (id JSONRPCID) IsSet() bool { return id.set }

// String is the id as JavaScript's String(id) renders it.
func (id JSONRPCID) String() string {
	if id.isString {
		return id.str
	}
	return strconv.FormatFloat(id.num, 'f', -1, 64)
}

func (id JSONRPCID) key() string {
	if id.isString {
		return "s:" + id.str
	}
	return "n:" + strconv.FormatFloat(id.num, 'g', -1, 64)
}

// MarshalJSON writes the id as a JSON string or number.
func (id JSONRPCID) MarshalJSON() ([]byte, error) {
	if id.isString {
		return json.Marshal(id.str)
	}
	if !id.set || math.IsNaN(id.num) || math.IsInf(id.num, 0) {
		return nil, errors.New("mcp: invalid JSON-RPC id")
	}
	return []byte(strconv.FormatFloat(id.num, 'f', -1, 64)), nil
}

// UnmarshalJSON reads a string or number; any other JSON value is an error.
func (id *JSONRPCID) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return errors.New("mcp: empty JSON-RPC id")
	}
	switch data[0] {
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*id = StringID(s)
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		var n float64
		if err := json.Unmarshal(data, &n); err != nil {
			return err
		}
		*id = NumberID(n)
	default:
		return errors.New("mcp: JSON-RPC id must be a string or a number")
	}
	return nil
}

// JSONRPCErrorObject is the error member of a JSON-RPC error response.
type JSONRPCErrorObject struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// JSONRPCMessage is a request, a notification, or a response. Use
// [JSONRPCMessage.IsRequest], [JSONRPCMessage.IsNotification], and
// [JSONRPCMessage.IsResponse] to tell them apart.
type JSONRPCMessage struct {
	JSONRPC string
	ID      *JSONRPCID
	Method  string
	Params  json.RawMessage
	// Result is the raw result of a success response. A response whose result
	// is JSON null has Result "null"; HasResult reports presence.
	Result json.RawMessage
	Error  *JSONRPCErrorObject

	hasMethod bool
}

// NewRequest builds a request message.
func NewRequest(id JSONRPCID, method string, params json.RawMessage) JSONRPCMessage {
	return JSONRPCMessage{JSONRPC: "2.0", ID: &id, Method: method, Params: params, hasMethod: true}
}

// NewNotification builds a notification message.
func NewNotification(method string, params json.RawMessage) JSONRPCMessage {
	return JSONRPCMessage{JSONRPC: "2.0", Method: method, Params: params, hasMethod: true}
}

// NewResult builds a success response.
func NewResult(id JSONRPCID, result json.RawMessage) JSONRPCMessage {
	if result == nil {
		result = json.RawMessage("null")
	}
	return JSONRPCMessage{JSONRPC: "2.0", ID: &id, Result: result}
}

// NewErrorResponse builds an error response.
func NewErrorResponse(id JSONRPCID, code int, message string, data json.RawMessage) JSONRPCMessage {
	return JSONRPCMessage{JSONRPC: "2.0", ID: &id, Error: &JSONRPCErrorObject{Code: code, Message: message, Data: data}}
}

// HasResult reports whether the message carries a result member.
func (m JSONRPCMessage) HasResult() bool { return m.Result != nil }

// IsRequest is upstream isJsonRpcRequest.
func (m JSONRPCMessage) IsRequest() bool {
	return m.JSONRPC == "2.0" && m.ID != nil && m.ID.set && m.hasMethod
}

// IsNotification is upstream isJsonRpcNotification.
func (m JSONRPCMessage) IsNotification() bool {
	return m.JSONRPC == "2.0" && m.ID == nil && m.hasMethod
}

// IsResponse is upstream isJsonRpcResponse.
func (m JSONRPCMessage) IsResponse() bool {
	if m.JSONRPC != "2.0" || m.ID == nil || !m.ID.set {
		return false
	}
	if m.Result != nil {
		return m.Error == nil
	}
	return m.Error != nil
}

// MarshalJSON writes the message with its members in wire order.
func (m JSONRPCMessage) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(`{"jsonrpc":`)
	version, _ := json.Marshal(m.JSONRPC)
	buf.Write(version)
	if m.ID != nil {
		id, err := m.ID.MarshalJSON()
		if err != nil {
			return nil, err
		}
		buf.WriteString(`,"id":`)
		buf.Write(id)
	}
	if m.hasMethod {
		method, _ := json.Marshal(m.Method)
		buf.WriteString(`,"method":`)
		buf.Write(method)
	}
	if m.Params != nil {
		buf.WriteString(`,"params":`)
		buf.Write(m.Params)
	}
	if m.Result != nil {
		buf.WriteString(`,"result":`)
		buf.Write(m.Result)
	}
	if m.Error != nil {
		body, err := json.Marshal(m.Error)
		if err != nil {
			return nil, err
		}
		buf.WriteString(`,"error":`)
		buf.Write(body)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// ParseJSONRPCMessage decodes one JSON-RPC message and validates its shape as
// upstream parseJsonRpcMessage does. It returns *McpError with the invalid
// request code for anything else.
func ParseJSONRPCMessage(data []byte) (JSONRPCMessage, error) {
	invalid := &McpError{Code: JSONRPCInvalidRequest, Message: "Invalid JSON-RPC message"}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return JSONRPCMessage{}, invalid
	}
	var m JSONRPCMessage
	if raw, ok := fields["jsonrpc"]; ok {
		var v string
		if json.Unmarshal(raw, &v) == nil {
			m.JSONRPC = v
		}
	}
	if raw, ok := fields["id"]; ok {
		var id JSONRPCID
		if err := id.UnmarshalJSON(raw); err != nil {
			// A non-id value keeps the member present so a notification check fails.
			m.ID = &JSONRPCID{}
		} else {
			m.ID = &id
		}
	}
	if raw, ok := fields["method"]; ok {
		var v string
		if json.Unmarshal(raw, &v) == nil {
			m.Method, m.hasMethod = v, true
		}
	}
	m.Params = fields["params"]
	if raw, ok := fields["result"]; ok {
		m.Result = raw
	}
	if raw, ok := fields["error"]; ok {
		var e JSONRPCErrorObject
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil && obj != nil {
			var code float64
			var message string
			if json.Unmarshal(obj["code"], &code) == nil && json.Unmarshal(obj["message"], &message) == nil {
				e = JSONRPCErrorObject{Code: int(code), Message: message, Data: obj["data"]}
				m.Error = &e
			}
		}
		if m.Error == nil {
			// An error member of the wrong shape makes the response invalid.
			m.Result = nil
			m.ID = &JSONRPCID{}
		}
	}
	if m.IsRequest() || m.IsNotification() || m.IsResponse() {
		return m, nil
	}
	return JSONRPCMessage{}, invalid
}

// JSON-RPC error codes, upstream JSON_RPC_ERROR_CODES.
const (
	JSONRPCParseError     = -32700
	JSONRPCInvalidRequest = -32600
	JSONRPCMethodNotFound = -32601
	JSONRPCInvalidParams  = -32602
	JSONRPCInternalError  = -32603
)

// McpError is a JSON-RPC error reported by the peer or by validation.
type McpError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

func (e *McpError) Error() string { return e.Message }

// McpConnectionClosedError reports a closed connection.
type McpConnectionClosedError struct{ Message string }

func (e *McpConnectionClosedError) Error() string {
	if e.Message == "" {
		return "MCP connection closed"
	}
	return e.Message
}

// NewConnectionClosedError returns the error with its default message.
func NewConnectionClosedError() *McpConnectionClosedError { return &McpConnectionClosedError{} }

// McpTimeoutError reports a request that exceeded its timeout.
type McpTimeoutError struct{ TimeoutMs int }

func (e *McpTimeoutError) Error() string {
	return fmt.Sprintf("MCP request timed out after %dms", e.TimeoutMs)
}

// McpAbortError reports a request whose context ended. Its name upstream is
// AbortError.
type McpAbortError struct{ Message string }

func (e *McpAbortError) Error() string {
	if e.Message == "" {
		return "MCP request aborted"
	}
	return e.Message
}

func isJSONObject(data []byte) bool {
	data = bytes.TrimSpace(data)
	return len(data) > 0 && data[0] == '{'
}

func isJSONArray(data []byte) bool {
	data = bytes.TrimSpace(data)
	return len(data) > 0 && data[0] == '['
}
