package ai

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// llamaPromptProbe is one classify request: the state message the prompt content carries and the raw JSON body the
// server's /apply-template answers with.
type llamaPromptProbe struct {
	Message  string `json:"message"`
	Template string `json:"template"`
}

// llamaPromptOracle runs Pi's classify against a fake llama-server per probe and emits one JSON line per probe:
// [the /apply-template request body with sorted keys, the first /completion prompt or null, the error message].
// The providers-registry/08 scenario runs the same program with the vendored Pi module.
const llamaPromptOracle = `
const canonical = (v) => Array.isArray(v) ? v.map(canonical) : v !== null && typeof v === "object" ? Object.fromEntries(Object.keys(v).sort().map((k) => [k, canonical(v[k])])) : v;
const json = (text) => new Response(text, { status: 200, headers: { "content-type": "application/json" } });
const rows = [];
for (const [index, probe] of input.entries()) {
  let template = null, prompt = null;
  const fetch = async (url, init) => {
    const body = JSON.parse(init.body);
    switch (new URL(url).pathname) {
      case "/tokenize": return json(JSON.stringify({ tokens: [...body.content].map((c) => c.codePointAt(0)) }));
      case "/apply-template": template = canonical(body); return json(probe.template);
      case "/completion":
        prompt ??= body.prompt;
        return json(JSON.stringify({ content: "A", completion_probabilities: [{ id: 65, token: "A", top_logprobs: [{ id: 65, token: "A", bytes: [], logprob: -0.1 }, { id: 66, token: "B", bytes: [], logprob: -2.5 }] }] }));
    }
    return new Response("not found", { status: 404 });
  };
  const model = { id: "qwen", name: "qwen", api: "llama-cpp-classify", provider: "llama.cpp", baseUrl: "http://llama-prompt-" + index + ".test:8080/v1", input: ["text"], contextWindow: 32768 };
  const context = { state: { message: probe.message }, questions: { pick: { type: "choice", instructions: "Pick", criteria: { a: "", b: "" } } } };
  const result = await classify(model, context, { fetch });
  // Go's encoder writes U+2028 and U+2029 as escapes; JSON.stringify writes them raw.
  rows.push(JSON.stringify([template, prompt, result.errorMessage ?? ""]).replace(/\u2028/g, "\\u2028").replace(/\u2029/g, "\\u2029"));
}
`

// llama-cpp-classify.ts renderPrompt against pinned Pi over the probes of llamaPromptProbes: the /apply-template request
// (model, system and user messages, thinking disabled), the prompt the completion request carries (an open <think> at
// the end closed at once, anything else as returned) and "did not return a prompt" for bodies without a string prompt.
func TestLlamaCppRenderPromptMatchesPi(t *testing.T) {
	probes := llamaPromptProbes()
	var want []string
	pioracle.Run(t, `const { classify } = await load("pi-ai/api/llama-cpp-classify.js");`+llamaPromptOracle+`emit(rows);`, probes, &want)
	failures := 0
	for i, probe := range probes {
		if got := goLlamaPrompt(t, i, probe); got != want[i] {
			if failures++; failures <= 3 {
				t.Errorf("probe %d %q differs from Pi\n Pig %s\n Pi  %s", i, probe.Template, got, want[i])
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// TestLlamaCppRenderPromptDump prints the probes for the Pi side of the providers-registry/08 parity scenario.
func TestLlamaCppRenderPromptDump(t *testing.T) {
	line, err := marshalJSONValue(llamaPromptProbes())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("llama-prompt-probes:%s\n", line)
	fmt.Printf("llama-prompt-oracle:%s\n", marshalJSONString(llamaPromptOracle))
}

// TestLlamaCppRenderPromptParity prints PiG's line for each probe for the providers-registry/08 parity scenario.
func TestLlamaCppRenderPromptParity(t *testing.T) {
	for i, probe := range llamaPromptProbes() {
		fmt.Printf("llama-prompt-observation:%s\n", goLlamaPrompt(t, i, probe))
	}
}

// goLlamaPrompt runs ClassifyLlamaCpp against the fake server llamaPromptOracle describes and returns the probe's line.
func goLlamaPrompt(t *testing.T, index int, probe llamaPromptProbe) string {
	t.Helper()
	var template, prompt any
	fetch := clsClient(func(r *http.Request) (*http.Response, error) {
		body := clsBody(t, r)
		switch r.URL.Path {
		case "/tokenize":
			data, _ := marshalJSONValue(map[string]any{"tokens": charTokens(body["content"].(string))})
			return clsJSON(200, string(data)), nil
		case "/apply-template":
			template = body
			return clsJSON(200, probe.Template), nil
		case "/completion":
			if prompt == nil {
				prompt = body["prompt"]
			}
			return clsJSON(200, `{"content":"A","completion_probabilities":[{"id":65,"token":"A","top_logprobs":[{"id":65,"token":"A","bytes":[],"logprob":-0.1},{"id":66,"token":"B","bytes":[],"logprob":-2.5}]}]}`), nil
		}
		return clsText(404, "not found", nil), nil
	})
	model := ClassifierModel{ID: "qwen", Name: "qwen", API: "llama-cpp-classify", Provider: "llama.cpp", BaseURL: fmt.Sprintf("http://llama-prompt-%d.test:8080/v1", index), Input: []string{"text"}, ContextWindow: 32768}
	request := ClassifierContext{State: JsonObject{"message": probe.Message}, Questions: ClassifierQuestions{{ID: "pick", Question: ClassifierChoiceQuestion{Instructions: "Pick", Criteria: clsChoices("a", "", "b", "")}}}}
	result := ClassifyLlamaCpp(t.Context(), model, request, ClassifierOptions{Fetch: fetch})
	line, err := marshalJSONValue([]any{template, prompt, result.ErrorMessage})
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReplacer("\u2028", `\u2028`, "\u2029", `\u2029`).Replace(string(line))
}

// llamaPromptProbes are fixed /apply-template bodies (string prompts with and without a trailing <think>, and bodies
// without a string prompt) followed by 300 seeded prompts built from template pieces.
func llamaPromptProbes() []llamaPromptProbe {
	probes := []llamaPromptProbe{
		{"plain", `{"prompt":"<|assistant|>\n"}`},
		{"open think", `{"prompt":"<|assistant|>\n<think>"}`},
		{"closed think", `{"prompt":"<|assistant|>\n<think></think>"}`},
		{"think then newline", `{"prompt":"<think>\n"}`},
		{"think then space", `{"prompt":"<think> "}`},
		{"upper case", `{"prompt":"<THINK>"}`},
		{"only think", `{"prompt":"<think>"}`},
		{"empty", `{"prompt":""}`},
		{"extra fields", `{"prompt":"x<think>","tokens":[1,2]}`},
		{"number prompt", `{"prompt":5}`},
		{"null prompt", `{"prompt":null}`},
		{"array prompt", `{"prompt":["<think>"]}`},
		{"no prompt", `{}`},
		{"null body", `null`},
		{"array body", `["<think>"]`},
		{"string body", `"<think>"`},
		{"number body", `7`},
		{"true body", `true`},
	}
	rng := rand.New(rand.NewPCG(20261013, 8))
	pick := func(items ...string) string { return items[rng.IntN(len(items))] }
	for range 300 {
		var prompt strings.Builder
		for range rng.IntN(5) {
			prompt.WriteString(pick("", "<|im_start|>assistant\n", "<think>", "</think>", "<think", "think>", "\n", " ", "<THINK>", "日本語 😀", "\u2028", "\"q\" \\ s", "<|assistant|>\n"))
		}
		encoded, _ := marshalJSONValue(prompt.String())
		message := pick("", "Help! My payouts have been failing.", "multi\nline", "</state>", "日本語 😀", "é\u2028x", "{\"a\":1}")
		probes = append(probes, llamaPromptProbe{Message: message, Template: `{"prompt":` + string(encoded) + `}`})
	}
	return probes
}
