package tools

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type borrowedSchemaTool interface {
	Schema() ai.ToolSchema
	BorrowedSchema() ai.ToolSchema
}

func borrowableSchemaTools() map[string]borrowedSchemaTool {
	return map[string]borrowedSchemaTool{
		"read": &ReadTool{}, "write": &WriteTool{}, "edit": &EditTool{}, "bash": &BashTool{}, "powershell": &PowerShellTool{},
		"grep": &GrepTool{}, "find": &FindTool{}, "ls": &LsTool{},
	}
}

// TestBorrowedSchemaIsTheSchema: the borrowed schema of every built-in tool equals its schema, and borrowing allocates
// nothing for the parameters.
func TestBorrowedSchemaIsTheSchema(t *testing.T) {
	for name, tool := range borrowableSchemaTools() {
		if got, want := tool.BorrowedSchema(), tool.Schema(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: borrowed schema %#v, schema %#v", name, got, want)
		}
		owned := testing.AllocsPerRun(20, func() { _ = tool.Schema() })
		borrowed := testing.AllocsPerRun(20, func() { _ = tool.BorrowedSchema() })
		if borrowed >= owned/2 {
			t.Errorf("%s: %v allocations to borrow the schema, %v to copy it: the parameters are copied", name, borrowed, owned)
		}
	}
}

// TestSchemaStaysPrivateWhenBorrowed: Schema still returns a map of its own, so a holder that edits it changes neither the cached
// literal nor the schema the next caller, borrowing or not, reads.
func TestSchemaStaysPrivateWhenBorrowed(t *testing.T) {
	for name, tool := range borrowableSchemaTools() {
		before, err := json.Marshal(tool.BorrowedSchema())
		if err != nil {
			t.Fatal(err)
		}
		schema := tool.Schema()
		schema.Parameters["type"] = "edited"
		delete(schema.Parameters, "properties")
		for _, again := range []ai.ToolSchema{tool.Schema(), tool.BorrowedSchema()} {
			after, err := json.Marshal(again)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Errorf("%s: an edit of Schema().Parameters reached a later schema:\n got  %s\n want %s", name, after, before)
			}
		}
	}
}
