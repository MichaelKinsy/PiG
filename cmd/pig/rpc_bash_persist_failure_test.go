//go:build unix

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi rpc-mode.ts returns executeBash/recordBashResult persistence failures as failed Bash responses, including user_bash result overrides.
func TestRPCBashPersistenceFailureRejectsResponse(t *testing.T) {
	t.Parallel()
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprint(override), func(t *testing.T) {
			dir := t.TempDir()
			sessionPath := filepath.Join(dir, "session.jsonl")
			header := fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":\"seeded\",\"timestamp\":\"2026-09-01T00:00:00.000Z\",\"cwd\":%q}\n", dir)
			if err := os.WriteFile(sessionPath, []byte(header), 0o600); err != nil {
				t.Fatal(err)
			}
			fixture := filepath.Join(dir, "fail-persistence.mjs")
			source := fmt.Sprintf(`import {renameSync,mkdirSync} from "node:fs";
export default function(pi) {
 pi.on("user_bash", () => {
  renameSync(%q, %q);
  mkdirSync(%q);
  if (%t) return {result: {output: "override", exitCode: 0, cancelled: false, truncated: false}};
 });
}`, sessionPath, sessionPath+".saved", sessionPath, override)
			if err := os.WriteFile(fixture, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			p := startRPCProcessAt(t, dir, []string{"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_HOME=" + t.TempDir()}, "--model", "test-faux/faux-1", "--session", sessionPath, "-e", fixture)
			p.sendJSON(map[string]any{"id": "seed", "type": "prompt", "message": "hello"})
			p.await("seed session persistence", func(record rpcRecord) bool { return record["type"] == "agent_settled" })
			if _, err := os.Stat(sessionPath); err != nil {
				t.Fatal(err)
			}
			p.sendJSON(map[string]any{"id": "bash", "type": "bash", "command": "printf output"})
			p.await("failed Bash response", func(record rpcRecord) bool {
				if record["type"] != "response" || record["id"] != "bash" {
					return false
				}
				message, _ := record["error"].(string)
				if record["success"] != false || !strings.Contains(message, "directory") {
					t.Errorf("response=%v; want failed persistence response", record)
				}
				return true
			})
			p.closeAndWait("after failed Bash persistence")
		})
	}
}
