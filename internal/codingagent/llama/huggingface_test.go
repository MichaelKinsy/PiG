package llama

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Ports the llama-extension.test.ts Hugging Face case.

func TestHuggingFaceSearchAndDetailsReadQuantizationsAndAccess(t *testing.T) {
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer hf-secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.URL.Path == "/api/models" && r.URL.RawQuery != "":
			query := r.URL.Query()
			if query.Get("search") != "qwen coder" || query.Get("filter") != "gguf" || query.Get("sort") != "downloads" {
				t.Errorf("search query = %q", r.URL.RawQuery)
			}
			if r.URL.RawQuery != "search=qwen+coder&filter=gguf&sort=downloads&direction=-1&limit=20" {
				t.Errorf("search query order = %q", r.URL.RawQuery)
			}
			writeJSON(w, []any{map[string]any{"id": "owner/model-GGUF", "downloads": 1200}, map[string]any{"downloads": 1}})
		case r.URL.RequestURI() == "/api/models/owner/model-GGUF?blobs=true":
			writeJSON(w, map[string]any{
				"id":    "owner/model-GGUF",
				"gated": "manual",
				"siblings": []any{
					map[string]any{"rfilename": "model-Q5_K_M.gguf", "size": 6000},
					map[string]any{"rfilename": "model-Q4_K_M-00001-of-00002.gguf", "size": 2000},
					map[string]any{"rfilename": "model-Q4_K_M-00002-of-00002.gguf", "size": 3000},
					map[string]any{"rfilename": "mmproj-F16.gguf", "size": 1000},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	client := NewHuggingFaceClient("hf-secret", url)

	models, err := client.Search(context.Background(), "qwen coder")
	if err != nil {
		t.Fatal(err)
	}
	if want := []HuggingFaceModel{{ID: "owner/model-GGUF", Downloads: 1200}}; !reflect.DeepEqual(models, want) {
		t.Fatalf("search = %+v, want %+v", models, want)
	}
	details, err := client.Details(context.Background(), "owner/model-GGUF")
	if err != nil {
		t.Fatal(err)
	}
	q4, q5 := 5000.0, 6000.0
	want := HuggingFaceModelDetails{
		ID:            "owner/model-GGUF",
		Gated:         "manual",
		Quantizations: []HuggingFaceQuantization{{Name: "Q4_K_M", Size: &q4}, {Name: "Q5_K_M", Size: &q5}},
	}
	if !reflect.DeepEqual(details, want) {
		t.Fatalf("details = %+v, want %+v", details, want)
	}
	if token := FindHuggingFaceToken(func(name string) string {
		if name == "HF_TOKEN" {
			return " hf-secret "
		}
		return ""
	}); token != "hf-secret" {
		t.Fatalf("token = %q", token)
	}
}

func TestHuggingFaceQuantizationOrderAndUnknownSizes(t *testing.T) {
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"siblings": []any{
			map[string]any{"rfilename": "sub/Model-UD-q8_k_xl.gguf"},
			map[string]any{"rfilename": "Model-BF16.gguf", "size": 10},
			map[string]any{"rfilename": "Model-F16.gguf", "size": 10},
			map[string]any{"rfilename": "README.md", "size": 1},
			map[string]any{"rfilename": "Model.gguf", "size": 1},
		}})
	})
	details, err := NewHuggingFaceClient("", url+"/").Details(context.Background(), "owner/name with space")
	if err != nil {
		t.Fatal(err)
	}
	ten := 10.0
	want := HuggingFaceModelDetails{
		ID: "owner/name with space",
		Quantizations: []HuggingFaceQuantization{
			{Name: "BF16", Size: &ten}, {Name: "F16", Size: &ten}, {Name: "UD-Q8_K_XL"},
		},
	}
	if !reflect.DeepEqual(details, want) {
		t.Fatalf("details = %+v, want %+v", details, want)
	}
}

func TestHuggingFaceErrorsMirrorUpstreamMessages(t *testing.T) {
	status := http.StatusTooManyRequests
	headers := map[string]string{"Ratelimit": `"api";r=0;t=42`}
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		for name, value := range headers {
			w.Header().Set(name, value)
		}
		w.WriteHeader(status)
		writeJSON(w, map[string]any{"error": "Repository not found"})
	})
	client := NewHuggingFaceClient("", url)
	if _, err := client.Search(context.Background(), "x"); err == nil || err.Error() != "Hugging Face rate limit reached; retry in 42s" {
		t.Fatalf("rate limit error = %v", err)
	}
	headers = map[string]string{"Retry-After": "7"}
	if _, err := client.Search(context.Background(), "x"); err == nil || err.Error() != "Hugging Face rate limit reached; retry in 7s" {
		t.Fatalf("retry-after error = %v", err)
	}
	headers = nil
	if _, err := client.Search(context.Background(), "x"); err == nil || err.Error() != "Hugging Face rate limit reached" {
		t.Fatalf("bare rate limit error = %v", err)
	}
	status = http.StatusNotFound
	if _, err := client.Details(context.Background(), "a/b"); err == nil || err.Error() != "Repository not found" {
		t.Fatalf("payload error = %v", err)
	}
}

func TestFindHuggingFaceTokenReadsTokenFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	hfHome := filepath.Join(dir, "hf")
	if err := os.MkdirAll(hfHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hfHome, "token"), []byte("  file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HF_TOKEN": "  ", "HF_TOKEN_PATH": filepath.Join(dir, "missing"), "HF_HOME": hfHome}
	if token := FindHuggingFaceToken(func(name string) string { return env[name] }); token != "file-token" {
		t.Fatalf("token = %q, want file-token", token)
	}
	if token := FindHuggingFaceToken(func(string) string { return "" }); token != "" {
		t.Fatalf("token without sources = %q", token)
	}
}
