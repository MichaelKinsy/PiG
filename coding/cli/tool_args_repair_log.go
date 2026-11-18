package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pig local (FA-09): every tool-arguments repair appends one JSON line to <agentDir>/logs/tool-argument-repairs.jsonl
// so the repairs can be counted (wc -l, or jq by keys/day). Logging never fails the call.
func installToolArgumentsRepairLog() {
	dir, err := codingagent.ResolveAgentDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "logs", "tool-argument-repairs.jsonl")
	cwd, _ := os.Getwd()
	ai.SetToolArgumentsRepairHook(func(r ai.ToolArgumentsRepair) {
		appendToolArgumentsRepair(path, cwd, r)
	})
}

func appendToolArgumentsRepair(path, cwd string, r ai.ToolArgumentsRepair) {
	line, err := json.Marshal(struct {
		TS    string   `json:"ts"`
		PID   int      `json:"pid"`
		Cwd   string   `json:"cwd"`
		Kind  string   `json:"kind"`
		Count int      `json:"count"`
		Keys  []string `json:"keys"`
	}{time.Now().UTC().Format(time.RFC3339Nano), os.Getpid(), cwd, r.Kind, r.Count, r.Keys})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}
