package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestToolArgumentsRepairIsLogged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "tool-argument-repairs.jsonl")
	ai.SetToolArgumentsRepairHook(func(r ai.ToolArgumentsRepair) { appendToolArgumentsRepair(path, "/w", r) })
	t.Cleanup(func() { ai.SetToolArgumentsRepairHook(nil) })
	for i := 0; i < 2; i++ {
		if call := ai.ParseToolCallArguments(`{"path": "a", "edits": [{"oldText">x", "newText">y"}]}`); call.ArgumentsError != "" {
			t.Fatal(call.ArgumentsError)
		}
	}
	ai.ParseToolCallArguments(`{"path": "a"}`) // valid: not logged
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d: %s", len(lines), data)
	}
	var rec struct {
		Kind  string
		Count int
		Keys  []string
		Cwd   string
	}
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil || rec.Kind != "tag-arrow" || rec.Count != 2 || rec.Cwd != "/w" || strings.Join(rec.Keys, ",") != "oldText,newText" {
		t.Fatalf("record %+v err %v", rec, err)
	}
}
