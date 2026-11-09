package sdk

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"regexp"
	"sort"
	"sync"
	"testing"
)

// piAsyncMembers are the AgentSession members Pi 1.5.0 declares `async` (agent-session.ts) that reach childSession.op/opAsync; prompt, steer, followUp, abort and bindExtensions have calls of their own. An awaited member sent over the synchronous childSession.op holds the call lane, so an abort sent while it waits queues behind it; a synchronous member sent over opAsync loses its call order.
var piAsyncMembers = map[string]bool{
	"sendCustomMessage": true, "sendUserMessage": true, "waitForIdle": true, "setModel": true, "cycleModel": true, "compact": true,
	"reload": true, "executeBash": true, "navigateTree": true, "exportToHtml": true, "summarizeForBugReport": true,
}

// TestChildSessionMembersRideTheirPiLane calls every AgentSession member against a recording host and checks the call method it used: opAsync exactly for Pi's async members.
func TestChildSessionMembersRideTheirPiLane(t *testing.T) {
	clientNet, hostNet := net.Pipe()
	client := newConn(clientNet)
	client.start()
	t.Cleanup(func() { _ = clientNet.Close(); _ = hostNet.Close() })
	var mu sync.Mutex
	lanes := map[string]string{}
	go func() {
		host := newConn(hostNet)
		for {
			data, err := host.readFrame()
			if err != nil {
				return
			}
			var request envelope
			if json.Unmarshal(data, &request) != nil || request.Type != msgCall || request.Call == nil {
				return
			}
			var op struct {
				Op string `json:"op"`
			}
			_ = json.Unmarshal(request.Call.Args, &op)
			mu.Lock()
			lanes[op.Op] = request.Call.Method
			mu.Unlock()
			reply := &envelope{Type: msgCallResult, ID: request.ID, CallResult: &callResultMsg{Result: json.RawMessage(`null`)}}
			encoded, _ := json.Marshal(reply)
			if host.writeFrame(encoded) != nil {
				return
			}
		}
	}()
	s := &AgentSession{conn: client, id: "lanes"}
	ctx := context.Background()
	calls := map[string]func(){
		"isStreaming":                func() { _, _ = s.IsStreaming() },
		"isIdle":                     func() { _, _ = s.IsIdle() },
		"isCompacting":               func() { _, _ = s.IsCompacting() },
		"isRetrying":                 func() { _, _ = s.IsRetrying() },
		"isBashRunning":              func() { _, _ = s.IsBashRunning() },
		"hasPendingBashMessages":     func() { _, _ = s.HasPendingBashMessages() },
		"retryAttempt":               func() { _, _ = s.RetryAttempt() },
		"pendingMessageCount":        func() { _, _ = s.PendingMessageCount() },
		"sessionId":                  func() { _, _ = s.SessionID() },
		"sessionFile":                func() { _, _ = s.SessionFile() },
		"sessionName":                func() { _, _ = s.SessionName() },
		"systemPrompt":               func() { _, _ = s.SystemPrompt() },
		"thinkingLevel":              func() { _, _ = s.ThinkingLevel() },
		"model":                      func() { _, _ = s.Model() },
		"routedModel":                func() { _, _ = s.RoutedModel() },
		"scopedModels":               func() { _, _ = s.ScopedModels() },
		"steeringMode":               func() { _, _ = s.SteeringMode() },
		"followUpMode":               func() { _, _ = s.FollowUpMode() },
		"autoCompactionEnabled":      func() { _, _ = s.AutoCompactionEnabled() },
		"autoRetryEnabled":           func() { _, _ = s.AutoRetryEnabled() },
		"supportsThinking":           func() { _, _ = s.SupportsThinking() },
		"getAvailableThinkingLevels": func() { _, _ = s.GetAvailableThinkingLevels() },
		"getLastAssistantText":       func() { _, _ = s.GetLastAssistantText() },
		"getActiveToolNames":         func() { _, _ = s.GetActiveToolNames() },
		"getCallableToolNames":       func() { _, _ = s.GetCallableToolNames() },
		"getSteeringMessages":        func() { _, _ = s.GetSteeringMessages() },
		"getFollowUpMessages":        func() { _, _ = s.GetFollowUpMessages() },
		"getAllTools":                func() { _, _ = s.GetAllTools() },
		"getToolDefinition":          func() { _, _ = s.GetToolDefinition("t") },
		"getContextUsage":            func() { _, _ = s.GetContextUsage() },
		"getSessionStats":            func() { _, _ = s.GetSessionStats() },
		"getUserMessagesForForking":  func() { _, _ = s.GetUserMessagesForForking() },
		"promptTemplates":            func() { _, _ = s.PromptTemplates() },
		"cacheWarmingStatus":         func() { _, _ = s.CacheWarmingStatus() },
		"hasExtensionHandlers":       func() { _, _ = s.HasExtensionHandlers("e") },
		"setModel":                   func() { _ = s.SetModel(ctx, map[string]any{"provider": "p", "id": "m"}) },
		"cycleModel":                 func() { _, _ = s.CycleModel(ctx, "forward") },
		"setThinkingLevel":           func() { _ = s.SetThinkingLevel("off") },
		"cycleThinkingLevel":         func() { _, _ = s.CycleThinkingLevel() },
		"setScopedModels":            func() { _ = s.SetScopedModels(nil) },
		"setActiveToolsByName":       func() { _ = s.SetActiveToolsByName(nil) },
		"setSteeringMode":            func() { _ = s.SetSteeringMode("all") },
		"setFollowUpMode":            func() { _ = s.SetFollowUpMode("all") },
		"setAutoCompactionEnabled":   func() { _ = s.SetAutoCompactionEnabled(true) },
		"setAutoRetryEnabled":        func() { _ = s.SetAutoRetryEnabled(true) },
		"setCacheWarmingMode":        func() { _ = s.SetCacheWarmingMode("off") },
		"setSessionName":             func() { _ = s.SetSessionName("n") },
		"clearQueue":                 func() { _, _ = s.ClearQueue() },
		"abortBash":                  func() { _ = s.AbortBash() },
		"abortBranchSummary":         func() { _ = s.AbortBranchSummary() },
		"abortCompaction":            func() { _ = s.AbortCompaction() },
		"abortRetry":                 func() { _ = s.AbortRetry() },
		"waitForIdle":                func() { _ = s.WaitForIdle(ctx) },
		"reload":                     func() { _ = s.Reload(ctx) },
		"compact":                    func() { _, _ = s.Compact(ctx, "") },
		"navigateTree":               func() { _, _ = s.NavigateTree(ctx, "id") },
		"executeBash":                func() { _, _ = s.ExecuteBash(ctx, "true") },
		"recordBashResult":           func() { _ = s.RecordBashResult("c", BashResult{}) },
		"exportToHtml":               func() { _, _ = s.ExportToHTML(ctx, "") },
		"exportToJsonl":              func() { _, _ = s.ExportToJsonl("") },
		"summarizeForBugReport":      func() { _, _ = s.SummarizeForBugReport(ctx, "") },
		"sendCustomMessage": func() {
			_ = s.SendCustomMessage(ctx, CustomMessage{CustomType: "x", Content: "c"}, SendMessageOptions{})
		},
		"sendUserMessage": func() { _ = s.SendUserMessage(ctx, "u", SendUserMessageOptions{}) },
	}
	// Every member the source sends is in the table: a new member needs its lane stated here.
	source, err := os.ReadFile("child_session_members.go")
	if err != nil {
		t.Fatal(err)
	}
	sent := map[string]bool{}
	for _, match := range regexp.MustCompile(`(?:\(s, |\.run\(|\.runAsync\(ctx, )"(\w+)"`).FindAllSubmatch(source, -1) {
		sent[string(match[1])] = true
	}
	for op := range sent {
		if calls[op] == nil {
			t.Errorf("member %s is sent by child_session_members.go but has no row in this test; state its lane", op)
		}
	}
	ops := make([]string, 0, len(calls))
	for op := range calls {
		ops = append(ops, op)
	}
	sort.Strings(ops)
	for _, op := range ops {
		calls[op]()
		mu.Lock()
		method := lanes[op]
		mu.Unlock()
		want := callChildSessionOp
		if piAsyncMembers[op] {
			want = callChildSessionOpAsync
		}
		if method != want {
			t.Errorf("%s is on the wrong lane: sent as %q, Pi's member needs %q", op, method, want)
		}
	}
	for op := range piAsyncMembers {
		if calls[op] == nil {
			t.Errorf("Pi async member %s has no row", op)
		}
	}
}
