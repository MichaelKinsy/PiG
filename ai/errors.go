package ai

// Ports packages/ai/src/utils/error-body.ts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	smithyhttp "github.com/aws/smithy-go/transport/http"
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
	if response, ok := errors.AsType[*smithyhttp.ResponseError](err); ok {
		result.Status = new(response.HTTPStatusCode())
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

// SafeJsonStringify formats JSON-compatible values without HTML escaping and falls back to their Go string representation when JSON encoding fails.
func SafeJsonStringify(value any) string {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Sprint(value)
	}
	return strings.TrimSuffix(buffer.String(), "\n")
}
