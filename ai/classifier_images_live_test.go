//go:build live

package ai

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// 512x512 solid PNGs from the TypeSafe SDK's live image tests (packages/ai/test/classifier-images-live.test.ts:12-15).
const (
	classifierLiveRed  = "iVBORw0KGgoAAAANSUhEUgAAAgAAAAIAAQMAAADOtka5AAAABlBMVEX/AAAAAABBoxIDAAAANklEQVR42u3BAQEAAACCIP+vbkhAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB8G4IAAAFjdVCkAAAAAElFTkSuQmCC"
	classifierLiveBlue = "iVBORw0KGgoAAAANSUhEUgAAAgAAAAIAAQMAAADOtka5AAAABlBMVEUAAP8AAAB7Yr8+AAAANklEQVR42u3BAQEAAACCIP+vbkhAAQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB8G4IAAAFjdVCkAAAAAElFTkSuQmCC"
)

// upstream: packages/ai/test/classifier-images-live.test.ts:colorContext
func classifierLiveColorContext(data string) ClassifierContext {
	return ClassifierContext{
		State:  JsonObject{"task": "Name the color of the attached image"},
		Images: []ImageContent{{Data: data, MimeType: "image/png"}},
		Questions: ClassifierQuestions{{ID: "color", Question: ClassifierChoiceQuestion{
			Instructions: "What color is the attached image?",
			Criteria:     clsChoices("red", "Red", "blue", "Blue", "green", "Green"),
		}}},
	}
}

// upstream: packages/ai/test/classifier-images-live.test.ts:expectColors. Each case sends a solid red and a solid blue
// PNG in separate requests, so a service that drops the image cannot pass by chance.
func expectClassifierLiveColors(t *testing.T, provider *ModelsProvider, id string, options ModelsClassifierOptions) {
	t.Helper()
	models := CreateModels()
	models.SetProvider(provider)
	model, _ := models.GetModelOfType(ModelTypeClassifier, provider.ID, id).(*ClassifierModel)
	if model == nil {
		t.Fatalf("missing %s/%s", provider.ID, id)
	}
	if !slices.Contains(model.Input, "image") {
		t.Fatalf("%s/%s input = %v, want image", provider.ID, id, model.Input)
	}
	for _, tc := range []struct{ data, color string }{{classifierLiveRed, "red"}, {classifierLiveBlue, "blue"}} {
		ctx, cancel := contextWithLiveTimeout(t)
		result := models.Classify(ctx, model, classifierLiveColorContext(tc.data), options)
		cancel()
		if result.ErrorMessage != "" {
			t.Fatalf("%s/%s %s: %s", provider.ID, id, tc.color, result.ErrorMessage)
		}
		if len(result.Answers) != 1 || result.Answers[0].ID != "color" {
			t.Fatalf("%s/%s %s answers = %+v", provider.ID, id, tc.color, result.Answers)
		}
		choice, ok := result.Answers[0].Answer.(ClassifierChoiceAnswer)
		if !ok || choice.Choice != tc.color {
			t.Fatalf("%s/%s answer = %+v, want choice %s", provider.ID, id, result.Answers[0].Answer, tc.color)
		}
	}
}

// upstream: packages/ai/test/classifier-images-live.test.ts:describe.skipIf(!TYPESAFE_API_KEY)
func TestTypeSafeImageClassificationLive(t *testing.T) {
	key := testenv.RequireLiveEnv(t, "TYPESAFE_API_KEY")
	t.Run("classifies images with jev-latest", func(t *testing.T) {
		expectClassifierLiveColors(t, TypesafeProvider(), "jev-latest", ModelsClassifierOptions{ClassifierOptions: ClassifierOptions{APIKey: key, APIKeySet: true}})
	})
}

// upstream: packages/ai/test/classifier-images-live.test.ts:describe.skipIf(!hasCloudflareWorkersAICredentials())
func TestCloudflareWorkersAIImageClassificationLive(t *testing.T) {
	key := testenv.RequireLiveEnv(t, "CLOUDFLARE_API_KEY", "CLOUDFLARE_ACCOUNT_ID")
	for _, id := range []string{"@cf/cloudflare/clef", "@cf/cloudflare/clef-flash", "@cf/cloudflare/clef-omni", "typesafe/jev", "typesafe/jev-1.14.0"} {
		t.Run("classifies images with "+id, func(t *testing.T) {
			expectClassifierLiveColors(t, CloudflareWorkersAIProvider(), id, ModelsClassifierOptions{ClassifierOptions: ClassifierOptions{
				APIKey: key, APIKeySet: true, Env: ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": os.Getenv("CLOUDFLARE_ACCOUNT_ID")},
			}})
		})
	}
}

// upstream: packages/ai/test/classifier-images-live.test.ts:describe.skipIf(!OPENROUTER_API_KEY)
func TestOpenRouterImageClassificationLive(t *testing.T) {
	key := testenv.RequireLiveEnv(t, "OPENROUTER_API_KEY")
	for _, id := range []string{"typesafe/jev-1.14", "cloudflare/clef-flash"} {
		t.Run("classifies images with "+id, func(t *testing.T) {
			expectClassifierLiveColors(t, OpenrouterProvider(), id, ModelsClassifierOptions{ClassifierOptions: ClassifierOptions{APIKey: key, APIKeySet: true}})
		})
	}
}

// Each upstream case has a 60 second timeout.
func contextWithLiveTimeout(t *testing.T) (context.Context, context.CancelFunc) {
	return context.WithTimeout(t.Context(), 60*time.Second)
}
