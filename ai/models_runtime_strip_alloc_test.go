package ai

import (
	"fmt"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

var (
	stripLookupChatSink *Model
	stripLookupAnySink  AnyModel
)

func newStripLookupTestModels(t *testing.T) *Models {
	t.Helper()
	kept := catalogMergeTestModel("custom", "kept-chat", "Kept Chat")
	kept.ProviderMeta.API = APIOpenAICompletions
	vertex := catalogMergeTestModel("custom", "vertex-chat", "Vertex Chat")
	vertex.ProviderMeta.API = APIGoogleVertex
	bedrock := catalogMergeTestModel("custom", "bedrock-chat", "Bedrock Chat")
	bedrock.ProviderMeta.API = APIBedrockConverseStream
	img := catalogMergeTestImage("custom", "kept-image", "Kept Image")

	baseline := make([]AnyModel, 0, 24)
	baseline = append(baseline, kept, vertex, bedrock, img)
	for i := range 20 {
		m := catalogMergeTestModel("custom", fmt.Sprintf("extra-%02d", i), fmt.Sprintf("Extra %d", i))
		m.ProviderMeta.API = APIOpenAICompletions
		baseline = append(baseline, m)
	}

	provider := CreateProvider(CreateProviderOptions{
		ID:     "custom",
		Auth:   configuredTestAuth(),
		Models: baseline,
		API:    catalogMergeTestStreams(),
	})
	models := CreateModels()
	t.Cleanup(func() { models.Close() })
	models.SetProvider(provider)
	if res := models.Refresh(t.Context()); len(res.Errors) != 0 {
		t.Fatalf("refresh errors: %v", res.Errors)
	}
	return models
}

// A stripped API hides its models from GetModel and GetModelOfType while leaving lookups allocation-free on hits and misses,
// including without build tags when an API is stripped at run time.
// Not t.Parallel(): pigstrip state is process-wide; Go runs non-parallel tests before resuming paused parallel tests.
func TestStrippedModelLookupAllocatesNothing(t *testing.T) {
	models := newStripLookupTestModels(t)

	// Confirm the vertex model is visible before stripping (unless the test binary was already built with the strip tag).
	if !pigstrip.Has(pigstrip.ListAPIs, string(APIGoogleVertex)) {
		if got := models.GetModel("custom", "vertex-chat"); got == nil {
			t.Fatal("unstripped GetModel(custom, vertex-chat) = nil, want model")
		}
		if got := models.GetModelOfType(ModelTypeChat, "custom", "vertex-chat"); got == nil {
			t.Fatal("unstripped GetModelOfType(chat, custom, vertex-chat) = nil, want model")
		}
	}

	// Strip after the provider published its catalog; the generation-keyed cache must observe the new strip immediately.
	t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(APIGoogleVertex)))

	if got := models.GetModel("custom", "vertex-chat"); got != nil {
		t.Fatalf("GetModel(custom, vertex-chat) = %v, want nil while %s is stripped", got, APIGoogleVertex)
	}
	if got := models.GetModelOfType(ModelTypeChat, "custom", "vertex-chat"); got != nil {
		t.Fatalf("GetModelOfType(chat, custom, vertex-chat) = %v, want nil while %s is stripped", got, APIGoogleVertex)
	}
	if got := models.GetModel("custom", "kept-chat"); got == nil {
		t.Fatal("GetModel(custom, kept-chat) = nil, want kept model")
	}
	if got := models.GetModelOfType(ModelTypeChat, "custom", "kept-chat"); got == nil {
		t.Fatal("GetModelOfType(chat, custom, kept-chat) = nil, want kept model")
	}
	if got := models.GetModelOfType(ModelTypeImage, "custom", "kept-image"); got == nil {
		t.Fatal("GetModelOfType(image, custom, kept-image) = nil, want image model")
	}
	if got := models.GetModelOfType(ModelTypeImage, "custom", "kept-chat"); got != nil {
		t.Fatalf("GetModelOfType(image, custom, kept-chat) = %v, want nil for wrong type", got)
	}

	// Warm once so the generation snapshot is published before measuring steady-state allocations.
	stripLookupChatSink = models.GetModel("custom", "kept-chat")
	stripLookupAnySink = models.GetModelOfType(ModelTypeChat, "custom", "kept-chat")

	if allocs := testing.AllocsPerRun(100, func() {
		stripLookupChatSink = models.GetModel("custom", "kept-chat")
		if stripLookupChatSink == nil {
			t.Fatal("missing kept-chat")
		}
	}); allocs != 0 {
		t.Fatalf("GetModel(hit) allocated %.1f allocs/run while stripped, want 0", allocs)
	}

	if allocs := testing.AllocsPerRun(100, func() {
		stripLookupChatSink = models.GetModel("custom", "vertex-chat")
		if stripLookupChatSink != nil {
			t.Fatal("stripped vertex-chat returned")
		}
	}); allocs != 0 {
		t.Fatalf("GetModel(stripped miss) allocated %.1f allocs/run while stripped, want 0", allocs)
	}

	if allocs := testing.AllocsPerRun(100, func() {
		stripLookupChatSink = models.GetModel("custom", "no-such-model")
		if stripLookupChatSink != nil {
			t.Fatal("unknown model returned")
		}
	}); allocs != 0 {
		t.Fatalf("GetModel(absent miss) allocated %.1f allocs/run while stripped, want 0", allocs)
	}

	if allocs := testing.AllocsPerRun(100, func() {
		stripLookupAnySink = models.GetModelOfType(ModelTypeChat, "custom", "kept-chat")
		if stripLookupAnySink == nil {
			t.Fatal("missing typed kept-chat")
		}
	}); allocs != 0 {
		t.Fatalf("GetModelOfType(hit) allocated %.1f allocs/run while stripped, want 0", allocs)
	}

	if allocs := testing.AllocsPerRun(100, func() {
		stripLookupAnySink = models.GetModelOfType(ModelTypeChat, "custom", "vertex-chat")
		if stripLookupAnySink != nil {
			t.Fatal("stripped typed vertex-chat returned")
		}
	}); allocs != 0 {
		t.Fatalf("GetModelOfType(stripped miss) allocated %.1f allocs/run while stripped, want 0", allocs)
	}

	if allocs := testing.AllocsPerRun(100, func() {
		stripLookupAnySink = models.GetModelOfType(ModelTypeChat, "custom", "no-such-model")
		if stripLookupAnySink != nil {
			t.Fatal("unknown typed model returned")
		}
	}); allocs != 0 {
		t.Fatalf("GetModelOfType(absent miss) allocated %.1f allocs/run while stripped, want 0", allocs)
	}

	// Second stripped API on top of the first: both hidden, still 0 allocs once warmed.
	t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(APIBedrockConverseStream)))
	if got := models.GetModel("custom", "bedrock-chat"); got != nil {
		t.Fatalf("GetModel(custom, bedrock-chat) = %v, want nil with two APIs stripped", got)
	}
	if got := models.GetModel("custom", "vertex-chat"); got != nil {
		t.Fatalf("GetModel(custom, vertex-chat) = %v, want nil with two APIs stripped", got)
	}
	if got := models.GetModel("custom", "kept-chat"); got == nil {
		t.Fatal("GetModel(custom, kept-chat) = nil with two APIs stripped")
	}
	if allocs := testing.AllocsPerRun(100, func() {
		stripLookupChatSink = models.GetModel("custom", "kept-chat")
	}); allocs != 0 {
		t.Fatalf("GetModel(hit, 2 stripped APIs) allocated %.1f allocs/run, want 0", allocs)
	}
}

// Lookups racing runtime Strip and undo never panic or see a torn snapshot, and observe the strip once Strip returns.
// This test does not call t.Parallel because pigstrip state is process-wide; running sequentially ensures Go finishes this test (and its Cleanup) before resuming paused parallel tests in package ai.
func TestStrippedModelLookupConcurrentWithStrip(t *testing.T) {
	models := newStripLookupTestModels(t)

	// Nil model entries in a custom provider slice must not panic in GetModel or GetModelOfType.
	nilModels := CreateModels()
	t.Cleanup(func() { nilModels.Close() })
	nilModels.SetProvider(&ModelsProvider{
		ID: "with-nil",
		GetModels: func() ([]*Model, error) {
			return []*Model{nil, catalogMergeTestModel("with-nil", "ok", "OK")}, nil
		},
		GetAllModels: func() ([]AnyModel, error) {
			return []AnyModel{nil, catalogMergeTestModel("with-nil", "ok", "OK")}, nil
		},
	})
	if got := nilModels.GetModel("with-nil", "ok"); got == nil {
		t.Fatal("GetModel skipped over nil returned nil")
	}
	if got := nilModels.GetModelOfType(ModelTypeChat, "with-nil", "ok"); got == nil {
		t.Fatal("GetModelOfType skipped over nil returned nil")
	}

	// Register the strip-undo cleanup before the loop so any failure or early exit restores process strip state.
	var lastUndo func()
	t.Cleanup(func() {
		if lastUndo != nil {
			lastUndo()
		}
	})

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = models.GetModel("custom", "kept-chat")
					_ = models.GetModel("custom", "vertex-chat")
					_ = models.GetModelOfType(ModelTypeChat, "custom", "kept-chat")
					_ = models.GetModelOfType(ModelTypeChat, "custom", "vertex-chat")
				}
			}
		})
	}
	t.Cleanup(func() {
		close(stop)
		readers.Wait()
	})

	alreadyStripped := pigstrip.Has(pigstrip.ListAPIs, string(APIGoogleVertex))
	for range 50 {
		lastUndo = pigstrip.Strip(pigstrip.ListAPIs, string(APIGoogleVertex))
		if got := models.GetModel("custom", "vertex-chat"); got != nil {
			t.Error("GetModel returned a model of a stripped API after Strip returned")
		}
		if got := models.GetModelOfType(ModelTypeChat, "custom", "vertex-chat"); got != nil {
			t.Error("GetModelOfType returned a model of a stripped API after Strip returned")
		}
		lastUndo()
		lastUndo = nil
		if !alreadyStripped {
			if got := models.GetModel("custom", "vertex-chat"); got == nil {
				t.Error("GetModel omitted the model after Strip was undone")
			}
		}
	}
}
