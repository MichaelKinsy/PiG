package coding

// pi: packages/coding-agent/src/core/model-runtime.ts

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// model-runtime.ts:828-860 generateImages awaits auth resolution and the provider, and a rejection becomes imageErrorResult(model, error, signal.aborted): a request aborted at any await is reported as `aborted`, any other failure as `error`; neither is thrown.
func TestModelRuntimeGenerateImagesReportsAbortAtEveryAwait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", nil)
		runtime := services.ModelRuntime()
		providerFailure := errors.New("provider exploded")
		authBlocked := make(chan struct{}, 1)
		var failProvider bool
		blockAuth := false
		started := make(chan struct{}, 1)
		auth := ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Pixels key", Resolve: func(ctx context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			if blockAuth {
				select {
				case authBlocked <- struct{}{}:
				default:
				}
				<-ctx.Done()
				return nil, context.Cause(ctx)
			}
			if input.Credential != nil && input.Credential.Key != "" {
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key}, Source: "stored"}, nil
			}
			return nil, nil
		}}}
		if err := runtime.RegisterNativeProvider(ai.CreateProvider(ai.CreateProviderOptions{
			ID:     "pixels",
			Auth:   auth,
			Models: []ai.AnyModel{imagesTestImageModel("pixels", "flux")},
			Images: ai.ProviderImageAPIMap{"test-images": {GenerateImages: func(ctx context.Context, model *ai.ImageModel, _ ai.ImagesContext, _ ai.ImagesOptions) (ai.AssistantImages, error) {
				if failProvider {
					return ai.AssistantImages{}, providerFailure
				}
				started <- struct{}{}
				<-ctx.Done()
				return ai.AssistantImages{}, context.Cause(ctx)
			}}},
		})); err != nil {
			t.Fatal(err)
		}
		services.Registry().SetRuntimeAPIKey("pixels", "sk-pixels")
		model, _ := runtime.GetModelOfType(ai.ModelTypeImage, "pixels", "flux").(*ai.ImageModel)
		if model == nil {
			t.Fatal("missing image model")
		}

		// Aborted while the provider generates: the awaited call ends with an aborted result.
		ctx, cancel := context.WithCancelCause(t.Context())
		result := make(chan ai.AssistantImages, 1)
		go func() { result <- runtime.GenerateImages(ctx, model, imagesTestContext) }()
		<-started
		synctest.Wait()
		if len(result) != 0 {
			t.Fatal("GenerateImages returned before the provider finished or the request was aborted")
		}
		cancel(errors.New("user pressed escape"))
		if got := <-result; got.StopReason != ai.ImagesStopReasonAborted {
			t.Fatalf("aborted in the provider: stop reason = %q (%q), want aborted", got.StopReason, got.ErrorMessage)
		}

		// A failure of a live request is an error result carrying the failure.
		failProvider = true
		if got := runtime.GenerateImages(t.Context(), model, imagesTestContext); got.StopReason != ai.ImagesStopReasonError || got.ErrorMessage == "" {
			t.Fatalf("provider failure: %+v, want an error result", got)
		}
		failProvider = false

		// Aborted while the request's auth resolves.
		blockAuth = true
		authCtx, authCancel := context.WithCancelCause(t.Context())
		go func() { result <- runtime.GenerateImages(authCtx, model, imagesTestContext) }()
		<-authBlocked
		authCancel(errors.New("user pressed escape"))
		if got := <-result; got.StopReason != ai.ImagesStopReasonAborted {
			t.Fatalf("aborted in auth resolution: stop reason = %q (%q), want aborted", got.StopReason, got.ErrorMessage)
		}
	})
}
