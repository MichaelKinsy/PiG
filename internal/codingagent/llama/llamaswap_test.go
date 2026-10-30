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
// GET /models and /v1/models list the configured models with a llama.cpp router style status.value, and GET /props?model= answers for (and loads) the model it names;
// every other path is Go's ServeMux 404. It has no /models/load, /models/unload or /models/sse, which Pi's /llama calls.
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
	case r.Method == http.MethodGet && r.URL.Path == "/props":
		id := r.URL.Query().Get("model")
		switch {
		case id == "":
			s.envelope(w, http.StatusNotFound, "no model id could be identified", "not_found_error", "not_found")
		case !s.configured(id):
			s.envelope(w, http.StatusNotFound, "no local server found for requested model", "not_found_error", "not_found")
		default:
			s.running = id
			writeJSON(w, map[string]any{"chat_template": "{{ messages }}"})
		}
	// POST /v1/chat/completions, GET /running, GET /unload and POST /api/models/unload[/<model>] also exist on llama-swap; /llama calls none of them.
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
			// The one place the port answers differently from Pi (D212): a 404 from the load and unload endpoints that llama-swap lacks names llama-swap and how to use it. A 404 from a
			// server that is not llama-swap keeps Pi's text. The port asks GET /running after such a 404, which Pi never sends.
			const piText = "llama.cpp returned HTTP 404"
			portText := piText
			if v.name == "llama-swap" {
				portText = llamaSwapMessage(goURL)
			}
			for i := range got {
				switch got[i].Step {
				case "load", "unload", "unloadAndWait":
					if want[i].Err != piText {
						t.Fatalf("Pi's %s = %q, want %q", got[i].Step, want[i].Err, piText)
					}
					if got[i].Err != portText {
						t.Fatalf("%s = %q, want %q", got[i].Step, got[i].Err, portText)
					}
					got[i].Err = want[i].Err
				}
			}
			if !reflect.DeepEqual(got, want) {
				g, _ := json.MarshalIndent(got, "", " ")
				w, _ := json.MarshalIndent(want, "", " ")
				t.Fatalf("port differs from Pi:\n got %s\nwant %s", g, w)
			}
			var gotLog []string
			probes := 0
			for _, request := range goRequests() {
				if strings.HasPrefix(request, "GET /running") {
					probes++
					continue
				}
				gotLog = append(gotLog, request)
			}
			if wantLog := piRequests(); !reflect.DeepEqual(gotLog, wantLog) {
				t.Fatalf("requests differ from Pi:\n got %q\nwant %q", gotLog, wantLog)
			}
			if probes != 3 {
				t.Fatalf("GET /running probes = %d, want one after each of the 3 failed 404 calls", probes)
			}

			byStep := map[string]clientOutcome{}
			for _, o := range got {
				byStep[o.Step] = o
			}
			if v.name == "llama-swap" {
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
		var identity struct{ Provider, ID string }
		encoded, _ := json.Marshal(model)
		_ = json.Unmarshal(encoded, &identity)
		outcome.IDs = append(outcome.IDs, identity.Provider+"/"+identity.ID)
	}
	return outcome
}

// llamaSwapMessage is what /llama says when load or unload hits llama-swap (D212). Written out here so the production text cannot drift without this test noticing.
func llamaSwapMessage(serverURL string) string {
	return "/llama cannot load or unload models on llama-swap: it loads a model when a request names it. Add llama-swap to models.json as an openai-completions provider with baseUrl " + serverURL +
		"/v1, then pick models with /model. See https://github.com/MichaelKinsy/PiG/blob/main/docs/site/docs/llama-cpp.md#llama-swap"
}

// /llama pointed at llama-swap: the catalog lists every configured model, and loading or unloading one fails with the llama-swap message (D212) instead of Pi's bare
// "llama.cpp returned HTTP 404", because the /models/load and /models/unload that index.ts calls do not exist on llama-swap. The command refreshes the catalog afterwards and loses
// nothing, and llama-swap never receives a chat request: /llama does not load a model by sending one.
func TestLlamaCommandAgainstLlamaSwapExplainsTheMissingEndpoints(t *testing.T) {
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
	want := []notification{{llamaSwapMessage(url), "error"}, {llamaSwapMessage(url), "error"}}
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

// llama-swap is recognized by the 404 itself when it names llama-swap in its error envelope's "src", with no GET /running needed.
func TestLoadAndUnloadRecognizeLlamaSwapByItsErrorEnvelope(t *testing.T) {
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"src":"llama-swap","error":{"message":"no router for requested model","type":"not_found_error","code":"not_found"}}`))
	})
	client, err := NewLlamaClient(url, "")
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"load":   func() error { return client.Load(t.Context(), swapBeta) },
		"unload": func() error { return client.Unload(t.Context(), swapAlpha) },
	} {
		if err := call(); err == nil || err.Error() != llamaSwapMessage(url) {
			t.Errorf("%s = %v, want the llama-swap message", name, err)
		}
	}
}

// A 404 from a server that is not llama-swap keeps Pi's text: the bare "llama.cpp returned HTTP 404" when the body says nothing (a router build without the endpoint, a proxy in
// front of one), and the server's own message when the body carries one. GET /running is not llama-swap's there.
func TestLoadAndUnloadKeepPisTextForA404ThatIsNotLlamaSwap(t *testing.T) {
	for _, test := range []struct{ name, body, want string }{
		{"bare 404", "404 page not found\n", "llama.cpp returned HTTP 404"},
		{"server message", `{"error":{"message":"model not found"}}`, "model not found"},
		{"running is not llama-swap's", "404 page not found\n", "llama.cpp returned HTTP 404"},
	} {
		t.Run(test.name, func(t *testing.T) {
			url := listen(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/running" && test.name == "running is not llama-swap's" {
					writeJSON(w, map[string]any{"status": "ok"}) // 200, but not llama-swap's {"running": [...]}
					return
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(test.body))
			})
			client, err := NewLlamaClient(url, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Load(t.Context(), swapBeta); err == nil || err.Error() != test.want {
				t.Errorf("load = %v, want %q", err, test.want)
			}
			if err := client.Unload(t.Context(), swapAlpha); err == nil || err.Error() != test.want {
				t.Errorf("unload = %v, want %q", err, test.want)
			}
		})
	}
}

// /llama against a llama.cpp router that answers 404 for load: the notice is Pi's text.
func TestLlamaCommandKeepsPisTextForARouter404(t *testing.T) {
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/models" {
			writeJSON(w, map[string]any{"data": []any{map[string]any{"id": "alpha", "status": map[string]any{"value": "unloaded"}}}})
			return
		}
		http.NotFound(w, r)
	})
	session, notices := commandFixture(t, url)
	ui := &fakeUI{t: t, actions: []LlamaManagerAction{{Type: LlamaManagerActionModel, Model: model("alpha", LlamaModelStatusUnloaded)}}}
	if err := session.manage(ui); err != nil {
		t.Fatal(err)
	}
	if want := []notification{{"llama.cpp returned HTTP 404", "error"}}; !reflect.DeepEqual(*notices, want) {
		t.Fatalf("notices = %+v, want %+v", *notices, want)
	}
}
