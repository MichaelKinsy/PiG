package main

import (
	"strings"
	"testing"
)

// rpc-mode.ts awaits executeBash after Session subscribers publish every correlated output update.
func TestRPCBashUpdatesPrecedeResponse(t *testing.T) {
	process := startRPCProcessAt(t, t.TempDir(), []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + t.TempDir()}, "--model", "test-faux/faux-1", "--no-session")
	for _, id := range []*string{new("bash-1"), nil, new("")} {
		command := map[string]any{"type": "bash", "command": "printf 'hello world'"}
		if id != nil {
			command["id"] = *id
		}
		process.sendJSON(command)
		var output strings.Builder
		process.await("bash response after output", func(record rpcRecord) bool {
			if record["type"] == "bash_execution_update" {
				value, present := record["id"]
				if present != (id != nil) || (id != nil && value != *id) {
					t.Errorf("event id=%v present=%v requested=%v", value, present, id)
				}
				delta, ok := record["delta"].(string)
				if !ok {
					t.Errorf("invalid delta=%v", record["delta"])
				}
				output.WriteString(delta)
			}
			if record["type"] != "response" || record["command"] != "bash" {
				return false
			}
			value, present := record["id"]
			if present != (id != nil) || (id != nil && value != *id) {
				t.Errorf("response id=%v present=%v requested=%v", value, present, command["id"])
			}
			if record["success"] != true {
				t.Errorf("response=%v", record)
			}
			if output.String() != "hello world" {
				t.Errorf("output before response=%q", output.String())
			}
			return true
		})
	}
	process.closeAndWait("after correlated bash updates")
}
