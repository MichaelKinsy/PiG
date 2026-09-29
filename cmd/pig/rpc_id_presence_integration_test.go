package main

import "testing"

// Correlation belongs to the common RPC envelope, not to Bash updates alone. Exercise immediate, queued, catalog, and rejected commands through the real process.
func TestRPCResponseIDsAcrossCommandPaths(t *testing.T) {
	process := startRPCProcessAt(t, t.TempDir(), []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + t.TempDir()}, "--model", "test-faux/faux-1", "--no-session")
	for _, id := range []*string{nil, new(""), new("request-1")} {
		for _, command := range []map[string]any{
			{"type": "get_state"},
			{"type": "get_commands"},
			{"type": "abort_retry"},
			{"type": "steer", "message": "queued steering"},
			{"type": "follow_up", "message": "queued follow-up"},
			{"type": "clear_queue"},
			{"type": "does_not_exist"},
			{"type": "set_model", "provider": false},
		} {
			if id != nil {
				command["id"] = *id
			}
			process.sendJSON(command)
			process.await("correlated response", func(record rpcRecord) bool {
				if record["type"] != "response" || record["command"] != command["type"] {
					return false
				}
				value, present := record["id"]
				if present != (id != nil) || (id != nil && value != *id) {
					t.Errorf("command=%v response id=%v present=%v", command, value, present)
				}
				wantSuccess := command["type"] != "does_not_exist" && command["type"] != "set_model"
				if record["success"] != wantSuccess {
					t.Errorf("unexpected command result: %v", record)
				}
				return true
			})
		}
	}
	process.closeAndWait("after command ID checks")
}
