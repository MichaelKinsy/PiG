package llama

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type llamaRefreshProbe struct {
	Catalog   string            `json:"catalog"`
	Autoload  string            `json:"autoload"`
	Templates map[string]string `json:"templates"`
	Stored    []json.RawMessage `json:"stored"`
	HasStored bool              `json:"hasStored"`
	Key       string            `json:"key"`
}

type llamaRefreshOutcome struct {
	Err    string `json:"err"`
	Models []any  `json:"models"`
}

// Left out of the corpus: n_ctx sent as a fraction, a string or a boolean (Pi forwards the JSON value as the model's contextWindow, a string included; Go's contextWindow is an int: a divergence awaiting owner sign-off in handoffs/owner-signoff.md, "llama.cpp n_ctx that is not an integer"; no D record or marker until the owner decides). Servers that send modalities as a string and stored models with no, partial or extended compat objects are in it. A reasoning model's thinkingLevelMap and thinkingTokenBudgetField are PiG's (D90) and are removed on both sides.
// The llama.cpp provider's refreshModels (llama/provider.js) against Pi over a local router server: which catalog entries are selectable (loaded, sleeping, autoload presets, failed and non-preset ones), chat versus decision models, the context window choice (loaded n_ctx, --ctx-size / -c / -ctx
// arguments with Number() parsing, the stored window, n_ctx_train, 128000), the thinking template check on loaded models, image input, classifier models and their API and base URL, restored models, and a failing props request. The published models are compared as JSON.
func TestLlamaProviderRefreshMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(63))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	// A stored model keeps whatever compat object it was saved with, including none, partial members and unknown ones.
	storedCompat := func() string {
		return pick(`,"compat":{"supportsStore":false,"supportsDeveloperRole":false,"supportsReasoningEffort":false,"supportsUsageInStreaming":true,"supportsStrictMode":false,"maxTokensField":"max_tokens"}`,
			`,"compat":{"supportsStore":false,"supportsDeveloperRole":false,"supportsReasoningEffort":false,"supportsUsageInStreaming":true,"supportsStrictMode":false,"maxTokensField":"max_tokens"}`, ``, `,"compat":{}`, `,"compat":{"supportsStore":true}`, `,"compat":{"maxTokensField":"max_completion_tokens","custom":1}`)
	}
	args := func() string {
		items := []string{}
		for range rng.Intn(4) {
			switch rng.Intn(4) {
			case 0:
				items = append(items, fmt.Sprintf("%q,%q", pick("--ctx-size", "-c", "-ctx", "--other", "-C"), pick("4096", "8192", "0", "-5", "1.5", "abc", "", " 2048 ", "0x1000", "1e3", "9007199254740993", "1e400", "Infinity", "0b11", "0o17")))
			case 1:
				items = append(items, fmt.Sprintf("%q", pick("--ctx-size", "-c", "--flag")))
			default:
				items = append(items, fmt.Sprintf("%q", pick("x", "4096", "-m", "model.gguf")))
			}
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	entry := func(id string) string {
		status := []string{fmt.Sprintf(`"value":%q`, pick("loaded", "loaded", "unloaded", "unloaded", "sleeping", "loading", "downloading"))}
		if rng.Intn(2) == 0 {
			status = append(status, `"args":`+args())
		}
		if rng.Intn(4) == 0 {
			status = append(status, `"failed":`+pick("true", "false"))
		}
		members := []string{fmt.Sprintf(`"id":%q`, id), "\"status\":{" + strings.Join(status, ",") + "}"}
		if rng.Intn(3) != 0 {
			members = append(members, fmt.Sprintf(`"source":%q`, pick("preset", "cache", "local")))
		}
		if rng.Intn(2) == 0 {
			arch := []string{}
			if rng.Intn(3) != 0 {
				arch = append(arch, `"input_modalities":`+pick(`["text"]`, `["text","image"]`, `["image"]`, `[]`, `"text,image"`, `"image"`, `"text"`, `""`))
			}
			if rng.Intn(3) != 0 {
				arch = append(arch, `"output_modalities":`+pick(`["text"]`, `["decisions"]`, `["text","decisions"]`, `[]`, `["decisions","text"]`, `"decisions"`, `"text,decisions"`, `"text"`, `""`))
			}
			members = append(members, "\"architecture\":{"+strings.Join(arch, ",")+"}")
		}
		if rng.Intn(2) == 0 {
			meta := []string{}
			if rng.Intn(2) == 0 {
				meta = append(meta, `"n_ctx":`+pick("4096", "0", "-1", "8192", "null"))
			}
			if rng.Intn(2) == 0 {
				meta = append(meta, `"n_ctx_train":`+pick("32768", "0", "131072", "null", "4096"))
			}
			members = append(members, "\"meta\":{"+strings.Join(meta, ",")+"}")
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	var probes []llamaRefreshProbe
	for range 700 {
		p := llamaRefreshProbe{Templates: map[string]string{}, Key: pick("", "key")}
		items := []string{}
		for i := range rng.Intn(6) {
			id := fmt.Sprintf("m%d", i)
			items = append(items, entry(id))
			p.Templates[id] = pick("{{ enable_thinking }}", "plain template", "", "x enable_thinking y", "ENABLE_THINKING")
		}
		p.Catalog = `{"data":[` + strings.Join(items, ",") + `]}`
		p.Autoload = pick("true", "false", "null", `"true"`, "{}")
		if rng.Intn(15) == 0 {
			p.Templates["m0"] = "!fail"
		}
		if rng.Intn(2) == 0 {
			p.HasStored = true
			for range rng.Intn(5) {
				id := fmt.Sprintf("m%d", rng.Intn(6))
				switch rng.Intn(5) {
				case 0:
					p.Stored = append(p.Stored, json.RawMessage(fmt.Sprintf(`{"type":"classifier","id":%q,"name":%q,"api":%q,"provider":"llama.cpp","baseUrl":"http://x","input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":%s}`, id, id, pick("llama-cpp-classify", "typesafe-system-one", "other"), pick("2048", "0", "16000"))))
				case 1:
					p.Stored = append(p.Stored, json.RawMessage(fmt.Sprintf(`{"id":%q,"name":%q,"api":"openai-completions","provider":%q,"baseUrl":"http://x","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":%s,"maxTokens":1%s}`, id, id, pick("llama.cpp", "llama.cpp", "other"), pick("2048", "0", "16000", "100000"), storedCompat())))
				default:
					p.Stored = append(p.Stored, json.RawMessage(fmt.Sprintf(`{"id":%q,"name":%q,"api":"openai-completions","provider":"llama.cpp","baseUrl":"http://x","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":%s,"maxTokens":1%s}`, id, id, pick("4096", "65536"), storedCompat())))
				}
			}
		}
		probes = append(probes, p)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/p")
		slash := strings.Index(rest, "/")
		n, _ := strconv.Atoi(rest[:slash])
		p := probes[n]
		switch rest[slash:] {
		case "/models":
			_, _ = w.Write([]byte(p.Catalog))
		case "/props":
			model := r.URL.Query().Get("model")
			if model == "" {
				_, _ = fmt.Fprintf(w, `{"models_autoload":%s}`, p.Autoload)
				return
			}
			template := p.Templates[model]
			if template == "!fail" {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"error":{"message":"props failed"}}`))
				return
			}
			encoded, _ := json.Marshal(map[string]string{"chat_template": template})
			_, _ = w.Write(encoded)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	var want []llamaRefreshOutcome
	pioracle.Run(t, `
const { createLlamaProvider } = await load("pi-coding-agent/extensions/llama/provider.js");
const out = [];
for (const [n, p] of input.probes.entries()) {
	const { provider } = createLlamaProvider();
	const url = input.url + "/p" + n;
	const controller = new AbortController();
	try {
		await provider.refreshModels({
			stored: p.hasStored ? { models: p.stored ?? [] } : undefined,
			allowNetwork: true,
			signal: controller.signal,
			credential: { type: "api_key", key: p.key || undefined, env: { LLAMA_BASE_URL: url } },
			publish: async (publication) => { publication.update?.(); return true; },
		});
		out.push({ models: provider.getAllModels() });
	} catch (e) { out.push({ err: e instanceof Error ? e.message : String(e), models: provider.getAllModels() }); }
}
emit(out);
`, struct {
		URL    string              `json:"url"`
		Probes []llamaRefreshProbe `json:"probes"`
	}{server.URL, probes}, &want)
	bad := 0
	for n, p := range probes {
		controller := CreateLlamaProvider()
		stored := &ai.ModelsStoreEntry{}
		for _, raw := range p.Stored {
			model, known, err := ai.DecodeStoredModel(raw)
			if err != nil || !known {
				continue
			}
			stored.Models = append(stored.Models, model)
		}
		if !p.HasStored {
			stored = nil
		}
		err := controller.Provider.RefreshModels(RefreshModelsContext{
			Ctx:          context.Background(),
			Stored:       stored,
			AllowNetwork: true,
			Credential:   &ai.Credential{Type: ai.CredentialAPIKey, Key: p.Key, Env: map[string]string{"LLAMA_BASE_URL": fmt.Sprintf("%s/p%d", server.URL, n)}},
			Publish: func(publication ModelsPublication) (bool, error) {
				if publication.Update != nil {
					publication.Update()
				}
				return true, nil
			},
		})
		got := llamaRefreshOutcome{}
		if err != nil {
			got.Err = err.Error()
		}
		got.Models = []any{}
		for _, model := range controller.Provider.GetAllModels() {
			encoded, _ := json.Marshal(model)
			var decoded any
			_ = json.Unmarshal(encoded, &decoded)
			got.Models = append(got.Models, decoded)
		}
		w := want[n]
		if w.Models == nil {
			w.Models = []any{}
		}
		for _, models := range [][]any{got.Models, w.Models} {
			for _, model := range models {
				object := model.(map[string]any)
				delete(object, "thinkingLevelMap")
				if compat, ok := object["compat"].(map[string]any); ok {
					delete(compat, "thinkingTokenBudgetField")
				}
			}
		}
		if got.Err != w.Err || !reflect.DeepEqual(got.Models, w.Models) {
			if bad++; bad <= 4 {
				g, _ := json.Marshal(got)
				e, _ := json.Marshal(w)
				t.Errorf("probe %d catalog=%s stored=%s autoload=%s templates=%v:\n got %s\nwant %s", n, p.Catalog, p.Stored, p.Autoload, p.Templates, g, e)
			}
		}
	}
	if bad > 4 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}
