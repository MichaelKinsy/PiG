// Ports packages/ai/src/utils/validation.ts.
package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/schema"
	"github.com/MichaelKinsy/PiG/schema/formats"
)

// validateToolArgs validates arguments against a schema held as a map; see validateToolArgsSchema.
func validateToolArgs(toolName string, parameters map[string]any, args json.RawMessage) (json.RawMessage, error) {
	if parameters == nil {
		parameters = map[string]any{}
	}
	//portlint:allow mapkeyorder a map[string]any schema records no authored member order; ValidateToolArguments passes ToolSchema.ParametersJSON and tools with an authored schema reach ValidateToolArgumentsJSON (agent/tool_execution.go)
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return nil, err
	}
	return validateToolArgsSchema(toolName, encoded, args)
}

// decodeJSON decodes a JSON text as JSON.parse does: objects keep JavaScript's own-key order, and the escape of a lone surrogate is that one UTF-16 unit (WTF-8 in the Go string), where encoding/json would hand the tool U+FFFD.
func decodeJSON(text []byte) (any, error) {
	return chordjson.DecodeUnquoting(text, jsstring.UnmarshalKeepingSurrogates[string])
}

// validateToolArgsSchema validates arguments against the JSON Schema rawSchema as Pi's validateToolArguments does: pi-schema's parse coerces them (a number sent as "42", one value for an array, null for an omitted
// optional property), then checks the result. It returns the coerced arguments as JSON.stringify writes them, in the member order of args; when nothing changed and args is already what JSON.stringify writes
// for them it returns args itself. A failure is Pi's message: pi-schema's errors, then the arguments received (validation.ts:54-69: parse at :59, the message at :64-68).
func validateToolArgsSchema(toolName string, rawSchema, args json.RawMessage) (json.RawMessage, error) {
	validator, err := toolArgumentValidators.entry(rawSchema).compiled()
	if err != nil {
		return nil, err
	}
	instance, err := decodeJSON(args)
	if err != nil {
		return nil, fmt.Errorf("Validation failed for tool \"%s\":\n  - invalid JSON: %w\n\nReceived arguments:\n%s", toolName, err, args)
	}
	// Parse copies what it changes and leaves instance alone, which is Pi's structuredClone of the arguments.
	result, err := validator.Parse(instance)
	if err != nil {
		return nil, err
	}
	if !result.OK {
		return nil, validationError(toolName, result.Errors, args)
	}
	if sameValue(result.Value, instance) && jsonstringify.IsCanonical(args) {
		return args, nil
	}
	//portlint:allow numbers a capacity hint for the output buffer, never observable: Pi's validateToolArguments (validation.ts) returns the structuredClone, with no size arithmetic
	headroom := len(args) / 8
	capacity := len(args)
	if capacity <= math.MaxInt-headroom { // on a 32-bit int the extra eighth is a hint to drop, not a size to wrap
		capacity += headroom
	}
	return appendStringify(make([]byte, 0, capacity), result.Value), nil
}

// validationError is the error validateToolArguments throws for a failed validation (validation.ts:64-68).
func validationError(toolName string, failures []schema.ValidationError, args json.RawMessage) error {
	var message strings.Builder
	message.WriteString(`Validation failed for tool "`)
	message.WriteString(toolName)
	message.WriteString("\":\n")
	for i, failure := range failures {
		if i > 0 {
			message.WriteByte('\n')
		}
		message.WriteString("  - ")
		message.WriteString(validationPath(failure.Path))
		message.WriteString(": ")
		message.WriteString(failure.Message)
	}
	message.WriteString("\n\nReceived arguments:\n")
	message.WriteString(prettyArguments(args))
	return errors.New(message.String())
}

// validationPath turns a pi-schema error path (a JSON Pointer) into the dotted path the model sees: `/edits/0/oldText` is `edits.0.oldText` (validation.ts:20-28: `~1` is decoded before `~0`).
func validationPath(pointer string) string {
	if pointer == "" {
		return "root"
	}
	segments := strings.Split(pointer[1:], "/")
	for i, segment := range segments {
		segments[i] = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
	}
	return strings.Join(segments, ".")
}

// prettyArguments is JSON.stringify(arguments, null, 2) of the arguments the call carries.
func prettyArguments(raw json.RawMessage) string {
	canonical, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return string(raw)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, canonical, "", "  "); err != nil {
		return string(raw)
	}
	return pretty.String()
}

// sameValue reports whether a and b are one JSON value, as JavaScript's `===` compares containers: by identity. Parse returns the value it was given when it changes nothing.
func sameValue(a, b any) bool {
	switch a := a.(type) {
	case *schema.Object:
		other, ok := b.(*schema.Object)
		return ok && a == other
	case []any:
		other, ok := b.([]any)
		return ok && len(a) == len(other) && (len(a) == 0 || &a[0] == &other[0])
	case nil:
		return b == nil
	case bool:
		other, ok := b.(bool)
		return ok && a == other
	case float64:
		other, ok := b.(float64)
		return ok && a == other
	case string:
		other, ok := b.(string)
		return ok && a == other
	}
	return false
}

// appendStringify appends JSON.stringify of a decoded or coerced JSON value: objects in own-key order, numbers and strings as JavaScript writes them (a lone surrogate as its \u escape).
func appendStringify(dst []byte, value any) []byte {
	switch value := value.(type) {
	case nil:
		return append(dst, "null"...)
	case bool:
		if value {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case float64:
		return append(dst, jsnumber.JSON(value)...)
	case string:
		return sdkjson.AppendStringifyString(dst, value)
	case []any:
		dst = append(dst, '[')
		for i, item := range value {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = appendStringify(dst, item)
		}
		return append(dst, ']')
	case *schema.Object:
		dst = append(dst, '{')
		first := true
		for key, member := range value.All() {
			if !first {
				dst = append(dst, ',')
			}
			first = false
			dst = sdkjson.AppendStringifyString(dst, key)
			dst = append(dst, ':')
			dst = appendStringify(dst, member)
		}
		return append(dst, '}')
	case map[string]any:
		keys := chordjson.MapOwnKeys(value)
		dst = append(dst, '{')
		for i, key := range keys {
			if i > 0 {
				dst = append(dst, ',')
			}
			dst = sdkjson.AppendStringifyString(dst, key)
			dst = append(dst, ':')
			dst = appendStringify(dst, value[key])
		}
		return append(dst, '}')
	}
	return append(dst, "null"...)
}

// toolFormats is pi-schema's `formats` entry point (packages/schema/src/formats/index.ts), shared by every compiled tool schema.
var toolFormats = sync.OnceValue(formats.Formats)

// toolArgumentValidators holds the compiled validator of each tool schema whose arguments were validated. A tool call validates against its tool's schema every time, so compiling it once per distinct schema
// leaves only the arguments to decode per call. Pi keeps one validator per schema object in a WeakMap (validation.ts:6).
var toolArgumentValidators validatorCache

const (
	// validatorCacheEntries bounds how many distinct tool schemas stay compiled; the oldest is dropped first. A session carries a few dozen tools, so 256 holds every tool of a session with room for extension churn; a dropped schema is compiled again on its next call.
	// pig additive (D112): Pi's validator cache is a WeakMap with no bound (validation.ts:6); the bound keeps a long-lived process from retaining a schema per tool definition it ever saw.
	validatorCacheEntries = 256
	// validatorCacheMaxBytes is the size of schema text above which a schema is validated without being kept: 1 MiB is far above any tool schema, and one oversized schema must not displace the small ones.
	// pig additive (D112): see validatorCacheEntries.
	validatorCacheMaxBytes = 1 << 20
)

// validatorCache is a bounded cache of compiled tool schemas by schema text.
type validatorCache struct {
	mu      sync.Mutex
	entries map[string]*validatorEntry
	order   []string
}

// validatorEntry is one schema's compiled validator, or the error compiling it failed with (Pi throws it on every call; the compile is deterministic, so it is kept).
type validatorEntry struct {
	raw       json.RawMessage
	once      sync.Once
	validator *schema.CoercingValidator
	err       error
}

// entry returns the cache entry for the schema text. A schema too large to cache gets an entry of its own that is not kept.
func (cache *validatorCache) entry(raw json.RawMessage) *validatorEntry {
	if len(raw) > validatorCacheMaxBytes {
		return &validatorEntry{raw: raw}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if found := cache.entries[string(raw)]; found != nil {
		return found
	}
	if cache.entries == nil {
		cache.entries = map[string]*validatorEntry{}
	}
	if len(cache.order) >= validatorCacheEntries {
		delete(cache.entries, cache.order[0])
		cache.order = cache.order[1:]
	}
	key := string(raw)
	created := &validatorEntry{raw: json.RawMessage(key)}
	cache.entries[key] = created
	cache.order = append(cache.order, key)
	return created
}

// compiled is the entry's validator, compiled on first use: pi-schema's compile with coercion, the formats, and a $ref that names nothing rejecting only the values at its place (validation.ts:14-17), so a dangling
// definition in an MCP server's schema does not make every call of the tool fail.
func (entry *validatorEntry) compiled() (*schema.CoercingValidator, error) {
	entry.once.Do(func() {
		document, err := decodeJSON(entry.raw)
		if err != nil {
			entry.err = fmt.Errorf("invalid tool parameters schema: %w", err)
			return
		}
		entry.validator, entry.err = schema.CompileCoercing(document, schema.CompileOptions{Formats: toolFormats(), UnresolvedRefs: schema.UnresolvedRefsReject})
	})
	return entry.validator, entry.err
}
