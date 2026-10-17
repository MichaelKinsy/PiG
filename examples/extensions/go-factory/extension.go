package exgo

import (
	"errors"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	ext := sdk.New("go-factory")
	ext.Tool("hello", "Return a hello greeting", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]any{"content": "hello from go"}, nil
	})
	// A tool with a parameter: the schema tells the model what to pass, the handler checks it, and a returned error is the
	// tool call's error result.
	ext.Tool("count_words", "Count the words in a text", sdk.Schema{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string", "description": "The text to count"}},
		"required":   []string{"text"},
	}, func(ctx sdk.Context, params map[string]any) (any, error) {
		text, _ := params["text"].(string)
		if text == "" {
			return nil, errors.New("text is required")
		}
		return map[string]any{"content": fmt.Sprintf("%d words", len(strings.Fields(text)))}, nil
	})
	return ext
}
