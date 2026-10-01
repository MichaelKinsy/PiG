package ai

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
)

// The model's stochastic drawing quality is live-only. These cases retain the exact prompts, image bytes, response assertions, and real image-provider request conversion from the upstream helpers.
func TestImagesUpstream(t *testing.T) {
	image, err := os.ReadFile("testdata/upstream-red-circle.png")
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(image)
	for _, tc := range []struct {
		name, prompt string
		imageInput   bool
	}{
		// .upstream/v0.99.1/packages/ai/test/images.test.ts:57; helper assertions at :14-25.
		{"should generate a basic image", "Generate a simple red circle on a plain white background. No text.", false},
		// .upstream/v0.99.1/packages/ai/test/images.test.ts:61; helper assertions at :27-49.
		{"should handle image input", "Create a variation of this image with a blue background.", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model, ok := GetImageModel(ProviderImagesOpenRouter, "google/gemini-2.5-flash-image")
			if !ok {
				t.Fatal("missing upstream image model")
			}
			if !slices.Contains(model.Input, "image") {
				t.Fatal("upstream test model no longer supports the exercised capabilities")
			}
			input := []ContentBlock{TextContent{Text: tc.prompt}}
			if tc.imageInput {
				input = append(input, ImageContent{MimeType: "image/png", Data: encoded})
			}
			requests := 0
			client := &http.Client{Transport: fetchOptionTransport(func(request *http.Request) (*http.Response, error) {
				requests++
				var payload struct {
					Messages []struct {
						Content []struct {
							Type, Text string
							ImageURL   struct {
								URL string `json:"url"`
							} `json:"image_url"`
						} `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					return nil, err
				}
				if len(payload.Messages) != 1 || len(payload.Messages[0].Content) != len(input) || payload.Messages[0].Content[0].Text != tc.prompt {
					t.Errorf("input payload=%#v", payload)
				}
				if tc.imageInput && (len(payload.Messages[0].Content) < 2 || payload.Messages[0].Content[1].ImageURL.URL != "data:image/png;base64,"+encoded) {
					t.Error("input image bytes were changed or dropped")
				}
				body, _ := json.Marshal(map[string]any{"id": "image-result", "choices": []any{map[string]any{"message": map[string]any{"content": "A red circle.", "images": []any{map[string]any{"image_url": "data:image/png;base64," + encoded}}}}}})
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body))), Request: request}, nil
			})}
			response, err := GenerateImages(t.Context(), model, ImagesContext{Input: input}, ProviderImagesOptions{APIKey: "test-key", Fetch: client})
			if err != nil {
				t.Fatal(err)
			}
			if response.StopReason != ImagesStopReasonStop || response.ErrorMessage != "" {
				t.Fatalf("generation=%+v", response)
			}
			hasImage := false
			for _, block := range response.Output {
				if block, ok := block.(ImageContent); ok {
					hasImage = true
					if block.Data != encoded || block.MimeType != "image/png" {
						t.Error("output image bytes changed")
					}
				}
			}
			if !hasImage || response.Timestamp <= 0 || requests != 1 {
				t.Fatalf("image=%v timestamp=%d requests=%d", hasImage, response.Timestamp, requests)
			}
		})
	}
}
