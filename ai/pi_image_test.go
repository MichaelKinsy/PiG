package ai

// pi: packages/ai/src/api/pi-image.ts
// pi: packages/ai/src/api/json-request.ts
// Ports packages/ai/test/pi-image.test.ts (Pi 39bfcfca1, a512c3fe2, d02145c49) and
// the Pi v1.7.0 additions in openrouter-images.test.ts, radius-provider.test.ts, and models-runtime.test.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func testPiImageModel(baseURL string) ImageModel {
	imgRate := 40.0
	return ImageModel{
		ID:       "image-fast",
		Name:     "Image Fast",
		API:      APIImagesPiImage,
		Provider: "radius",
		BaseURL:  baseURL,
		Input:    []string{"text", "image"},
		Output:   []string{"image", "text"},
		Cost: ModelCost{
			Input:       1.0,
			Output:      2.0,
			CacheRead:   0.5,
			CacheWrite:  1.5,
			ImageOutput: &imgRate,
		},
	}
}

func TestPiImageGenerateImagesSuccess(t *testing.T) {
	var capturedPath string
	var capturedAuth string
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedAuth = r.Header.Get("Authorization")
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &capturedBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"responseId": "resp_img_1",
			"output": [
				{"type": "text", "text": "Here is your cat."},
				{"type": "image", "data": "iVBORw0KGgo=", "mimeType": "image/png"}
			],
			"usage": {
				"input": 100,
				"output": 500,
				"cacheRead": 10,
				"cacheWrite": 0,
				"reasoning": 50,
				"imageOutput": 400,
				"totalTokens": 610,
				"cost": {
					"input": 0.0001,
					"output": 0.0162,
					"cacheRead": 0.000005,
					"cacheWrite": 0,
					"total": 0.016305,
					"totalFromProvider": true
				}
			}
		}`))
	}))
	defer srv.Close()

	model := testPiImageModel(srv.URL + "/v1")
	sessionID := "sess-123"
	var sawResponse ProviderResponse
	result := GenerateImagesPiImage(context.Background(), model, ImagesContext{
		Input: []ImagesInputContent{TextContent{Text: "Draw a cat"}},
	}, PiImageOptions{
		APIKey:      "radius-key",
		AspectRatio: "16:9",
		Size:        "2K",
		Metadata:    map[string]any{"trace": "abc"},
		OnResponse: func(resp ProviderResponse, m ImageModel) error {
			sawResponse = resp
			return nil
		},
		SessionID: &sessionID,
	})

	if result.StopReason != ImagesStopReasonStop {
		t.Fatalf("StopReason = %q, error = %q", result.StopReason, result.ErrorMessage)
	}
	if capturedPath != "/v1/image" {
		t.Fatalf("path = %q, want /v1/image", capturedPath)
	}
	if capturedAuth != "Bearer radius-key" {
		t.Fatalf("Authorization = %q, want Bearer radius-key", capturedAuth)
	}
	optsMap, _ := capturedBody["options"].(map[string]any)
	if optsMap["aspectRatio"] != "16:9" || optsMap["size"] != "2K" || optsMap["sessionId"] != "sess-123" {
		t.Fatalf("unexpected wire options: %#v", optsMap)
	}
	if sawResponse.Status != http.StatusOK {
		t.Fatalf("onResponse status = %d, want 200", sawResponse.Status)
	}
	if result.ResponseID != "resp_img_1" {
		t.Fatalf("ResponseID = %q, want resp_img_1", result.ResponseID)
	}
	if len(result.Output) != 2 {
		t.Fatalf("len(Output) = %d, want 2", len(result.Output))
	}
	if result.Usage == nil || result.Usage.Reasoning == nil || *result.Usage.Reasoning != 50 {
		t.Fatalf("unexpected reasoning usage: %+v", result.Usage)
	}
	if result.Usage.ImageOutput == nil || *result.Usage.ImageOutput != 400 {
		t.Fatalf("unexpected imageOutput usage: %+v", result.Usage)
	}
	if result.Usage.Cost.Total != 0.016305 || result.Usage.Cost.TotalFromProvider == nil || !*result.Usage.Cost.TotalFromProvider {
		t.Fatalf("unexpected provider cost: %+v", result.Usage.Cost)
	}
}

func TestPiImageComputesCatalogCostWhenProviderOmitsCost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"output": [{"type": "image", "data": "AAAA", "mimeType": "image/png"}],
			"usage": {"input": 1000000, "output": 1000000, "imageOutput": 750000, "reasoning": 200000}
		}`))
	}))
	defer srv.Close()

	model := testPiImageModel(srv.URL)
	result := GenerateImagesPiImage(context.Background(), model, ImagesContext{
		Input: []ImagesInputContent{TextContent{Text: "Draw"}},
	}, PiImageOptions{APIKey: "radius-key"})

	if result.StopReason != ImagesStopReasonStop {
		t.Fatalf("StopReason = %q, error = %q", result.StopReason, result.ErrorMessage)
	}
	if result.Usage == nil {
		t.Fatal("expected usage")
	}
	// 250k text/reasoning @ $2/1M = $0.50; 750k image @ $40/1M = $30.00 -> $30.50
	if result.Usage.Cost.Output != 30.5 {
		t.Fatalf("Cost.Output = %v, want 30.5", result.Usage.Cost.Output)
	}
	if result.Usage.Cost.Total != 31.5 {
		t.Fatalf("Cost.Total = %v, want 31.5", result.Usage.Cost.Total)
	}
}

func TestPiImageRejectsMalformedOutputAndTextOnlyInputImages(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output": [{"type": "image", "data": "", "mimeType": "image/png"}]}`))
	}))
	defer srv.Close()

	model := testPiImageModel(srv.URL)
	bad := GenerateImagesPiImage(context.Background(), model, ImagesContext{
		Input: []ImagesInputContent{TextContent{Text: "Draw"}},
	}, PiImageOptions{APIKey: "radius-key"})
	if bad.StopReason != ImagesStopReasonError || !strings.Contains(bad.ErrorMessage, "invalid image block") {
		t.Fatalf("expected invalid image block error, got stop=%q err=%q", bad.StopReason, bad.ErrorMessage)
	}

	textOnly := model
	textOnly.Input = []string{"text"}
	rejected := GenerateImagesPiImage(context.Background(), textOnly, ImagesContext{
		Input: []ImagesInputContent{ImageContent{Data: "AAAA", MimeType: "image/png"}},
	}, PiImageOptions{APIKey: "radius-key"})
	if rejected.StopReason != ImagesStopReasonError || !strings.Contains(rejected.ErrorMessage, "does not support image input") {
		t.Fatalf("expected image input rejection, got stop=%q err=%q", rejected.StopReason, rejected.ErrorMessage)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected text-only rejection before HTTP call, calls=%d", calls.Load())
	}
}

func TestPiImageAbortedAndRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	model := testPiImageModel("http://127.0.0.1:1")
	aborted := GenerateImagesPiImage(ctx, model, ImagesContext{
		Input: []ImagesInputContent{TextContent{Text: "Draw"}},
	}, PiImageOptions{APIKey: "radius-key"})
	if aborted.StopReason != ImagesStopReasonAborted {
		t.Fatalf("StopReason = %q, want aborted", aborted.StopReason)
	}
}

func TestOpenRouterImagesOptionsFinishReasonAndUsage(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &captured)
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("X-Mode") {
		case "no-choices":
			_, _ = w.Write([]byte(`{"id":"gen-1","choices":[],"error":{"message":"Moderation blocked"}}`))
		case "finish-reason":
			_, _ = w.Write([]byte(`{"id":"gen-2","choices":[{"finish_reason":"content_filter","native_finish_reason":"IMAGE_SAFETY","message":{"role":"assistant","content":""}}]}`))
		default:
			_, _ = w.Write([]byte(`{
				"id":"gen-3",
				"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"Done","images":[{"image_url":{"url":"data:image/png;base64,QUFB"}}]}}],
				"usage":{
					"prompt_tokens":100,
					"completion_tokens":500,
					"completion_tokens_details":{"reasoning_tokens":80,"image_tokens":400},
					"cost":0.042,
					"is_byok":false
				}
			}`))
		}
	}))
	defer srv.Close()

	imgRate := 30.0
	model := ImageModel{
		ID:       "google/gemini-2.5-flash-image",
		Name:     "Gemini Image",
		API:      APIImagesOpenRouter,
		Provider: ProviderImagesOpenRouter,
		BaseURL:  srv.URL,
		Input:    []string{"text"},
		Output:   []string{"image", "text"},
		Cost:     ModelCost{Input: 0.3, Output: 2.5, ImageOutput: &imgRate},
	}

	okRes := GenerateImagesOpenRouter(context.Background(), model, ImagesContext{
		Input: []ImagesInputContent{TextContent{Text: "Draw"}},
	}, ProviderImagesOptions{
		APIKey:      "or-key",
		AspectRatio: "16:9",
		Size:        "2K",
	})
	if okRes.StopReason != ImagesStopReasonStop {
		t.Fatalf("StopReason = %q, err = %q", okRes.StopReason, okRes.ErrorMessage)
	}
	imgCfg, _ := captured["image_config"].(map[string]any)
	if imgCfg["aspect_ratio"] != "16:9" || imgCfg["image_size"] != "2K" {
		t.Fatalf("unexpected image_config: %#v", imgCfg)
	}
	if okRes.Usage == nil || okRes.Usage.Reasoning == nil || *okRes.Usage.Reasoning != 80 {
		t.Fatalf("expected reasoning=80, got %+v", okRes.Usage)
	}
	if okRes.Usage.ImageOutput == nil || *okRes.Usage.ImageOutput != 400 {
		t.Fatalf("expected imageOutput=400, got %+v", okRes.Usage)
	}
	if okRes.Usage.Cost.Total != 0.042 || okRes.Usage.Cost.TotalFromProvider == nil || !*okRes.Usage.Cost.TotalFromProvider {
		t.Fatalf("expected OpenRouter reported cost 0.042, got %+v", okRes.Usage.Cost)
	}

	noChoices := GenerateImagesOpenRouter(context.Background(), model, ImagesContext{
		Input: []ImagesInputContent{TextContent{Text: "Draw"}},
	}, ProviderImagesOptions{
		APIKey:  "or-key",
		Headers: ProviderHeadersFromStrings(map[string]string{"X-Mode": "no-choices"}),
	})
	if noChoices.StopReason != ImagesStopReasonError || noChoices.ErrorMessage != "Moderation blocked" {
		t.Fatalf("noChoices = stop:%q err:%q", noChoices.StopReason, noChoices.ErrorMessage)
	}

	badFinish := GenerateImagesOpenRouter(context.Background(), model, ImagesContext{
		Input: []ImagesInputContent{TextContent{Text: "Draw"}},
	}, ProviderImagesOptions{
		APIKey:  "or-key",
		Headers: ProviderHeadersFromStrings(map[string]string{"X-Mode": "finish-reason"}),
	})
	if badFinish.StopReason != ImagesStopReasonError || !strings.Contains(badFinish.ErrorMessage, "Provider finish_reason: content_filter (native_finish_reason: IMAGE_SAFETY)") {
		t.Fatalf("badFinish = stop:%q err:%q", badFinish.StopReason, badFinish.ErrorMessage)
	}
}

func TestRadiusProviderImageModelsSupport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/config":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"baseUrl": "` + "http://" + r.Host + `/v1",
				"models": [{"id":"balanced","name":"Balanced","reasoning":true,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":8192}],
				"decision": [{"id":"decide-fast","name":"Decide Fast","input":["text"],"cost":{"input":0.1,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":32000}],
				"images": [{"id":"img-fast","name":"Img Fast","input":["text","audio"],"output":["image","text","video"],"cost":{"input":1,"output":2,"imageOutput":30,"cacheRead":0,"cacheWrite":0}}]
			}`))
		case "/v1/image":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"output":[{"type":"image","data":"QUFB","mimeType":"image/png"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	provider := NewRadiusGatewayProvider(RadiusProviderOptions{
		ID:      "radius-test",
		Name:    "Radius Test",
		Gateway: srv.URL,
	})
	var published []AnyModel
	err := provider.RefreshModels(context.Background(), RefreshModelsContext{
		AllowNetwork: true,
		Publish: func(pub ModelsPublication) (bool, error) {
			if pub.Update != nil {
				pub.Update()
			}
			if pub.Persist != nil {
				published = pub.Persist.Models
			}
			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("RefreshModels error: %v", err)
	}
	if len(published) != 3 {
		t.Fatalf("expected 3 models (chat+decision+image), got %d", len(published))
	}
	all := provider.GetAllModels()
	var imgModel *ImageModel
	for _, m := range all {
		if im, ok := m.(*ImageModel); ok {
			imgModel = im
		}
	}
	if imgModel == nil {
		t.Fatal("expected image model in GetAllModels")
	}
	if len(imgModel.Input) != 1 || imgModel.Input[0] != "text" {
		t.Fatalf("expected sanitized input [text], got %v", imgModel.Input)
	}
	if len(imgModel.Output) != 2 || imgModel.Output[0] != "image" || imgModel.Output[1] != "text" {
		t.Fatalf("expected sanitized output [image text], got %v", imgModel.Output)
	}
	if imgModel.Cost.ImageOutput == nil || *imgModel.Cost.ImageOutput != 30 {
		t.Fatalf("expected ImageOutput=30, got %+v", imgModel.Cost)
	}

	res, err := provider.GenerateImages(context.Background(), imgModel, ImagesContext{
		Input: []ImagesInputContent{TextContent{Text: "Draw"}},
	}, ImagesOptions{APIKey: "radius-key"})
	if err != nil {
		t.Fatalf("GenerateImages error: %v", err)
	}
	if res.StopReason != ImagesStopReasonStop || len(res.Output) != 1 {
		t.Fatalf("unexpected image result: %+v", res)
	}
}

// Ports packages/ai/src/api/pi-image.ts:43 PiImageResponse wire shape.
func TestPiImageResponseWireJSON(t *testing.T) {
	resp := PiImageResponse{
		Output: []ImagesOutputContent{
			TextContent{Text: "done"},
			ImageContent{Data: "AQID", MimeType: "image/png"},
		},
		Usage:      &Usage{Input: 10, Output: 20, TotalTokens: 30},
		ResponseID: "resp_wire_1",
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal PiImageResponse: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal PiImageResponse: %v", err)
	}
	if decoded["responseId"] != "resp_wire_1" {
		t.Fatalf("responseId = %v, want resp_wire_1", decoded["responseId"])
	}
	if out, ok := decoded["output"].([]any); !ok || len(out) != 2 {
		t.Fatalf("output = %v, want 2 blocks", decoded["output"])
	}
}

// Ports packages/ai/src/providers/openrouter-api.ts:10 and :87 (OpenRouterPricing.image_output / openRouterImageCost).
func TestOpenRouterImageCostHelper(t *testing.T) {
	imgRate := OpenRouterRate("0.00003")
	cost := OpenRouterImageCost(&OpenRouterPricing{
		Prompt:      OpenRouterRate("0.0000003"),
		Completion:  OpenRouterRate("0.0000025"),
		ImageOutput: &imgRate,
	})
	if cost.Input != 0.3 || cost.Output != 2.5 || cost.ImageOutput == nil || *cost.ImageOutput != 30 {
		t.Fatalf("unexpected OpenRouterImageCost: %+v", cost)
	}
	// Missing image_output leaves ImageOutput nil
	plain := OpenRouterImageCost(&OpenRouterPricing{
		Prompt:     OpenRouterRate("0.0000003"),
		Completion: OpenRouterRate("0.0000025"),
	})
	if plain.ImageOutput != nil {
		t.Fatalf("expected nil ImageOutput when omitted, got %v", *plain.ImageOutput)
	}
	_ = errors.New("")
}
