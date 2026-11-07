package sdk

import (
	"encoding/json"
	"strings"
	"testing"
)

// The host's state snapshots carry the whole model object and the session id, and the Node runtime answers ctx.model and
// ctx.sessionManager.getSessionId() from them (runtime.mjs applyState), so a read costs no host round trip. These rows count the calls a
// handler sends: a read that still asks the host fails them.

const replicaModelA = `{"id":"model-a","name":"Model A Name","provider":"prov-a","contextWindow":128000,"maxTokens":4096,"reasoning":true,"cost":{"input":1.5,"output":6,"cacheRead":0.15,"cacheWrite":1.875}}`

type replicaReads struct {
	model      *ModelInfo
	modelErr   error
	sessionID  string
	idErr      error
	managerID  string
	managerErr error
}

func newReplicaProbe(t *testing.T, ready *readyMsg, edit func(*Extension, *Context)) (*mockHost, *replicaProbe, chan error) {
	t.Helper()
	ext := New("replica-reads")
	probe := &replicaProbe{ext: ext}
	ext.Command("read", "read", func(ctx Context, _ string) error {
		if edit != nil {
			edit(ext, &ctx)
		}
		probe.reads.model, probe.reads.modelErr = ctx.GetModelInfo()
		probe.reads.sessionID, probe.reads.idErr = ctx.GetSessionID()
		probe.reads.managerID, probe.reads.managerErr = ctx.SessionManager().GetSessionID()
		return nil
	})
	host, _, done := surfaceHost(t, ext, ready)
	probe.host = host
	return host, probe, done
}

type replicaProbe struct {
	ext   *Extension
	host  *mockHost
	reads replicaReads
}

// run sends the read command and answers the host calls it makes from answers (method -> result), returning the calls' methods.
func (p *replicaProbe) run(t *testing.T, answers map[string]string) []string {
	t.Helper()
	p.reads = replicaReads{}
	calls, resp := runSurfaceCommand(t, p.host, "read", func(call *callMsg) *callResultMsg {
		key := call.Method
		if key == "sessionRead" {
			var args struct {
				Method string `json:"method"`
			}
			_ = json.Unmarshal(call.Args, &args)
			key += ":" + args.Method
		}
		if reply, ok := answers[key]; ok {
			return &callResultMsg{Result: json.RawMessage(reply)}
		}
		return &callResultMsg{Error: &errorInfo{Code: "unexpected", Message: "unexpected host call " + key}}
	})
	if resp.Error != nil {
		t.Fatalf("command failed: %+v", resp.Error)
	}
	methods := make([]string, 0, len(calls))
	for _, call := range calls {
		methods = append(methods, call.Method)
	}
	return methods
}

func TestModelObjectAndSessionIDAreReadFromTheStateSnapshots(t *testing.T) {
	ready := &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"model":` + replicaModelA + `,"session":{"sessionId":"session-one","sessionName":""}}`)}
	_, probe, done := newReplicaProbe(t, ready, nil)
	defer surfaceShutdown(t, probe.host, done)

	check := func(step, wantID, wantName string, wantWindow int, wantSession string) {
		t.Helper()
		if calls := probe.run(t, nil); len(calls) != 0 {
			t.Fatalf("%s: the reads sent host calls %v, want none", step, calls)
		}
		r := probe.reads
		if r.modelErr != nil || r.idErr != nil || r.managerErr != nil {
			t.Fatalf("%s: errors %v %v %v", step, r.modelErr, r.idErr, r.managerErr)
		}
		if wantID == "" {
			if r.model != nil {
				t.Errorf("%s: model = %+v, want nil", step, r.model)
			}
		} else if r.model == nil || r.model.ID != wantID || r.model.Name != wantName || r.model.ContextWindow != wantWindow {
			t.Errorf("%s: model = %+v, want id %q name %q window %d", step, r.model, wantID, wantName, wantWindow)
		}
		if r.sessionID != wantSession || r.managerID != wantSession {
			t.Errorf("%s: session id = %q, manager %q, want %q", step, r.sessionID, r.managerID, wantSession)
		}
	}

	// The ready state, through the one path every snapshot takes.
	check("ready", "model-a", "Model A Name", 128000, "session-one")
	r := probe.reads
	if r.model.Provider != "prov-a" || r.model.MaxOutputTokens != 4096 || !r.model.Reasoning || r.model.InputCostPer1M != 1.5 || r.model.OutputCostPer1M != 6 || r.model.CacheReadCostPer1M != 0.15 || r.model.CacheWriteCostPer1M != 1.875 {
		t.Errorf("the whole model object is replicated, got %+v", r.model)
	}

	// A model switch and a replaced Session.
	sendState(t, probe.host, `{"model":{"id":"model-b","name":"Model B Name","provider":{"id":"prov-b"},"contextWindow":1000},"session":{"sessionId":"session-two","sessionName":"n"}}`)
	check("switch and replace", "model-b", "Model B Name", 1000, "session-two")
	if probe.reads.model.Provider != "prov-b" {
		t.Errorf("provider = %q, want prov-b", probe.reads.model.Provider)
	}

	// An absent member keeps the replica.
	sendState(t, probe.host, `{"isIdle":true}`)
	check("absent members", "model-b", "Model B Name", 1000, "session-two")

	// The host omits the session id only when it is empty, so a Session snapshot without one clears it.
	sendState(t, probe.host, `{"session":{"sessionName":""}}`)
	check("empty session id", "model-b", "Model B Name", 1000, "")

	// Null is no model.
	sendState(t, probe.host, `{"model":null,"session":{"sessionId":"session-three"}}`)
	check("null model", "", "", 0, "session-three")

	// A repeated model object is a no-op, and a new one after it replaces it.
	sendState(t, probe.host, `{"model":`+replicaModelA+`}`)
	sendState(t, probe.host, `{"model":`+replicaModelA+`}`)
	check("model back", "model-a", "Model A Name", 128000, "session-three")
}

// The replica hands out a private copy: a caller that edits what it read must not change the next read.
func TestReplicatedModelInfoIsPrivateToTheCaller(t *testing.T) {
	ready := &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"model":{"id":"m","name":"Limited","provider":"p","inputLimits":{"image":{"maxBytes":10}}}}`)}
	ext := New("replica-private")
	var first, second *ModelInfo
	ext.Command("read", "read", func(ctx Context, _ string) error {
		var err error
		if first, err = ctx.GetModelInfo(); err != nil {
			return err
		}
		first.Name = "edited"
		first.InputLimits["image"] = "edited"
		second, err = ctx.GetModelInfo()
		return err
	})
	host, _, done := surfaceHost(t, ext, ready)
	defer surfaceShutdown(t, host, done)
	if calls, resp := runSurfaceCommand(t, host, "read", func(*callMsg) *callResultMsg { return &callResultMsg{} }); len(calls) != 0 || resp.Error != nil {
		t.Fatalf("calls %v, error %+v", calls, resp.Error)
	}
	if second == nil || second.Name != "Limited" {
		t.Fatalf("second read = %+v, want the replicated name", second)
	}
	if limit, _ := second.InputLimits["image"].(map[string]any); limit["maxBytes"] != float64(10) {
		t.Fatalf("second read limits = %v, an edit of the first read leaked into the replica", second.InputLimits)
	}
}

// Before the first snapshot the value is unknown, not absent: a context made while the factory loads asks the host, as it always did.
func TestReadsBeforeTheFirstSnapshotAskTheHost(t *testing.T) {
	_, probe, done := newReplicaProbe(t, nil, nil)
	defer surfaceShutdown(t, probe.host, done)
	calls := probe.run(t, map[string]string{
		"getModelInfo": replicaModelA, "getSessionID": `{"sessionId":"from-host"}`, "sessionRead:getSessionId": `"manager-from-host"`,
	})
	if got, want := strings.Join(calls, ","), "getModelInfo,getSessionID,sessionRead"; got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
	r := probe.reads
	if r.model == nil || r.model.ID != "model-a" || r.sessionID != "from-host" || r.managerID != "manager-from-host" {
		t.Fatalf("reads = %+v", r)
	}
}

// A replacement context belongs to another Session, which this process does not replicate: it asks the host.
func TestReplacementContextReadsAskTheHost(t *testing.T) {
	ready := &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"model":` + replicaModelA + `,"session":{"sessionId":"session-one"}}`)}
	_, probe, done := newReplicaProbe(t, ready, func(_ *Extension, ctx *Context) { ctx.replacement = &replacementState{} })
	defer surfaceShutdown(t, probe.host, done)
	calls := probe.run(t, map[string]string{
		"getModelInfo": `{"id":"replacement-model","name":"R","provider":"p"}`, "getSessionID": `{"sessionId":"replacement-session"}`, "sessionRead:getSessionId": `"replacement-manager"`,
	})
	if got, want := strings.Join(calls, ","), "getModelInfo,getSessionID,sessionRead"; got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
	if r := probe.reads; r.model == nil || r.model.ID != "replacement-model" || r.sessionID != "replacement-session" || r.managerID != "replacement-manager" {
		t.Fatalf("reads = %+v", r)
	}
}

// A runtime the host replaced or reloaded answered every call with its stale message; the local read returns the same error.
func TestReplicatedReadsOfAStaleContextReturnTheStaleMessage(t *testing.T) {
	ready := &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"model":` + replicaModelA + `,"session":{"sessionId":"session-one"}}`)}
	_, probe, done := newReplicaProbe(t, ready, func(ext *Extension, _ *Context) {
		message := "This extension ctx is stale after session replacement or reload."
		ext.stale.Store(&message)
	})
	defer surfaceShutdown(t, probe.host, done)
	if calls := probe.run(t, nil); len(calls) != 0 {
		t.Fatalf("calls = %v, want none", calls)
	}
	r := probe.reads
	for name, err := range map[string]error{"GetModelInfo": r.modelErr, "GetSessionID": r.idErr, "SessionManager.GetSessionID": r.managerErr} {
		if err == nil || !strings.Contains(err.Error(), "stale after session replacement") {
			t.Errorf("%s error = %v, want the stale message", name, err)
		}
	}
}

// A model object that does not decode keeps failing as the host-reply decode did.
func TestReplicatedModelThatDoesNotDecodeIsAnError(t *testing.T) {
	ready := &readyMsg{Cwd: "/tmp", Width: 80, State: json.RawMessage(`{"model":{"id":5},"session":{"sessionId":"s"}}`)}
	_, probe, done := newReplicaProbe(t, ready, nil)
	defer surfaceShutdown(t, probe.host, done)
	if calls := probe.run(t, nil); len(calls) != 0 {
		t.Fatalf("calls = %v, want none", calls)
	}
	if err := probe.reads.modelErr; err == nil || !strings.Contains(err.Error(), "host reply to getModelInfo") {
		t.Fatalf("error = %v, want the decode error of the host reply", err)
	}
	if probe.reads.model != nil {
		t.Fatalf("model = %+v, want nil", probe.reads.model)
	}
}
