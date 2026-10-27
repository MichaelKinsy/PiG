package testfixture

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

// ProviderDiag registers, while it loads, one provider the model runtime accepts and one it rejects (a model with no "api"), and a tool that shows the extension loaded. Pi queues both registrations; the services that apply them report the rejected one as a diagnostic and the extension stays loaded (TestProviderRegistrationFailureIsADiagnosticAcrossSDKs).
func ProviderDiag() *sdk.Extension {
	ext := sdk.New("provider-diag")
	model := func(id string) []any {
		return []any{map[string]any{"id": id, "name": id, "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4096, "maxTokens": 1024}}
	}
	ext.RegisterProvider("diag-good", sdk.ProviderConfig{"api": "openai-completions", "baseUrl": "https://good.invalid/v1", "apiKey": "key", "models": model("good-model")})
	ext.RegisterProvider("diag-bad", sdk.ProviderConfig{"baseUrl": "https://bad.invalid/v1", "apiKey": "key", "models": model("bad-model")})
	ext.RegisterTool(sdk.ToolDefinition{
		Name: "diag_ping", Label: "diag_ping", Description: "diag_ping", Parameters: sdk.Schema{"type": "object"},
		Execute: func(sdk.Context, map[string]any) (any, error) { return "pong", nil },
	})
	return ext
}
