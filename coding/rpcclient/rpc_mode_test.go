package rpcclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Ports of upstream test/rpc.test.ts against a real `pig --mode rpc`
// subprocess. Upstream drives anthropic/claude-sonnet-4-5 with an API key;
// these drive PiG's hermetic test-faux provider, whose "reply with exactly:"
// prompt returns the requested text. A tiny keepRecentTokens lets a single
// exchange compact, as upstream's live model output does.

type rpcModeFixture struct {
	client   *RpcClient
	agentDir string
	cwd      string
}

func newRpcModeFixture(t *testing.T, provider, model string, files map[string]string) rpcModeFixture {
	t.Helper()
	home := t.TempDir()
	fixture := rpcModeFixture{agentDir: filepath.Join(home, "agent"), cwd: filepath.Join(home, "cwd")}
	for _, dir := range []string{fixture.agentDir, fixture.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files["settings.json"] = `{"compaction":{"keepRecentTokens":1}}`
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(fixture.agentDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fixture.client = NewRpcClient(RpcClientOptions{
		CliPath:  pigBinary,
		Cwd:      fixture.cwd,
		Env:      map[string]string{"HOME": home, "PIG_CODING_AGENT_DIR": fixture.agentDir, "PIG_TEST_FAUX": "1"},
		Provider: provider,
		Model:    model,
		Args:     []string{"--no-extensions", "--offline"},
	})
	t.Cleanup(fixture.client.Stop)
	if err := fixture.client.Start(); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func newFauxFixture(t *testing.T) rpcModeFixture {
	t.Helper()
	return newRpcModeFixture(t, "test-faux", "echo", map[string]string{})
}

// A reasoning model from models.json; these tests never contact it.
func newReasoningFixture(t *testing.T) rpcModeFixture {
	t.Helper()
	models := `{"providers":{"fixture":{"baseUrl":"http://127.0.0.1:1/v1","apiKey":"unused","api":"openai-completions",` +
		`"models":[{"id":"reasoner","name":"Reasoner","reasoning":true}]}}}`
	return newRpcModeFixture(t, "fixture", "reasoner", map[string]string{"models.json": models})
}

func (f rpcModeFixture) promptAndWait(t *testing.T, message string) []JsonAgentSessionEvent {
	t.Helper()
	events, err := f.client.PromptAndWait(message, nil, DefaultTimeout)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// sessionFileEntries reads the single session file under agentDir/sessions,
// as upstream reads sessionDir/sessions/<cwd dir>/*.jsonl.
func (f rpcModeFixture) sessionFileEntries(t *testing.T) []map[string]any {
	t.Helper()
	sessionDirs, err := os.ReadDir(filepath.Join(f.agentDir, "sessions"))
	if err != nil || len(sessionDirs) == 0 {
		t.Fatalf("no session directory: %v", err)
	}
	cwdDir := filepath.Join(f.agentDir, "sessions", sessionDirs[0].Name())
	files, err := filepath.Glob(filepath.Join(cwdDir, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("session files = %v, %v; want exactly one", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("session line %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func entriesOfType(entries []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, entry := range entries {
		if entry["type"] == typ {
			out = append(out, entry)
		}
	}
	return out
}

func messageRole(entry map[string]any) string {
	message, _ := entry["message"].(map[string]any)
	role, _ := message["role"].(string)
	return role
}

func TestRpcModeShouldGetState(t *testing.T) {
	f := newFauxFixture(t)
	state, err := f.client.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Model == nil || state.Model.Provider != "test-faux" || state.Model.ID != "echo" {
		t.Fatalf("state.model = %+v", state.Model)
	}
	if state.IsStreaming || state.MessageCount != 0 {
		t.Fatalf("fresh state = %+v, want idle with zero messages", state)
	}
}

func TestRpcModeShouldSaveMessagesToSessionFile(t *testing.T) {
	f := newFauxFixture(t)
	events := f.promptAndWait(t, "reply with exactly: hello")
	messageEnds := 0
	for _, event := range events {
		if event.Type == "message_end" {
			messageEnds++
		}
	}
	if messageEnds < 2 {
		t.Fatalf("message_end events = %d, want user + assistant", messageEnds)
	}
	entries := f.sessionFileEntries(t)
	if entries[0]["type"] != "session" {
		t.Fatalf("first entry = %v, want session header", entries[0])
	}
	var roles []string
	for _, entry := range entriesOfType(entries, "message") {
		roles = append(roles, messageRole(entry))
	}
	if !slices.Contains(roles, "user") || !slices.Contains(roles, "assistant") {
		t.Fatalf("message roles = %v", roles)
	}
}

func TestRpcModeShouldHandleManualCompaction(t *testing.T) {
	f := newFauxFixture(t)
	f.promptAndWait(t, "reply with exactly: hello")
	result, err := f.client.Compact(nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary == "" || result.TokensBefore <= 0 {
		t.Fatalf("compaction = %+v", result)
	}
	compactions := entriesOfType(f.sessionFileEntries(t), "compaction")
	if len(compactions) != 1 || compactions[0]["summary"] == "" {
		t.Fatalf("compaction entries = %v", compactions)
	}
}

func TestRpcModeShouldExecuteBashCommand(t *testing.T) {
	f := newFauxFixture(t)
	result, err := f.client.Bash("echo hello")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Output) != "hello" || result.ExitCode == nil || *result.ExitCode != 0 || result.Cancelled {
		t.Fatalf("bash = %+v", result)
	}
}

func TestRpcModeShouldAddBashOutputToContext(t *testing.T) {
	f := newFauxFixture(t)
	f.promptAndWait(t, "reply with exactly: hi")
	unique := "test-" + time.Now().Format("150405.000000000")
	if _, err := f.client.Bash("echo " + unique); err != nil {
		t.Fatal(err)
	}
	var bashMessages []map[string]any
	for _, entry := range entriesOfType(f.sessionFileEntries(t), "message") {
		if messageRole(entry) == "bashExecution" {
			bashMessages = append(bashMessages, entry)
		}
	}
	if len(bashMessages) != 1 {
		t.Fatalf("bashExecution messages = %d, want 1", len(bashMessages))
	}
	message := bashMessages[0]["message"].(map[string]any)
	if output, _ := message["output"].(string); !strings.Contains(output, unique) {
		t.Fatalf("bash message output = %q, want %q", output, unique)
	}
}

// Upstream asks a live model to repeat the bash output. test-faux cannot read
// its context back, so this asserts the context the model received instead:
// the bashExecution message precedes the prompt in the agent messages.
func TestRpcModeShouldIncludeBashOutputInLLMContext(t *testing.T) {
	f := newFauxFixture(t)
	unique := "unique-" + time.Now().Format("150405.000000000")
	if _, err := f.client.Bash("echo " + unique); err != nil {
		t.Fatal(err)
	}
	f.promptAndWait(t, "reply with exactly: ok")
	messages, err := f.client.GetMessages()
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, message := range messages {
		roles = append(roles, message.Role)
	}
	bashIndex := slices.Index(roles, "bashExecution")
	if bashIndex == -1 || bashIndex > slices.Index(roles, "user") || !strings.Contains(string(messages[bashIndex].Raw), unique) {
		t.Fatalf("message roles = %v; bash output must precede the prompt", roles)
	}
}

func TestRpcModeShouldSetAndGetThinkingLevel(t *testing.T) {
	f := newReasoningFixture(t)
	if err := f.client.SetThinkingLevel("high"); err != nil {
		t.Fatal(err)
	}
	state, err := f.client.GetState()
	if err != nil {
		t.Fatal(err)
	}
	if state.ThinkingLevel != "high" {
		t.Fatalf("thinkingLevel = %q, want high", state.ThinkingLevel)
	}
}

func TestRpcModeShouldCycleThinkingLevel(t *testing.T) {
	f := newReasoningFixture(t)
	initial, err := f.client.GetState()
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.client.CycleThinkingLevel()
	if err != nil || result == nil || result.Level == initial.ThinkingLevel {
		t.Fatalf("cycle = %+v, %v (initial %q)", result, err, initial.ThinkingLevel)
	}
	state, err := f.client.GetState()
	if err != nil || state.ThinkingLevel != result.Level {
		t.Fatalf("state after cycle = %q, %v; want %q", state.ThinkingLevel, err, result.Level)
	}
}

func TestRpcModeShouldGetAvailableThinkingLevels(t *testing.T) {
	f := newReasoningFixture(t)
	levels, err := f.client.GetAvailableThinkingLevels()
	if err != nil || len(levels) == 0 {
		t.Fatalf("levels = %v, %v", levels, err)
	}
	state, err := f.client.GetState()
	if err != nil || !slices.Contains(levels, state.ThinkingLevel) {
		t.Fatalf("state level %q not in %v (%v)", state.ThinkingLevel, levels, err)
	}
	cycled, err := f.client.CycleThinkingLevel()
	if err != nil {
		t.Fatal(err)
	}
	if cycled != nil {
		if !slices.Contains(levels, cycled.Level) {
			t.Fatalf("cycled level %q not in %v", cycled.Level, levels)
		}
		if len(levels) > 1 && cycled.Level == state.ThinkingLevel {
			t.Fatalf("cycle stayed on %q", cycled.Level)
		}
	}
	// test-faux has a single level, so its cycle reports null.
	faux := newFauxFixture(t)
	if cycled, err := faux.client.CycleThinkingLevel(); err != nil || cycled != nil {
		t.Fatalf("single-level cycle = %+v, %v; want nil", cycled, err)
	}
}

func TestRpcModeShouldGetAvailableModels(t *testing.T) {
	f := newFauxFixture(t)
	models, err := f.client.GetAvailableModels()
	if err != nil || len(models) == 0 {
		t.Fatalf("models = %v, %v", models, err)
	}
	for _, model := range models {
		if model.Provider == "" || model.ID == "" || model.ContextWindow <= 0 {
			t.Fatalf("model = %+v", model)
		}
	}
}

func TestRpcModeShouldGetSessionStats(t *testing.T) {
	f := newFauxFixture(t)
	f.promptAndWait(t, "reply with exactly: hello")
	stats, err := f.client.GetSessionStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.SessionFile == nil || stats.SessionID == "" || stats.UserMessages < 1 || stats.AssistantMessages < 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestRpcModeShouldCreateNewSession(t *testing.T) {
	f := newFauxFixture(t)
	fresh, err := f.client.GetState()
	if err != nil || fresh.MessageCount != 0 {
		t.Fatalf("fresh state = %+v, %v; want zero messages", fresh, err)
	}
	f.promptAndWait(t, "reply with exactly: hello")
	state, err := f.client.GetState()
	if err != nil || state.MessageCount == 0 {
		t.Fatalf("messageCount before = %d (fresh %d), %v", state.MessageCount, fresh.MessageCount, err)
	}
	if _, err := f.client.NewSession(nil); err != nil {
		t.Fatal(err)
	}
	state, err = f.client.GetState()
	if err != nil || state.MessageCount != 0 || state.SessionID == fresh.SessionID {
		t.Fatalf("after new session: messageCount %d (fresh %d), session %q, %v", state.MessageCount, fresh.MessageCount, state.SessionID, err)
	}
}

func TestRpcModeShouldExportToHTML(t *testing.T) {
	f := newFauxFixture(t)
	f.promptAndWait(t, "reply with exactly: hello")
	result, err := f.client.ExportHtml(nil)
	if err != nil {
		t.Fatal(err)
	}
	path := result.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(f.cwd, path)
	}
	if !strings.HasSuffix(result.Path, ".html") {
		t.Fatalf("export path = %q", result.Path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("exported file: %v", err)
	}
}

func TestRpcModeShouldGetLastAssistantText(t *testing.T) {
	f := newFauxFixture(t)
	text, err := f.client.GetLastAssistantText()
	if err != nil || text != nil {
		t.Fatalf("initial text = %v, %v; want nil", text, err)
	}
	f.promptAndWait(t, "reply with exactly: test123")
	text, err = f.client.GetLastAssistantText()
	if err != nil || text == nil || !strings.Contains(*text, "test123") {
		t.Fatalf("text = %v, %v", text, err)
	}
}

func TestRpcModeShouldGetSessionEntriesWithSinceCursor(t *testing.T) {
	f := newFauxFixture(t)
	f.promptAndWait(t, "reply with exactly: ok")
	all, err := f.client.GetEntries(nil)
	if err != nil || len(all.Entries) < 2 {
		t.Fatalf("entries = %d, %v", len(all.Entries), err)
	}
	for _, entry := range all.Entries {
		if entry.ID == "" {
			t.Fatalf("entry without id: %s", entry.Raw)
		}
	}
	if all.LeafID == nil || *all.LeafID != all.Entries[len(all.Entries)-1].ID {
		t.Fatalf("leafId = %v, want last entry id", all.LeafID)
	}
	since, err := f.client.GetEntries(&all.Entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(entryIDs(since.Entries), entryIDs(all.Entries[1:])) || since.LeafID == nil || *since.LeafID != *all.LeafID {
		t.Fatalf("since entries = %v, leaf %v", entryIDs(since.Entries), since.LeafID)
	}
	unknown := "nonexistent-id"
	if _, err := f.client.GetEntries(&unknown); err == nil || !strings.Contains(err.Error(), "Entry not found") {
		t.Fatalf("unknown since error = %v", err)
	}
}

func entryIDs(entries []SessionEntry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	return ids
}

func TestRpcModeShouldGetSessionTree(t *testing.T) {
	f := newFauxFixture(t)
	f.promptAndWait(t, "reply with exactly: ok")
	entries, err := f.client.GetEntries(nil)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := f.client.GetTree()
	if err != nil {
		t.Fatal(err)
	}
	if tree.LeafID == nil || entries.LeafID == nil || *tree.LeafID != *entries.LeafID {
		t.Fatalf("tree leaf %v, entries leaf %v", tree.LeafID, entries.LeafID)
	}
	if len(tree.Tree) != 1 {
		t.Fatalf("roots = %d, want 1", len(tree.Tree))
	}
	var chain []string
	nodes := tree.Tree
	for len(nodes) == 1 {
		chain = append(chain, nodes[0].Entry.ID)
		nodes = nodes[0].Children
	}
	if len(nodes) != 0 || !slices.Equal(chain, entryIDs(entries.Entries)) {
		t.Fatalf("tree chain = %v, entries = %v", chain, entryIDs(entries.Entries))
	}
}

func TestRpcModeShouldRetainPreCompactionEntriesInGetEntries(t *testing.T) {
	f := newFauxFixture(t)
	f.promptAndWait(t, "reply with exactly: ok")
	before, err := f.client.GetEntries(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.Compact(nil); err != nil {
		t.Fatal(err)
	}
	after, err := f.client.GetEntries(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Entries) < len(before.Entries) || !slices.Equal(entryIDs(after.Entries[:len(before.Entries)]), entryIDs(before.Entries)) {
		t.Fatalf("pre-compaction entries not retained in order")
	}
	if !slices.ContainsFunc(after.Entries, func(e SessionEntry) bool { return e.Type == "compaction" }) {
		t.Fatal("no compaction entry after compact")
	}
}

func TestRpcModeShouldSetAndGetSessionName(t *testing.T) {
	f := newFauxFixture(t)
	state, err := f.client.GetState()
	if err != nil || state.SessionName != nil {
		t.Fatalf("initial sessionName = %v, %v", state.SessionName, err)
	}
	f.promptAndWait(t, "reply with exactly: ok")
	if err := f.client.SetSessionName("my-test-session"); err != nil {
		t.Fatal(err)
	}
	state, err = f.client.GetState()
	if err != nil || state.SessionName == nil || *state.SessionName != "my-test-session" {
		t.Fatalf("sessionName = %v, %v", state.SessionName, err)
	}
	infos := entriesOfType(f.sessionFileEntries(t), "session_info")
	if len(infos) != 1 || infos[0]["name"] != "my-test-session" {
		t.Fatalf("session_info entries = %v", infos)
	}
}
