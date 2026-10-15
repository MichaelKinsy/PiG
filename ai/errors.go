package ai

// Ports packages/ai/src/utils/error-body.ts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

const MaxProviderErrorBodyChars = 4000

// NormalizedProviderError separates a provider's HTTP status and body from its error message.
type NormalizedProviderError struct {
	Status             *int    `json:"status,omitempty"`
	Body               *string `json:"body,omitempty"`
	Message            string  `json:"message"`
	MessageCarriesBody bool    `json:"messageCarriesBody"`
}

// providerError carries the raw error fields owned by the HTTP adapters. Native SDK errors retain their own types.
type providerError struct {
	status  *int
	body    any
	message string
	prefix  []string
}

// ProviderStatus is the HTTP status of the response the error reports; a retry policy reads it.
func (err *providerError) ProviderStatus() *int { return err.status }

func (err *providerError) Error() string {
	return FormatProviderError(NormalizeProviderError(err), err.prefix...)
}

// NormalizeProviderError extracts supported error metadata without serializing unread response streams or SDK wrapper objects.
func NormalizeProviderError(value any) NormalizedProviderError {
	if own, ok := value.(*providerError); ok {
		if own == nil {
			return NormalizedProviderError{Message: "null"}
		}
		body := providerErrorBody(own.body)
		status := own.status
		if status != nil {
			status = new(*status)
		}
		return NormalizedProviderError{Status: status, Body: body, Message: own.message, MessageCarriesBody: body == nil || strings.Contains(own.message, *body)}
	}
	err, ok := value.(error)
	if !ok {
		return NormalizedProviderError{Message: SafeJsonStringify(value)}
	}
	result := NormalizedProviderError{Message: err.Error(), MessageCarriesBody: true}
	// pig additive (D92): only the Bedrock provider (the AWS SDK) produces a status-carrying response error; a build without it has none.
	if status, ok := awsResponseErrorStatus(err); ok {
		result.Status = new(status)
	}
	return result
}

func providerErrorBody(value any) *string {
	var text string
	switch value := value.(type) {
	case string:
		text = value
	case json.RawMessage:
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) != nil || len(object) == 0 {
			return nil
		}
		text = SafeJsonStringify(value)
	default:
		if value == nil {
			return nil
		}
		reflected := reflect.ValueOf(value)
		if reflected.Kind() != reflect.Map || reflected.Type().Key().Kind() != reflect.String || reflected.Len() == 0 {
			return nil
		}
		text = SafeJsonStringify(value)
	}
	text = strings.TrimFunc(text, func(r rune) bool { return r == '\ufeff' || r != '\u0085' && unicode.IsSpace(r) })
	if text == "" {
		return nil
	}
	return new(TruncateErrorText(text, MaxProviderErrorBodyChars))
}

// FormatProviderError adds body/status only when they are not already carried by the message. Omit prefix to return the unprefixed provider error.
func FormatProviderError(norm NormalizedProviderError, prefix ...string) string {
	message := norm.Message
	if !norm.MessageCarriesBody && norm.Status != nil && norm.Body != nil {
		message = *norm.Body
	}
	if len(prefix) > 0 && norm.Status != nil {
		return fmt.Sprintf("%s (%d): %s", prefix[0], *norm.Status, message)
	}
	if !norm.MessageCarriesBody && norm.Status != nil && norm.Body != nil {
		return fmt.Sprintf("%d: %s", *norm.Status, message)
	}
	return message
}

// TruncateErrorText caps error text in UTF-16 units and reports the number of omitted units.
func TruncateErrorText(text string, maxChars int) string {
	length := utf16Length(text)
	if length <= maxChars {
		return text
	}
	return fmt.Sprintf("%s... [truncated %d chars]", truncateUTF16(text, maxChars), length-maxChars)
}

// SafeJsonStringify formats JSON-compatible values without HTML escaping and falls back to their Go string representation when encoding fails.
func SafeJsonStringify(value any) string {
	state := safeJSONEncoders.Get().(*safeJSONEncoder)
	defer state.release()
	return string(state.stringify(value))
}

// appendSafeJsonStringify appends SafeJsonStringify(value) to dst without building the string.
func appendSafeJsonStringify(dst []byte, value any) []byte {
	state := safeJSONEncoders.Get().(*safeJSONEncoder)
	defer state.release()
	return append(dst, state.stringify(value)...)
}

// maxPooledSafeJSONBuffer bounds the buffers a pooled safeJSONEncoder keeps (golang/go#23199): one large tool argument must not pin them.
// pig additive (D112): Pi keeps no encoder between JSON.stringify calls; a state that is not pooled is made again and stringifies the same.
const maxPooledSafeJSONBuffer = 64 << 10

// release returns state to the pool unless its buffers grew past maxPooledSafeJSONBuffer; the pool makes a fresh state when it is empty.
func (state *safeJSONEncoder) release() {
	if state.buffer.Cap() > maxPooledSafeJSONBuffer || cap(state.canonical) > maxPooledSafeJSONBuffer {
		return
	}
	safeJSONEncoders.Put(state)
}

// safeJSONEncoder keeps an encoder and its buffers between calls: the transcript estimate of the faux provider stringifies every tool call of the whole history on every model call.
type safeJSONEncoder struct {
	buffer    bytes.Buffer
	encoder   *json.Encoder
	canonical []byte
}

var safeJSONEncoders = sync.Pool{New: func() any {
	state := &safeJSONEncoder{}
	state.encoder = json.NewEncoder(&state.buffer)
	state.encoder.SetEscapeHTML(false)
	return state
}}

// stringify is SafeJsonStringify(value); the result is valid until the encoder's next call.
func (state *safeJSONEncoder) stringify(value any) []byte {
	state.buffer.Reset()
	if err := state.encoder.Encode(value); err != nil {
		return append(state.canonical[:0], fmt.Sprint(value)...)
	}
	encoded := bytes.TrimSuffix(state.buffer.Bytes(), []byte("\n"))
	// JSON.stringify writes U+2028 and U+2029 raw, -0 as 0, numbers in ECMAScript form and integer keys first; encoding/json differs on each.
	canonical, err := jsonstringify.AppendCanonical(state.canonical[:0], encoded)
	if err != nil {
		return encoded
	}
	state.canonical = canonical
	return canonical
}
