package codemode

// pi: packages/durable/src/codemode/models.ts

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// blockingCatalog is a catalog whose getAvailableOfType waits for the call's context, as a provider request does.
type blockingCatalog struct {
	durable.Models
	started chan struct{}
}

func (c *blockingCatalog) GetModelsOfType(ai.ModelType, ...string) []ai.AnyModel { return nil }
func (c *blockingCatalog) GetAvailableOfType(ctx context.Context, _ ai.ModelType, _ ...string) ([]ai.AnyModel, error) {
	close(c.started)
	<-ctx.Done()
	return nil, context.Cause(ctx)
}
func (c *blockingCatalog) GetModelOfType(ai.ModelType, string, string) ai.AnyModel { return nil }
func (c *blockingCatalog) Classify(context.Context, *ai.ClassifierModel, ai.ClassifierContext, ...ai.ModelsClassifierOptions) ai.ClassifierResult {
	return ai.ClassifierResult{}
}
func (c *blockingCatalog) GenerateImages(context.Context, *ai.ImageModel, ai.ImagesContext, ...ai.ModelsImagesOptions) ai.AssistantImages {
	return ai.AssistantImages{}
}

// models.ts catalogCall: models.getAvailableOfType() awaits the catalog with the call's abort signal, so the script waits
// for the catalog, and aborting the call reaches it and the call rejects with the abort's cause.
func TestGetAvailableOfTypeWaitsForTheCatalogAndAbortReachesIt(t *testing.T) {
	catalog := &blockingCatalog{started: make(chan struct{})}
	ctx, cancel := context.WithCancelCause(testContext)
	defer cancel(nil)
	type outcome struct {
		result []byte
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := catalogCall(ctx, "models.getAvailableOfType", []byte(`["image"]`), catalog)
		done <- outcome{result, err}
	}()
	<-catalog.started
	select {
	case got := <-done:
		t.Fatalf("catalogCall returned %q, %v before the catalog answered", got.result, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	aborted := errors.New("script aborted")
	cancel(aborted)
	select {
	case got := <-done:
		if !errors.Is(got.err, aborted) {
			t.Fatalf("catalogCall after the abort = %q, %v; want the abort's cause", got.result, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("catalogCall did not return after the call's context was cancelled: the abort does not reach the catalog")
	}
}
