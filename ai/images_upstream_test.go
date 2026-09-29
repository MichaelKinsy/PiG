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
		name, prompt           string
		imageInput, textOutput bool
	}{
		// .upstream/v0.87.1/packages/ai/test/images.test.ts:77; helper assertions at :15-26.
		{"should generate a basic image", "Generate a simple red circle on a plain white background. No text.", false, false},
		// .upstream/v0.87.1/packages/ai/test/images.test.ts:81; helper assertions at :28-46.
		{"should handle text plus image output", "Generate a red circle and include a brief description of the image.", false, true},
		// .upstream/v0.87.1/packages/ai/test/images.test.ts:85; helper assertions at :48-72.
		{"should handle image input", "Create a variation of this image with a blue background.", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model, ok := GetImageModel(ProviderImagesOpenRouter, "google/gemini-2.5-flash-image")
			if !ok {
				t.Fatal("missing upstream image model")
			}
			if !slices.Contains(model.Input, "image") || !slices.Contains(model.Output, "text") {
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
			hasImage, hasText := false, false
			for _, block := range response.Output {
				switch block := block.(type) {
				case ImageContent:
					hasImage = true
					if block.Data != encoded || block.MimeType != "image/png" {
						t.Error("output image bytes changed")
					}
				case TextContent:
					hasText = strings.TrimSpace(block.Text) != ""
				}
			}
			if !hasImage || (tc.textOutput && !hasText) || response.Timestamp <= 0 || requests != 1 {
				t.Fatalf("image=%v text=%v timestamp=%d requests=%d", hasImage, hasText, response.Timestamp, requests)
			}
		})
	}
}
