package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type provider struct{ calls atomic.Int32 }

func (*provider) ID() string   { return "cache-test" }
func (*provider) Close() error { return nil }
func (p *provider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.calls.Add(1)
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hello"}}, Provider: "cache-test", API: "cache-test-api", Model: "cache-test", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli(), Usage: ai.Usage{Output: 1, CacheRead: 100000, TotalTokens: 100001}}
	stream := ai.NewAssistantMessageEventStream()
	if err := stream.Push(ai.StartEvent{Partial: message}); err != nil {
		return nil, err
	}
	if err := stream.Push(ai.DoneEvent{Message: message, Reason: ai.StopReasonStop}); err != nil {
		return nil, err
	}
	return stream, nil
}
func printJSON(value any) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		panic(err)
	}
}
func main() {
	generated, ok := ai.LookupModelExact("anthropic/claude-opus-4-6")
	if !ok {
		panic("missing model")
	}
	model := generated.ToModel()
	model.PromptCache = ai.ModelPromptCache{"short": 300, "long": 3600}
	ttl := []any{}
	for _, retention := range []ai.CacheRetention{"", ai.CacheRetentionLong, ai.CacheRetentionNone} {
		value, ok := icodingagent.GetPromptCacheTtlMs(model, ai.StreamOptions{CacheRetention: retention})
		if ok {
			ttl = append(ttl, value)
		} else {
			ttl = append(ttl, nil)
		}
	}
	printJSON(ttl)
	dir, err := os.MkdirTemp("", "cache-decision-")
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			panic(err)
		}
	}()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"cacheWarming":"idle"}`), 0600); err != nil {
		panic(err)
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		panic(err)
	}
	p := &provider{}
	model.ID = "cache-test"
	model.Provider = p
	model.ProviderMeta = ai.ProviderMetadata{ProviderID: "cache-test", API: "cache-test-api", BaseURL: "https://cache.invalid"}
	model.PromptCache = ai.ModelPromptCache{"short": 11}
	model.Capabilities = ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 4096, InputCostPer1M: 10, OutputCostPer1M: 50, CacheReadCostPer1M: 0.25, CacheWriteCostPer1M: 12.5}
	runner := inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"cache_warming_decision": {func(...any) (any, error) {
		return &extension.CacheWarmingDecisionEventResult{Action: new(extension.CacheWarmingActionStop)}, nil
	}}}}}, dir)
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, SkipBuiltinTools: true})
	if err != nil {
		panic(err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range session.Events() {
		}
	}()
	defer func() {
		if err := session.Close(); err != nil {
			panic(err)
		}
		<-drained
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := session.Send(ctx, "hi"); err != nil {
		panic(err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		status := session.CacheWarmingStatus()
		if status != nil && status.Reason == "stopped by extension" {
			printJSON([]any{status.State, status.Reason, status.ExtensionOverride, p.calls.Load()})
			return
		}
		select {
		case <-ctx.Done():
			panic(status)
		case <-ticker.C:
		}
	}
}
