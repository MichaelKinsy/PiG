package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's ctx.model is the live Session model and ctx.sessionManager.getSessionId() its id (runner.ts:906, agent-session.ts). The Node runtime answers both from the
// host's state snapshot (runtime.mjs applyState), which carries the whole model object and the session id, and the host pushes that snapshot before every dispatch, before
// the reply of a Session write and when the model or the Session changes (Host.hostStateChanged). Every SDK now answers them from the same snapshot, so a read costs no
// host round trip: the row counts the calls each SDK sends and fails an SDK that still asks the host, with the model, the id and the usage the host holds moving between
// sentinels no SDK defaults to. The context usage stays a host call in the SDKs (Pi computes it live from the branch, and a snapshot taken at a dispatch is stale after
// waitForIdle, compact or navigateTree and for a reader between requests), so the row asserts its value, not its call count.
func TestReplicatedReadsAreLocalAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("replica reads complete")
				}
			})
			state := &replicaHostState{model: replicaModels["a"], session: "replica-session-one", tokens: 777, window: 100000}
			state.bind(h)
			command, ok := findCommand(h.runner, "replica-probe")
			if !ok {
				t.Fatal("replica-probe command missing")
			}
			h.wire.on.Store(true)
			probe := func(arg string) (replicaReport, []string) {
				t.Helper()
				h.wire.drain()
				*h.notify = nil
				if err := command.Handler(h.runner.DispatchContext(t.Context()), arg); err != nil {
					t.Fatal(err)
				}
				if len(*h.notify) != 1 {
					t.Fatalf("notifications = %v", *h.notify)
				}
				var report replicaReport
				if err := json.Unmarshal([]byte(strings.TrimSuffix((*h.notify)[0], ":info")), &report); err != nil {
					t.Fatalf("report %q: %v", (*h.notify)[0], err)
				}
				return report, replicaReadCalls(h.wire.drain())
			}
			check := func(step string, got replicaReport, calls []string, model string, session string) {
				t.Helper()
				want := replicaReport{Model: replicaWant[model], SessionID: session, ManagerSessionID: session, Usage: state.usage(),
					SessionName: replicaOptional(replicaNameOf(session)), SessionFile: replicaOptional(replicaFileOf(session)), ManagerSessionName: replicaOptional(replicaNameOf(session)), ManagerSessionFile: replicaOptional(replicaFileOf(session))}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: report\n got  %+v\n want %+v", step, got, want)
				}
				if len(calls) != 0 {
					t.Errorf("%s: the SDK sent host calls %v for reads the state snapshot answers", step, calls)
				}
			}

			report, calls := probe("")
			check("snapshot", report, calls, "a", "replica-session-one")

			// A model switch the host makes, and a replaced Session (Session.ReplaceInner reports the same change).
			state.set(replicaModels["b"], "replica-session-two", 4242)
			h.bridge.HostStateChanged()
			report, calls = probe("")
			check("model switch and session replace", report, calls, "b", "replica-session-two")

			// Pi's setModel is synchronous: a read right after it sees the new model.
			report, calls = probe("set-model:prov-c/model-c")
			check("read after setModel", report, calls, "c", "replica-session-two")

			// No model: Pi's undefined.
			state.set("", "replica-session-two", 4242)
			report, calls = probe("")
			check("no model", report, calls, "", "replica-session-two")

			// The model is back and the Session is replaced once more.
			state.set(replicaModels["a"], "replica-session-three", 99)
			report, calls = probe("")
			check("model restored", report, calls, "a", "replica-session-three")

			// An in-memory Session has no file and no name: Pi's undefined, not an empty string.
			state.set(replicaModels["a"], "replica-session-memory", 99)
			report, calls = probe("")
			check("in-memory Session", report, calls, "a", "replica-session-memory")
		})
	}
}

// replicaReport is the replica-probe fixtures' JSON report.
type replicaReport struct {
	Model                                                            *replicaModelReport `json:"model"`
	SessionID                                                        string              `json:"sessionId"`
	ManagerSessionID                                                 string              `json:"managerSessionId"`
	SessionName, SessionFile, ManagerSessionName, ManagerSessionFile *string
	Usage                                                            *extension.ContextUsage `json:"usage"`
}

// replicaNameOf and replicaFileOf are the name and file the host holds for a session id: an id ending in "-memory" is an in-memory Session with neither (Pi's undefined).
func replicaNameOf(session string) string {
	if strings.HasSuffix(session, "-memory") {
		return ""
	}
	return "name of " + session
}

func replicaFileOf(session string) string {
	if strings.HasSuffix(session, "-memory") {
		return ""
	}
	return "/replica/" + session + ".jsonl"
}

func replicaOptional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

type replicaModelReport struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Provider            string  `json:"provider"`
	ContextWindow       int     `json:"contextWindow"`
	MaxOutputTokens     int     `json:"maxOutputTokens"`
	Reasoning           bool    `json:"reasoning"`
	InputCostPer1M      float64 `json:"inputCostPer1M"`
	OutputCostPer1M     float64 `json:"outputCostPer1M"`
	CacheReadCostPer1M  float64 `json:"cacheReadCostPer1M"`
	CacheWriteCostPer1M float64 `json:"cacheWriteCostPer1M"`
}

// replicaModels are the model objects the host holds, as the host encodes a catalog model (extension.ModelObject): every member the SDKs read, with values no SDK defaults to.
var replicaModels = map[string]string{
	"a": `{"id":"replica-a","name":"Replica Name A","provider":"replica-prov-a","api":"openai-completions","contextWindow":123456,"maxTokens":4321,"reasoning":true,"input":["text"],"cost":{"input":1.25,"output":2.5,"cacheRead":0.125,"cacheWrite":3.75}}`,
	"b": `{"id":"replica-b","name":"Replica Name B","provider":"replica-prov-b","api":"openai-completions","contextWindow":654321,"maxTokens":8642,"reasoning":false,"input":["text","image"],"cost":{"input":0.5,"output":1,"cacheRead":0.05,"cacheWrite":0.625}}`,
	"c": `{"id":"model-c","name":"Replica Name C","provider":"prov-c","api":"openai-completions","contextWindow":2048,"maxTokens":512,"reasoning":true,"input":["text"],"cost":{"input":7,"output":14,"cacheRead":0.7,"cacheWrite":8.75}}`,
}

// replicaWant is what each model reads as through the SDKs' typed model view.
var replicaWant = map[string]*replicaModelReport{
	"a": {ID: "replica-a", Name: "Replica Name A", Provider: "replica-prov-a", ContextWindow: 123456, MaxOutputTokens: 4321, Reasoning: true, InputCostPer1M: 1.25, OutputCostPer1M: 2.5, CacheReadCostPer1M: 0.125, CacheWriteCostPer1M: 3.75},
	"b": {ID: "replica-b", Name: "Replica Name B", Provider: "replica-prov-b", ContextWindow: 654321, MaxOutputTokens: 8642, Reasoning: false, InputCostPer1M: 0.5, OutputCostPer1M: 1, CacheReadCostPer1M: 0.05, CacheWriteCostPer1M: 0.625},
	"c": {ID: "model-c", Name: "Replica Name C", Provider: "prov-c", ContextWindow: 2048, MaxOutputTokens: 512, Reasoning: true, InputCostPer1M: 7, OutputCostPer1M: 14, CacheReadCostPer1M: 0.7, CacheWriteCostPer1M: 8.75},
	"":  nil,
}

// replicaHostState is the host Session the probe reads: its model, session id and context usage.
type replicaHostState struct {
	mu      sync.Mutex
	model   string
	session string
	tokens  int
	window  int
}

func (s *replicaHostState) set(model, session string, tokens int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.model, s.session, s.tokens = model, session, tokens
}

func (s *replicaHostState) usage() *extension.ContextUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	tokens := s.tokens
	percent := float64(tokens) * 100 / float64(s.window)
	return &extension.ContextUsage{Tokens: &tokens, ContextWindow: s.window, Percent: &percent}
}

func (s *replicaHostState) bind(h *harness) {
	h.bridge.SetHostAction("getModelInfo", func() json.RawMessage {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.model == "" {
			return nil
		}
		return json.RawMessage(s.model)
	})
	h.bridge.SetHostAction("getSessionID", func() string {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.session
	})
	h.bridge.SetHostAction("getSessionName", func() string {
		s.mu.Lock()
		defer s.mu.Unlock()
		return replicaNameOf(s.session)
	})
	h.bridge.SetHostAction("getSessionFile", func() string {
		s.mu.Lock()
		defer s.mu.Unlock()
		return replicaFileOf(s.session)
	})
	h.bridge.SetHostAction("getContextUsage", s.usage)
	// A read of the session manager asks the host for the id through sessionRead.
	h.bridge.SetHostAction("sessionRead", func(method string, _ json.RawMessage) (any, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch method {
		case "getSessionId":
			return s.session, nil
		case "getSessionName":
			return replicaOptional(replicaNameOf(s.session)), nil
		case "getSessionFile":
			return replicaOptional(replicaFileOf(s.session)), nil
		}
		return nil, fmt.Errorf("replica row: unexpected sessionRead %s", method)
	})
	h.bridge.SetHostAction("setModel", func(_ context.Context, model string) (bool, error) {
		for _, candidate := range replicaModels {
			var m struct{ Provider, ID string }
			if json.Unmarshal([]byte(candidate), &m) == nil && m.Provider+"/"+m.ID == model {
				s.mu.Lock()
				s.model = candidate
				s.mu.Unlock()
				return true, nil
			}
		}
		return false, nil
	})
}

// drain returns every call recorded since the last drain.
func (w *wireTap) drain() []wireCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	calls := w.calls
	w.calls = nil
	return calls
}

// replicaReadCalls names the calls that read what the state snapshot carries: getModelInfo, getSessionID, and sessionRead of getSessionId.
func replicaReadCalls(calls []wireCall) []string {
	var out []string
	for _, call := range calls {
		switch call.method {
		case "getModelInfo", "getSessionID", "getSessionName", "getSessionFile":
			out = append(out, call.method)
		case "sessionRead":
			var args struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(call.args, &args) == nil && (args.Method == "getSessionId" || args.Method == "getSessionName" || args.Method == "getSessionFile") {
				out = append(out, "sessionRead:"+args.Method)
			}
		}
	}
	return out
}
