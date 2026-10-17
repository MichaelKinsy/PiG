package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
)

// The chat completions body of an OpenRouter image generation is read untyped (openrouter-images.ts generateImages): a member of another type is
// skipped or fails with the TypeError V8 throws for the expression that reads it. These functions walk the parsed JSON in Pi's order, so the id, the usage
// and the text read before a failing member stay on the result.

// parseOpenRouterImagesBody is the SDK's response.json() followed by the reads of generateImages: an empty body is undefined, which fails at the first read.
func parseOpenRouterImagesBody(data []byte) (any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, jsTypeError("undefined", "id")
	}
	if syntax := jsonparse.Validate(data); syntax != nil {
		return nil, syntax
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// jsTypeError is the TypeError V8 throws for a property read on an undefined or null value.
func jsTypeError(word, property string) error {
	return fmt.Errorf("Cannot read properties of %s (reading '%s')", word, property)
}

// jsMember is `value[key]` for a parsed JSON value that is not undefined or null; a primitive has no such member.
func jsMember(value any, key string) (any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	member, ok := object[key]
	return member, ok
}

// jsRead is `value.key`: a missing member is undefined (ok false), and reading a member of undefined or null throws.
func jsRead(value any, present bool, key string) (any, bool, error) {
	if !present {
		return nil, false, jsTypeError("undefined", key)
	}
	if value == nil {
		return nil, false, jsTypeError("null", key)
	}
	member, ok := jsMember(value, key)
	return member, ok, nil
}

func jsTruthy1(value any, present bool) bool {
	if !present {
		return false
	}
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case float64:
		return value != 0 && !math.IsNaN(value)
	default:
		return true
	}
}

// readOpenRouterImagesChat applies generateImages to a parsed body: the response id, the usage, then the first choice's text and images.
func readOpenRouterImagesChat(response any, model ImageModel, out *AssistantImages) error {
	if response == nil {
		return jsTypeError("null", "id")
	}
	if id, ok := jsMember(response, "id"); ok {
		if text, isString := id.(string); isString {
			out.ResponseID = text
		}
	}
	if usage, ok := jsMember(response, "usage"); ok && jsTruthy1(usage, true) {
		parsed, err := parseOpenRouterImagesUsageValue(usage, model)
		if err != nil {
			return err
		}
		out.Usage = parsed
	}
	choices, present := jsMember(response, "choices")
	var choice any
	var chosen bool
	switch list := choices.(type) {
	case []any:
		if len(list) > 0 {
			choice, chosen = list[0], true
		}
	case string:
		// "abc"[0] is the first character.
		if list != "" {
			choice, chosen = string([]rune(list)[:1]), true
		}
	default:
		if !present {
			return jsTypeError("undefined", "0")
		}
		if choices == nil {
			return jsTypeError("null", "0")
		}
	}
	if !chosen || !jsTruthy1(choice, true) {
		return nil
	}
	message, hasMessage, err := jsRead(choice, true, "message")
	if err != nil {
		return err
	}
	content, hasContent, err := jsRead(message, hasMessage, "content")
	if err != nil {
		return err
	}
	if text, ok := content.(string); hasContent && ok && text != "" {
		out.Output = append(out.Output, TextContent{Text: text})
	}
	images, hasImages := jsMember(message, "images")
	if !hasImages || images == nil {
		return nil
	}
	list, isList := images.([]any)
	if !isList {
		if _, isString := images.(string); isString {
			// A string iterates by character; no character has an image_url.
			return nil
		}
		return jsNotIterable(images)
	}
	for _, image := range list {
		imageURL, hasURL, err := jsRead(image, true, "image_url")
		if err != nil {
			return err
		}
		if _, isString := imageURL.(string); !isString {
			imageURL, hasURL = jsMember(imageURL, "url")
		}
		if !hasURL || imageURL == nil {
			continue
		}
		text, isString := imageURL.(string)
		if !isString {
			return errors.New("imageUrl?.startsWith is not a function")
		}
		mediaType, data, ok := parseDataImageURL(text)
		if !ok {
			continue
		}
		out.Output = append(out.Output, ImageContent{MimeType: mediaType, Data: data})
	}
	return nil
}

// jsNotIterable is the TypeError V8 throws for `for (const x of value)` when value is a number, boolean or object.
func jsNotIterable(value any) error {
	switch value := value.(type) {
	case float64:
		return fmt.Errorf("number %s is not iterable (cannot read property Symbol(Symbol.iterator))", jsJSONValueString(mustMarshalJSON(value)))
	case bool:
		return fmt.Errorf("boolean %t is not iterable (cannot read property Symbol(Symbol.iterator))", value)
	default:
		return errors.New("object is not iterable (cannot read property Symbol(Symbol.iterator))")
	}
}

func mustMarshalJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

// parseOpenRouterImagesUsageValue is parseUsage over an untyped usage: absent and null counts are 0, and a usage or details value that is not an object has no counts. A count that is not a whole number is not modeled, as the Go usage holds integers.
func parseOpenRouterImagesUsageValue(value any, model ImageModel) (*Usage, error) {
	count := func(holder any, key string) (int, error) {
		member, ok := jsMember(holder, key)
		if !ok || member == nil {
			return 0, nil
		}
		number, isNumber := member.(float64)
		if !isNumber || number != math.Trunc(number) || math.Abs(number) > 1<<53 {
			return 0, fmt.Errorf("json: cannot unmarshal %s into Go struct field openRouterImagesUsage.%s of type int", jsKind(member), key)
		}
		return int(number), nil
	}
	var raw openRouterImagesUsage
	var err error
	if raw.PromptTokens, err = count(value, "prompt_tokens"); err != nil {
		return nil, err
	}
	if raw.CompletionTokens, err = count(value, "completion_tokens"); err != nil {
		return nil, err
	}
	if details, ok := jsMember(value, "prompt_tokens_details"); ok && details != nil {
		raw.PromptTokensDetails = &struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		}{}
		if raw.PromptTokensDetails.CachedTokens, err = count(details, "cached_tokens"); err != nil {
			return nil, err
		}
		if raw.PromptTokensDetails.CacheWriteTokens, err = count(details, "cache_write_tokens"); err != nil {
			return nil, err
		}
	}
	return parseOpenRouterImagesUsage(raw, model), nil
}

func jsKind(value any) string {
	switch value.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case []any:
		return "array"
	default:
		return "object"
	}
}
