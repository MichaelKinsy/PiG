package exgo

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	ext := sdk.New("go-factory")
	ext.Tool("hello", "Return a hello greeting", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		return map[string]any{"content": "hello from go"}, nil
	})
	return ext
}
