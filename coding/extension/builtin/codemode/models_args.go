package codemode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// The argument checks of the `models` globals. A script argument is raw JSON; nil is undefined, which reaches a global
// only as a missing trailing argument or a missing object member.

// withArticle is `an image`, `a classifier`.
//
// upstream: execute.ts withArticle
func withArticle(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an " + word
	}
	return "a " + word
}

// jsonKind is the first byte of a JSON value with leading space skipped, or 0 for undefined.
func jsonKind(raw json.RawMessage) byte {
	trimmed := strings.TrimLeft(string(raw), " \t\r\n")
	if trimmed == "" {
		return 0
	}
	return trimmed[0]
}

// isRecord reports whether raw is a JSON object (not an array or null).
//
// upstream: execute.ts isRecord
func isRecord(raw json.RawMessage) bool { return jsonKind(raw) == '{' }

// objectOf is the object raw holds, with its members in JavaScript's property order, or nil.
func objectOf(raw json.RawMessage) *orderedjson.Object {
	if !isRecord(raw) {
		return nil
	}
	canonical, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return nil
	}
	object, err := orderedjson.Parse(canonical)
	if err != nil {
		return nil
	}
	return object
}

// field is the member key of the object raw holds, or nil (undefined) when raw is not an object or lacks the member.
func field(raw json.RawMessage, key string) json.RawMessage {
	if object := objectOf(raw); object != nil {
		if value, ok := object.Get(key); ok {
			return value
		}
	}
	return nil
}

// jsonStringify is JSON.stringify of a script value: `undefined` for undefined.
func jsonStringify(raw json.RawMessage) string {
	if raw == nil {
		return "undefined"
	}
	canonical, err := jsonstringify.Canonicalize(raw)
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	return string(canonical)
}

// describeValue names a script value in an error message: `undefined`, `a string`, `an array`, or its keys
// (`{ prompt }`).
//
// upstream: execute.ts describeValue
func describeValue(raw json.RawMessage) string {
	switch jsonKind(raw) {
	case 0:
		return "undefined"
	case 'n':
		return "null"
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) == nil && len(items) == 0 {
			return "an empty array"
		}
		return "an array"
	case '{':
		object := objectOf(raw)
		if object == nil || object.Len() == 0 {
			return "{}"
		}
		keys := object.Keys()
		more := ""
		if len(keys) > 6 {
			keys, more = keys[:6], ", ..."
		}
		return "{ " + strings.Join(keys, ", ") + more + " }"
	case '"':
		return "a string"
	case 't', 'f':
		return "a boolean"
	}
	return "a number"
}

// isStrings reports whether values is a non-empty list of strings.
func isStrings(values []json.RawMessage) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if jsonKind(value) != '"' {
			return false
		}
	}
	return true
}

// memberValues are the values of the object raw holds, in property order.
func memberValues(raw json.RawMessage) []json.RawMessage {
	object := objectOf(raw)
	if object == nil {
		return nil
	}
	values := make([]json.RawMessage, 0, object.Len())
	for _, key := range object.Keys() {
		value, _ := object.Get(key)
		values = append(values, value)
	}
	return values
}

// classifierContextShape is upstream CLASSIFIER_CONTEXT_SHAPE.
const classifierContextShape = `{ state: { ... }, questions: { <id>: { type: "choice", instructions, criteria: { <label>: <meaning> } } | { type: "score", instructions, criteria: [<lowest level>, ..., <highest level>] } | { type: "bool", instructions, criteria: { true: <meaning>, false: <meaning> } } } }`

// checkClassifierContext checks a script's classifier context, so mistakes fail with the expected shape instead of a
// provider error.
//
// upstream: execute.ts checkClassifierContext
func checkClassifierContext(context json.RawMessage) error {
	fail := func(problem string) error {
		return fmt.Errorf("models.classify() %s. Expected context: %s. See \"Classify\" in %s.", problem, classifierContextShape, DocsPath())
	}
	if !isRecord(context) {
		return fail("expects a context object as its second argument, got " + describeValue(context))
	}
	if state := field(context, "state"); !isRecord(state) {
		return fail("context.state must be an object, got " + describeValue(state))
	}
	questions := field(context, "questions")
	object := objectOf(questions)
	if object == nil || object.Len() == 0 {
		return fail("context.questions must map question IDs to questions, got " + describeValue(questions))
	}
	for _, id := range object.Keys() {
		question, _ := object.Get(id)
		at := "context.questions." + id
		if !isRecord(question) {
			return fail(at + " must be a question object, got " + describeValue(question))
		}
		if jsonKind(field(question, "instructions")) != '"' {
			return fail(at + ".instructions must be a string")
		}
		criteria := field(question, "criteria")
		questionType, _ := stringArg(field(question, "type"))
		switch questionType {
		case "choice":
			if !isRecord(criteria) || !isStrings(memberValues(criteria)) {
				return fail(at + ` is a "choice" question, so criteria must map each label to its meaning`)
			}
		case "score":
			var levels []json.RawMessage
			if jsonKind(criteria) != '[' || json.Unmarshal(criteria, &levels) != nil || !isStrings(levels) {
				return fail(at + ` is a "score" question, so criteria must list the levels as strings, lowest first`)
			}
		case "bool":
			if !isRecord(criteria) || jsonKind(field(criteria, "true")) != '"' || jsonKind(field(criteria, "false")) != '"' {
				return fail(at + ` is a "bool" question, so criteria must be { true: string, false: string }`)
			}
		default:
			return fail(at + `.type must be "choice", "score", or "bool", got ` + jsonStringify(field(question, "type")))
		}
	}
	return nil
}

// checkImagesContext checks a script's image context, so mistakes such as `{ prompt }` fail with the expected shape.
//
// upstream: execute.ts checkImagesContext
func checkImagesContext(context json.RawMessage) error {
	fail := func(problem string) error {
		return fmt.Errorf("models.generateImages() %s. Expected context: { input: [{ type: \"text\", text: <prompt> }, ...optional { type: \"image\", data: <base64>, mimeType } references] }. See \"Generate images\" in %s.", problem, DocsPath())
	}
	if !isRecord(context) {
		return fail("expects a context object as its second argument, got " + describeValue(context))
	}
	input := field(context, "input")
	var blocks []json.RawMessage
	if jsonKind(input) != '[' || json.Unmarshal(input, &blocks) != nil || len(blocks) == 0 {
		return fail("context.input must be a non-empty array of blocks, got " + describeValue(input))
	}
	for index, block := range blocks {
		if isRecord(block) {
			blockType, _ := stringArg(field(block, "type"))
			if blockType == "text" && jsonKind(field(block, "text")) == '"' {
				continue
			}
			if blockType == "image" && jsonKind(field(block, "data")) == '"' && jsonKind(field(block, "mimeType")) == '"' {
				continue
			}
		}
		return fail(fmt.Sprintf("context.input[%d] must be a text or image block, got %s", index, describeValue(block)))
	}
	return nil
}

// imagesContextOf is the context checkImagesContext accepted, as ai.ImagesContext holds it: the text (and a string
// textSignature) of text blocks and the data and MIME type of image blocks. Upstream passes the checked object on
// as is, so another member of a block, whatever its type, never fails the call.
//
// upstream: execute.ts checkImagesContext (returns `context as unknown as ImagesContext`)
func imagesContextOf(context json.RawMessage) ai.ImagesContext {
	var blocks []json.RawMessage
	_ = json.Unmarshal(field(context, "input"), &blocks)
	input := make([]ai.ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if blockType, _ := stringArg(field(block, "type")); blockType == "text" {
			text, _ := stringArg(field(block, "text"))
			signature, _ := stringArg(field(block, "textSignature"))
			input = append(input, ai.TextContent{Text: text, TextSignature: signature})
			continue
		}
		data, _ := stringArg(field(block, "data"))
		mimeType, _ := stringArg(field(block, "mimeType"))
		input = append(input, ai.ImageContent{Data: data, MimeType: mimeType})
	}
	return ai.ImagesContext{Input: input}
}
