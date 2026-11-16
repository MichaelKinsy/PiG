package extensionconformance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// providerHookScenario is a set of handlers each subscribed to before_provider_request (payload) and before_provider_headers (headers) in this order, and what
// Pi sends after them. A handler's mode is what event_mutate_probe does:
//
//	edit    edits the object it is given in place and returns nothing
//	replace edits nothing and returns a replacement ({replaced: returned} for a payload, {x-pig-replaced: returned} for headers)
//	both    edits the object in place under a key of its own and returns the replacement
//	delete  (headers) sets the header the edit wrote to null
//
// Pi's rule (runner.ts emitBeforeProviderRequest, emitBeforeProviderHeaders): a payload handler's result replaces the payload every later handler and the request
// see (an in-place edit of the object it replaced is gone), and what it edits in place without a result stays; a headers handler's result is ignored, so only
// the in-place edits count and a null value deletes a header.
type providerHookScenario struct {
	name        string
	payload     []string
	headers     []string
	wantPayload map[string]any
	wantHeaders map[string]string // by the in-place edits; the header the request starts with is x-fixture-kept
}

const providerHookKept = "kept-header-value"

var providerHookReplaced = map[string]any{"replaced": "returned"}

func providerHookScenarios() []providerHookScenario {
	edited := func(members ...string) map[string]string {
		want := map[string]string{"x-fixture-kept": providerHookKept}
		for _, member := range members {
			want["x-pig-"+member] = inPlaceEdit
		}
		return want
	}
	replacedAnd := func(members ...string) map[string]any {
		want := map[string]any{"replaced": "returned"}
		for _, member := range members {
			want[member] = inPlaceEdit
		}
		return want
	}
	return []providerHookScenario{
		{
			// A returned map changes no header, whatever its place among the handlers; the payload a handler returns is what the next handler edits.
			name: "replace-then-edit", payload: []string{"replace", ""}, headers: []string{"replace", "", "both"},
			wantPayload: replacedAnd("inPlace"), wantHeaders: edited("inPlace", "inPlaceAlongside"),
		},
		{
			// An edit of the payload before a handler that returns a payload is gone with the object it edited.
			name: "edit-then-replace", payload: []string{"", "replace"}, headers: []string{"both", "", "replace"},
			wantPayload: providerHookReplaced, wantHeaders: edited("inPlace", "inPlaceAlongside"),
		},
		{
			// A handler that edits and returns another payload sends the one it returned; one that sets a header to null deletes it from the request.
			name: "edit-and-return-then-delete", payload: []string{"both"}, headers: []string{"", "delete"},
			wantPayload: providerHookReplaced, wantHeaders: edited(),
		},
		{
			// The edit of a payload another handler returned is kept; the payload of a handler that edits it and returns a fresh one is the fresh one.
			name: "replace-both-edit", payload: []string{"replace", "both", ""}, headers: []string{"delete", "replace"},
			wantPayload: replacedAnd("inPlace"), wantHeaders: edited(),
		},
	}
}

// TestConformance_ProviderHookEditsAndResultsCombine pins what reaches the request when a before_provider_request or before_provider_headers handler returns a
// value, edits its event's object in place, or does both, and when handlers of the two kinds follow one another (providerHookScenario). Pi's runner answers the
// same table (TestProviderHookEditsAndResultsMatchPi). A before_provider_headers handler's return value is ignored in Pi: an SDK that applies a returned map
// sends headers no handler edited, and the row binds the returned map to a header only it writes. The row holds for a Runner's emit and for the request a
// Session sends to a recording provider server, in every SDK, isolated and packed, and the fused Go extension.
func TestConformance_ProviderHookEditsAndResultsCombine(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			for _, scenario := range providerHookScenarios() {
				t.Run(scenario.name, func(t *testing.T) {
					t.Run("runner", func(t *testing.T) {
						h := mutateHarness(t, tc, scenario.payload, scenario.headers)
						ctx := context.Background()
						gotPayload, err := h.runner.EmitBeforeProviderRequest(ctx, json.RawMessage(inPlacePayload))
						if err != nil {
							t.Fatal(err)
						}
						raw, err := json.Marshal(gotPayload)
						if err != nil {
							t.Fatal(err)
						}
						var payload map[string]any
						if err := json.Unmarshal(raw, &payload); err != nil {
							t.Fatalf("%s: %v", raw, err)
						}
						if want := scenario.wantPayload; !reflect.DeepEqual(payload, want) {
							t.Errorf("the payload after the handlers\n got %s\nwant %v", raw, want)
						}
						kept := providerHookKept
						gotHeaders, err := h.runner.EmitBeforeProviderHeaders(ctx, map[string]*string{"x-fixture-kept": &kept})
						if err != nil {
							t.Fatal(err)
						}
						seen := map[string]string{}
						for name, value := range gotHeaders {
							if value != nil {
								seen[name] = *value
							}
						}
						if !reflect.DeepEqual(seen, scenario.wantHeaders) {
							t.Errorf("the headers after the handlers\n got %v\nwant %v", seen, scenario.wantHeaders)
						}
					})
					t.Run("request", func(t *testing.T) {
						recorded := sendRecordedRequest(t, mutateHarness(t, tc, scenario.payload, scenario.headers), t.TempDir(), func(baseURL string) ai.Provider {
							return ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "test", Model: "probe", APIKey: "fixture-key", BaseURL: baseURL})
						})
						var body map[string]any
						if err := json.Unmarshal(recorded.body, &body); err != nil {
							t.Fatalf("%s: %v", recorded.body, err)
						}
						// Every scenario's payload handlers return a payload, so the request carries exactly what they left and none of the request's own members.
						if !reflect.DeepEqual(body, scenario.wantPayload) {
							t.Errorf("the recorded request body\n got %s\nwant %v", recorded.body, scenario.wantPayload)
						}
						for _, member := range []string{"inPlace", "inPlaceAlongside", "replaced"} {
							want := scenario.wantHeaders["x-pig-"+member]
							if got := recorded.header.Get("x-pig-" + member); got != want {
								t.Errorf("the recorded request has header x-pig-%s = %q, want %q", member, got, want)
							}
						}
					})
				})
			}
		})
	}
}

// TestProviderHookEditsAndResultsMatchPi runs Pi's own ExtensionRunner (the compiled runner.ts the Node runtime ships) over handlers that do what each mode of
// providerHookScenario does, and requires what it returns to be what the rows above require of every SDK: the oracle of the table.
func TestProviderHookEditsAndResultsMatchPi(t *testing.T) {
	t.Parallel()
	runner, err := filepath.Abs("../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-coding-agent/core/extensions/runner.js")
	if err != nil {
		t.Fatal(err)
	}
	const body = `
const { ExtensionRunner } = await import(input.runner);
const edit = "in-place-edit";
const handlerFor = (event, mode) => async (e) => {
	const headers = event === "before_provider_headers";
	const key = mode === "both" ? "inPlaceAlongside" : "inPlace";
	if (headers) {
		if (mode === "delete") e.headers["x-pig-inPlace"] = null;
		else if (mode !== "replace") e.headers["x-pig-" + key] = edit;
	} else if (mode !== "replace") {
		e.payload[key] = edit;
		if (e.payload.zeta) e.payload.zeta[key] = edit;
	}
	if (mode === "replace" || mode === "both") return headers ? { "x-pig-replaced": "returned" } : { replaced: "returned" };
};
const out = [];
for (const scenario of input.scenarios) {
	const handlers = new Map([["before_provider_request", scenario.payload.map((mode) => handlerFor("before_provider_request", mode))], ["before_provider_headers", scenario.headers.map((mode) => handlerFor("before_provider_headers", mode))]]);
	const pi = new ExtensionRunner([{ path: "/probe.mjs", handlers }], {}, "/", undefined, undefined);
	const payload = await pi.emitBeforeProviderRequest(input.payload);
	const headers = await pi.emitBeforeProviderHeaders({ "x-fixture-kept": input.kept });
	out.push({ payload, headers });
}
return out;
`
	scenarios := providerHookScenarios()
	type modes struct {
		Payload []string `json:"payload"`
		Headers []string `json:"headers"`
	}
	sent := make([]modes, len(scenarios))
	for i, scenario := range scenarios {
		sent[i] = modes{Payload: append([]string{}, scenario.payload...), Headers: append([]string{}, scenario.headers...)}
	}
	var got []struct {
		Payload map[string]any
		Headers map[string]*string
	}
	out := testenv.PiEvalJSON(t, body, map[string]any{"runner": "file://" + runner, "payload": mustJSONValue(t, inPlacePayload), "kept": providerHookKept, "scenarios": sent})
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(scenarios) {
		t.Fatalf("Pi answered %d scenarios, want %d: %s", len(got), len(scenarios), out)
	}
	for i, scenario := range scenarios {
		if want := scenario.wantPayload; !reflect.DeepEqual(got[i].Payload, want) {
			t.Errorf("%s: Pi's payload after the handlers\n got %v\nwant %v", scenario.name, got[i].Payload, want)
		}
		seen := map[string]string{}
		for name, value := range got[i].Headers {
			if value != nil {
				seen[name] = *value
			}
		}
		if !reflect.DeepEqual(seen, scenario.wantHeaders) {
			t.Errorf("%s: Pi's headers after the handlers\n got %v\nwant %v", scenario.name, seen, scenario.wantHeaders)
		}
	}
}
