package llama

// pi: packages/coding-agent/src/extensions/llama/client.ts, provider.ts, index.ts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// fakeLlamaSwap serves the HTTP surface of llama-swap (github.com/mostlygeek/llama-swap, internal/server/server.go and api.go) and nothing else:
// GET /models and /v1/models list the configured models with a llama.cpp router style status.value, GET /running, GET /unload, POST /api/models/unload[/<model>],
// GET /props?model= and POST /v1/chat/completions load the model they name, and every other path is Go's ServeMux 404. It has no /models/load, /models/unload or
// /models/sse, which Pi's /llama calls.
type fakeLlamaSwap struct {
	mu       sync.Mutex
	models   []string
	running  string
	requests []string
}

func newFakeLlamaSwap(t *testing.T, models ...string) (*fakeLlamaSwap, string) {
	swap := &fakeLlamaSwap{models: models}
	server := httptest.NewServer(swap)
	t.Cleanup(server.Close)
	return swap, server.URL
}

func (s *fakeLlamaSwap) configured(id string) bool { return slices.Contains(s.models, id) }

func (s *fakeLlamaSwap) envelope(w http.ResponseWriter, status int, message, typ, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"src": "llama-swap", "error": map[string]any{"message": message, "type": typ, "code": code}})
}

func (s *fakeLlamaSwap) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	s.requests = append(s.requests, fmt.Sprintf("%s %s %s", r.Method, r.URL.RequestURI(), strings.TrimSpace(string(body))))
	switch {
	case r.Method == http.MethodGet && (r.URL.Path == "/models" || r.URL.Path == "/v1/models"):
		data := []any{}
		for _, id := range s.models {
			status := "unloaded"
			if id == s.running {
				status = "loaded"
			}
			data = append(data, map[string]any{"id": id, "object": "model", "created": 1700000000, "owned_by": "llama-swap",
				"status": map[string]any{"value": status}, "meta": map[string]any{"n_ctx": 131072}})
		}
		writeJSON(w, map[string]any{"object": "list", "data": data})
	case r.Method == http.MethodGet && r.URL.Path == "/running":
		running := []any{}
		if s.running != "" {
			running = append(running, map[string]any{"model": s.running, "state": "ready"})
		}
		writeJSON(w, map[string]any{"running": running})
	case r.Method == http.MethodGet && r.URL.Path == "/unload":
		s.running = ""
		_, _ = w.Write([]byte("OK"))
	case r.Method == http.MethodPost && r.URL.Path == "/api/models/unload":
		s.running = ""
		_, _ = w.Write([]byte("OK"))
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/models/unload/"):
		if id := strings.TrimPrefix(r.URL.Path, "/api/models/unload/"); id == s.running {
			s.running = ""
		}
		_, _ = w.Write([]byte("OK"))
	case r.Method == http.MethodGet && r.URL.Path == "/props":
		id := r.URL.Query().Get("model")
		if id == "" {
			s.envelope(w, http.StatusNotFound, "no model id could be identified", "not_found_error", "not_found")
		} else if !s.configured(id) {
			s.envelope(w, http.StatusNotFound, "no local server found for requested model", "not_found_error", "not_found")
		} else {
			s.running = id
			writeJSON(w, map[string]any{"chat_template": "{{ messages }}"})
		}
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		var request struct{ Model string }
		_ = json.Unmarshal(body, &request)
		if !s.configured(request.Model) {
			s.envelope(w, http.StatusNotFound, "no local server found for requested model", "not_found_error", "not_found")
			return
		}
		s.running = request.Model
		writeJSON(w, map[string]any{"object": "chat.completion", "model": request.Model, "choices": []any{map[string]any{"index": 0, "finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": "served by " + request.Model}}}})
	default:
		http.NotFound(w, r)
	}
}

func (s *fakeLlamaSwap) log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// fakeLlamaServer is a plain single-model llama-server: /models and /v1/models list the model in the OpenAI shape with no status, and the router endpoints do not exist.
func newFakeLlamaServer(t *testing.T) (*[]string, string) {
	var mu sync.Mutex
	var requests []string
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		switch r.URL.Path {
		case "/models", "/v1/models":
			writeJSON(w, map[string]any{"object": "list", "data": []any{map[string]any{"id": "model.gguf", "object": "model", "created": 1700000000, "owned_by": "llamacpp", "meta": map[string]any{"n_ctx_train": 32768}}}})
		case "/props":
			writeJSON(w, map[string]any{"chat_template": "{{ messages }}"})
		default:
			http.NotFound(w, r)
		}
	})
	return &requests, url
}

const (
	swapAlpha = "qwen3.8-q4-uncensored-split-layer"
	swapBeta  = "qwen3.8-q6-uncensored-split-tensor"
	swapGamma = "qwen3.8-flash-next-q2"
)

type clientOutcome struct {
	Step string   `json:"step"`
	Err  string   `json:"err"`
	IDs  []string `json:"ids"`
}

// Every llama.cpp client call /llama makes (list, load, unload, unload-and-wait, props) against Pi's client over a llama-swap and a plain llama-server, and the catalog the
// llama.cpp provider publishes from each. llama-swap lists its models with status.value, so the catalog renders as /llama's list, but the router endpoints are missing: load
// and unload are HTTP 404 in Pi and in Pi's port alike, and only the model llama-swap runs is selectable (Pi lists an unloaded model only when it has source "preset", which
// llama-swap never sends). A plain llama-server lists models without status, which Pi rejects as not running in router mode. Pi's behavior is the contract: this test fails if
// the port ever answers differently.
func TestLlamaClientAndCatalogAgainstLlamaSwapAndLlamaServerMatchPi(t *testing.T) {
	steps := []string{"list", "load", "unload", "unloadAndWait", "props", "propsModel", "list"}
	type variant struct {
		name  string
		start func(t *testing.T) (url string, requests func() []string)
	}
	variants := []variant{
		{"llama-swap", func(t *testing.T) (string, func() []string) {
			swap, url := newFakeLlamaSwap(t, swapAlpha, swapBeta, swapGamma)
			swap.running = swapAlpha
			return url, swap.log
		}},
		{"llama-server", func(t *testing.T) (string, func() []string) {
			requests, url := newFakeLlamaServer(t)
			return url, func() []string { return slices.Clone(*requests) }
		}},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			// Pi first, against its own server, then the port against another with the same initial state.
			piURL, piRequests := v.start(t)
			var want []clientOutcome
			pioracle.Run(t, `
const { LlamaClient } = await load("pi-coding-agent/extensions/llama/client.js");
const { createLlamaProvider } = await load("pi-coding-agent/extensions/llama/provider.js");
const client = new LlamaClient(input.url);
const out = [];
for (const step of input.steps) {
	const outcome = { step, err: "", ids: [] };
	try {
		if (step === "list") outcome.ids = (await client.list()).map((m) => m.id + "|" + m.status.value);
		else if (step === "load") await client.load(input.beta);
		else if (step === "unload") await client.unload(input.alpha);
		else if (step === "unloadAndWait") await client.unloadAndWait(input.alpha);
		else if (step === "props") await client.props();
		else if (step === "propsModel") await client.props({ model: input.alpha });
	} catch (e) { outcome.err = e instanceof Error ? e.message : String(e); }
	out.push(outcome);
}
const { provider } = createLlamaProvider();
let refreshErr = "";
try {
	await provider.refreshModels({ allowNetwork: true, signal: new AbortController().signal,
		credential: { type: "api_key", key: undefined, env: { LLAMA_BASE_URL: input.url } },
		publish: async (publication) => { publication.update?.(); return true; } });
} catch (e) { refreshErr = e instanceof Error ? e.message : String(e); }
out.push({ step: "refresh", err: refreshErr, ids: provider.getAllModels().map((m) => m.provider + "/" + m.id) });
emit(out);
`, struct {
				URL   string   `json:"url"`
				Steps []string `json:"steps"`
				Alpha string   `json:"alpha"`
				Beta  string   `json:"beta"`
			}{piURL, steps, swapAlpha, swapBeta}, &want)

			goURL, goRequests := v.start(t)
			client, err := NewLlamaClient(goURL, "")
			if err != nil {
				t.Fatal(err)
			}
			var got []clientOutcome
			for _, step := range steps {
				outcome := clientOutcome{Step: step, IDs: []string{}}
				var callErr error
				switch step {
				case "list":
					var models []LlamaModelInfo
					if models, callErr = client.List(t.Context(), false); callErr == nil {
						for _, m := range models {
							outcome.IDs = append(outcome.IDs, m.ID+"|"+string(m.Status.Value))
						}
					}
				case "load":
					callErr = client.Load(t.Context(), swapBeta)
				case "unload":
					callErr = client.Unload(t.Context(), swapAlpha)
				case "unloadAndWait":
					callErr = client.UnloadAndWait(t.Context(), swapAlpha)
				case "props":
					_, callErr = client.Props(t.Context(), "")
				case "propsModel":
					_, callErr = client.Props(t.Context(), swapAlpha)
				}
				if callErr != nil {
					outcome.Err = callErr.Error()
				}
				got = append(got, outcome)
			}
			controller := CreateLlamaProvider()
			refresh := outcomeOfRefresh(t, controller, goURL)
			got = append(got, refresh)

			for i := range want {
				if want[i].IDs == nil {
					want[i].IDs = []string{}
				}
			}
			if !reflect.DeepEqual(got, want) {
				g, _ := json.MarshalIndent(got, "", " ")
				w, _ := json.MarshalIndent(want, "", " ")
				t.Fatalf("port differs from Pi:\n got %s\nwant %s", g, w)
			}
			if gotLog, wantLog := goRequests(), piRequests(); !reflect.DeepEqual(gotLog, wantLog) {
				t.Fatalf("requests differ from Pi:\n got %q\nwant %q", gotLog, wantLog)
			}

			byStep := map[string]clientOutcome{}
			for _, o := range got {
				byStep[o.Step] = o
			}
			if v.name == "llama-swap" {
				// The values Pi produces, so a port that "fixed" this would be distinguishable.
				if byStep["load"].Err != "llama.cpp returned HTTP 404" || byStep["unload"].Err != "llama.cpp returned HTTP 404" || byStep["unloadAndWait"].Err != "llama.cpp returned HTTP 404" {
					t.Fatalf("load/unload against llama-swap = %q / %q / %q, want Pi's HTTP 404", byStep["load"].Err, byStep["unload"].Err, byStep["unloadAndWait"].Err)
				}
				if want := []string{swapAlpha + "|loaded", swapBeta + "|unloaded", swapGamma + "|unloaded"}; !reflect.DeepEqual(byStep["list"].IDs, want) {
					t.Fatalf("llama-swap catalog = %v, want %v", byStep["list"].IDs, want)
				}
				if want := []string{"llama.cpp/" + swapAlpha, "llama.cpp/" + swapAlpha}; !reflect.DeepEqual(byStep["refresh"].IDs, want) {
					// Both the chat model and its classifier entry exist for the one loaded model.
					t.Fatalf("selectable llama.cpp models over llama-swap = %v, want only the loaded %s", byStep["refresh"].IDs, swapAlpha)
				}
			} else if byStep["list"].Err != "Server is not running in llama.cpp router mode" {
				t.Fatalf("list against a plain llama-server = %q, want Pi's router mode error", byStep["list"].Err)
			}
		})
	}
}

func outcomeOfRefresh(t *testing.T, controller *LlamaProviderController, url string) clientOutcome {
	t.Helper()
	outcome := clientOutcome{Step: "refresh", IDs: []string{}}
	err := controller.Provider.RefreshModels(RefreshModelsContext{
		Ctx:          context.Background(),
		AllowNetwork: true,
		Credential:   &ai.Credential{Type: ai.CredentialAPIKey, Env: map[string]string{"LLAMA_BASE_URL": url}},
		Publish: func(publication ModelsPublication) (bool, error) {
			if publication.Update != nil {
				publication.Update()
			}
			return true, nil
		},
	})
	if err != nil {
		outcome.Err = err.Error()
	}
	for _, model := range controller.Provider.GetAllModels() {
		outcome.IDs = append(outcome.IDs, string(model.ModelProvider())+"/"+model.ModelID())
	}
	return outcome
}

// /llama pointed at llama-swap: the catalog lists every configured model, and loading or unloading one fails with Pi's text, "llama.cpp returned HTTP 404", because the
// /models/load and /models/unload that index.ts calls do not exist on llama-swap. The command refreshes the catalog afterwards and loses nothing, and llama-swap never receives a chat
// request: /llama does not load a model by sending one.
func TestLlamaCommandAgainstLlamaSwapFailsLikePi(t *testing.T) {
	swap, url := newFakeLlamaSwap(t, swapAlpha, swapBeta, swapGamma)
	swap.running = swapAlpha
	session, notices := commandFixture(t, url)
	ui := &fakeUI{t: t, confirm: true, selects: map[string]string{"1 model is loaded": "Keep loaded and load"}, actions: []LlamaManagerAction{
		{Type: LlamaManagerActionModel, Model: model(swapBeta, LlamaModelStatusUnloaded)},
		{Type: LlamaManagerActionModel, Model: model(swapAlpha, LlamaModelStatusLoaded)},
	}}
	if err := session.manage(ui); err != nil {
		t.Fatal(err)
	}
	want := []notification{{"llama.cpp returned HTTP 404", "error"}, {"llama.cpp returned HTTP 404", "error"}}
	if !reflect.DeepEqual(*notices, want) {
		t.Fatalf("notices = %+v, want %+v", *notices, want)
	}
	var posts []string
	for _, request := range swap.log() {
		if strings.HasPrefix(request, "POST") {
			posts = append(posts, request)
		}
	}
	wantPosts := []string{`POST /models/load {"model":"` + swapBeta + `"}`, `POST /models/unload {"model":"` + swapAlpha + `"}`}
	if !reflect.DeepEqual(posts, wantPosts) {
		t.Fatalf("POSTs = %q, want %q", posts, wantPosts)
	}
	if swap.running != swapAlpha {
		t.Fatalf("llama-swap runs %q after the failed load and unload, want %q", swap.running, swapAlpha)
	}
}

// The path that works with llama-swap, in Pi and in the port: llama-swap as an openai-completions provider. The request carries the model in its `model` field and llama-swap
// swaps to it. See the llama-swap-models-json journey (test/journeys) for the same path through the model selector in Pi and PiG side by side.
func TestFakeLlamaSwapSwapsOnTheModelField(t *testing.T) {
	swap, url := newFakeLlamaSwap(t, swapAlpha, swapBeta)
	for _, id := range []string{swapAlpha, swapBeta, swapAlpha} {
		response, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"`+id+`","messages":[]}`))
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("chat %s: %v %v", id, response, err)
		}
		_ = response.Body.Close()
		if swap.running != id {
			t.Fatalf("running = %q after a request for %q", swap.running, id)
		}
	}
	response, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"nope","messages":[]}`))
	if err != nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("unrouted model: %v %v", response, err)
	}
	_ = response.Body.Close()
}
