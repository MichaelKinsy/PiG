package ai

import (
	"encoding/json"
	"os/exec"
	"testing"
)

// pi: packages/ai/src/api/openai-completions.ts
// pi: packages/ai/catalog/providers/github-copilot.ts
//
// Pi 86918f9eeb (#12110): the installed Pi (the pin with the commit's backport blocks) builds the request of every GitHub Copilot
// chat-completions model of its own catalog, and of other providers' models the commit must leave alone, at every thinking level,
// and the Go provider sends the same reasoning_effort (or none) for the same catalog model and level.
func TestGitHubCopilotCompletionsReasoningEffortMatchesPi(t *testing.T) {
	type probe struct {
		Provider string `json:"provider"`
		ID       string `json:"id"`
		Level    string `json:"level"`
	}
	models := [][2]string{{"zai", "glm-5.3"}, {"moonshotai", "kimi-k3"}, {"openai", "gpt-4o-mini"}}
	for _, generated := range GeneratedModels {
		if generated.Provider == "github-copilot" && generated.API == APIOpenAICompletions {
			models = append(models, [2]string{generated.Provider, generated.ID})
		}
	}
	if len(models) < 6 {
		t.Fatalf("the catalog has %d GitHub Copilot chat-completions models, want at least 3", len(models)-3)
	}
	var probes []probe
	for _, model := range models {
		for _, level := range []string{"", "low", "medium", "high", "max"} {
			probes = append(probes, probe{model[0], model[1], level})
		}
	}
	const script = `(async () => {
const [root, probesJSON] = process.argv.slice(1);
const load = (rel) => import(require("node:url").pathToFileURL(require("node:path").join(root, rel)).href);
const oai = await load("node_modules/@earendil-works/pi-ai/dist/api/openai-completions.js");
const { MODELS } = await load("node_modules/@earendil-works/pi-ai/dist/models.generated.js");
const out = [];
for (const probe of JSON.parse(probesJSON)) {
	const model = MODELS[probe.provider]?.[probe.id];
	if (!model) { out.push({ error: "no such model" }); continue; }
	let captured = null;
	const options = { apiKey: "test", onPayload: (payload) => { captured = payload; throw new Error("payload captured"); } };
	if (probe.level) options.reasoning = probe.level;
	try {
		await oai.streamSimple(model, { messages: [{ role: "user", content: "Hi", timestamp: 1 }] }, options).result();
	} catch {}
	out.push(captured === null ? { error: "no payload" } : { present: "reasoning_effort" in captured, effort: JSON.stringify(captured.reasoning_effort ?? null) });
}
process.stdout.write(JSON.stringify(out) + "\n", () => process.exit(0));
})()`
	encoded, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), string(encoded)).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	var want []struct {
		Error   string `json:"error"`
		Present bool   `json:"present"`
		Effort  string `json:"effort"`
	}
	if err := json.Unmarshal(output, &want); err != nil || len(want) != len(probes) {
		t.Fatalf("undecodable Pi output %q: %v", output, err)
	}
	sent := 0
	for i, probe := range probes {
		name := probe.Provider + "/" + probe.ID + "/" + probe.Level
		if probe.Level == "" {
			name += "none"
		}
		t.Run(name, func(t *testing.T) {
			if want[i].Error != "" {
				t.Fatalf("Pi: %s", want[i].Error)
			}
			payload, _, _ := captureToolChoiceRequest(t, toolChoiceModel(t, probe.Provider, probe.ID, false), toolChoiceHi(), StreamOptions{Thinking: ThinkingLevel(probe.Level)}, nil)
			got, present := payload["reasoning_effort"]
			effort := "null"
			if present {
				effort = string(got)
			}
			if present != want[i].Present || effort != want[i].Effort {
				t.Fatalf("PiG reasoning_effort present=%v %s\n Pi reasoning_effort present=%v %s", present, effort, want[i].Present, want[i].Effort)
			}
			if probe.Provider == "github-copilot" && present {
				sent++
			}
		})
	}
	if !t.Failed() && sent == 0 {
		t.Fatal("no GitHub Copilot chat-completions request carried reasoning_effort: the installed Pi does not hold Pi 86918f9eeb")
	}
}
