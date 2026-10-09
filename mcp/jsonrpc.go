package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
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
	return jsnumber.String(id.num)
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

// JSONRPCResponse is jsonrpc.ts:34 JsonRpcResponse = JsonRpcSuccessResponse | JsonRpcErrorResponse: a response always has its id and carries either
// a result or an error object, never both. [JSONRPCSuccessResponse] and [JSONRPCErrorResponse] are its two forms; [JSONRPCMessage.AsResponse]
// produces one from a message for which [JSONRPCMessage.IsResponse] holds, and the client's handleResponse takes it (client.ts:476).
type JSONRPCResponse interface {
	// ResponseID is the id of the request the response answers.
	ResponseID() JSONRPCID
	jsonrpcResponse()
}

// JSONRPCSuccessResponse is jsonrpc.ts:22-26 JsonRpcSuccessResponse.
type JSONRPCSuccessResponse struct {
	JSONRPC string
	ID      JSONRPCID
	// Result is the raw result; JSON null is the raw "null".
	Result json.RawMessage
}

// JSONRPCErrorResponse is jsonrpc.ts:28-32 JsonRpcErrorResponse.
type JSONRPCErrorResponse struct {
	JSONRPC string
	ID      JSONRPCID
	Error   JSONRPCErrorObject
}

// ResponseID returns the response's id.
func (r JSONRPCSuccessResponse) ResponseID() JSONRPCID { return r.ID }
func (JSONRPCSuccessResponse) jsonrpcResponse()        {}

// ResponseID returns the response's id.
func (r JSONRPCErrorResponse) ResponseID() JSONRPCID { return r.ID }
func (JSONRPCErrorResponse) jsonrpcResponse()        {}

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
	// hasError records that the wire object had an `error` member, valid or not: a response with both a result and an
	// error member is not a response.
	hasError bool
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

// AsResponse is the JsonRpcResponse form of the message: ok is true exactly when [JSONRPCMessage.IsResponse] holds, and the result is a
// [JSONRPCSuccessResponse] or, for an error response, a [JSONRPCErrorResponse].
func (m JSONRPCMessage) AsResponse() (response JSONRPCResponse, ok bool) {
	if !m.IsResponse() {
		return nil, false
	}
	if m.Error != nil {
		return JSONRPCErrorResponse{JSONRPC: m.JSONRPC, ID: *m.ID, Error: *m.Error}, true
	}
	return JSONRPCSuccessResponse{JSONRPC: m.JSONRPC, ID: *m.ID, Result: m.Result}, true
}

// IsResponse is upstream isJsonRpcResponse.
func (m JSONRPCMessage) IsResponse() bool {
	if m.JSONRPC != "2.0" || m.ID == nil || !m.ID.set {
		return false
	}
	if m.Result != nil {
		return m.Error == nil && !m.hasError
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
	invalid := func() error { return NewMcpError(JSONRPCInvalidRequest, "Invalid JSON-RPC message", nil) }
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return JSONRPCMessage{}, invalid()
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
		if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &v) == nil {
			m.Method, m.hasMethod = v, true
		}
	}
	m.Params = fields["params"]
	if raw, ok := fields["result"]; ok {
		m.Result = raw
	}
	if raw, ok := fields["error"]; ok {
		m.hasError = true
		var e JSONRPCErrorObject
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil && obj != nil {
			var message string
			code, isNumber := jsonNumber(obj["code"])
			if isNumber && isJSONString(obj["message"]) && json.Unmarshal(obj["message"], &message) == nil {
				e = JSONRPCErrorObject{Code: errorCode(code), Message: message, Data: obj["data"]}
				m.Error = &e
			}
		}
	}
	if m.IsRequest() || m.IsNotification() || m.IsResponse() {
		return m, nil
	}
	return JSONRPCMessage{}, invalid()
}

// JSON-RPC error codes, the members of upstream JSON_RPC_ERROR_CODES (jsonrpc.ts:37).
const (
	JSONRPCParseError     = -32700
	JSONRPCInvalidRequest = -32600
	JSONRPCMethodNotFound = -32601
	JSONRPCInvalidParams  = -32602
	JSONRPCInternalError  = -32603
)

// JSONRPCErrorCodes is upstream JSON_RPC_ERROR_CODES: the object whose members name the standard JSON-RPC error codes
// (`JSON_RPC_ERROR_CODES.invalidRequest`). The mcp package and its callers read the codes through it.
var JSONRPCErrorCodes = struct {
	ParseError     int
	InvalidRequest int
	MethodNotFound int
	InvalidParams  int
	InternalError  int
}{JSONRPCParseError, JSONRPCInvalidRequest, JSONRPCMethodNotFound, JSONRPCInvalidParams, JSONRPCInternalError}

// McpError is a JSON-RPC error reported by the peer or by validation.
type McpError struct {
	Code    int
	Message string
	Data    json.RawMessage
}

// NewMcpError is `new McpError(code, message, data)`.
func NewMcpError(code int, message string, data json.RawMessage) *McpError {
	e := &McpError{Code: code, Message: message, Data: data}
	return e
}

func (e *McpError) Error() string { return e.Message }

// Name is the `name` property, "McpError".
func (e *McpError) Name() string { return "McpError" }

// Cause is the `cause` property. No upstream constructor sets one, so it is always nil.
func (e *McpError) Cause() error { return nil }

// McpConnectionClosedError reports a closed connection.
type McpConnectionClosedError struct {
	Message string
}

// NewMcpConnectionClosedError is `new McpConnectionClosedError(message)`; an empty message is upstream's default
// "MCP connection closed".
func NewMcpConnectionClosedError(message string) *McpConnectionClosedError {
	e := &McpConnectionClosedError{Message: message}
	return e
}

func (e *McpConnectionClosedError) Error() string {
	if e.Message == "" {
		return "MCP connection closed"
	}
	return e.Message
}

// Name is the `name` property, "McpConnectionClosedError".
func (e *McpConnectionClosedError) Name() string { return "McpConnectionClosedError" }

// Cause is the `cause` property. No upstream constructor sets one, so it is always nil.
func (e *McpConnectionClosedError) Cause() error { return nil }

// McpTimeoutError reports a request that exceeded its timeout.
type McpTimeoutError struct {
	TimeoutMs int
}

// NewMcpTimeoutError is `new McpTimeoutError(timeoutMs)`.
func NewMcpTimeoutError(timeoutMs int) *McpTimeoutError {
	e := &McpTimeoutError{TimeoutMs: timeoutMs}
	return e
}

func (e *McpTimeoutError) Error() string {
	return fmt.Sprintf("MCP request timed out after %dms", e.TimeoutMs)
}

// Name is the `name` property, "McpTimeoutError".
func (e *McpTimeoutError) Name() string { return "McpTimeoutError" }

// Message is the `message` property, the text of Error.
func (e *McpTimeoutError) Message() string { return e.Error() }

// Cause is the `cause` property. No upstream constructor sets one, so it is always nil.
func (e *McpTimeoutError) Cause() error { return nil }

// McpAbortError reports a request whose context ended.
type McpAbortError struct {
	Message string
}

// NewMcpAbortError is `new McpAbortError(message)`; an empty message is upstream's default "MCP request aborted".
func NewMcpAbortError(message string) *McpAbortError {
	e := &McpAbortError{Message: message}
	return e
}

func (e *McpAbortError) Error() string {
	if e.Message == "" {
		return "MCP request aborted"
	}
	return e.Message
}

// Name is the `name` property, upstream's "AbortError".
func (e *McpAbortError) Name() string { return "AbortError" }

// Cause is the `cause` property. No upstream constructor sets one, so it is always nil.
func (e *McpAbortError) Cause() error { return nil }

func isJSONObject(data []byte) bool {
	data = bytes.TrimSpace(data)
	return len(data) > 0 && data[0] == '{'
}

func isJSONArray(data []byte) bool {
	data = bytes.TrimSpace(data)
	return len(data) > 0 && data[0] == '['
}

// jsonNumber reads a JSON number token, which JSON.parse turns into a JavaScript number: one beyond the float64 range
// becomes an infinity and stays a number. Any other JSON value is not a number.
func jsonNumber(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, false
	}
	return f, true
}

// errorCode converts a JSON-RPC error code to an int; an infinite code saturates.
func errorCode(code float64) int {
	switch {
	case code >= math.MaxInt:
		return math.MaxInt
	case code <= math.MinInt:
		return math.MinInt
	}
	return int(code)
}
