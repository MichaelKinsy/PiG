package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// types.ts ProviderRequestOptions.fetch: a FetchFunction is the transport of a client; each request reaches the function and its response or error comes back from the call.
func TestFetchFunctionIsAnHTTPTransportThatReturnsItsResponseAndError(t *testing.T) {
	var seen []string
	var header string
	var fetch FetchFunction = func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.URL.String())
		if request.Method == http.MethodPost {
			body, _ := io.ReadAll(request.Body)
			header = request.Header.Get("X-Test") + "|" + string(body)
		}
		if strings.HasSuffix(request.URL.Path, "/fail") {
			return nil, errors.New("fetch failed")
		}
		return &http.Response{StatusCode: http.StatusTeapot, Status: "418 I'm a teapot", Header: http.Header{}, Body: io.NopCloser(strings.NewReader("tea")), Request: request}, nil
	}
	client := &http.Client{Transport: fetch}
	response, err := client.Get("https://fetch.test/ok")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTeapot || string(body) != "tea" {
		t.Fatalf("response = %d %q, want the function's response", response.StatusCode, body)
	}
	failed, err := client.Get("https://fetch.test/fail")
	if err == nil {
		_ = failed.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "fetch failed") {
		t.Fatalf("error = %v, want the function's error", err)
	}
	post, _ := http.NewRequest(http.MethodPost, "https://fetch.test/post", strings.NewReader("payload"))
	post.Header.Set("X-Test", "yes")
	postResponse, err := client.Do(post)
	if err != nil {
		t.Fatal(err)
	}
	_ = postResponse.Body.Close()
	if header != "yes|payload" {
		t.Fatalf("the function saw %q, want the request's header and body", header)
	}
	seen = seen[:2]
	if len(seen) != 2 || seen[0] != "https://fetch.test/ok" {
		t.Fatalf("requests = %v", seen)
	}
}

// api/openrouter-images.lazy.ts openrouterImagesApi: the accessor forwards the model, request context and every option (API key, base URL) so a keyed request reaches the server exactly as the direct call does.
func TestOpenRouterImagesAPIForwardsTheKeyedRequestToTheServer(t *testing.T) {
	var authorization, requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		requestBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"accessor reached the server","code":403}}`))
	}))
	defer server.Close()
	model := ImageModel{ID: "m", API: APIImagesOpenRouter, Provider: "openrouter", BaseURL: server.URL, Output: []string{"image"}}
	request := ImagesContext{Input: []ImagesInputContent{TextContent{Text: "draw"}}}
	result, err := OpenRouterImagesAPI().GenerateImages(context.Background(), &model, request, ImagesOptions{APIKey: "sk-accessor"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != ImagesStopReasonError || !strings.Contains(result.ErrorMessage, "accessor reached the server") || result.Model != "m" || result.Provider != "openrouter" || result.API != APIImagesOpenRouter {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(requestBody, "draw") {
		t.Fatalf("request body = %q, want the request context's prompt", requestBody)
	}
	keyless, _ := OpenRouterImagesAPI().GenerateImages(context.Background(), &model, request, ImagesOptions{})
	if keyless.ErrorMessage != "No API key for provider: openrouter" {
		t.Fatalf("keyless message = %q", keyless.ErrorMessage)
	}
	if authorization != "Bearer sk-accessor" {
		t.Fatalf("authorization = %q, want the accessor's API key forwarded", authorization)
	}
}
