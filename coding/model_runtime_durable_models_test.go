package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// ModelRuntime implements pi-ai Models (model-runtime.ts:171), and the coding worker passes it as the Harness's models (session-worker.ts:787, experimental/durable/runtime.ts:141).
var _ durable.Models = (*ModelRuntime)(nil)

// TestModelRuntimeAnswersDurableHarnessGeneration opens a durable Harness whose models are a ModelRuntime and proves generation resolves the model and streams through the runtime's registry.
// Pi source: packages/durable/src/harness/types.ts
// mutation-checked: zeroing the results of Conversation.Submit fails it
func TestModelRuntimeAnswersDurableHarnessGeneration(t *testing.T) {
	ctx := context.Background()
	services, _ := nativeCompatServices(t, "", nil)
	runtime := services.ModelRuntime()
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "durable-faux"})
	t.Cleanup(func() { _ = faux.Close() })
	faux.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("runtime answer")}})})
	if err := runtime.RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	model := faux.GetModel()
	if runtime.GetModel(model.ProviderMeta.ProviderID, model.ID) == nil {
		t.Fatalf("runtime does not resolve %s/%s", model.ProviderMeta.ProviderID, model.ID)
	}

	opened, err := harness.OpenHarness(ctx, storage.NewMemoryStorage(), harness.HarnessOptions{Models: runtime, Registry: harness.CreateRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(context.Background()) })
	root, err := opened.Root(ctx, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: model.ProviderMeta.ProviderID, ModelId: model.ID})}})
	if err != nil {
		t.Fatal(err)
	}
	submission, err := root.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("question")})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := submission.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != durable.SubmissionDone || settled.Answer == nil {
		t.Fatalf("submission = %+v", settled)
	}
	entry, err := durable.Commit(ctx, root, func(tx durable.Tx) (*durable.EntryRecord, error) { return tx.Entry(*settled.Answer) })
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil || len(entry.Model) == 0 {
		t.Fatalf("answer entry = %+v", entry)
	}
	assistant, ok := entry.Model[0].(ai.AssistantMessage)
	if !ok || assistant.Provider != model.ProviderMeta.ProviderID || len(assistant.Content) == 0 {
		t.Fatalf("answer = %+v", entry.Model[0])
	}
	if text, _ := assistant.Content[0].(ai.TextContent); text.Text != "runtime answer" {
		t.Fatalf("answer text = %+v", assistant.Content)
	}
	if calls := faux.CallCount(); calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
}
