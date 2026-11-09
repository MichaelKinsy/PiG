package testfixture

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ProviderSnapshotBurst is how many text_delta events the snapshot provider pushes, each after one more "x" was appended to the shared partial message.
const ProviderSnapshotBurst = 100

// ProviderSnapshotTail is appended to the shared message after the last text_delta was pushed and before the done event.
const ProviderSnapshotTail = "-AFTER"

// registerProviderSnapshot registers a provider whose producer goroutine appends to one shared partial message and pushes it, without yielding between the
// two. An event the host receives must show the message as it was when the author pushed it: Pi's providers wait for the network between pushes, so the
// consumer has turned the event into its wire form before the producer touches the message again.
func registerProviderSnapshot(ext *sdk.Extension) {
	ext.RegisterProvider("snapshot-provider", sdk.ProviderConfig{
		"api": "snapshot-extension-api", "baseUrl": "https://extension.invalid", "apiKey": "extension-key",
		"models": []any{map[string]any{"id": "faux", "name": "Faux", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096}},
		"streamSimple": func(_ sdk.Context, model, _, _ map[string]any) (*sdk.ModelEventStream, error) {
			block := map[string]any{"type": "text", "text": ""}
			message := map[string]any{"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": []any{block}, "stopReason": "stop", "timestamp": 1, "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}
			stream := sdk.CreateAssistantMessageEventStream()
			go func() {
				stream.Push(map[string]any{"type": "start", "partial": message})
				text := ""
				for range ProviderSnapshotBurst {
					text += "x"
					block["text"] = text
					stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "x", "partial": message})
				}
				block["text"] = text + ProviderSnapshotTail
				stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
			}()
			return stream, nil
		},
	})
}

// ProviderSnapshotWant is the text the host must see in each event: the empty message of start, i "x" in the i-th text_delta, and every "x" plus the tail in done.
func ProviderSnapshotWant() (start string, deltas []string, done string) {
	for i := 1; i <= ProviderSnapshotBurst; i++ {
		deltas = append(deltas, strings.Repeat("x", i))
	}
	return "", deltas, strings.Repeat("x", ProviderSnapshotBurst) + ProviderSnapshotTail
}
