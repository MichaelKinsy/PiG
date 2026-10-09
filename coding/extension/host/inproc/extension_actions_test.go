package inproc_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi runner.ts bindCore copies the ExtensionActions the mode supplies (types.ts:2147-2163) into the runtime
// every extension context reads, so an action bound there is reachable from ctx without a ContextActions twin.
func TestBindCoreRoutesExtensionActionsToContexts(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	active := []string{"read"}
	var refreshed int
	var appended []string
	runner.BindCore(extension.ExtensionActions{
		GetAllTools:    func() []extension.ToolInfo { return []extension.ToolInfo{{Name: "read"}, {Name: "bash"}} },
		GetActiveTools: func() []string { return slices.Clone(active) },
		SetActiveTools: func(names []string) { active = slices.Clone(names) },
		RefreshTools:   func() error { refreshed++; return nil },
		AppendEntry:    func(customType string, data any) error { appended = append(appended, customType); return nil },
	}, extension.ContextActions{}, nil)
	ctx := extension.FromContext(runner.DispatchContext(t.Context()))
	if all := ctx.GetAllTools(); len(all) != 2 || all[1].Name != "bash" {
		t.Fatalf("GetAllTools = %+v", all)
	}
	ctx.SetActiveTools([]string{"bash", "read"})
	if got := ctx.GetActiveTools(); !slices.Equal(got, []string{"bash", "read"}) {
		t.Fatalf("GetActiveTools = %v", got)
	}
	if err := ctx.RefreshTools(); err != nil || refreshed != 1 {
		t.Fatalf("RefreshTools = %v, calls=%d", err, refreshed)
	}
	tc := runner.CreateToolContext(t.Context(), "call-1")
	if tc == nil {
		t.Fatal("no tool context")
	}
	if err := tc.AppendEntry("note", map[string]any{"k": 1}); err != nil || !slices.Equal(appended, []string{"note"}) {
		t.Fatalf("AppendEntry = %v, appended=%v", err, appended)
	}
}
