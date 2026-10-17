package codingagent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/ai/src/models.ts:732-735 (getAllAvailable): without filterAllModels, filterModels receives the provider's chat models and
// only decides which chat ids stay. The listed models keep their order and every non-chat model stays.
func TestNativeVisibleModelsKeepsTheListedOrderAndFiltersChatByID(t *testing.T) {
	first := &ai.Model{ID: "chat-a"}
	image := &ai.ImageModel{ID: "image"}
	second := &ai.Model{ID: "chat-b"}
	dropped := &ai.Model{ID: "chat-c"}
	var seen []string
	provider := &extension.NativeProvider{
		FilterModels: func(_ context.Context, models []*ai.Model, _ *ai.Credential) ([]*ai.Model, error) {
			for _, model := range models {
				seen = append(seen, model.ID)
			}
			// Reversed, plus a model the provider does not list: neither the order nor the extra model reaches the result.
			return []*ai.Model{second, first, {ID: "unlisted"}}, nil
		},
	}
	visible, err := nativeVisibleModels(t.Context(), provider, []ai.AnyModel{first, image, second, dropped}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, model := range visible {
		ids = append(ids, model.ModelID())
	}
	if want := []string{"chat-a", "image", "chat-b"}; !slices.Equal(ids, want) {
		t.Fatalf("visible = %v, want %v", ids, want)
	}
	if want := []string{"chat-a", "chat-b", "chat-c"}; !slices.Equal(seen, want) {
		t.Fatalf("filterModels saw %v, want the chat models %v", seen, want)
	}

	// With getModels, filterModels sees the provider's current chat list (models.ts:733 provider.getModels()).
	seen = nil
	provider.GetModels = func(context.Context) ([]*ai.Model, error) { return []*ai.Model{second}, nil }
	if _, err := nativeVisibleModels(t.Context(), provider, []ai.AnyModel{first, image, second}, nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"chat-b"}; !slices.Equal(seen, want) {
		t.Fatalf("filterModels saw %v, want getModels' list %v", seen, want)
	}
}
