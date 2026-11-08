package powershell

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/test/p2/powershell.test.ts: the plugin's per-agent choice (Pi Mobile's offerPowerShell, per agent
// location): Durable's deferred `powershell` tool is offered to an agent by its own tool selection, leaving its other
// choices alone.

// agentAfter applies change to a stored agent state (JSON text) as a commit does, and reads it back.
func agentAfter(t *testing.T, stored string, change func(draft *delta.Object)) (state map[string]any, offered bool) {
	t.Helper()
	session := p2test.OpenHarness(t)
	if _, err := session.Root(p2test.Background, nil); err != nil {
		t.Fatal(err)
	}
	decoded, err := chordjson.Decode([]byte(stored))
	if err != nil {
		t.Fatal(err)
	}
	read := func() *delta.JsonObject {
		value, err := session.SnapshotErased(p2test.Background, harness.AgentDoc, durable.ROOT_CONVERSATION_ID)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if _, err := session.Commit(p2test.Background, func(tx durable.Tx) (any, error) {
		draft, err := durable.TxDoc[harness.AgentState](tx, harness.AgentDoc, durable.ROOT_CONVERSATION_ID)
		if err != nil {
			return nil, err
		}
		for key, value := range decoded.(*delta.JsonObject).All() {
			if err := draft.Set(key, value); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Commit(p2test.Background, func(tx durable.Tx) (any, error) {
		draft, err := durable.TxDoc[harness.AgentState](tx, harness.AgentDoc, durable.ROOT_CONVERSATION_ID)
		if err != nil {
			return nil, err
		}
		change(draft)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	encoded, err := chordjson.Marshal(read())
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonInto(encoded, &state); err != nil {
		t.Fatal(err)
	}
	typed, err := durable.FromJsonValue[harness.AgentState](read())
	if err != nil {
		t.Fatal(err)
	}
	return state, offersPowerShell(&typed)
}

func TestPowerShellIsOfferedByAnAgentsOwnToolSelectionOtherChoicesUntouched(t *testing.T) {
	set := func(on bool) func(*delta.Object) {
		return func(draft *delta.Object) {
			if err := setPowerShell(draft, on); err != nil {
				t.Fatal(err)
			}
		}
	}
	check := func(what string, stored string, change func(*delta.Object), want string, wantOffered bool) {
		t.Helper()
		got, offered := agentAfter(t, stored, change)
		var expected map[string]any
		if err := jsonInto([]byte(want), &expected); err != nil {
			t.Fatal(err)
		}
		// The document's own bookkeeping keys are not the agent's choices.
		for key := range got {
			if key != "tools" && key != "modelTools" {
				delete(got, key)
			}
		}
		if !reflect.DeepEqual(got, expected) || offered != wantOffered {
			t.Errorf("%s: state %v offered %v, want %v offered %v", what, got, offered, expected, wantOffered)
		}
	}
	// The main agent: every tool enabled, none loaded.
	check("main, on", `{}`, set(true), `{"modelTools":{"add":["powershell"]}}`, true)
	check("main, off again", `{"modelTools":{"add":["powershell"]}}`, set(false), `{"modelTools":{}}`, false)
	// A subagent: its chosen tools and the team tool it loads.
	subagent := `{"tools":["bash","team"],"modelTools":{"add":["team"]}}`
	check("subagent, on", subagent, set(true), `{"tools":["bash","team","powershell"],"modelTools":{"add":["team","powershell"]}}`, true)
	check("subagent, off again", `{"tools":["bash","team","powershell"],"modelTools":{"add":["team","powershell"]}}`, set(false), subagent, false)
	// Explicit arrays and removals.
	check("listed", `{"tools":{"remove":["powershell"]},"modelTools":["read"]}`, set(true), `{"tools":{"remove":[]},"modelTools":["read","powershell"]}`, true)
}

func jsonInto(data []byte, into any) error { return json.Unmarshal(data, into) }
