package testfixture

import (
	"errors"
	"os"
	"strconv"
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
				var text strings.Builder
				for range ProviderSnapshotBurst {
					text.WriteString("x")
					block["text"] = text.String()
					stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "x", "partial": message})
				}
				block["text"] = text.String() + ProviderSnapshotTail
				stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
			}()
			return stream, nil
		},
	})
}

// ProviderCancelBurstBefore is how many text_delta events the cancel-burst provider pushes before it waits for the cancel of its request; it pushes as many after it, and done.
const ProviderCancelBurstBefore = 3

// registerProviderCancelBurst registers a provider whose producer goroutine is cancelled in the middle of a burst: it pushes start and ProviderCancelBurstBefore text_delta events,
// waits until its request is cancelled, and goes on pushing as if nothing had happened. The host has the events pushed before the cancel, the SDK forwards none of the later
// ones, and the SDK answers the request with the abort error, never with a result.
func registerProviderCancelBurst(ext *sdk.Extension) {
	ext.RegisterProvider("cancel-burst-provider", sdk.ProviderConfig{
		"api": "cancel-burst-extension-api", "baseUrl": "https://extension.invalid", "apiKey": "extension-key",
		"models": []any{map[string]any{"id": "faux", "name": "Faux", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096}},
		"streamSimple": func(ctx sdk.Context, model, _, _ map[string]any) (*sdk.ModelEventStream, error) {
			block := map[string]any{"type": "text", "text": ""}
			message := map[string]any{"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": []any{block}, "stopReason": "stop", "timestamp": 1, "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}
			stream := sdk.CreateAssistantMessageEventStream()
			go func() {
				stream.Push(map[string]any{"type": "start", "partial": message})
				for i := range 2 * ProviderCancelBurstBefore {
					if i == ProviderCancelBurstBefore {
						<-ctx.Done()
					}
					block["text"] = strings.Repeat("x", i+1)
					stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "x", "partial": message})
				}
				stream.Push(map[string]any{"type": "done", "reason": "stop", "message": message})
			}()
			return stream, nil
		},
	})
}

// registerProviderCancelEnd registers a provider that answers the cancel of its request by ending its stream with a result: it pushes start and ProviderCancelBurstBefore
// text_delta events, waits until its request is cancelled, and calls End with the message, pushing nothing more.
func registerProviderCancelEnd(ext *sdk.Extension) {
	ext.RegisterProvider("cancel-end-provider", sdk.ProviderConfig{
		"api": "cancel-end-extension-api", "baseUrl": "https://extension.invalid", "apiKey": "extension-key",
		"models": []any{map[string]any{"id": "faux", "name": "Faux", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096}},
		"streamSimple": func(ctx sdk.Context, model, _, _ map[string]any) (*sdk.ModelEventStream, error) {
			block := map[string]any{"type": "text", "text": ""}
			message := map[string]any{"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": []any{block}, "stopReason": "stop", "timestamp": 1, "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}
			stream := sdk.CreateAssistantMessageEventStream()
			go func() {
				stream.Push(map[string]any{"type": "start", "partial": message})
				for i := range ProviderCancelBurstBefore {
					block["text"] = strings.Repeat("x", i+1)
					stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "x", "partial": message})
				}
				<-ctx.Done()
				stream.End(message)
			}()
			return stream, nil
		},
	})
}

// ProviderCancelErrorMessage is the errorMessage of the message the cancel-error provider ends its stream with once its request's signal aborts.
const ProviderCancelErrorMessage = "the author saw the abort"

// registerProviderCancelError registers a provider that answers the abort of its request with an error event that holds its partial message: it pushes start and
// ProviderCancelBurstBefore text_delta events, waits until its request's signal aborts, and pushes the error event (reason aborted) whose message is the partial with
// stopReason aborted and ProviderCancelErrorMessage.
func registerProviderCancelError(ext *sdk.Extension) {
	ext.RegisterProvider("cancel-error-provider", sdk.ProviderConfig{
		"api": "cancel-error-extension-api", "baseUrl": "https://extension.invalid", "apiKey": "extension-key",
		"models": []any{map[string]any{"id": "faux", "name": "Faux", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096}},
		"streamSimple": func(ctx sdk.Context, model, _, _ map[string]any) (*sdk.ModelEventStream, error) {
			block := map[string]any{"type": "text", "text": ""}
			message := map[string]any{"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": []any{block}, "stopReason": "stop", "timestamp": 1, "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}
			stream := sdk.CreateAssistantMessageEventStream()
			go func() {
				stream.Push(map[string]any{"type": "start", "partial": message})
				for i := range ProviderCancelBurstBefore {
					block["text"] = strings.Repeat("x", i+1)
					stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": "x", "partial": message})
				}
				<-ctx.Done()
				message["stopReason"] = "aborted"
				message["errorMessage"] = ProviderCancelErrorMessage
				stream.Push(map[string]any{"type": "error", "reason": "aborted", "error": message})
			}()
			return stream, nil
		},
	})
}

// ProviderCancelThrowMessage is the error the cancel-throw provider's streamSimple fails with once its request's signal aborts.
const ProviderCancelThrowMessage = "the author's stream threw at the abort"

// registerProviderCancelThrow registers a provider whose streamSimple waits until its request's signal aborts and fails with ProviderCancelThrowMessage, showing nothing.
func registerProviderCancelThrow(ext *sdk.Extension) {
	ext.RegisterProvider("cancel-throw-provider", sdk.ProviderConfig{
		"api": "cancel-throw-extension-api", "baseUrl": "https://extension.invalid", "apiKey": "extension-key",
		"models": []any{map[string]any{"id": "faux", "name": "Faux", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096}},
		"streamSimple": func(ctx sdk.Context, _, _, _ map[string]any) (*sdk.ModelEventStream, error) {
			<-ctx.Done()
			return nil, errors.New(ProviderCancelThrowMessage)
		},
	})
}

// registerProviderCancelNever registers a provider whose author never reacts to the abort of its request: it pushes start and one text_delta whose text is the id of its
// process, waits until its request's signal aborts, and returns: it pushes nothing more and never ends its stream.
func registerProviderCancelNever(ext *sdk.Extension) {
	ext.RegisterProvider("cancel-never-provider", sdk.ProviderConfig{
		"api": "cancel-never-extension-api", "baseUrl": "https://extension.invalid", "apiKey": "extension-key",
		"models": []any{map[string]any{"id": "faux", "name": "Faux", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096}},
		"streamSimple": func(ctx sdk.Context, model, _, _ map[string]any) (*sdk.ModelEventStream, error) {
			block := map[string]any{"type": "text", "text": ""}
			message := map[string]any{"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": []any{block}, "stopReason": "stop", "timestamp": 1, "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}
			stream := sdk.CreateAssistantMessageEventStream()
			go func() {
				stream.Push(map[string]any{"type": "start", "partial": message})
				pid := strconv.Itoa(os.Getpid())
				block["text"] = pid
				stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": pid, "partial": message})
				<-ctx.Done()
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
