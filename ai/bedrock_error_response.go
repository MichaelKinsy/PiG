//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// bedrockErrorResponse is the status, error-type header and body of a non-2xx ConverseStream response, copied while the SDK reads the body.
type bedrockErrorResponse struct {
	status    int
	errorType []string

	mu   sync.Mutex
	body bytes.Buffer
}

// bedrockErrorBody copies what the SDK's error deserializer reads; the SDK reads the whole error body before it returns.
type bedrockErrorBody struct {
	io.ReadCloser
	response *bedrockErrorResponse
}

func (body *bedrockErrorBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	if n > 0 {
		body.response.mu.Lock()
		body.response.body.Write(p[:n])
		body.response.mu.Unlock()
	}
	return n, err
}

// bedrockResponseError is an SDK error for a non-2xx response, with the text Pi's AWS SDK and formatBedrockError give the same response.
type bedrockResponseError struct {
	err  error
	text string
}

func (err *bedrockResponseError) Error() string { return err.text }
func (err *bedrockResponseError) Unwrap() error { return err.err }

// piError returns err with Pi's text for the response the SDK failed on, or err when no error response was read.
func (response *bedrockErrorResponse) piError(err error) error {
	if response == nil || err == nil || isGoTransportError(err) {
		return err
	}
	response.mu.Lock()
	body := bytes.Clone(response.body.Bytes())
	response.mu.Unlock()
	text, ok := formatBedrockErrorResponse(response.status, response.errorType, body)
	if !ok {
		return err
	}
	return &bedrockResponseError{err: err, text: text}
}

// bedrockDeserializationHint is the line @smithy/core's deserializer middleware appends to an error without $metadata.
const bedrockDeserializationHint = "Deserialization error: to see the raw response, inspect the hidden field {error}.$response on this object."

// formatBedrockErrorResponse is Pi's message for a non-2xx response: @smithy/core HttpBindingProtocol.deserializeResponse parses the body as JSON, @aws-sdk/core AwsRestJsonProtocol.handleError names the exception (loadRestJsonErrorCode, or "Unknown") and decorateServiceException copies the body's members onto it, and formatBedrockError (bedrock-converse-stream.ts:393-410) composes the prefix, the status and body (error-body.ts normalizeProviderError) and the message. ok is false for a body member shape this port does not model.
func formatBedrockErrorResponse(status int, errorType []string, body []byte) (string, bool) {
	data := orderedjson.New()
	if len(body) > 0 {
		if !json.Valid(body) {
			message := jsonparse.SyntaxError(body)
			if message == nil {
				return "", false
			}
			core := string(utf16.Decode(message)) + "\n  " + bedrockDeserializationHint
			return core + bedrockDataRetentionHint(core), true
		}
		if orderedjson.IsObject(body) {
			parsed, err := orderedjson.Parse(body)
			if err != nil {
				return "", false
			}
			data = parsed
		}
	}
	name, ok := bedrockRestJSONErrorCode(errorType, data)
	if !ok {
		return "", false
	}
	message, ok := bedrockErrorMessage(data)
	if !ok {
		return "", false
	}
	// error-body.ts extractStatus: the copied statusCode and status members come before $metadata.httpStatusCode.
	for _, key := range []string{"statusCode", "status"} {
		if raw, found := data.Get(key); found {
			var number float64
			if json.Unmarshal(raw, &number) == nil {
				if number != float64(int(number)) {
					return "", false
				}
				status = int(number)
				break
			}
		}
	}
	core := message
	if body, found := bedrockErrorBodyText(data); found && !strings.Contains(message, body) {
		core = strconv.Itoa(status) + ": " + body
	}
	prefix, known := bedrockErrorPrefixes[name]
	if !known {
		prefix = name
	}
	return prefix + ": " + core + bedrockDataRetentionHint(core), true
}

// bedrockRestJSONErrorCode is @aws-sdk/core loadRestJsonErrorCode with sanitizeErrorCode, defaulting to "Unknown" as AwsRestJsonProtocol.handleError does.
func bedrockRestJSONErrorCode(errorType []string, data *orderedjson.Object) (string, bool) {
	if len(errorType) > 0 {
		return sanitizeBedrockErrorCode(strings.Join(errorType, ", ")), true
	}
	for _, key := range data.Keys() {
		if jsstring.ToLower(key) != "code" {
			continue
		}
		raw, _ := data.Get(key)
		return bedrockErrorCodeValue(raw)
	}
	if raw, found := data.Get("__type"); found {
		return bedrockErrorCodeValue(raw)
	}
	return "Unknown", true
}

func bedrockErrorCodeValue(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return sanitizeBedrockErrorCode(text), true
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		value, err := number.Float64()
		if err != nil {
			return "", false
		}
		return sanitizeBedrockErrorCode(jsnumber.String(value)), true
	}
	return "", false
}

func sanitizeBedrockErrorCode(value string) string {
	value, _, _ = strings.Cut(value, ",")
	value, _, _ = strings.Cut(value, ":")
	if strings.Contains(value, "#") {
		value = strings.Split(value, "#")[1]
	}
	return value
}

// bedrockErrorMessage is the exception's message after decorateServiceException: message, then Message, then "UnknownError", each taken only when truthy.
func bedrockErrorMessage(data *orderedjson.Object) (string, bool) {
	for _, key := range []string{"message", "Message"} {
		raw, found := data.Get(key)
		if !found || string(raw) == "null" {
			continue
		}
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return "", false
		}
		if text != "" {
			return text, true
		}
	}
	return "UnknownError", true
}

// bedrockErrorBodyText is error-body.ts extractBody over the members decorateServiceException copied: a string body, then a non-empty plain-object error, trimmed and capped. The SDK's $response.body is a consumed stream, which counts as no body.
func bedrockErrorBodyText(data *orderedjson.Object) (string, bool) {
	text, found := "", false
	if raw, ok := data.Get("body"); ok && json.Unmarshal(raw, &text) == nil {
		found = true
	} else if raw, ok := data.Get("error"); ok && orderedjson.IsObject(raw) {
		if object, err := orderedjson.Parse(raw); err == nil && object.Len() > 0 {
			text, found = SafeJsonStringify(json.RawMessage(raw)), true
		}
	}
	if text = jsstring.Trim(text); !found || text == "" {
		return "", false
	}
	return TruncateErrorText(text, MaxProviderErrorBodyChars), true
}

// captureBedrockErrorResponse copies a non-2xx response for piError.
func captureBedrockErrorResponse(response *http.Response) *bedrockErrorResponse {
	captured := &bedrockErrorResponse{status: response.StatusCode, errorType: response.Header.Values("X-Amzn-Errortype")}
	response.Body = &bedrockErrorBody{ReadCloser: response.Body, response: captured}
	return captured
}
