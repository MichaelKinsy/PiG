package factoryload_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
)

// RegisterTools registers a batch as registerTool would register each tool: a tool registerTool rejects is left out with
// registerTool's error at its index, and the others are registered in order.
func TestRegisterToolsRejectsOnlyTheToolRegisterToolWouldReject(t *testing.T) {
	var errs []error
	ext, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		batch, ok := pi.(interface {
			RegisterTools([]extension.ToolDefinition) []error
		})
		if !ok {
			t.Fatal("the factory API has no RegisterTools")
		}
		errs = batch.RegisterTools([]extension.ToolDefinition{
			{Name: "a", Parameters: []byte(`{"type":"object"}`)},
			{Name: "b", Parameters: []byte(`[]`)},
			{Name: "c", Parameters: []byte(`{"type":"object"}`)},
		})
		return nil
	}, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "<v>")
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 3 || errs[0] != nil || errs[2] != nil || errs[1] == nil ||
		errs[1].Error() != `Tool "b" registered by extension "<v>" must define an object parameter schema.` {
		t.Fatalf("errors = %v, want only b's registerTool error at index 1", errs)
	}
	if got := ext.RegisteredToolNames(); !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("registered tools = %v, want [a c]", got)
	}
}
