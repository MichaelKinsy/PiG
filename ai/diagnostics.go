package ai

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// DiagnosticCoder is implemented by an error that carries a JavaScript-style `error.code`: a string or a number.
type DiagnosticCoder interface {
	DiagnosticCode() any
}

// FormatThrownValue renders a thrown value as text: an error reports its message, or its name when the message is empty, a string is itself, and anything else is formatted like String(value). A nil value is `undefined` (utils/diagnostics.ts formatThrownValue).
func FormatThrownValue(value any) string {
	switch value := value.(type) {
	case nil:
		return "undefined"
	case error:
		if message := value.Error(); message != "" {
			return message
		}
		return errorName(value)
	case string:
		return value
	}
	return fmt.Sprint(value)
}

// errorName is the Go counterpart of `error.name`: the error's Name method when it has one, otherwise "Error". A JavaScript Error subclass that does not assign `name` reports "Error", so the Go type name is never used.
func errorName(err error) string {
	if named, ok := err.(interface{ Name() string }); ok {
		return named.Name()
	}
	return "Error"
}

// ExtractDiagnosticError summarizes a thrown value for a diagnostic. A value that is not an error is reported as a `ThrownValue`. An error reports its name (an empty name is omitted, like upstream's `error.name || undefined`), its message (its name when the message is empty), and a string or numeric code from DiagnosticCoder. Go errors carry no stack, so Stack stays empty (utils/diagnostics.ts extractDiagnosticError).
// pig divergence (D103): JS Error.stack is not modelled; a Go stack would persist build-machine paths.
func ExtractDiagnosticError(value any) DiagnosticErrorInfo {
	err, ok := value.(error)
	if !ok {
		return DiagnosticErrorInfo{Name: "ThrownValue", Message: FormatThrownValue(value)}
	}
	info := DiagnosticErrorInfo{Name: errorName(err), Message: FormatThrownValue(err)}
	if coder, ok := err.(DiagnosticCoder); ok {
		code := coder.DiagnosticCode()
		if code != nil {
			switch reflect.TypeOf(code).Kind() {
			case reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
				// upstream keeps a code only when `typeof code` is "string" or "number".
				info.Code = code
			}
		}
	}
	return info
}

// MarshalJSON writes the diagnostic as Pi's JSON.stringify does: details, when present even if empty, keep the member order they were built or read in.
func (diagnostic AssistantMessageDiagnostic) MarshalJSON() ([]byte, error) {
	var details json.RawMessage
	if diagnostic.Details != nil {
		var err error
		if details, err = orderedjson.MarshalInSourceOrder(map[string]any(diagnostic.Details), diagnostic.detailsOrder); err != nil {
			return nil, err
		}
	}
	// The encoder that called MarshalJSON applies its own HTML escaping to this text.
	return orderedjson.Marshal(struct {
		Type      string               `json:"type"`
		Timestamp int64                `json:"timestamp"`
		Error     *DiagnosticErrorInfo `json:"error,omitempty"`
		Details   json.RawMessage      `json:"details,omitempty"`
	}{diagnostic.Type, diagnostic.Timestamp, diagnostic.Error, details})
}

// UnmarshalJSON reads a diagnostic and keeps the member order of its details, so a diagnostic loaded from a session is written back as Pi writes the parsed object.
func (diagnostic *AssistantMessageDiagnostic) UnmarshalJSON(data []byte) error {
	type plain AssistantMessageDiagnostic
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*diagnostic = AssistantMessageDiagnostic(decoded)
	if diagnostic.Details != nil {
		var raw struct {
			Details json.RawMessage `json:"details"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		diagnostic.detailsOrder = raw.Details
	}
	return nil
}

// diagnosticDetails builds a details object member by member and records the order Pi's object literal gives the members.
type diagnosticDetails struct {
	values JsonObject
	order  *orderedjson.Object
}

func newDiagnosticDetails() diagnosticDetails {
	return diagnosticDetails{values: JsonObject{}, order: orderedjson.New()}
}

// set adds key. order is JSON text with value's member order when value holds an object whose order the map would lose; nil uses value's own encoding.
func (details diagnosticDetails) set(key string, value any, order json.RawMessage) {
	details.values[key] = value
	if order == nil {
		order, _ = json.Marshal(value)
	}
	details.order.Set(key, order)
}

// attach sets diagnostic's details and their member order.
func (details diagnosticDetails) attach(diagnostic AssistantMessageDiagnostic) AssistantMessageDiagnostic {
	diagnostic.Details = details.values
	diagnostic.detailsOrder, _ = details.order.MarshalJSON()
	return diagnostic
}

// CreateAssistantMessageDiagnostic builds a timestamped diagnostic for a thrown value (utils/diagnostics.ts createAssistantMessageDiagnostic).
func CreateAssistantMessageDiagnostic(diagnosticType string, thrown any, details JsonObject) AssistantMessageDiagnostic {
	info := ExtractDiagnosticError(thrown)
	return AssistantMessageDiagnostic{Type: diagnosticType, Timestamp: time.Now().UnixMilli(), Error: &info, Details: details}
}

// AppendAssistantMessageDiagnostic replaces the message's diagnostics with a new slice that ends with diagnostic, so a slice shared with another message is never written through (utils/diagnostics.ts appendAssistantMessageDiagnostic).
func AppendAssistantMessageDiagnostic(message *AssistantMessage, diagnostic AssistantMessageDiagnostic) {
	diagnostics := make([]AssistantMessageDiagnostic, 0, len(message.Diagnostics)+1)
	diagnostics = append(diagnostics, message.Diagnostics...)
	diagnostics = append(diagnostics, diagnostic)
	message.Diagnostics = diagnostics
}
