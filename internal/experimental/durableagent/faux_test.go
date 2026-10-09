package durableagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// fauxAgent is a Harness over the faux provider with its root conversation on the faux model.
type fauxAgent struct {
	// pending is the count of faux responses not yet used.
	pending func() int
	harness harness.Harness
	root    harness.Conversation
}

// openFauxAgent opens a Harness over store with registry and an environment of cwd, and answers its requests from steps in order.
func openFauxAgent(t *testing.T, store durable.Storage, registry harness.Registry, cwd string, steps ...ai.FauxResponseStep) *fauxAgent {
	t.Helper()
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	faux.SetResponses(steps)
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	envs := NewExecutionEnvs(cwd)
	opened, err := harness.OpenHarness(context.Background(), store, harness.HarnessOptions{
		Models: models, Registry: registry, Env: envs.Env,
		OnReport: func(err error) { t.Errorf("report: %v", err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = opened.Close(context.Background())
		_ = envs.Cleanup(context.Background())
		_ = faux.Close()
	})
	model := faux.GetModel()
	root, err := opened.Root(context.Background(), &harness.RootOptions{Agent: &harness.AgentChange{
		Cwd: harness.SetTo(cwd), Model: harness.SetTo(durable.ModelRef{Provider: model.ProviderMeta.ProviderID, ModelId: model.ID}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &fauxAgent{pending: faux.PendingResponseCount, harness: opened, root: root}
}

func answerStep(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}})
}

func callStep(name string, args map[string]any, id string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(name, args, &ai.FauxToolCallOptions{ID: id})}, StopReason: "toolUse"})
}

// prompt submits text to conversation and waits for its settlement.
func prompt(t *testing.T, conversation harness.Conversation, text string) durable.SubmissionRecord {
	t.Helper()
	submission, err := conversation.Submit(context.Background(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text)})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := submission.Wait(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return settled
}

func entriesOf(t *testing.T, conversation harness.Conversation) []durable.EntryRecord {
	t.Helper()
	page, err := conversation.Entries(context.Background(), durable.EntryQuery{}, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}
