package docs_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/docs"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

// addTools (agent.ts): an array gets the names it lacks, a {remove} filter loses them, unset tools are left alone.
func TestAddToolsMatchesAgentTs(t *testing.T) {
	for _, tc := range []struct{ name, tools, want string }{
		{"array gets the missing names in order", `["read"]`, `{"tools":["read","bash","edit"],"modelTools":{"add":["bash","edit"]}}`},
		{"remove filter loses the names", `{"remove":["bash","grep"]}`, `{"tools":{"remove":["grep"]},"modelTools":{"add":["bash","edit"]}}`},
		{"a filter without them is untouched", `{"remove":["grep"]}`, `{"tools":{"remove":["grep"]},"modelTools":{"add":["bash","edit"]}}`},
		{"unset tools write nothing to tools", ``, `{"modelTools":{"add":["bash","edit"]}}`},
	} {
		mem := sessiontest.New()
		s := session.New(mem, mem.NextID(), func() float64 { return 1 })
		var got string
		if _, err := s.Commit(func(tx *session.Tx) error {
			root, err := tx.CreateRootConversation()
			if err != nil {
				return err
			}
			id, _ := root.Get("id")
			conv := session.ID(id.(float64))
			agent, err := tx.Doc(docs.AgentDef, session.DocArgs{Owner: conv})
			if err != nil {
				return err
			}
			if tc.tools != "" {
				v, _ := jv.ParseString(`{"tools":` + tc.tools + `}`)
				tools, _ := v.(*jv.Object).Get("tools")
				if err := agent.Set("tools", tools); err != nil {
					return err
				}
			}
			if err := docs.AddTools(tx, conv, []any{"bash", "edit"}); err != nil {
				return err
			}
			got = jv.Stringify(agent.Snapshot())
			return nil
		}, session.TxScope{}); err != nil {
			t.Fatal(err)
		}
		want := tc.want
		if tc.name == "array gets the missing names in order" {
			want = `{"tools":["read","bash","edit"],"modelTools":{"add":["bash","edit"]}}`
		}
		if got != want {
			t.Errorf("%s: %s, want %s", tc.name, got, want)
		}
	}
}

// Pi 42a3497d agent.ts addTools applies to `tools` and `modelTools`, each by the same rule.
func TestAddToolsExtendsModelToolsAsWell(t *testing.T) {
	for _, tc := range []struct{ name, state, want string }{
		{"both arrays", `{"tools":["read"],"modelTools":["read"]}`, `{"tools":["read","bash","edit"],"modelTools":["read","bash","edit"]}`},
		{"only modelTools set", `{"modelTools":["read","bash"]}`, `{"modelTools":["read","bash","edit"]}`},
		{"modelTools remove loses the names and gains the add", `{"modelTools":{"remove":["bash","grep"]}}`, `{"modelTools":{"remove":["grep"],"add":["bash","edit"]}}`},
		{"a remove emptied by the load is dropped", `{"modelTools":{"remove":["bash"]}}`, `{"modelTools":{"add":["bash","edit"]}}`},
		{"the add keeps its order and takes each name once", `{"modelTools":{"add":["edit","grep"]}}`, `{"modelTools":{"add":["edit","grep","bash"]}}`},
		{"unset modelTools gets the add, tools stay as they are", `{"tools":["read"]}`, `{"tools":["read","bash","edit"],"modelTools":{"add":["bash","edit"]}}`},
	} {
		mem := sessiontest.New()
		s := session.New(mem, mem.NextID(), func() float64 { return 1 })
		var got string
		if _, err := s.Commit(func(tx *session.Tx) error {
			root, err := tx.CreateRootConversation()
			if err != nil {
				return err
			}
			id, _ := root.Get("id")
			conv := session.ID(id.(float64))
			agent, err := tx.Doc(docs.AgentDef, session.DocArgs{Owner: conv})
			if err != nil {
				return err
			}
			v, _ := jv.ParseString(tc.state)
			object := v.(*jv.Object)
			for _, key := range object.Keys() {
				value, _ := object.Get(key)
				if err := agent.Set(key, value); err != nil {
					return err
				}
			}
			if err := docs.AddTools(tx, conv, []any{"bash", "edit"}); err != nil {
				return err
			}
			got = jv.Stringify(agent.Snapshot())
			return nil
		}, session.TxScope{}); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}
