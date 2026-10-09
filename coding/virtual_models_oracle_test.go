package coding

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// virtualBranchCorpus holds session branches (JSON entries) that differ in what getBranchSelection and getVirtualModelState branch on: model changes of virtual and physical models, assistant messages of either kind, custom state entries, and entries with missing or ill-typed members.
func virtualBranchCorpus() [][]string {
	change := func(provider, id string) string {
		return fmt.Sprintf(`{"type":"model_change","id":"c","provider":%q,"modelId":%q}`, provider, id)
	}
	reply := func(api, provider, model string) string {
		return fmt.Sprintf(`{"type":"message","id":"m","message":{"role":"assistant","api":%q,"provider":%q,"model":%q,"content":[],"stopReason":"stop"}}`, api, provider, model)
	}
	user := `{"type":"message","id":"u","message":{"role":"user","content":"hi"}}`
	state := func(provider, id, payload string) string {
		return fmt.Sprintf(`{"type":"custom","id":"s","customType":"pi.virtual-model-state","data":{"provider":%q,"modelId":%q,"state":%s}}`, provider, id, payload)
	}
	return [][]string{
		{}, {user},
		{change("p", "phys")}, {change("p", "virt")},
		{change("p", "virt"), reply("openai", "p", "phys")},
		{change("p", "phys"), reply("openai", "p", "phys")},
		{change("p", "gone"), reply("openai", "p", "phys")},
		{change("p", "virt"), reply("pi-virtual", "p", "virt")},
		{change("p", "virt"), reply("openai", "p", "phys"), change("p", "phys")},
		{change("p", "virt"), reply("openai", "p", "phys"), change("p", "virt2"), user},
		{reply("openai", "p", "phys")},
		{reply("openai", "p", "phys"), reply("openai", "q", "other")},
		{change("p", "virt"), reply("openai", "p", "a"), reply("openai", "p", "b")},
		{change("p", "virt"), user, reply("pi-virtual", "p", "virt"), user},
		{`{"type":"model_change","id":"c"}`, reply("openai", "p", "phys")},
		{change("p", "virt"), `{"type":"message","id":"m","message":{"role":"assistant","api":"openai","model":"phys","content":[]}}`},
		{`{"type":"model_change","id":"c","provider":1,"modelId":null}`},
		{change("p", "phys"), `{"type":"model_change","id":"c","provider":["a",null,[1,2]],"modelId":{"x":1}}`},
		{`{"type":"model_change","id":"c","provider":true,"modelId":1.5e21}`, reply("openai", "p", "phys")},
		{change("p", "virt"), reply("openai", "p", "phys"), `{"type":"model_change","id":"c","provider":"p"}`},
		{`{"type":"model_change","id":"c","provider":"p","modelId":"virt"}`, `{"type":"model_change","id":"d","provider":null,"modelId":"virt"}`, reply("openai", "p", "phys")},
		{state("p", "virt", `{"n":1}`), state("p", "virt", `{"n":2}`), state("q", "virt", `3`)},
		{state("p", "virt", `{"n":1}`), state("p", "other", `4`)},
		{state("p", "virt", `null`), state("p", "virt", `{"n":1}`)},
		{state("p", "virt", `{"n":1}`), state("p", "virt", `null`)},
		{`{"type":"custom","id":"s","customType":"pi.virtual-model-state"}`, state("p", "virt", `5`)},
		{state("p", "virt", `5`), `{"type":"custom","id":"s","customType":"pi.virtual-model-state","data":null}`},
		{state("p", "virt", `5`), `{"type":"custom","id":"s","customType":"other","data":{"provider":"p","modelId":"virt","state":6}}`},
		{state("p", "virt", `5`), `{"type":"custom","id":"s","customType":"pi.virtual-model-state","data":{"provider":"p","modelId":"virt"}}`},
		{state("p", "virt", `5`), `{"type":"custom","id":"s","customType":"pi.virtual-model-state","data":"text"}`},
		{state("p", "virt", `{"b":1,"a":[1,{"z":2,"y":3}],"2":0,"1":1}`)},
	}
}

type virtualOracleResult struct {
	Selection *struct {
		Provider   string `json:"provider"`
		ModelID    string `json:"modelId"`
		Resolvable bool   `json:"resolvable"`
	} `json:"selection"`
	State any `json:"state"`
}

// TestBranchSelectionAndStateMatchPi runs getBranchSelection and getVirtualModelState of Pi 1.0.4 and of Pig over the same session branches. A model counts as virtual when its API is pi-virtual, and `virt` and `virt2` are the registered virtual models.
func TestBranchSelectionAndStateMatchPi(t *testing.T) {
	corpus := virtualBranchCorpus()
	branches := make([][]json.RawMessage, len(corpus))
	for i := range branches {
		branches[i] = []json.RawMessage{}
	}
	for i, entries := range corpus {
		for _, entry := range entries {
			branches[i] = append(branches[i], json.RawMessage(entry))
		}
	}
	var want []virtualOracleResult
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/virtual-models.js");
const catalog = { "p/virt": { api: "pi-virtual" }, "p/virt2": { api: "pi-virtual" }, "p/phys": { api: "openai" } };
emit(input.map((branch) => ({
	selection: (() => { const s = mod.getBranchSelection(branch, (provider, id) => catalog[provider + "/" + id]); return s ? { provider: String(s.provider), modelId: String(s.modelId), resolvable: typeof s.provider === "string" && typeof s.modelId === "string" } : null; })(),
	state: mod.getVirtualModelState(branch, "p", "virt") ?? null,
})));`, branches, &want)
	for i, entries := range corpus {
		var branch []icodingagent.SessionEntry
		for _, entry := range entries {
			var base icodingagent.SessionEntryBase
			if err := json.Unmarshal([]byte(entry), &base); err != nil {
				t.Fatal(err)
			}
			branch = append(branch, sessionentry.DecodeSessionEntry(json.RawMessage(entry)))
		}
		selection := GetBranchSelection(branch, func(provider, id string) *ai.Model {
			switch provider + "/" + id {
			case "p/virt", "p/virt2":
				return &ai.Model{ProviderMeta: ai.ProviderMetadata{API: VirtualModelAPI}}
			case "p/phys":
				return &ai.Model{ProviderMeta: ai.ProviderMetadata{API: ai.APIOpenAICompletions}}
			}
			return nil
		})
		state := GetVirtualModelState(branch, "p", "virt")
		w := want[i]
		gotSel := "none"
		if selection != nil {
			gotSel = selection.Provider + "/" + selection.ModelID
			if selection.Unresolvable {
				gotSel += " unresolvable"
			}
		}
		wantSel := "none"
		if w.Selection != nil {
			wantSel = w.Selection.Provider + "/" + w.Selection.ModelID
			if !w.Selection.Resolvable {
				wantSel += " unresolvable"
			}
		}
		if gotSel != wantSel {
			t.Errorf("branch %d %v: selection = %s, Pi %s", i, corpus[i], gotSel, wantSel)
		}
		var gotState any
		if len(state) > 0 {
			if err := json.Unmarshal(state, &gotState); err != nil {
				t.Fatal(err)
			}
		}
		if fmt.Sprint(gotState) != fmt.Sprint(w.State) {
			t.Errorf("branch %d %v: state = %v, Pi %v", i, corpus[i], gotState, w.State)
		}
	}
}
