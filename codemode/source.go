package codemode

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// CodemodeOptionsPrefix starts the optional first line of a script.
//
// Ports packages/codemode/src/source.ts (CODEMODE_OPTIONS_PREFIX).
const CodemodeOptionsPrefix = "// @options:"

// SourceOptions are the fields of the options line.
type SourceOptions struct {
	// MaxOutputTokens is the token budget for the script's output; nil means unset.
	MaxOutputTokens *int64
	// TimeoutMs is the hard deadline for the whole script in milliseconds; nil means unset.
	TimeoutMs *int64
}

// ParsedSource is a script with its options line split off.
type ParsedSource struct {
	// Code is the script with the options line replaced by an empty line, so line numbers are unchanged.
	Code    string
	Options SourceOptions
}

// SourceError reports empty input or invalid options.
type SourceError struct {
	Message string
}

func (e *SourceError) Error() string { return e.Message }

// Name is the upstream error's `name`.
func (*SourceError) Name() string { return "CodemodeSourceError" }

// NewSourceError is `new CodemodeSourceError(message)`.
func NewSourceError(message string) *SourceError {
	return &SourceError{Message: message}
}

const (
	supportedFieldsText = "`max_output_tokens` and `timeout_ms`"
	// maxTimeoutMs is the largest delay setTimeout supports, which bounds timeout_ms.
	maxTimeoutMs = 2_147_483_647
)

// CodemodeSourceGrammar is the Lark grammar for providers with grammar-constrained tool input. It only fixes the
// shape of the options line; ParseCodemodeSource checks the options JSON and the code.
//
// Ports packages/codemode/src/source.ts (CODEMODE_SOURCE_GRAMMAR).
const CodemodeSourceGrammar = `
start: options_source | plain_source
options_source: OPTIONS_LINE NEWLINE SOURCE
plain_source: SOURCE

OPTIONS_LINE: /[ \t]*\/\/ @options:[^\r\n]*/
NEWLINE: /\r?\n/
SOURCE: /[\s\S]+/
`

func sourceErrorf(format string, args ...any) error {
	return NewSourceError(fmt.Sprintf(format, args...))
}

// safeInteger converts a JSON value to an integer if it is a number that Number.isSafeInteger accepts and is not negative.
func safeInteger(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	if err != nil || f != math.Trunc(f) || f < 0 || f > 1<<53-1 {
		return 0, false
	}
	return int64(f), true
}

func parseOptions(directive string) (SourceOptions, error) {
	if directive == "" {
		return SourceOptions{}, sourceErrorf("@options must be a JSON object with supported fields %s", supportedFieldsText)
	}
	if err := jsonparse.Validate([]byte(directive)); err != nil {
		// err carries the message V8's JSON.parse throws.
		return SourceOptions{}, sourceErrorf("@options must be valid JSON with supported fields %s: %s", supportedFieldsText, err)
	}
	value, err := decodeJSON([]byte(directive))
	if err != nil {
		return SourceOptions{}, sourceErrorf("@options must be valid JSON with supported fields %s: %s", supportedFieldsText, err)
	}
	fields, ok := value.(*object)
	if !ok {
		return SourceOptions{}, sourceErrorf("@options must be a JSON object with supported fields %s", supportedFieldsText)
	}
	for _, key := range fields.order() {
		if key != "max_output_tokens" && key != "timeout_ms" {
			return SourceOptions{}, sourceErrorf("@options only supports %s; got `%s`", supportedFieldsText, key)
		}
	}
	var options SourceOptions
	if v, ok := fields.get("max_output_tokens"); ok {
		n, isInt := safeInteger(v)
		if !isInt {
			return SourceOptions{}, sourceErrorf("@options field `max_output_tokens` must be a non-negative safe integer")
		}
		options.MaxOutputTokens = &n
	}
	if v, ok := fields.get("timeout_ms"); ok {
		n, isInt := safeInteger(v)
		if !isInt || n == 0 || n > maxTimeoutMs {
			return SourceOptions{}, sourceErrorf("@options field `timeout_ms` must be a positive integer up to %d", int64(maxTimeoutMs))
		}
		options.TimeoutMs = &n
	}
	return options, nil
}

// ParseCodemodeSource splits an optional first-line `// @options: {...}` from the script. It fails with a
// *SourceError for empty input and invalid options.
//
// Ports packages/codemode/src/source.ts (parseCodemodeSource).
func ParseCodemodeSource(input string) (ParsedSource, error) {
	if jsstring.Trim(input) == "" {
		return ParsedSource{}, sourceErrorf("Expected JavaScript source text (non-empty). Provide JS only, optionally with a first line `// @options: {\"max_output_tokens\": 1000}`.")
	}
	newline := strings.IndexByte(input, '\n')
	firstLine := input
	if newline != -1 {
		firstLine = input[:newline]
	}
	firstLine = strings.TrimSuffix(firstLine, "\r")
	trimmed := jsstring.TrimStart(firstLine)
	if !strings.HasPrefix(trimmed, CodemodeOptionsPrefix) {
		return ParsedSource{Code: input}, nil
	}
	code := ""
	if newline != -1 {
		code = input[newline:]
	}
	if jsstring.Trim(code) == "" {
		return ParsedSource{}, sourceErrorf("The @options line must be followed by JavaScript source on subsequent lines")
	}
	options, err := parseOptions(jsstring.Trim(trimmed[len(CodemodeOptionsPrefix):]))
	if err != nil {
		return ParsedSource{}, err
	}
	return ParsedSource{Code: code, Options: options}, nil
}
