package testfixture

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ModelCall registers /model-call, which makes one model call through the host
// for the provider named in its arguments. Every SDK's model calls are host
// calls, so the request the provider receives is PiG's own.
func ModelCall() *sdk.Extension {
	ext := sdk.New("model-call")
	ext.Command("model-call", "Make one model call", func(ctx sdk.Context, args string) error {
		result := ctx.ModelRegistry().Complete(
			map[string]any{"provider": args, "modelId": "m", "api": "openai-completions"},
			map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hello", "timestamp": 1}}},
			map[string]any{"sessionId": "sdk-session"},
		)
		if fmt.Sprint(result["stopReason"]) != "stop" {
			return fmt.Errorf("model call = %v", result)
		}
		return nil
	})
	return ext
}
