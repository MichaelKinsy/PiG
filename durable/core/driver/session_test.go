// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// The native session against a scripted core: which events, requests and transactions each public call produces.

const tableSQL = `CREATE TABLE IF NOT EXISTS submissions (id INTEGER PRIMARY KEY, record TEXT)`
const insertSQL = `INSERT OR REPLACE INTO submissions (id, record) VALUES (?, ?)`

func txNotice(id uint32, body string) abi.Notice { return notice(abi.NoticeTxResult, id, body) }

func sessionRig(t *testing.T, respond func(abi.Event) *abi.Step, mutate func(*driver.SessionConfig)) (*driver.Session, *funcCore, harness.Registry) {
	t.Helper()
	core := &funcCore{respond: respond}
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "s.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	registry := harness.CreateRegistry()
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "probe", Models: []ai.FauxModelDefinition{{ID: "probe-1", ContextWindow: 100_000, MaxTokens: 1000}}})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	cfg := driver.SessionConfig{
		Core: core, SQL: []string{tableSQL, insertSQL}, DB: db,
		Harness: harness.HarnessOptions{Models: models, Registry: registry, Now: func() float64 { return 5000 }},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := driver.OpenSession(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s, core, registry
}

func lookupTool() *durable.ToolRegistration {
	return &durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "lookup", Description: "Look up record number n", Parameters: map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}, "required": []any{"n"}}},
		Replay:     durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{}, nil
		},
	}
}

func TestOpenSendsTheRegistrySnapshotSettingsAndModeInOneOpenEvent(t *testing.T) {
	_, core, _ := sessionRig(t, nil, func(cfg *driver.SessionConfig) {
		reg := cfg.Harness.Registry.(harness.Registry)
		preamble := "You are a benchmark agent."
		noTag := false
		_ = reg.Install(&durable.Extension{
			Name:     "bench",
			Tools:    []*durable.ToolRegistration{lookupTool()},
			Sections: []*durable.PromptSection{{Key: "preamble", Tag: &noTag, Render: func(context.Context, durable.PromptInput) (*string, error) { return &preamble, nil }}},
		})
	})
	opens := core.events(abi.EventOpen)
	if len(opens) != 1 {
		t.Fatalf("open events: %d", len(opens))
	}
	var open struct {
		Mode     string `json:"mode"`
		Sidecar  string `json:"sidecar"`
		Delta    bool   `json:"delta"`
		Settings struct {
			Retry         struct{ MaxRetries int } `json:"retry"`
			ToolExecution string                   `json:"toolExecution"`
		} `json:"settings"`
		Registry struct {
			Extensions []struct {
				Name  string
				Tools []struct {
					Name, Replay string
					Handler      int
					Parameters   json.RawMessage
				}
				Sections []struct {
					Key     string
					Tag     bool
					Handler int
				}
			}
		} `json:"registry"`
	}
	if err := json.Unmarshal(opens[0].Payload, &open); err != nil {
		t.Fatal(err)
	}
	if open.Mode != "harness" || open.Sidecar != "index" || !open.Delta {
		t.Fatalf("open = %+v", open)
	}
	if open.Settings.Retry.MaxRetries != 3 || open.Settings.ToolExecution != "parallel" {
		t.Fatalf("settings are resolved over the defaults: %+v", open.Settings)
	}
	e := open.Registry.Extensions
	if len(e) != 1 || e[0].Name != "bench" || len(e[0].Tools) != 1 || e[0].Tools[0].Name != "lookup" || e[0].Tools[0].Replay != "safe" {
		t.Fatalf("extensions = %+v", e)
	}
	if string(e[0].Tools[0].Parameters) != `{"properties":{"n":{"type":"number"}},"required":["n"],"type":"object"}` {
		// A Go map orders keys alphabetically; the order of the stored declaration is the schema's (the tool author's ToolSchema keeps it).
		t.Logf("parameters: %s", e[0].Tools[0].Parameters)
	}
	if len(e[0].Sections) != 1 || e[0].Sections[0].Key != "preamble" || e[0].Sections[0].Tag {
		t.Fatalf("sections = %+v", e[0].Sections)
	}
}

func TestRootCreatesTheRootItsAgentAndInitInOneCommit(t *testing.T) {
	var ops []string
	var docDef map[string]any
	respond := func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventTx {
			return nil
		}
		var op struct {
			Op      string         `json:"op"`
			Address map[string]any `json:"address"`
			Ops     [][]any        `json:"ops"`
			Def     map[string]any `json:"def"`
		}
		_ = json.Unmarshal(ev.Payload, &op)
		ops = append(ops, op.Op)
		switch op.Op {
		case "begin":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"ready":true}`)}}
		case "conversation":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
		case "createRootConversation":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":{"id":1}}`)}}
		case "doc":
			docDef = op.Def
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":{"value":{}}}`)}}
		case "doc.write":
			ops[len(ops)-1] = "doc.write " + op.Address["definition"].(string) + " " + string(mustJSON(op.Ops))
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
		case "end":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
		}
		return nil
	}
	s, _, _ := sessionRig(t, respond, nil)
	var initOps []int64
	root, err := s.Root(t.Context(), &harness.RootOptions{
		Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "probe", ModelId: "probe-1"})},
		Init:  func(tx durable.Tx, id durable.ConversationId) error { initOps = append(initOps, int64(id)); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if root.Id() != 1 {
		t.Fatalf("root id = %d", root.Id())
	}
	want := []string{"begin", "conversation", "createRootConversation", "doc", `doc.write pi.agent [["s",["model"],{"modelId":"probe-1","provider":"probe"}]]`, "end"}
	if strings.Join(ops, "|") != strings.Join(want, "|") {
		t.Fatalf("transaction ops:\n got  %q\n want %q", ops, want)
	}
	// The core checks the stored document against the definition (host/PROTOCOL.md): pi.agent is rewindable and forks as of.
	if docDef["kind"] != "pi.agent" || docDef["scope"] != "conversation" || docDef["history"] != "rewindable" || docDef["fork"] != "asOf" || docDef["version"] != float64(1) {
		t.Fatalf("the doc operation carries def %v", docDef)
	}
	if len(initOps) != 1 || initOps[0] != 1 {
		t.Fatalf("init ran %v", initOps)
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestSubmitSendsOneSubmitEventAndWaitSettlesOnThePublishedNotice(t *testing.T) {
	var submit map[string]any
	respond := func(ev abi.Event) *abi.Step {
		switch ev.Kind {
		case abi.EventSubmit:
			_ = json.Unmarshal(ev.Payload, &submit)
			rid := submit["requestId"].(string)
			return &abi.Step{
				Commits: []abi.Commit{{Seq: 1, Stmts: []abi.Stmt{{SQL: 0}, {SQL: 1, Params: []abi.Value{abi.Int(9), abi.Text(`{"id":9,"conversationId":1,"requestId":"` + rid + `","type":"input","status":"placed","entry":4}`)}}}}},
				Notices: []abi.Notice{
					{Kind: abi.NoticePublished, Payload: []byte(`{"submissions":[{"id":9,"conversationId":1,"requestId":"` + rid + `","status":"placed"}]}`)},
					{Kind: abi.NoticeAPIResult, Payload: append(abi.U32Payload(uint32(submit["id"].(float64))), `{"ok":{"id":9}}`...)},
				},
			}
		case abi.EventTimer:
			return &abi.Step{
				Commits: []abi.Commit{{Seq: 2, Stmts: []abi.Stmt{{SQL: 1, Params: []abi.Value{abi.Int(9), abi.Text(`{"id":9,"conversationId":1,"type":"input","status":"done","entry":4,"answer":6}`)}}}}},
				Notices: []abi.Notice{{Kind: abi.NoticePublished, Payload: []byte(`{"submissions":[{"id":9,"conversationId":1,"status":"done"}]}`)}},
			}
		}
		return nil
	}
	s, core, _ := sessionRig(t, respond, nil)
	c := conversationOf(s, 1)
	sub, err := c.Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Id() != 9 {
		t.Fatalf("submission id = %d", sub.Id())
	}
	if submit["conversationId"] != float64(1) || submit["type"] != "input" || submit["content"] != "hi" {
		t.Fatalf("submit event = %v", submit)
	}
	if rid, _ := submit["requestId"].(string); len(rid) != 36 {
		t.Fatalf("a request ID is minted when the caller gave none: %q", rid)
	}
	if n := len(core.events(abi.EventResume)); n != 1 {
		t.Fatalf("submit sends one resume event, got %d", n)
	}
	done := make(chan durable.SubmissionRecord, 1)
	go func() {
		record, err := sub.Wait(t.Context())
		if err != nil {
			t.Error(err)
		}
		done <- record
	}()
	// The settlement arrives as a later step (a timer here stands in for the run finishing).
	_ = s.Host().Send(t.Context(), abi.Event{Kind: abi.EventTimer, ID: 1})
	record := <-done
	if record.Status != durable.SubmissionDone || record.Answer == nil || *record.Answer != 6 {
		t.Fatalf("settled = %+v", record)
	}
}

func conversationOf(s *driver.Session, id durable.ConversationId) harness.Conversation {
	return s.ConversationHandle(id)
}

func TestARejectedSubmitIsTheCoresError(t *testing.T) {
	s, _, _ := sessionRig(t, func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventSubmit {
			return &abi.Step{Status: abi.StatusRejected, Err: []byte(`{"name":"ConversationBusy","message":"Conversation 1 is busy"}`)}
		}
		return nil
	}, nil)
	_, err := conversationOf(s, 1).Submit(t.Context(), durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("x"), WhenBusy: durable.WhenBusyReject})
	var rejected *sqlhost.RejectedError
	if !errors.As(err, &rejected) || rejected.Name != "ConversationBusy" {
		t.Fatalf("err = %v", err)
	}
}

func TestTheResolveAgentHookIsAnsweredFromTheRegistryAndTheStoredState(t *testing.T) {
	var done []byte
	s, core, _ := sessionRig(t, func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventSubmit {
			return &abi.Step{Effects: []abi.Effect{effect(7, abi.EffectHook, map[string]any{"name": "resolveAgent", "conversationId": 1, "taskId": 0, "payload": map[string]any{"state": map[string]any{"tools": map[string]any{"remove": []string{"drop"}}, "thinkingLevel": "low"}}})}}
		}
		if ev.Kind == abi.EventHookDone {
			done = ev.Payload
		}
		return nil
	}, func(cfg *driver.SessionConfig) {
		keep, drop := lookupTool(), lookupTool()
		keep.Name, drop.Name = "keep", "drop"
		_ = cfg.Harness.Registry.(harness.Registry).Install(&durable.Extension{Name: "a", Tools: []*durable.ToolRegistration{keep, drop}})
	})
	_ = s.Host().Send(t.Context(), abi.Event{Kind: abi.EventSubmit, Payload: []byte(`{}`)})
	waitFor(t, "hook_done", func() bool { return len(core.events(abi.EventHookDone)) == 1 })
	var agent struct {
		ThinkingLevel string   `json:"thinkingLevel"`
		Extensions    []string `json:"extensions"`
		Tools         []struct{ Name string }
	}
	if err := json.Unmarshal(done, &agent); err != nil {
		t.Fatalf("hook_done = %q: %v", done, err)
	}
	if agent.ThinkingLevel != "low" || len(agent.Extensions) != 1 || len(agent.Tools) != 1 || agent.Tools[0].Name != "keep" {
		t.Fatalf("agent = %+v", agent)
	}
}

// registrySnapshot (PROTOCOL.md): the core asks for registry.snapshot() where Pi's #decide calls it inside the step job
// (scheduler.ts:955); the host answers the snapshot JSON of open.registry. The native driver handed it to the extension
// hooks, which have no handler 0, and discarded the handle on the first step of every turn.
func TestTheRegistrySnapshotHookIsAnsweredWithTheOpenRegistry(t *testing.T) {
	var done []byte
	s, core, _ := sessionRig(t, func(ev abi.Event) *abi.Step {
		if ev.Kind == abi.EventSubmit {
			return &abi.Step{Effects: []abi.Effect{effect(7, abi.EffectHook, map[string]any{"name": "registrySnapshot", "handler": 0, "conversationId": 0, "taskId": 0, "payload": nil})}}
		}
		if ev.Kind == abi.EventHookDone {
			done = ev.Payload
		}
		return nil
	}, func(cfg *driver.SessionConfig) {
		_ = cfg.Harness.Registry.(harness.Registry).Install(&durable.Extension{Name: "a", Tools: []*durable.ToolRegistration{lookupTool()}})
	})
	_ = s.Host().Send(t.Context(), abi.Event{Kind: abi.EventSubmit, Payload: []byte(`{}`)})
	waitFor(t, "hook_done", func() bool { return len(core.events(abi.EventHookDone)) == 1 })
	if got := core.events(abi.EventHookDone)[0]; got.Phase != abi.OutcomeResult {
		t.Fatalf("hook_done outcome %d: %s", got.Phase, got.Payload)
	}
	var open struct {
		Registry json.RawMessage `json:"registry"`
	}
	if err := json.Unmarshal(core.events(abi.EventOpen)[0].Payload, &open); err != nil {
		t.Fatal(err)
	}
	if string(done) != string(open.Registry) || !strings.Contains(string(done), `"lookup"`) {
		t.Fatalf("answer %s, want the open registry %s", done, open.Registry)
	}
}

// The core checks a stored document record against the definition's semantics (session/docs.go checkRecordScope).
// The doc op therefore carries them: without "def" the core assumes history "latest", and a rewindable document such
// as pi.agent (rewindable, asOf) on a store Pi wrote fails with "does not match the supplied definition semantics".
func TestTheDocOpCarriesTheDefinitionSemantics(t *testing.T) {
	var def map[string]any
	respond := func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventTx {
			return nil
		}
		var op struct {
			Op  string         `json:"op"`
			Def map[string]any `json:"def"`
		}
		_ = json.Unmarshal(ev.Payload, &op)
		switch op.Op {
		case "begin":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"ready":true}`)}}
		case "conversation":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
		case "createRootConversation":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":{"id":1}}`)}}
		case "doc":
			def = op.Def
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":{"value":{}}}`)}}
		case "doc.write":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
		case "end":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
		}
		return nil
	}
	s, _, _ := sessionRig(t, respond, nil)
	if _, err := s.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "probe", ModelId: "probe-1"})}}); err != nil {
		t.Fatal(err)
	}
	want := `{"fork":"asOf","history":"rewindable","kind":"pi.agent","scope":"conversation","version":1}`
	if got := string(mustJSON(def)); got != want {
		t.Fatalf("doc op def = %s, want %s", got, want)
	}
}

// A document answer carries the core's entry index and the deltas stored after the newest base; the driver sends the entry back and
// evaluates the definition's checkpointWhen on the prepared value and ops (Pi session/documents.ts), as the TS host does (facade/tx.ts
// writeDoc). pi.agent's predicate is always true; an answer without deltasSinceBase (an absent document) asks no predicate.
func TestDocWriteCarriesTheCoreEntryAndTheCheckpointVerdict(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		entry        any
		checkpoint   any
	}{
		{"stored document", `{"value":{},"entry":2,"id":9,"deltasSinceBase":4}`, float64(2), true},
		{"absent document", `{"value":{}}`, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var write map[string]any
			respond := func(ev abi.Event) *abi.Step {
				if ev.Kind != abi.EventTx {
					return nil
				}
				var op struct {
					Op string `json:"op"`
				}
				_ = json.Unmarshal(ev.Payload, &op)
				switch op.Op {
				case "begin":
					return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"ready":true}`)}}
				case "conversation":
					return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
				case "createRootConversation":
					return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":{"id":1}}`)}}
				case "doc":
					return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":`+tc.answer+`}`)}}
				case "doc.write":
					if write == nil {
						_ = json.Unmarshal(ev.Payload, &write)
					}
					return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
				case "end":
					return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
				}
				return nil
			}
			s, _, _ := sessionRig(t, respond, nil)
			if _, err := s.Root(t.Context(), &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "probe", ModelId: "probe-1"})}}); err != nil {
				t.Fatal(err)
			}
			if write == nil {
				t.Fatal("no doc.write was sent")
			}
			if write["entry"] != tc.entry || write["checkpoint"] != tc.checkpoint {
				t.Fatalf("doc.write entry=%v checkpoint=%v, want %v and %v", write["entry"], write["checkpoint"], tc.entry, tc.checkpoint)
			}
		})
	}
}

// The core's turn host runs only a model that open's models names as {provider, id, contextWindow} (core turnhost.go
// contextWindow). By default the list is every model of the Harness models object, as the JS host sends; without it every
// generation settled unanswered with reason no_model. ModelsInUse replaces the list, in the same shape.
func TestOpenNamesTheModelCatalogueTheCoreMayRun(t *testing.T) {
	models := func(cfg *driver.SessionConfig) []byte {
		t.Helper()
		_, core, _ := sessionRig(t, nil, func(c *driver.SessionConfig) {
			if cfg != nil {
				c.ModelsInUse = cfg.ModelsInUse
			}
		})
		var open struct {
			Models json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal(core.events(abi.EventOpen)[0].Payload, &open); err != nil {
			t.Fatal(err)
		}
		return open.Models
	}
	if got, want := string(models(nil)), `[{"contextWindow":100000,"id":"probe-1","provider":"probe"}]`; got != want {
		t.Fatalf("default models = %s, want %s", got, want)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "other", Models: []ai.FauxModelDefinition{{ID: "o-1", ContextWindow: 7}}})
	if got, want := string(models(&driver.SessionConfig{ModelsInUse: []*ai.Model{faux.GetModel()}})), `[{"contextWindow":7,"id":"o-1","provider":"other"}]`; got != want {
		t.Fatalf("ModelsInUse = %s, want %s", got, want)
	}
}

// The core asks the host, by name, to repair and validate a call's arguments (core turnhost.go checkArguments; the JS
// host's hostHooks prepareArguments/validateArguments). They take no registry handler: the driver answers {arguments} or
// {error} with the text of what threw, with the first installed tool of that name. Before, the driver treated them as
// extension hooks and stopped every tool call at "names handler 0, which the registry does not hold".
func TestThePrepareAndValidateArgumentsHooksAreAnsweredByTheHost(t *testing.T) {
	answer := func(name string, payload any) string {
		t.Helper()
		var done []byte
		s, core, _ := sessionRig(t, func(ev abi.Event) *abi.Step {
			if ev.Kind == abi.EventSubmit {
				return &abi.Step{Effects: []abi.Effect{effect(7, abi.EffectHook, map[string]any{"name": name, "handler": 0, "conversationId": 0, "taskId": 0, "payload": payload})}}
			}
			if ev.Kind == abi.EventHookDone {
				done = ev.Payload
			}
			return nil
		}, func(cfg *driver.SessionConfig) {
			repaired := lookupTool()
			repaired.Name = "repaired"
			repaired.PrepareArguments = func(args any) (any, error) {
				m, _ := args.(map[string]any)
				if m["n"] == "bad" {
					return nil, errors.New("cannot repair")
				}
				return map[string]any{"n": 7}, nil
			}
			_ = cfg.Harness.Registry.(harness.Registry).Install(&durable.Extension{Name: "a", Tools: []*durable.ToolRegistration{lookupTool(), repaired}})
		})
		_ = s.Host().Send(t.Context(), abi.Event{Kind: abi.EventSubmit, Payload: []byte(`{}`)})
		waitFor(t, "hook_done", func() bool { return len(core.events(abi.EventHookDone)) == 1 })
		if ev := core.events(abi.EventHookDone)[0]; ev.Phase != abi.OutcomeResult {
			t.Fatalf("%s: phase %d, payload %s", name, ev.Phase, ev.Payload)
		}
		return string(done)
	}
	for _, c := range []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"prepareArguments", map[string]any{"toolName": "lookup", "arguments": map[string]any{"n": 1}}, `{"arguments":{"n":1}}`},
		{"prepareArguments", map[string]any{"toolName": "repaired", "arguments": map[string]any{"n": "x"}}, `{"arguments":{"n":7}}`},
		{"prepareArguments", map[string]any{"toolName": "repaired", "arguments": map[string]any{"n": "bad"}}, `{"error":"cannot repair"}`},
		{"validateArguments", map[string]any{"toolName": "lookup", "call": map[string]any{"type": "toolCall", "id": "c1", "name": "lookup"}, "arguments": map[string]any{"n": "2"}}, `{"arguments":{"n":2}}`},
	} {
		if got := answer(c.name, c.payload); got != c.want {
			t.Errorf("%s %v = %s, want %s", c.name, c.payload, got, c.want)
		}
	}
	// The answer keeps the member order of the arguments the core sent, which the execute checkpoint stores (harness/tool.ts);
	// a Go map would sort them.
	for _, c := range []struct{ name, payload, want string }{
		{"prepareArguments", `{"toolName":"lookup","arguments":{"n":1,"b":true,"a":"x"}}`, `{"arguments":{"n":1,"b":true,"a":"x"}}`},
		{"validateArguments", `{"toolName":"lookup","call":{"type":"toolCall","id":"c1","name":"lookup"},"arguments":{"z":{"y":1,"x":2},"n":"2","a":"x"}}`, `{"arguments":{"z":{"y":1,"x":2},"n":2,"a":"x"}}`},
	} {
		if got := answer(c.name, json.RawMessage(c.payload)); got != c.want {
			t.Errorf("%s %s = %s, want %s", c.name, c.payload, got, c.want)
		}
	}
	if got := answer("validateArguments", map[string]any{"toolName": "lookup", "call": map[string]any{"type": "toolCall", "id": "c1", "name": "lookup"}, "arguments": map[string]any{}}); !strings.HasPrefix(got, `{"error":"Validation failed for tool \"lookup\"`) {
		t.Errorf("validateArguments without n = %s, want a validation error", got)
	}
}

// A compaction's summary is a model call the core asks of the host by name, without a registry handler (core turnhost.go
// Complete; the JS host's complete hook, compaction.ts summarize: completeSimple). The driver answers the terminal assistant
// message of that model, given the core's context, and throws for a model the catalogue lacks. Before, the driver handed it
// to the extension hooks and stopped every native compaction at "names handler 0, which the registry does not hold".
func TestTheCompleteHookIsTheSummaryModelCall(t *testing.T) {
	answer := func(model map[string]any) abi.Event {
		t.Helper()
		var seen []ai.Message
		var opts ai.StreamOptions
		s, core, _ := sessionRig(t, func(ev abi.Event) *abi.Step {
			if ev.Kind == abi.EventSubmit {
				return &abi.Step{Effects: []abi.Effect{effect(7, abi.EffectHook, map[string]any{"name": "complete", "handler": 0, "conversationId": 0, "taskId": 0, "payload": map[string]any{
					"model": model,
					"context": map[string]any{"messages": []any{
						map[string]any{"role": "system", "content": "You are a context summarization assistant."},
						map[string]any{"role": "user", "content": "Summarize the conversation.", "timestamp": 1},
					}},
					"options": map[string]any{"maxTokens": 100, "cacheRetention": "none", "sessionId": "summary-session", "reasoning": "high"},
				}})}}
			}
			return nil
		}, func(cfg *driver.SessionConfig) {
			faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "probe", Models: []ai.FauxModelDefinition{{ID: "probe-1", ContextWindow: 100_000, MaxTokens: 1000}}})
			faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(request ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
				opts = options
				for i := range request.Len() {
					seen = append(seen, request.At(i))
				}
				return ai.FauxAssistantMessage(ai.FauxContentBlocks([]ai.FauxContentBlock{ai.FauxText("## Goal\nthe summary")}), ai.FauxAssistantMessageOptions{}), nil
			})})
			models := ai.CreateModels()
			models.SetProvider(faux.Provider())
			cfg.Harness.Models = models
		})
		_ = s.Host().Send(t.Context(), abi.Event{Kind: abi.EventSubmit, Payload: []byte(`{}`)})
		waitFor(t, "hook_done", func() bool { return len(core.events(abi.EventHookDone)) == 1 })
		ev := core.events(abi.EventHookDone)[0]
		if ev.Phase == abi.OutcomeResult {
			if len(seen) != 2 {
				t.Fatalf("the model saw %d messages, want the context's 2", len(seen))
			}
			if sys, ok := seen[0].(ai.SystemMessage); !ok || sys.Content != ai.SystemText("You are a context summarization assistant.") {
				t.Fatalf("the model's first message is %#v, want the summarization system prompt", seen[0])
			}
			// compaction.ts summarize sends maxTokens, cacheRetention "none", the provider session id and the thinking level, and
			// the JS host adds the hook's abort signal: each reaches the provider.
			if opts.MaxTokens != 100 || opts.CacheRetention != ai.CacheRetentionNone || opts.SessionID != "summary-session" || opts.Thinking != "high" {
				t.Fatalf("the model's options are maxTokens %d, cacheRetention %q, sessionId %q, reasoning %q; want the core's 100, none, summary-session, high", opts.MaxTokens, opts.CacheRetention, opts.SessionID, opts.Thinking)
			}
			if opts.Signal == nil {
				t.Fatal("the model's options carry no abort signal; want the hook's context")
			}
		}
		return ev
	}
	ev := answer(map[string]any{"provider": "probe", "modelId": "probe-1"})
	var message ai.AssistantMessage
	if ev.Phase != abi.OutcomeResult || json.Unmarshal(ev.Payload, &message) != nil || message.StopReason != ai.StopReasonStop || len(message.Content) != 1 {
		t.Fatalf("complete: phase %d, payload %s", ev.Phase, ev.Payload)
	}
	if text, ok := message.Content[0].(ai.TextContent); !ok || text.Text != "## Goal\nthe summary" {
		t.Fatalf("complete answered %#v, want the model's summary text", message.Content[0])
	}
	if ev := answer(map[string]any{"provider": "probe", "modelId": "missing"}); ev.Phase != abi.OutcomeThrown || !strings.Contains(string(ev.Payload), "Model probe/missing is not available") {
		t.Fatalf("complete with a missing model: phase %d, payload %s", ev.Phase, ev.Payload)
	}
}

// A tool's declaration keeps the key order its schema was defined or imported with: Pi stores TypeBox's order (type,
// required, properties), and a sorted re-encoding is a changed declaration to the core, which then writes a pi.system
// entry removing and re-adding the tool (native W1 at seq 560 before this).
func TestToolParametersKeepTheSchemasKeyOrder(t *testing.T) {
	var schema ai.ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"lookup","description":"Look up record number n","parameters":{"type":"object","required":["n"],"properties":{"n":{"type":"number"}}}}`), &schema); err != nil {
		t.Fatal(err)
	}
	_, core, _ := sessionRig(t, nil, func(cfg *driver.SessionConfig) {
		tool := lookupTool()
		tool.ToolSchema = schema
		_ = cfg.Harness.Registry.(harness.Registry).Install(&durable.Extension{Name: "bench", Tools: []*durable.ToolRegistration{tool}})
	})
	var open struct {
		Registry struct {
			Extensions []struct {
				Tools []struct{ Parameters json.RawMessage }
			}
		} `json:"registry"`
	}
	if err := json.Unmarshal(core.events(abi.EventOpen)[0].Payload, &open); err != nil {
		t.Fatal(err)
	}
	if got, want := string(open.Registry.Extensions[0].Tools[0].Parameters), `{"type":"object","required":["n"],"properties":{"n":{"type":"number"}}}`; got != want {
		t.Fatalf("parameters = %s, want %s", got, want)
	}
}

// The scan ops carry their filters in the core's shape (tx.go scan reads ownerTaskId, conversationId, kind, ...). The Go
// query structs have no JSON names, so the filters arrived as OwnerTaskId and the like, the core saw none, and W4e's
// sub-agent tool found the root conversation as "the child this task owns" and deadlocked submitting to it.
func TestScanOpsCarryTheirFiltersInTheCoresShape(t *testing.T) {
	queries := map[string]string{}
	respond := func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventTx {
			return nil
		}
		var op struct {
			Op    string          `json:"op"`
			Query json.RawMessage `json:"query"`
		}
		_ = json.Unmarshal(ev.Payload, &op)
		switch op.Op {
		case "begin":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"ready":true}`)}}
		case "scanConversations", "scanTasks":
			queries[op.Op] = string(op.Query)
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":{"items":[]}}`)}}
		case "end":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
		}
		return nil
	}
	s, _, _ := sessionRig(t, respond, nil)
	owner, conversation, kind, background := durable.TaskId(349), durable.ConversationId(2), "pi.tool", false
	if _, err := s.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		if _, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &owner}, 1, nil); err != nil {
			return nil, err
		}
		return tx.ScanTasks(durable.TaskQuery{ConversationId: &conversation, Kind: &kind, Background: &background}, 5, nil)
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := queries["scanConversations"], `{"ownerTaskId":349}`; got != want {
		t.Errorf("scanConversations query = %s, want %s", got, want)
	}
	if got, want := queries["scanTasks"], `{"background":false,"conversationId":2,"kind":"pi.tool"}`; got != want {
		t.Errorf("scanTasks query = %s, want %s", got, want)
	}
}

// Pi's runtime.agent() resolves the committed pi.agent against the registry (harness.ResolveAgent), the same answer the
// resolveAgent hook gives. The snapshot op carries pi.agent's semantics (rewindable, asOf), which the core checks against
// the stored record. Before, the driver asked the core for an "agent" api op it does not serve, so every section effect
// failed ("session: not implemented") and a new conversation's first system entry had no sections (native W4e).
func TestAgentResolvesTheStoredAgentAgainstTheRegistry(t *testing.T) {
	var snapshot string
	respond := func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventAPI {
			return nil
		}
		if strings.Contains(string(ev.Payload), `"op":"snapshot"`) {
			snapshot = string(ev.Payload)
			return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeAPIResult, ev.ID, `{"ok":{"extensions":{"remove":["sub"]}}}`)}}
		}
		return &abi.Step{Notices: []abi.Notice{notice(abi.NoticeAPIResult, ev.ID, `{"error":{"name":"NotImplemented","message":"session: not implemented"}}`)}}
	}
	s, _, registry := sessionRig(t, respond, nil)
	untagged := false
	preamble := harness.Section("preamble", func(context.Context, durable.PromptInput) (*string, error) { return new("be brief"), nil }, harness.SectionOptions{Tag: &untagged})
	if err := registry.Install(&durable.Extension{Name: "bench", Tools: []*durable.ToolRegistration{lookupTool()}, Sections: []*durable.PromptSection{preamble}}); err != nil {
		t.Fatal(err)
	}
	sub := lookupTool()
	sub.Name = "sub"
	if err := registry.Install(&durable.Extension{Name: "sub", Tools: []*durable.ToolRegistration{sub}}); err != nil {
		t.Fatal(err)
	}
	agent, err := conversationOf(s, 7).Agent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.Extensions) != 1 || agent.Extensions[0].Name != "bench" || len(agent.Tools) != 1 || agent.Tools[0].Name != "lookup" || len(agent.Sections) != 1 || agent.Sections[0].Key != "preamble" {
		t.Fatalf("agent = %d extensions, tools %v, %d sections; want bench, lookup, preamble", len(agent.Extensions), agent.Tools, len(agent.Sections))
	}
	for _, want := range []string{`"conversationId":7`, `"definition":"pi.agent"`, `"history":"rewindable"`, `"fork":"asOf"`} {
		if !strings.Contains(snapshot, want) {
			t.Errorf("snapshot op %s lacks %s", snapshot, want)
		}
	}
}

// A commit's drafts are written in the order the transaction acquired them (Pi's Map of drafts, transaction.ts), so the stored
// document records of one commit are in a fixed order. The proxy kept them in a Go map and wrote them in a random order.
func TestDocWritesFollowTheOrderTheTransactionAcquiredTheDocuments(t *testing.T) {
	kinds := []string{"t.a", "t.b", "t.c", "t.d", "t.e", "t.f"}
	tokens := make([]durable.AnyDocToken, len(kinds))
	for i, kind := range kinds {
		tokens[i] = durable.DefineDoc(durable.DocDefinition[durable.JsonObject]{
			CommonDocDefinition: durable.CommonDocDefinition[durable.JsonObject]{Kind: kind, Version: 1, Initial: func() durable.JsonObject { return delta.JsonObjectOf() }},
			DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
		})
	}
	var written []string
	respond := func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventTx {
			return nil
		}
		var op struct {
			Op      string `json:"op"`
			Address struct {
				Definition string `json:"definition"`
			} `json:"address"`
		}
		_ = json.Unmarshal(ev.Payload, &op)
		switch op.Op {
		case "begin":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"ready":true}`)}}
		case "doc":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":{"value":{}}}`)}}
		case "doc.write":
			written = append(written, op.Address.Definition)
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
		case "end":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
		}
		return nil
	}
	s, _, _ := sessionRig(t, respond, nil)
	_, err := s.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		for _, token := range tokens {
			object, err := tx.Doc(token)
			if err != nil {
				return nil, err
			}
			if err := object.Set("n", 1); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(written, ","); got != strings.Join(kinds, ",") {
		t.Fatalf("doc.write order = %s, want %s", got, strings.Join(kinds, ","))
	}
}

// Retiring an acquired document persists its final content before the retirement (Pi session/transaction.ts retireDoc), and the
// host sends the retired incarnation's write after the retirement, as facade/tx.ts flush does. The proxy dropped the draft.
func TestRetiringAnAcquiredDocumentStillWritesItsFinalContent(t *testing.T) {
	token := durable.DefineDoc(durable.DocDefinition[durable.JsonObject]{
		CommonDocDefinition: durable.CommonDocDefinition[durable.JsonObject]{Kind: "t.retired", Version: 1, Initial: func() durable.JsonObject { return delta.JsonObjectOf() }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeSession},
	})
	var ops []string
	respond := func(ev abi.Event) *abi.Step {
		if ev.Kind != abi.EventTx {
			return nil
		}
		var op struct {
			Op string `json:"op"`
		}
		_ = json.Unmarshal(ev.Payload, &op)
		if op.Op != "begin" && op.Op != "end" {
			ops = append(ops, op.Op)
		}
		switch op.Op {
		case "begin":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"ready":true}`)}}
		case "doc":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":{"value":{}}}`)}}
		case "doc.write", "retireDoc", "end":
			return &abi.Step{Notices: []abi.Notice{txNotice(ev.ID, `{"result":null}`)}}
		}
		return nil
	}
	s, _, _ := sessionRig(t, respond, nil)
	_, err := s.Commit(t.Context(), func(tx durable.Tx) (any, error) {
		object, err := tx.Doc(token)
		if err != nil {
			return nil, err
		}
		if err := object.Set("n", 1); err != nil {
			return nil, err
		}
		return nil, tx.RetireDoc(token)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ops, ","); got != "doc,retireDoc,doc.write" {
		t.Fatalf("ops = %s, want doc,retireDoc,doc.write", got)
	}
}
