package rpcclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Every RpcClient command method sends exactly the command body of its upstream namesake
// (modes/rpc/rpc-client.ts: abort :226, newSession :243, setModel :259, cycleModel :267-272,
// setThinkingLevel :287, setSteeringMode :310, setFollowUpMode :317, compact :324, setAutoCompaction :332,
// setAutoRetry :339, abortRetry :346, bash :353, abortBash :361, getSessionStats :368, exportHtml :376,
// switchSession :385, fork :394, getForkMessages :411, getEntries :419, getTree :427,
// getLastAssistantText :435, setSessionName :443, getMessages :450, getCommands :458). JSON.stringify drops an
// undefined member, so an omitted optional argument is absent from the line. The canned child answers every
// command with success and no data, so the decoded results are not asserted here (the result shapes have their own
// tests); the logged line is the observable effect of the call, and a method that sends nothing, the wrong type or
// a misnamed member fails it.
// mutation-checked: returning nil without sending from RpcClient.Abort, RpcClient.AbortBash, RpcClient.AbortRetry, RpcClient.SetAutoCompaction, RpcClient.SetAutoRetry, RpcClient.SetFollowUpMode, RpcClient.SetSteeringMode, RpcClient.SetSessionName, RpcClient.SetThinkingLevel fails it
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:226 (abort)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:361 (abortBash)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:346 (abortRetry)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:332 (setAutoCompaction)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:339 (setAutoRetry)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:317 (setFollowUpMode)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:310 (setSteeringMode)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:443 (setSessionName)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:287 (setThinkingLevel)
// packages/coding-agent/src/modes/rpc/rpc-client.ts:74 (RpcClient.start); packages/coding-agent/src/modes/rpc/rpc-client.ts:226 (RpcClient.abort); packages/coding-agent/src/modes/rpc/rpc-client.ts:243 (RpcClient.newSession); packages/coding-agent/src/modes/rpc/rpc-client.ts:251 (RpcClient.getState); packages/coding-agent/src/modes/rpc/rpc-client.ts:259 (RpcClient.setModel); packages/coding-agent/src/modes/rpc/rpc-client.ts:267 (RpcClient.cycleModel); packages/coding-agent/src/modes/rpc/rpc-client.ts:279 (RpcClient.getAvailableModels); packages/coding-agent/src/modes/rpc/rpc-client.ts:287 (RpcClient.setThinkingLevel); packages/coding-agent/src/modes/rpc/rpc-client.ts:294 (RpcClient.cycleThinkingLevel); packages/coding-agent/src/modes/rpc/rpc-client.ts:302 (RpcClient.getAvailableThinkingLevels); packages/coding-agent/src/modes/rpc/rpc-client.ts:310 (RpcClient.setSteeringMode); packages/coding-agent/src/modes/rpc/rpc-client.ts:317 (RpcClient.setFollowUpMode); packages/coding-agent/src/modes/rpc/rpc-client.ts:324 (RpcClient.compact); packages/coding-agent/src/modes/rpc/rpc-client.ts:332 (RpcClient.setAutoCompaction); packages/coding-agent/src/modes/rpc/rpc-client.ts:339 (RpcClient.setAutoRetry); packages/coding-agent/src/modes/rpc/rpc-client.ts:346 (RpcClient.abortRetry); packages/coding-agent/src/modes/rpc/rpc-client.ts:353 (RpcClient.bash); packages/coding-agent/src/modes/rpc/rpc-client.ts:361 (RpcClient.abortBash); packages/coding-agent/src/modes/rpc/rpc-client.ts:368 (RpcClient.getSessionStats); packages/coding-agent/src/modes/rpc/rpc-client.ts:376 (RpcClient.exportHtml); packages/coding-agent/src/modes/rpc/rpc-client.ts:385 (RpcClient.switchSession); packages/coding-agent/src/modes/rpc/rpc-client.ts:394 (RpcClient.fork); packages/coding-agent/src/modes/rpc/rpc-client.ts:411 (RpcClient.getForkMessages); packages/coding-agent/src/modes/rpc/rpc-client.ts:419 (RpcClient.getEntries); packages/coding-agent/src/modes/rpc/rpc-client.ts:427 (RpcClient.getTree); packages/coding-agent/src/modes/rpc/rpc-client.ts:435 (RpcClient.getLastAssistantText); packages/coding-agent/src/modes/rpc/rpc-client.ts:443 (RpcClient.setSessionName); packages/coding-agent/src/modes/rpc/rpc-client.ts:450 (RpcClient.getMessages); packages/coding-agent/src/modes/rpc/rpc-client.ts:458 (RpcClient.getCommands).
func TestRpcClientCommandMethodsSendTheirUpstreamCommandBodies(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name string
		call func(*RpcClient)
		want string
	}{
		{"abort", func(c *RpcClient) { _ = c.Abort() }, `{"type":"abort","id":"req_1"}`},
		{"newSession", func(c *RpcClient) { _, _ = c.NewSession(nil) }, `{"type":"new_session","id":"req_1"}`},
		{"newSession with parent", func(c *RpcClient) { _, _ = c.NewSession(str("/p.jsonl")) }, `{"type":"new_session","parentSession":"/p.jsonl","id":"req_1"}`},
		{"getState", func(c *RpcClient) { _, _ = c.GetState() }, `{"type":"get_state","id":"req_1"}`},
		{"setModel", func(c *RpcClient) { _, _ = c.SetModel("anthropic", "claude") }, `{"type":"set_model","provider":"anthropic","modelId":"claude","id":"req_1"}`},
		{"cycleModel", func(c *RpcClient) { _, _ = c.CycleModel() }, `{"type":"cycle_model","id":"req_1"}`},
		{"getAvailableModels", func(c *RpcClient) { _, _ = c.GetAvailableModels() }, `{"type":"get_available_models","id":"req_1"}`},
		{"setThinkingLevel", func(c *RpcClient) { _ = c.SetThinkingLevel("high") }, `{"type":"set_thinking_level","level":"high","id":"req_1"}`},
		{"cycleThinkingLevel", func(c *RpcClient) { _, _ = c.CycleThinkingLevel() }, `{"type":"cycle_thinking_level","id":"req_1"}`},
		{"getAvailableThinkingLevels", func(c *RpcClient) { _, _ = c.GetAvailableThinkingLevels() }, `{"type":"get_available_thinking_levels","id":"req_1"}`},
		{"setSteeringMode", func(c *RpcClient) { _ = c.SetSteeringMode("one-at-a-time") }, `{"type":"set_steering_mode","mode":"one-at-a-time","id":"req_1"}`},
		{"setFollowUpMode", func(c *RpcClient) { _ = c.SetFollowUpMode("all") }, `{"type":"set_follow_up_mode","mode":"all","id":"req_1"}`},
		{"compact", func(c *RpcClient) { _, _ = c.Compact(nil) }, `{"type":"compact","id":"req_1"}`},
		{"compact with instructions", func(c *RpcClient) { _, _ = c.Compact(str("keep names")) }, `{"type":"compact","customInstructions":"keep names","id":"req_1"}`},
		{"setAutoCompaction", func(c *RpcClient) { _ = c.SetAutoCompaction(false) }, `{"type":"set_auto_compaction","enabled":false,"id":"req_1"}`},
		{"setAutoRetry", func(c *RpcClient) { _ = c.SetAutoRetry(true) }, `{"type":"set_auto_retry","enabled":true,"id":"req_1"}`},
		{"abortRetry", func(c *RpcClient) { _ = c.AbortRetry() }, `{"type":"abort_retry","id":"req_1"}`},
		{"bash", func(c *RpcClient) { _, _ = c.Bash("echo hi") }, `{"type":"bash","command":"echo hi","id":"req_1"}`},
		{"abortBash", func(c *RpcClient) { _ = c.AbortBash() }, `{"type":"abort_bash","id":"req_1"}`},
		{"getSessionStats", func(c *RpcClient) { _, _ = c.GetSessionStats() }, `{"type":"get_session_stats","id":"req_1"}`},
		{"exportHtml", func(c *RpcClient) { _, _ = c.ExportHtml(nil) }, `{"type":"export_html","id":"req_1"}`},
		{"exportHtml with path", func(c *RpcClient) { _, _ = c.ExportHtml(str("/o.html")) }, `{"type":"export_html","outputPath":"/o.html","id":"req_1"}`},
		{"switchSession", func(c *RpcClient) { _, _ = c.SwitchSession("/s.jsonl") }, `{"type":"switch_session","sessionPath":"/s.jsonl","id":"req_1"}`},
		{"fork", func(c *RpcClient) { _, _ = c.Fork("e1") }, `{"type":"fork","entryId":"e1","id":"req_1"}`},
		{"getForkMessages", func(c *RpcClient) { _, _ = c.GetForkMessages() }, `{"type":"get_fork_messages","id":"req_1"}`},
		{"getEntries", func(c *RpcClient) { _, _ = c.GetEntries(nil) }, `{"type":"get_entries","id":"req_1"}`},
		{"getEntries since", func(c *RpcClient) { _, _ = c.GetEntries(str("e0")) }, `{"type":"get_entries","since":"e0","id":"req_1"}`},
		{"getTree", func(c *RpcClient) { _, _ = c.GetTree() }, `{"type":"get_tree","id":"req_1"}`},
		{"getLastAssistantText", func(c *RpcClient) { _, _ = c.GetLastAssistantText() }, `{"type":"get_last_assistant_text","id":"req_1"}`},
		{"setSessionName", func(c *RpcClient) { _ = c.SetSessionName("named") }, `{"type":"set_session_name","name":"named","id":"req_1"}`},
		{"getMessages", func(c *RpcClient) { _, _ = c.GetMessages() }, `{"type":"get_messages","id":"req_1"}`},
		{"getCommands", func(c *RpcClient) { _, _ = c.GetCommands() }, `{"type":"get_commands","id":"req_1"}`},
		{"getExtensions", func(c *RpcClient) { _, _ = c.GetExtensions() }, `{"type":"get_extensions","id":"req_1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, logPath := childClient(t, "canned")
			if err := client.Start(); err != nil {
				t.Fatal(err)
			}
			tc.call(client)
			if got := loggedCommands(t, logPath); !slices.Equal(got, []string{tc.want}) {
				t.Fatalf("sent %q, want %q", got, tc.want)
			}
		})
	}
}

// RpcClient.start (rpc-client.ts:74-100) spawns cliPath with ["--mode","rpc"], then "--provider <provider>" and
// "--model <model>" when set, then options.args, in cwd with env {...process.env, ...options.env}.
// packages/coding-agent/src/modes/rpc/rpc-client.ts:74 (RpcClient.start); packages/coding-agent/src/modes/rpc/rpc-client.ts:145 (RpcClient.stop).
func TestRpcClientStartLaunchesTheAgentWithItsOptions(t *testing.T) {
	cwd := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "launch.json")
	for _, tc := range []struct {
		name     string
		provider string
		model    string
		args     []string
		want     []string
	}{
		{"mode only", "", "", nil, []string{"--mode", "rpc"}},
		{"provider and model then args", "faux", "faux-model", []string{"--no-extensions", "--offline"}, []string{"--mode", "rpc", "--provider", "faux", "--model", "faux-model", "--no-extensions", "--offline"}},
		{"model alone", "", "m1", []string{"-x"}, []string{"--mode", "rpc", "--model", "m1", "-x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewRpcClient(RpcClientOptions{
				CliPath: os.Args[0], Cwd: cwd, Provider: tc.provider, Model: tc.model, Args: tc.args,
				Env: map[string]string{childModeEnv: "report-launch", "PIG_RPCCLIENT_TEST_LOG": logPath, "PIG_RPCCLIENT_TEST_MARK": "from-options"},
			})
			t.Cleanup(client.Stop)
			if err := client.Start(); err != nil {
				t.Fatal(err)
			}
			var report struct {
				Args []string `json:"args"`
				Cwd  string   `json:"cwd"`
				Env  string   `json:"env"`
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				data, err := os.ReadFile(logPath)
				if err == nil && json.Unmarshal(data, &report) == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("no launch report: %v", err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !slices.Equal(report.Args, tc.want) || report.Env != "from-options" {
				t.Fatalf("args = %q env = %q, want %q and from-options", report.Args, report.Env, tc.want)
			}
			if resolved, _ := filepath.EvalSymlinks(cwd); report.Cwd != cwd && report.Cwd != resolved {
				t.Fatalf("cwd = %q, want %q", report.Cwd, cwd)
			}
			client.Stop()
			_ = os.Remove(logPath)
		})
	}
}
