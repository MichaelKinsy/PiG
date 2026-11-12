package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// borrowingTool builds its schema anew on every call, as a tool that computes it per call does, and counts which of the two
// accessors the loop used.
type borrowingTool struct {
	fakeTool
	build    func() ai.ToolSchema
	owned    int
	borrowed int
}

func (t *borrowingTool) Schema() ai.ToolSchema         { t.owned++; return t.build() }
func (t *borrowingTool) BorrowedSchema() ai.ToolSchema { t.borrowed++; return t.build() }

func borrowedParameters(description string) map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": description}}, "required": []any{"path"}}
}

func newBorrowingTool(name string, description *string) *borrowingTool {
	return &borrowingTool{name: name, build: func() ai.ToolSchema {
		return ai.ToolSchema{Name: name, Description: "a tool", Parameters: borrowedParameters(*description)}
	}}
}

func changesOf(messages []AgentMessage) (added, removed []string) {
	for _, message := range messages {
		if message.System == nil {
			continue
		}
		for _, tool := range message.System.ToolsAdded {
			added = append(added, tool.Name)
		}
		for _, tool := range message.System.ToolsRemoved {
			removed = append(removed, tool.Name)
		}
	}
	return added, removed
}

// TestDeclareToolChangesComparesBorrowedSchemasByContent: the loop reads a borrowing tool's schema through BorrowedSchema, and a
// tool that rebuilds an equal schema on every call is not re-declared while one whose schema changed is (a changed definition is
// a removal and an addition, transcript.ts getToolStateChanges).
func TestDeclareToolChangesComparesBorrowedSchemasByContent(t *testing.T) {
	description := "the path"
	tool := newBorrowingTool("lookup", &description)
	tools := []AgentTool{tool}
	committed := declareToolChanges(tools, nil, nil)
	if added, _ := changesOf(committed); !reflect.DeepEqual(added, []string{"lookup"}) {
		t.Fatalf("first declaration added %v", added)
	}
	tool.owned, tool.borrowed = 0, 0
	if got := declareToolChanges(tools, committed, nil); len(got) != 0 {
		t.Fatalf("a rebuilt equal schema was declared again: %#v", got)
	}
	if tool.owned != 0 || tool.borrowed != 1 {
		t.Fatalf("Schema called %d times and BorrowedSchema %d times; the comparison should borrow once", tool.owned, tool.borrowed)
	}
	description = "the path, changed"
	added, removed := changesOf(declareToolChanges(tools, committed, nil))
	if !reflect.DeepEqual(added, []string{"lookup"}) || !reflect.DeepEqual(removed, []string{"lookup"}) {
		t.Fatalf("a changed schema was added %v and removed %v, want one removal and one addition of lookup", added, removed)
	}
}

// TestDeclaredToolIsNotTheBorrowedSchema: the declaration the loop adds is a copy the transcript owns, so a tool that returns one
// shared parameter map (a built-in's cached one) is never changed through the transcript, and a later change of the shared map in
// place, by an in-process holder, is still seen as a changed definition, as before.
func TestDeclaredToolIsNotTheBorrowedSchema(t *testing.T) {
	for name, borrowing := range map[string]bool{"borrowing tool": true, "plain tool": false} {
		t.Run(name, func(t *testing.T) {
			shared := borrowedParameters("the path")
			schema := ai.ToolSchema{Name: "shared", Description: "a tool", Parameters: shared}
			var tool AgentTool = &samplingTool{name: "shared", schema: schema}
			if borrowing {
				tool = &borrowingTool{name: "shared", build: func() ai.ToolSchema { return schema }}
			}
			tools := []AgentTool{tool}
			committed := declareToolChanges(tools, nil, nil)
			declared := committed[0].System.ToolsAdded[0]
			declared.Parameters["type"] = "edited through the transcript"
			if shared["type"] != "object" {
				t.Fatal("the declaration aliases the tool's parameters")
			}
			declared.Parameters["type"] = "object"
			if got := declareToolChanges(tools, committed, nil); len(got) != 0 {
				t.Fatalf("an unchanged shared schema was declared again: %#v", got)
			}
			shared["properties"].(map[string]any)["path"].(map[string]any)["description"] = "edited in place by a holder"
			added, removed := changesOf(declareToolChanges(tools, committed, nil))
			if !reflect.DeepEqual(added, []string{"shared"}) || !reflect.DeepEqual(removed, []string{"shared"}) {
				t.Fatalf("an in-place edit of the tool's parameters was added %v and removed %v, want a re-declaration", added, removed)
			}
			encoded, _ := json.Marshal(committed[0].System.ToolsAdded[0])
			if want := `"description":"the path"`; !json.Valid(encoded) || !containsString(string(encoded), want) {
				t.Fatalf("the committed declaration changed with the tool: %s", encoded)
			}
		})
	}
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
