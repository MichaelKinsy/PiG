// Ports packages/durable/test/chat-support.ts.

package harness

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// fauxHandle is the faux provider surface the harness tests drive (pi-ai FauxProviderHandle).
type fauxHandle interface {
	SetResponses(responses []ai.FauxResponseStep)
	AppendResponses(responses []ai.FauxResponseStep)
	PendingResponseCount() int
	CallCount() int
	Provider() *ai.ModelsProvider
}

// chatState holds the models and registry that survive a close/reopen, like a host process's own objects.
type chatState struct {
	Faux     fauxHandle
	Models   *ai.Models
	Registry Registry
	Reports  *reportLog
	// mu guards Settings and Now, which tests change between decisions.
	mu       sync.Mutex
	settings HarnessSettings
	now      func() float64
}

// SetSettings replaces the live Harness settings.
func (setup *chatState) SetSettings(change func(settings *HarnessSettings)) {
	setup.mu.Lock()
	defer setup.mu.Unlock()
	change(&setup.settings)
}

// SetNow replaces the Harness clock.
func (setup *chatState) SetNow(now func() float64) {
	setup.mu.Lock()
	defer setup.mu.Unlock()
	setup.now = now
}

// chatSetup registers a faux provider (pi-ai fauxProvider(options)) on fresh models.
func chatSetup(t *testing.T, config ...ai.FauxConfig) *chatState {
	t.Helper()
	var options ai.FauxConfig
	if len(config) > 0 {
		options = config[0]
	}
	faux := ai.NewFauxProvider(options)
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	return &chatState{
		Faux:     faux,
		Models:   models,
		Registry: CreateRegistry(),
		Reports:  &reportLog{},
		now:      func() float64 { return float64(time.Now().UnixMilli()) },
	}
}

type openChatOptions struct {
	// env is one environment for every conversation; envFunc an Env function.
	env     env.ExecutionEnv
	envFunc func(ctx context.Context, target EnvTarget) (env.ExecutionEnv, error)
}

// openChat opens a Harness over storage and returns its root, configured with the faux model on first creation.
func openChat(t *testing.T, storage durable.Storage, setup *chatState, options ...openChatOptions) (Harness, Conversation) {
	t.Helper()
	var option openChatOptions
	if len(options) > 0 {
		option = options[0]
	}
	harnessOptions := HarnessOptions{
		Models:   setup.Models,
		Registry: setup.Registry,
		Settings: func() *HarnessSettings {
			setup.mu.Lock()
			defer setup.mu.Unlock()
			settings := setup.settings
			return &settings
		},
		Now: func() float64 {
			setup.mu.Lock()
			now := setup.now
			setup.mu.Unlock()
			return now()
		},
		OnReport: setup.Reports.add,
	}
	switch {
	case option.envFunc != nil:
		harnessOptions.Env = option.envFunc
	case option.env != nil:
		environment := option.env
		harnessOptions.Env = func(context.Context, EnvTarget) (env.ExecutionEnv, error) { return environment, nil }
	}
	harness, err := OpenHarness(testContext, storage, harnessOptions)
	if err != nil {
		t.Fatal(err)
	}
	root, err := harness.Root(testContext, &RootOptions{Agent: &AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	return harness, root
}

// allEntries returns the raw entries of a conversation, oldest first.
func allEntries(t *testing.T, conversation Conversation) []durable.EntryRecord {
	t.Helper()
	page, err := conversation.Entries(testContext, durable.EntryQuery{}, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]durable.EntryRecord, 0, len(page.Items))
	for _, v := range slices.Backward(page.Items) {
		entries = append(entries, v)
	}
	return entries
}

// textOf returns the text of the first text content of a message; ok is false when there is none.
func textOf(message ai.Message) (string, bool) {
	switch typed := message.(type) {
	case ai.UserMessage:
		if text, isText := typed.Content.(ai.UserText); isText {
			return string(text), true
		}
		if blocks, isBlocks := typed.Content.(ai.UserContentBlocks); isBlocks {
			for _, block := range blocks {
				if text, ok := block.(ai.TextContent); ok {
					return text.Text, true
				}
			}
		}
	case ai.AssistantMessage:
		for _, block := range typed.Content {
			if text, ok := block.(ai.TextContent); ok {
				return text.Text, true
			}
		}
	case ai.ToolResultMessage:
		for _, block := range typed.Content {
			if text, ok := block.(ai.TextContent); ok {
				return text.Text, true
			}
		}
	}
	return "", false
}

// waitFor polls check in real time until it holds; for waits that span throttle windows and timers.
func waitFor(t *testing.T, check func() bool, timeout ...time.Duration) {
	t.Helper()
	limit := 5 * time.Second
	if len(timeout) > 0 {
		limit = timeout[0]
	}
	deadline := time.Now().Add(limit)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatal("Condition was not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type unansweredStep struct {
	step    ai.FauxResponseStep
	reached chan struct{}
}

// unanswered is a faux response that never answers; the run stays busy until its generation is cancelled. reached is closed once the request was sent, after the generation's preparation and request commits.
func unanswered() unansweredStep {
	reached := make(chan struct{})
	var once sync.Once
	return unansweredStep{
		reached: reached,
		step: ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.FauxResponse, error) {
			once.Do(func() { close(reached) })
			<-options.Signal.Done()
			return ai.FauxResponse{}, context.Cause(options.Signal)
		}),
	}
}
