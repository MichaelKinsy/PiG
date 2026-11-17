package extensionconformance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestConformance_InPlaceProviderHookEditsReachTheRequest pins what Pi does with an edit a before_provider_request or before_provider_headers handler makes in
// place. Pi hands every handler the one payload (runner.ts emitBeforeProviderRequest returns currentPayload, and a handler's undefined result leaves it) and
// the one headers object (emitBeforeProviderHeaders ignores the return value and returns headers), and its catch keeps the object as the handler left it, so
// a handler that edits event.payload or event.headers and returns nothing, or edits and then fails, changes the request. Each extension handler below edits the
// object it is given with a value only this test uses (`in-place-edit`) and returns nothing; the second handler of each event fails after its edit. The row
// holds for a Runner's emit and for the request a real Session sends to a recording provider server, in every SDK, isolated and packed, and the fused Go
// extension: an SDK that does not send the edited object back loses the member from the request.
func TestConformance_InPlaceProviderHookEditsReachTheRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	const edit = inPlaceEdit
	wantPayload := inPlaceWantPayload()

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()

			// A Session closes its Runner with it, so each scenario holds a harness of its own.
			t.Run("runner", func(t *testing.T) {
				h := probeHarness(t, tc, true)
				t.Run("before_provider_request", func(t *testing.T) {
					got, err := h.runner.EmitBeforeProviderRequest(ctx, json.RawMessage(inPlacePayload))
					if err != nil {
						t.Fatal(err)
					}
					raw, err := json.Marshal(got)
					if err != nil {
						t.Fatal(err)
					}
					var decoded map[string]any
					if err := json.Unmarshal(raw, &decoded); err != nil {
						t.Fatalf("%s: %v", raw, err)
					}
					if !reflect.DeepEqual(decoded, wantPayload) {
						t.Errorf("the payload after the handlers\n got %s\nwant %v", raw, wantPayload)
					}
				})
				t.Run("before_provider_headers", func(t *testing.T) {
					kept := "kept-header-value"
					got, err := h.runner.EmitBeforeProviderHeaders(ctx, map[string]*string{"x-fixture-kept": &kept})
					if err != nil {
						t.Fatal(err)
					}
					seen := map[string]string{}
					for name, value := range got {
						if value != nil {
							seen[name] = *value
						}
					}
					want := inPlaceWantHeaders(kept)
					if !reflect.DeepEqual(seen, want) {
						t.Errorf("the headers after the handlers\n got %v\nwant %v", seen, want)
					}
				})
			})

			t.Run("request", func(t *testing.T) {
				recorded := sendRecordedRequest(t, probeHarness(t, tc, true), t.TempDir(), func(baseURL string) ai.Provider {
					return ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "test", Model: "probe", APIKey: "fixture-key", BaseURL: baseURL})
				})
				var body map[string]any
				if err := json.Unmarshal(recorded.body, &body); err != nil {
					t.Fatalf("%s: %v", recorded.body, err)
				}
				for _, key := range []string{"inPlace", "inPlaceBeforeError"} {
					if body[key] != edit {
						t.Errorf("the recorded request body has %s = %v, want %q (members %v)", key, body[key], edit, keysOf(body))
					}
					if got := recorded.header.Get("x-pig-" + key); got != edit {
						t.Errorf("the recorded request has header x-pig-%s = %q, want %q", key, got, edit)
					}
				}
			})

			// A provider whose payload is a map (Google, Vertex, pi-messages) has no order of its own in Go, and an SDK that re-encodes the payload it echoes
			// may reorder it: the bytes of the request after the edit must be what Pi's JSON.stringify writes for the same edit of the same object. The object
			// is the payload pi-messages builds, {model, context, options} (pi-messages.ts), whose values the baseline run supplies (an extension that only
			// observes the request), and Pi's two edits append their members to it.
			t.Run("request/bytes/pi-messages", func(t *testing.T) {
				cwd := t.TempDir() // the system prompt names it
				baseline := sendRecordedRequest(t, probeHarness(t, tc, false), cwd, newPiMessagesProbeProvider).body
				recorded := sendRecordedRequest(t, probeHarness(t, tc, true), cwd, newPiMessagesProbeProvider)
				want := testenv.NodeJSON(t, `
const sent = JSON.parse(input.baseline);
const body = { model: sent.model, context: sent.context, options: sent.options };
body.inPlace = "in-place-edit";
body.inPlaceBeforeError = "in-place-edit";
return JSON.stringify(body);
`, mustMarshal(t, map[string]string{"baseline": string(baseline)}))
				var wantText string
				if err := json.Unmarshal(want, &wantText); err != nil {
					t.Fatal(err)
				}
				// The messages stamp the time they were made at and the options name the session: no two runs share them.
				got := stamped.ReplaceAllString(string(recorded.body), `"$1":0`)
				wantText = stamped.ReplaceAllString(wantText, `"$1":0`)
				if got != wantText {
					at := 0
					for at < len(got) && at < len(wantText) && got[at] == wantText[at] {
						at++
					}
					t.Errorf("the request body after the edits is not what Pi writes; the bytes first differ at %d of %d (want %d)\n got ...%s\nwant ...%s", at, len(got), len(wantText), got[max(at-120, 0):min(at+160, len(got))], wantText[max(at-120, 0):min(at+160, len(wantText))])
				}
			})
		})
	}
}

// probeHarness is a harness of the SDK case whose extension edits the payload and the headers of a provider request in place with each handler
// event_mutate_probe subscribes (one that returns and one that fails after its edit), or, when probes is not set, only observes the request with a handler that
// returns nothing and edits nothing, so the request is built through the same hook path.
func probeHarness(t *testing.T, tc harnessCase, probes bool) *harness {
	t.Helper()
	if !probes {
		h := openProbeHarness(t, tc)
		runConformanceCommandArgs(t, h, "event_probe_on", "before_provider_request")
		return h
	}
	return mutateHarness(t, tc, []string{"", "fail"}, []string{"", "fail"})
}

// mutateHarness is a harness of the SDK case whose extension subscribes, in this order, one event_mutate_probe handler of each mode to before_provider_request
// (payload modes) and to before_provider_headers (header modes).
func mutateHarness(t *testing.T, tc harnessCase, payload, headers []string) *harness {
	t.Helper()
	h := openProbeHarness(t, tc)
	for event, modes := range map[string][]string{"before_provider_request": payload, "before_provider_headers": headers} {
		for _, mode := range modes {
			runConformanceCommandArgs(t, h, "event_mutate_probe", strings.TrimSpace(event+" "+mode))
		}
	}
	return h
}

// openProbeHarness is the harness of the SDK case, shut down with the test.
func openProbeHarness(t *testing.T, tc harnessCase) *harness {
	t.Helper()
	h := tc.make(t)
	t.Cleanup(func() {
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	})
	return h
}

// inPlaceEdit is the value the probe handlers write; no other test uses it.
const inPlaceEdit = "in-place-edit"

// inPlacePayload is the payload the Runner row hands the handlers.
const inPlacePayload = `{"model":"m","messages":[{"role":"user","content":"x"}],"stream":true,"zeta":{"z":1,"a":2}}`

// inPlaceWantPayload is inPlacePayload after the first handler's edit and the failing handler's: each handler sets one member of the payload and of its zeta.
func inPlaceWantPayload() map[string]any {
	return map[string]any{
		"model": "m", "messages": []any{map[string]any{"role": "user", "content": "x"}}, "stream": true,
		"zeta":               map[string]any{"z": float64(1), "a": float64(2), "inPlace": inPlaceEdit, "inPlaceBeforeError": inPlaceEdit},
		"inPlace":            inPlaceEdit,
		"inPlaceBeforeError": inPlaceEdit,
	}
}

// inPlaceWantHeaders is the headers the Runner row hands the handlers, after each handler's one member.
func inPlaceWantHeaders(kept string) map[string]string {
	return map[string]string{"x-fixture-kept": kept, "x-pig-inPlace": inPlaceEdit, "x-pig-inPlaceBeforeError": inPlaceEdit}
}

// TestInPlaceProviderHookEditsMatchPi runs Pi's own ExtensionRunner (the compiled runner.ts the Node runtime ships) over two extensions that do what the probe
// handlers do, and requires what it returns to be what the rows above require of every SDK: the oracle of the rows.
func TestInPlaceProviderHookEditsMatchPi(t *testing.T) {
	t.Parallel()
	runner, err := filepath.Abs("../../coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-coding-agent/core/extensions/runner.js")
	if err != nil {
		t.Fatal(err)
	}
	const body = `
const { ExtensionRunner } = await import(input.runner);
const edit = "in-place-edit";
const handlerFor = (event, fail) => async (e) => {
	const key = fail ? "inPlaceBeforeError" : "inPlace";
	if (event === "before_provider_headers") e.headers["x-pig-" + key] = edit;
	else { e.payload[key] = edit; if (e.payload.zeta) e.payload.zeta[key] = edit; }
	if (fail) throw new Error("failed after the edit");
};
const handlers = new Map(["before_provider_request", "before_provider_headers"].map((event) => [event, [handlerFor(event, false), handlerFor(event, true)]]));
const errors = [];
const pi = new ExtensionRunner([{ path: "/probe.mjs", handlers }], {}, "/", undefined, undefined);
pi.onError?.((report) => errors.push(report.event));
const payload = await pi.emitBeforeProviderRequest(input.payload);
const headers = await pi.emitBeforeProviderHeaders({ "x-fixture-kept": input.kept });
return { payload, headers, errors };
`
	var got struct {
		Payload map[string]any
		Headers map[string]string
		Errors  []string
	}
	out := testenv.PiEvalJSON(t, body, map[string]any{"runner": "file://" + runner, "payload": mustJSONValue(t, inPlacePayload), "kept": "kept-header-value"})
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if want := inPlaceWantPayload(); !reflect.DeepEqual(got.Payload, want) {
		t.Errorf("Pi's payload after the handlers\n got %v\nwant %v", got.Payload, want)
	}
	if want := inPlaceWantHeaders("kept-header-value"); !reflect.DeepEqual(got.Headers, want) {
		t.Errorf("Pi's headers after the handlers\n got %v\nwant %v", got.Headers, want)
	}
	if want := []string{"before_provider_request", "before_provider_headers"}; !reflect.DeepEqual(got.Errors, want) {
		t.Errorf("the errors Pi's runner reported\n got %v\nwant %v", got.Errors, want)
	}
}

func mustJSONValue(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func keysOf(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	return keys
}

// recordedRequest is the one request a provider server received: its body as the bytes the provider wrote and its headers, and the JSON of the entries the
// session that sent it held when its run had ended.
type recordedRequest struct {
	body    []byte
	header  http.Header
	entries string
}

// newPiMessagesProbeProvider is the pi-messages provider over a recording server: its payload is a map, so its member order is the one Pi's object has.
func newPiMessagesProbeProvider(baseURL string) ai.Provider {
	return ai.NewPiMessagesProvider(ai.PiMessagesConfig{ProviderID: "radius", Model: "probe", APIKey: "fixture-key", BaseURL: baseURL})
}

// stamped matches the values of a request that no two runs of it share: the time of a message and the id of the session.
var stamped = regexp.MustCompile(`"(timestamp|sessionId)":(?:[0-9]+|"[^"]*")`)

// sendRecordedRequest sends one prompt through a real Session over the harness's Runner (working in cwd) and the provider newProvider builds on a recording
// server, and returns the request the server received.
func sendRecordedRequest(t *testing.T, h *harness, cwd string, newProvider func(baseURL string) ai.Provider) recordedRequest {
	t.Helper()
	var mu sync.Mutex
	var requests []recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		requests = append(requests, recordedRequest{body: body, header: r.Header.Clone()})
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.HasSuffix(r.URL.Path, "/messages") {
			_, _ = w.Write([]byte("data: {\"type\":\"start\"}\n\ndata: {\"type\":\"done\",\"reason\":\"stop\",\"usage\":{\"input\":1,\"output\":1,\"cacheRead\":0,\"cacheWrite\":0,\"totalTokens\":2,\"cost\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0,\"total\":0}}}\n\n"))
			return
		}
		_, _ = w.Write([]byte("data: {\"id\":\"test\",\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	provider := newProvider(server.URL)
	defer func() { _ = provider.Close() }()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	bridged, diagnostics := coding.BridgeNewRunnerTools(h.runner.Tools())
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: &ai.Model{ID: "probe", Provider: provider}, Runner: h.runner, Tools: bridged, SkipBuiltinTools: true, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range session.Events() {
		}
	}()
	_, sendErr := session.Send(context.Background(), "probe")
	// The entries the session holds when the run has ended, for a row that asks what an edit left in the saved transcript.
	entries := string(mustMarshal(t, session.Entries()))
	_ = session.Close()
	<-done
	if sendErr != nil {
		t.Fatal(sendErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("the server recorded %d requests, want 1", len(requests))
	}
	recorded := requests[0]
	recorded.entries = entries
	return recorded
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
