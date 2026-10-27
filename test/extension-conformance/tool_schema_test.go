package extensionconformance

import (
	"testing"
)

// Pi loader.ts:273-285 throws before inserting an invalid schema, so a factory may catch the error and continue registering other capabilities.
func TestToolSchemaRejectionAcrossSDKs(t *testing.T) {
	for _, tc := range allHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			for _, tool := range h.runner.Tools() {
				if tool.Definition.Name == "schema-invalid" {
					t.Fatal("invalid declaration survived caught registration error")
				}
			}
			cmd, ok := findCommand(h.runner, "schema-probe")
			if !ok {
				t.Fatal("schema-probe missing")
			}
			h.ui.ClearRecorded()
			if err := cmd.Handler(h.runner.DispatchContext(t.Context()), ""); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return len(h.ui.Recorded()) > 0 })
			if got := h.ui.Recorded(); len(got) != 1 || got[0] != "schema-rejected:true:info" {
				t.Fatalf("record=%q", got)
			}
		})
	}
}
