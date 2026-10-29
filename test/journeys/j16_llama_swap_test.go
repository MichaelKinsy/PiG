//go:build parity

package journeys

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MichaelKinsy/PiG/test/journeys/harness"
)

// The llama-swap gateway as its source defines it (github.com/mostlygeek/llama-swap, internal/server/api.go, internal/router/loading.go):
// GET /models and GET /v1/models list the configured models with a llama.cpp router style status.value, there is no /models/load, and a model
// loads when a request names it in the `model` field. While it loads, a streaming chat completion carries bare reasoning_content frames
// (no id, no role, no finish_reason) ahead of the upstream reply, and a failed load ends the stream with an error envelope frame and [DONE].
const (
	swapLoaded   = "qwen3.8-q4-uncensored-layer"
	swapSecond   = "qwen3.8-q6-uncensored-tensor"
	swapThird    = "qwen3.8-flash-next-q2"
	swapBroken   = "qwen3.8-broken-split"
	swapUnrouted = "qwen3.8-not-in-config"
)

var swapModels = []string{swapLoaded, swapSecond, swapThird, swapBroken, swapUnrouted}

func swapFrame(text string) string {
	b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": text}}}})
	return string(b)
}

// swapLoading is the progress llama-swap streams while a model it does not yet run starts.
func swapLoading() []string {
	return []string{swapFrame("\n"), swapFrame("."), swapFrame("."), swapFrame("\n"), swapFrame("Done! (0.50s)"), swapFrame("\n"), swapFrame("━━━━━"), swapFrame("\n"), swapFrame(" ")}
}

func swapEnvelope(message, typ, code string) string {
	b, _ := json.Marshal(map[string]any{"src": "llama-swap", "error": map[string]any{"message": message, "type": typ, "code": code}})
	return string(b)
}

// swapReply is a pure function of the request's model, as the harness requires: the model the gateway already runs answers at once, every other
// configured model first streams the loading frames, the broken one fails to start, and an unrouted one is refused with HTTP 404.
// served is the reply text: the model that answered and the prompt it answered, so each turn's text is unique on screen.
func served(r *harness.Request) string {
	return fmt.Sprintf("served by %s (%s)", r.Model, short(r.Last().Text))
}

func swapReply(r *harness.Request) (harness.Reply, bool) {
	switch r.Model {
	case swapLoaded:
		return harness.Say(served(r)), true
	case swapSecond, swapThird:
		return harness.Say(served(r)).WithPreamble(swapLoading()...), true
	case swapBroken:
		return harness.RawStream(append(swapLoading()[:2], swapEnvelope("["+r.Model+"] upstream exited unexpectedly", "server_error", "internal_server_error"))...), true
	case swapUnrouted:
		return harness.Fail(http.StatusNotFound, swapEnvelope("no local server found for requested model", "not_found_error", "not_found")), true
	}
	return harness.Reply{}, false
}

// mountSwap serves the catalog the way llama-swap's handleListModels does: every model carries status.value, and the model it runs is loaded.
func mountSwap(p *harness.Provider) {
	catalog := func(w http.ResponseWriter, _ *http.Request) {
		var data []any
		for _, id := range swapModels[:4] {
			status, meta := "unloaded", map[string]any{}
			if id == swapLoaded {
				status, meta = "loaded", map[string]any{"n_ctx": 131072}
			}
			data = append(data, map[string]any{"id": id, "object": "model", "created": 1700000000, "owned_by": "llama-swap", "status": map[string]any{"value": status}, "meta": meta})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}
	// GET /props needs ?model= and answers from that model's own server (README: "requires ?model={model_id}").
	p.Handle("GET /props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("model") == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(swapEnvelope("no model id could be identified", "not_found_error", "not_found")))
			return
		}
		_, _ = w.Write([]byte(`{"chat_template":"{{ messages }}"}`))
	})
	p.Handle("GET /models", catalog)
	p.Handle("GET /v1/models", catalog)
}

func swapServed(c *harness.Ctx, model string) {
	reqs := c.P.Requests()
	c.Expect(len(reqs) > 0 && reqs[len(reqs)-1].Model == model, "the newest provider request names model %q, got %v", model, lastModel(reqs))
}

func lastModel(reqs []*harness.Request) string {
	if len(reqs) == 0 {
		return "(no request)"
	}
	return reqs[len(reqs)-1].Model
}

// pickModel selects a models.json model in the /model selector: open it, list, filter to the model, Enter.
func pickModel(c *harness.Ctx, id, name string) {
	c.Say("/model")
	c.WaitFor("Only showing models from configured providers", "Model catalogs refreshed.")
	c.Quiet(c.Settle())
	c.Snap(name + "-model-list")
	c.Type(id + " strict-wire")
	c.Quiet(c.Settle())
	c.Snap(name + "-model-filtered")
	c.Key("Enter")
	c.WaitFor("Model: " + id)
	c.Quiet(c.Settle())
	c.Snap(name + "-model-selected")
}

func init() {
	register(&harness.Journey{
		Name: "llama-swap-models-json",
		Doc: "A llama-swap gateway configured as a models.json provider, beside the llama.cpp provider logged in to the same gateway: /model lists the models, a second model is selected and a prompt names it in the request's model field " +
			"(llama-swap loads it on that request, streaming bare loading frames ahead of the reply), the selector switches back, a model that fails to start ends the stream with the gateway's error envelope, and an unrouted model is refused with HTTP 404.",
		Touches:     []string{"internal/codingagent/", "ai/", "agent/", "tui/", "coding/"},
		Normalizers: []harness.Normalizer{bugReportHint},
		Env: func(harness.Kind) harness.Env {
			return harness.Env{
				NoModelArg:  true,
				ReadyMarker: swapLoaded,
				Models:      swapModels,
				// The llama.cpp provider lists the gateway's catalog only with network access.
				ExtraEnv:      []string{"PI_OFFLINE=0"},
				AgentDirFiles: map[string]string{"auth.json": `{"llama.cpp":{"type":"api_key","key":"local","env":{"LLAMA_BASE_URL":"{{PROVIDER_URL}}"}}}`},
			}
		},
		Mount:  mountSwap,
		Script: script(swapReply),
		Backbone: []harness.Step{
			{Name: "launch", Run: func(c *harness.Ctx) { c.Launch("--model", "strict-wire/"+swapLoaded); c.Snap("startup") }},
			{Name: "first-turn", Run: func(c *harness.Ctx) {
				prompt(c, "Hello from the loaded model", 1)
				c.WaitFor("served by " + swapLoaded + " (Hello from the loaded model)")
				swapServed(c, swapLoaded)
				c.Snap("turn-loaded")
			}},
		},
		Pool: []harness.Action{
			{Step: harness.Step{Name: "select-second-and-prompt", Run: func(c *harness.Ctx) {
				pickModel(c, swapSecond, "second")
				prompt(c, "Hello from the second model", 1)
				c.WaitFor("served by " + swapSecond + " (Hello from the second model)")
				swapServed(c, swapSecond)
				c.Snap("turn-second")
			}}},
			{Needs: []string{"select-second-and-prompt"}, Step: harness.Step{Name: "switch-back", Run: func(c *harness.Ctx) {
				pickModel(c, swapLoaded, "back")
				prompt(c, "Hello again from the first model", 1)
				c.WaitFor("served by " + swapLoaded + " (Hello again from the first model)")
				swapServed(c, swapLoaded)
				c.Snap("turn-back")
			}}},
			{Step: harness.Step{Name: "broken-model", Run: func(c *harness.Ctx) {
				pickModel(c, swapBroken, "broken")
				prompt(c, "Try the model that fails to start", 1)
				c.Quiet(c.Settle())
				swapServed(c, swapBroken)
				c.Snap("turn-broken")
			}}},
			{Step: harness.Step{Name: "unrouted-model", Run: func(c *harness.Ctx) {
				pickModel(c, swapUnrouted, "unrouted")
				prompt(c, "Try the model the gateway does not route", 1)
				c.WaitFor("no local server found for requested model")
				swapServed(c, swapUnrouted)
				c.Snap("turn-unrouted")
			}}},
		},
		Tail: []harness.Step{{Name: "quit", Run: func(c *harness.Ctx) { c.Snap("before-quit"); c.Quit() }}},
	})
}
