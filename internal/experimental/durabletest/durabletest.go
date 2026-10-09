// Package durabletest opens pi-durable Harnesses for the experimental tests: an in-memory one over the faux provider (packages/coding-agent/test/experimental-durable-support.ts openFauxConversation), and a file-backed SQLite one for worker processes. Both are wrapped by durableadapter.Session.
package durabletest

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableadapter"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// ModelRef is the faux model the root conversation starts with.
var ModelRef = services.ModelRef{Provider: "faux", ModelId: "faux-1"}

// Step is one faux provider response. It receives the run's context and the text of the last user message, and returns the answer text. A step that waits on ctx ends when the run is aborted.
type Step func(ctx context.Context, input string) (string, error)

// Text is a step that answers immediately.
func Text(answer string) Step {
	return func(context.Context, string) (string, error) { return answer, nil }
}

// Pending returns a step that never answers: it ends only when its run is aborted. Reached closes when the provider is called.
func Pending() (step Step, reached <-chan struct{}) {
	reach := make(chan struct{})
	var once sync.Once
	return func(ctx context.Context, _ string) (string, error) {
		once.Do(func() { close(reach) })
		<-ctx.Done()
		return "", context.Cause(ctx)
	}, reach
}

// FauxConversation is an opened Harness with its root conversation. Harness and Conversation are the same adapter.
type FauxConversation struct {
	Harness      *durableadapter.Session
	Conversation *durableadapter.Session
	// Created reports that OpenFile found no storage, so its root conversation is new.
	Created bool
	faux    interface{ Close() error }
}

// Close closes the Harness and the faux provider.
func (opened *FauxConversation) Close(ctx context.Context) error {
	err := opened.Harness.Close(ctx)
	if closeErr := opened.faux.Close(); err == nil {
		err = closeErr
	}
	return err
}

func fauxSteps(steps []Step) []ai.FauxResponseStep {
	result := make([]ai.FauxResponseStep, len(steps))
	for i, step := range steps {
		result[i] = ai.FauxFactoryStep(func(transcript ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			ctx := options.Signal
			if ctx == nil {
				ctx = context.Background()
			}
			answer, err := step(ctx, lastUserText(transcript.Messages()))
			if err != nil {
				return ai.FauxResponse{}.AssistantMessage(), err
			}
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(answer)}}.AssistantMessage(), nil
		})
	}
	return result
}

func lastUserText(messages []ai.Message) string {
	for _, message := range slices.Backward(messages) {
		user, ok := message.(ai.UserMessage)
		if !ok {
			continue
		}
		switch content := user.Content.(type) {
		case ai.UserText:
			return string(content)
		case ai.UserContentBlocks:
			var text strings.Builder
			for _, block := range content {
				if block, ok := block.(ai.TextContent); ok {
					text.WriteString(block.Text)
				}
			}
			return text.String()
		}
	}
	return ""
}

func open(ctx context.Context, store durable.Storage, steps []Step) (*FauxConversation, error) {
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	faux.SetResponses(fauxSteps(steps))
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	opened, err := harness.OpenHarness(ctx, store, harness.HarnessOptions{Models: models, Registry: harness.CreateRegistry()})
	if err != nil {
		_ = faux.Close()
		return nil, err
	}
	model := faux.GetModel()
	root, err := opened.Root(ctx, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: model.ProviderMeta.ProviderID, ModelId: model.ID})}})
	if err != nil {
		_ = opened.Close(ctx)
		_ = faux.Close()
		return nil, err
	}
	session := &durableadapter.Session{Harness: opened, Conversation: root}
	return &FauxConversation{Harness: session, Conversation: session, faux: faux}, nil
}

// OpenFauxConversation opens an in-memory Harness whose root conversation answers from the given steps in order.
func OpenFauxConversation(steps ...Step) *FauxConversation {
	opened, err := open(context.Background(), storage.NewMemoryStorage(), steps)
	if err != nil {
		panic(err)
	}
	return opened
}

// OpenFile opens a Harness over a SQLite file; the root conversation of an absent file starts with the faux model.
func OpenFile(path string, steps ...Step) (*FauxConversation, error) {
	return openFile(path, steps)
}

func openFile(path string, steps []Step) (*FauxConversation, error) {
	created := !fileExists(path)
	store, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		return nil, err
	}
	opened, err := open(context.Background(), store, steps)
	if err != nil {
		return nil, err
	}
	opened.Created = created
	return opened, nil
}

// ReadAgent reads the root conversation's agent document from a Harness file that no worker has open; it is nil for a file that was never written.
func ReadAgent(path string) (*services.AgentState, error) {
	if !fileExists(path) {
		return nil, nil
	}
	store, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		return nil, err
	}
	reader, err := harness.OpenHarness(context.Background(), store, harness.HarnessOptions{Registry: harness.CreateRegistry()})
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close(context.Background()) }()
	root, err := reader.Conversation(context.Background(), durable.ROOT_CONVERSATION_ID)
	if err != nil || root == nil {
		return nil, err
	}
	session := &durableadapter.Session{Harness: reader, Conversation: root}
	document, err := session.AgentDocument(context.Background(), root.Id())
	if err != nil || document == nil {
		return nil, err
	}
	defer document.Dispose()
	return document.Value(), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
