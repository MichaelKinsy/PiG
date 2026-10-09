package cli

// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts handleCommand: Pi reads each command's fields without checking their types, so a
// missing or wrongly typed field takes the JavaScript path: a template string coerces it, a method call on it throws a TypeError whose message
// the response carries, and a truthiness test reads it. Pig decodes commands into typed structs, which reject such a field with a decoder
// message; this adapter answers the same way Pi does for the fields below before the typed decode runs.

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// rpcJSValue is one command field as JavaScript sees it after JSON.parse.
type rpcJSValue struct {
	raw     json.RawMessage
	present bool
}

func (v rpcJSValue) isString() bool { return v.present && len(v.raw) > 0 && v.raw[0] == '"' }
func (v rpcJSValue) isNull() bool   { return v.present && bytes.Equal(v.raw, []byte("null")) }
func (v rpcJSValue) isBool() bool {
	return v.present && (bytes.Equal(v.raw, []byte("true")) || bytes.Equal(v.raw, []byte("false")))
}

// text is the template-literal coercion `${value}`.
func (v rpcJSValue) text() string {
	if !v.present {
		return "undefined"
	}
	var decoded any
	if err := json.Unmarshal(v.raw, &decoded); err != nil {
		return "undefined"
	}
	return rpcJSTemplate(decoded)
}

func rpcJSTemplate(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return tui.JSNumberString(typed)
	case []any:
		parts := make([]string, len(typed))
		for i, element := range typed {
			if element != nil {
				parts[i] = rpcJSTemplate(element)
			}
		}
		return strings.Join(parts, ",")
	default:
		return "[object Object]"
	}
}

// truthy is JavaScript truthiness of a parsed JSON value.
func (v rpcJSValue) truthy() bool {
	if !v.present {
		return false
	}
	var decoded any
	if json.Unmarshal(v.raw, &decoded) != nil {
		return false
	}
	switch typed := decoded.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0 && !math.IsNaN(typed)
	default:
		return true
	}
}

// cannotRead is the TypeError message of `value.<property>(...)`: undefined and null have no properties, and any other value lacks the method.
func (v rpcJSValue) cannotRead(property, expression string) string {
	switch {
	case !v.present:
		return "Cannot read properties of undefined (reading '" + property + "')"
	case v.isNull():
		return "Cannot read properties of null (reading '" + property + "')"
	}
	return expression + "." + property + " is not a function"
}

// rpcJSAdapt applies Pi's untyped reading of a command's fields. It returns the message of the error Pi's handler throws or answers, or rewrites
// the command's fields to the typed values Pi's handler ends up using. Known difference: set_steering_mode, set_follow_up_mode,
// set_auto_compaction and set_auto_retry store and report any JSON value in Pi (get_state echoes it); Pig coerces a non-boolean `enabled` to
// its truthiness and rejects a non-string `mode` with the decoder's message. A compact `customInstructions` that is not a string is rejected
// with the decoder's message where Pi's compaction reads it (state-dependent TypeErrors), and new_session and export_html treat a non-string
// field as absent. images are read as rpcJSImages describes.
func rpcJSAdapt(env RPCCommandEnvelope) (RPCCommandEnvelope, string, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(env.Raw, &fields) != nil {
		return env, "", false
	}
	field := func(name string) rpcJSValue {
		raw, ok := fields[name]
		return rpcJSValue{raw: raw, present: ok}
	}
	rewrite := func(name string, value any) (RPCCommandEnvelope, string, bool) {
		encoded, err := json.Marshal(value)
		if err != nil {
			return env, "", false
		}
		fields[name] = encoded
		raw, err := json.Marshal(fields)
		if err != nil {
			return env, "", false
		}
		env.Raw = raw
		return env, "", false
	}
	drop := func(name string) (RPCCommandEnvelope, string, bool) {
		delete(fields, name)
		raw, err := json.Marshal(fields)
		if err != nil {
			return env, "", false
		}
		env.Raw = raw
		return env, "", false
	}
	switch env.Type {
	case RPCCommandNewSession:
		// session-manager.ts newSession({ parentSession }) writes any value into the header; only a path string names a parent here.
		if parent := field("parentSession"); parent.present && !parent.isString() {
			return drop("parentSession")
		}
	case RPCCommandExportHtml:
		if path := field("outputPath"); path.present && !path.isString() {
			return drop("outputPath")
		}
	case RPCCommandPrompt, RPCCommandSteer, RPCCommandFollowUp:
		// agent-session.ts:1977 text.startsWith("/") in prompt, steer and followUp.
		if message := field("message"); !message.isString() {
			return env, message.cannotRead("startsWith", "text"), true
		}
		if images := field("images"); images.present {
			if message, rewritten := rpcJSImages(images, env.Type == RPCCommandPrompt); message != "" {
				return env, message, true
			} else if rewritten != nil {
				return rewrite("images", rewritten)
			} else if !images.isNull() && !images.truthy() {
				return drop("images")
			}
		}
	case RPCCommandSetModel:
		provider, modelID := field("provider"), field("modelId")
		if !provider.isString() || !modelID.isString() {
			// A model's provider and id are strings, so a non-string never equals them.
			return env, "Model not found: " + provider.text() + "/" + modelID.text(), true
		}
	case RPCCommandSetSessionName:
		if name := field("name"); !name.isString() {
			return env, name.cannotRead("trim", "command.name"), true
		}
	case RPCCommandSwitchSession:
		if path := field("sessionPath"); !path.isString() {
			return env, path.cannotRead("startsWith", "normalized"), true
		}
	case RPCCommandFork:
		if entryID := field("entryId"); !entryID.isString() {
			return env, "Invalid entry ID for forking", true
		}
	case RPCCommandGetEntries:
		if since := field("since"); since.present && !since.isString() {
			return env, "Entry not found: " + since.text(), true
		}
	case RPCCommandBash:
		if command := field("command"); !command.isString() {
			return rewrite("command", command.text())
		}
		if exclude := field("excludeFromContext"); exclude.present && !exclude.isBool() {
			return rewrite("excludeFromContext", exclude.truthy())
		}
	case RPCCommandSetThinkingLevel:
		if level := field("level"); !level.isString() {
			// setThinkingLevel treats a value that is not an available level as unavailable; no string equals a non-string.
			return rewrite("level", "\x00")
		}
	case RPCCommandSetAutoCompaction, RPCCommandSetAutoRetry:
		if enabled := field("enabled"); !enabled.isBool() {
			return rewrite("enabled", enabled.truthy())
		}
	}
	return env, "", false
}

const nodeBufferFromMessage = "The first argument must be of type string or an instance of Buffer, ArrayBuffer, or Array or an Array-like Object. Received "

// rpcJSImages reads a command's images the way Pi's handlers do. prompt validates each image while it converts it (Buffer.from(data, "base64"), then
// mimeType.split), so the first malformed image fails with Node's or V8's message. steer and follow_up only spread the array, so a malformed image
// queues; Pig cannot queue what it cannot decode, so it drops such an image and keeps the well-formed ones. A falsy value or a string (whose
// characters are not images) is no image. The second result replaces the field when images were dropped.
func rpcJSImages(images rpcJSValue, isPrompt bool) (string, []json.RawMessage) {
	if !images.truthy() {
		return "", nil
	}
	if images.isString() {
		if isPrompt {
			return nodeBufferFromMessage + "undefined", nil
		}
		return "", []json.RawMessage{}
	}
	var list []json.RawMessage
	if json.Unmarshal(images.raw, &list) != nil {
		if isPrompt {
			return "images is not iterable", nil
		}
		return "Spread syntax requires ...iterable[Symbol.iterator] to be a function", nil
	}
	kept := make([]json.RawMessage, 0, len(list))
	for _, raw := range list {
		var element map[string]json.RawMessage
		isObject := json.Unmarshal(raw, &element) == nil && element != nil
		valid := false
		if isObject {
			data, mime := element["data"], element["mimeType"]
			valid = len(data) > 0 && data[0] == '"' && len(mime) > 0 && mime[0] == '"'
		}
		if valid {
			kept = append(kept, raw)
			continue
		}
		if isPrompt {
			return rpcJSImageError(raw, element), nil
		}
	}
	if len(kept) == len(list) {
		return "", nil
	}
	return "", kept
}

// rpcJSImageError is the message prompt fails with for the first image that cannot convert.
func rpcJSImageError(raw json.RawMessage, element map[string]json.RawMessage) string {
	if bytes.Equal(raw, []byte("null")) {
		return "Cannot read properties of null (reading 'data')"
	}
	data := rpcJSValue{raw: element["data"], present: element != nil && element["data"] != nil}
	switch {
	case !data.isString() && data.present && len(data.raw) > 0 && data.raw[0] == '[':
		// An array is an array-like Buffer source; Pig cannot decode it as base64 text.
	case !data.isString():
		return nodeBufferFromMessage + rpcJSReceived(data)
	}
	mime := rpcJSValue{raw: element["mimeType"], present: element["mimeType"] != nil}
	if mime.isString() {
		return ""
	}
	return mime.cannotRead("split", "mimeType")
}

// rpcJSReceived is the tail of Node's ERR_INVALID_ARG_TYPE for a value that is not a string or buffer.
func rpcJSReceived(v rpcJSValue) string {
	if !v.present {
		return "undefined"
	}
	var decoded any
	if json.Unmarshal(v.raw, &decoded) != nil {
		return "undefined"
	}
	switch typed := decoded.(type) {
	case nil:
		return "null"
	case bool:
		if typed {
			return "type boolean (true)"
		}
		return "type boolean (false)"
	case float64:
		if typed == 0 && math.Signbit(typed) {
			return "type number (-0)"
		}
		return "type number (" + tui.JSNumberString(typed) + ")"
	default:
		return "an instance of Object"
	}
}
