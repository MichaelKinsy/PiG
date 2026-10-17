package cli

import (
	"encoding/json"
	"testing"
)

// rpc-types.ts RpcCommand (lines 20-70, plus get_messages and get_commands) and RpcExtensionUIRequest are discriminated unions: the "type" / "method" members
// are closed literal sets. The Go constants carry exactly those literals and the decoders produce them.
func TestRPCClosedUnionConstantsAreTheUpstreamLiterals(t *testing.T) {
	commands := map[RPCCommandType]string{
		RPCCommandPrompt: "prompt", RPCCommandSteer: "steer", RPCCommandFollowUp: "follow_up", RPCCommandAbort: "abort", RPCCommandClearQueue: "clear_queue",
		RPCCommandNewSession: "new_session", RPCCommandGetState: "get_state", RPCCommandSetModel: "set_model", RPCCommandCycleModel: "cycle_model",
		RPCCommandGetAvailableModels: "get_available_models", RPCCommandSetThinkingLevel: "set_thinking_level", RPCCommandCycleThinkingLevel: "cycle_thinking_level",
		RPCCommandGetAvailableThinkingLevels: "get_available_thinking_levels", RPCCommandSetSteeringMode: "set_steering_mode", RPCCommandSetFollowUpMode: "set_follow_up_mode",
		RPCCommandCompact: "compact", RPCCommandSetAutoCompaction: "set_auto_compaction", RPCCommandSetAutoRetry: "set_auto_retry", RPCCommandAbortRetry: "abort_retry",
		RPCCommandBash: "bash", RPCCommandAbortBash: "abort_bash", RPCCommandGetSessionStats: "get_session_stats", RPCCommandExportHtml: "export_html",
		RPCCommandSwitchSession: "switch_session", RPCCommandFork: "fork", RPCCommandClone: "clone", RPCCommandGetForkMessages: "get_fork_messages",
		RPCCommandGetEntries: "get_entries", RPCCommandGetTree: "get_tree", RPCCommandGetLastAssistantText: "get_last_assistant_text",
		RPCCommandSetSessionName: "set_session_name", RPCCommandGetMessages: "get_messages", RPCCommandGetCommands: "get_commands",
	}
	for constant, literal := range commands {
		if string(constant) != literal {
			t.Errorf("command constant %q, want %q", constant, literal)
		}
		var env RPCCommandEnvelope
		if err := json.Unmarshal([]byte(`{"type":"`+literal+`"}`), &env); err != nil || env.Type != constant {
			t.Errorf("type %q decoded to %q (%v)", literal, env.Type, err)
		}
	}
	if len(commands) != 33 {
		t.Errorf("RpcCommand has %d distinct commands, want 33", len(commands))
	}
	methods := map[RPCUIMethod]string{
		RPCUIMethodSelect: "select", RPCUIMethodConfirm: "confirm", RPCUIMethodInput: "input", RPCUIMethodEditor: "editor", RPCUIMethodNotify: "notify",
		RPCUIMethodSetStatus: "setStatus", RPCUIMethodSetWidget: "setWidget", RPCUIMethodSetTitle: "setTitle", RPCUIMethodSetEditorText: "set_editor_text",
	}
	for constant, literal := range methods {
		if string(constant) != literal {
			t.Errorf("ui method constant %q, want %q", constant, literal)
		}
		line, err := json.Marshal(rpcUIRequest{Method: constant})
		if err != nil || !json.Valid(line) {
			t.Fatalf("marshal: %v", err)
		}
		var back struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(line, &back); err != nil || back.Method != literal {
			t.Errorf("method %q serialized as %q", literal, back.Method)
		}
	}
}
