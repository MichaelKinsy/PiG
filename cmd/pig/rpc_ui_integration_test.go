package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type rpcRecord = map[string]any

// rpcProcess drives one `pig --mode rpc` child over its JSONL stdin/stdout.
// Every wait synchronizes on the awaited stdout record; testbudget.Wait only
// bounds how long a hang takes to fail.
type rpcProcess struct {
	t          *testing.T
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stderr     *lockedBuffer
	records    chan rpcRecord
	stopOutput chan struct{}
	outputDone chan struct{}
	outputErr  error
	budget     time.Duration
	exited     bool
}

func startRPCProcess(t *testing.T, env []string, args ...string) *rpcProcess {
	t.Helper()
	return startRPCProcessAt(t, "", env, args...)
}

func startRPCProcessAt(t *testing.T, cwd string, env []string, args ...string) *rpcProcess {
	t.Helper()
	binary := buildPigBinaryForSignalTest(t)
	cmd := exec.Command(binary, append([]string{"--mode", "rpc"}, args...)...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &rpcProcess{
		t: t, cmd: cmd, stdin: stdin, stderr: &lockedBuffer{},
		records: make(chan rpcRecord, 64),
		budget:  testbudget.Wait(t),
	}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p.scanOutput(stdout)
	t.Cleanup(func() {
		close(p.stopOutput)
		_ = stdout.Close()
		<-p.outputDone
		if p.exited {
			return
		}
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return p
}

func (p *rpcProcess) scanOutput(stdout io.Reader) {
	p.stopOutput = make(chan struct{})
	p.outputDone = make(chan struct{})
	go func() {
		defer close(p.outputDone)
		defer close(p.records)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			var value rpcRecord
			if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
				p.outputErr = err
				return
			}
			select {
			case p.records <- value:
			case <-p.stopOutput:
				return
			}
		}
		p.outputErr = scanner.Err()
	}()
}

// startRPCUIFixture starts RPC mode with the rpc-ui.mjs extension and returns
// once get_commands lists the fixture's commands. RPC mode reads stdin only
// after extension loading settles, so this waits on the load itself, and a
// load failure fails here with pig's stderr instead of as a later timeout.
func startRPCUIFixture(t *testing.T, env []string, args ...string) *rpcProcess {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-ui.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	p := startRPCProcess(t, append([]string{"PIG_TEST_FAUX=1"}, env...), append(args, "-e", fixture)...)
	p.send(`{"id":"fixture-commands","type":"get_commands"}`)
	p.await("rpc-ui fixture command listing", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "fixture-commands" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		commands, _ := data["commands"].([]any)
		for _, value := range commands {
			if command, _ := value.(map[string]any); command["name"] == "block-model-select" && command["source"] == "extension" {
				return true
			}
		}
		t.Fatalf("rpc-ui extension did not load; get_commands = %v\n%s", record, p.stderr.String())
		return false
	})
	return p
}

func (p *rpcProcess) send(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.stdin, line+"\n"); err != nil {
		p.t.Fatal(err)
	}
}

func (p *rpcProcess) sendJSON(value any) {
	p.t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		p.t.Fatal(err)
	}
	p.send(string(encoded))
}

// await consumes records until done reports true.
func (p *rpcProcess) await(what string, done func(rpcRecord) bool) {
	p.t.Helper()
	p.awaitProgress(func() string { return what }, done)
}

// awaitProgress is await with a description evaluated at failure time, so a
// timeout reports which of several awaited records arrived.
func (p *rpcProcess) awaitProgress(what func() string, done func(rpcRecord) bool) {
	p.t.Helper()
	timer := time.NewTimer(p.budget)
	defer timer.Stop()
	for {
		select {
		case record, ok := <-p.records:
			if !ok {
				p.t.Fatalf("RPC output ended before %s: %v\n%s", what(), p.outputErr, p.stderr.String())
			}
			if done(record) {
				return
			}
		case <-timer.C:
			p.t.Fatalf("RPC %s timed out after %s\n%s", what(), p.budget, p.stderr.String())
		}
	}
}

// closeInput sends EOF to the RPC process.
func (p *rpcProcess) closeInput() {
	p.t.Helper()
	if err := p.stdin.Close(); err != nil {
		p.t.Fatal(err)
	}
}

// waitForExit requires a clean exit after stdin closes. A process that never
// exits is killed and fails the test once the wait budget expires.
func (p *rpcProcess) waitForExit(what string) {
	p.t.Helper()
	waited := make(chan error, 1)
	go func() { waited <- p.cmd.Wait() }()
	timer := time.NewTimer(p.budget)
	defer timer.Stop()
	select {
	case err := <-waited:
		p.exited = true
		if err != nil {
			p.t.Fatalf("RPC process: %v\n%s", err, p.stderr.String())
		}
	case <-timer.C:
		_ = p.cmd.Process.Kill()
		<-waited
		p.exited = true
		p.t.Fatalf("RPC process did not stop after EOF %s within %s\n%s", what, p.budget, p.stderr.String())
	}
}

// closeAndWait sends EOF and requires a clean exit.
func (p *rpcProcess) closeAndWait(what string) {
	p.t.Helper()
	p.closeInput()
	p.waitForExit(what)
}

func isSuccessResponse(record rpcRecord, id string) bool {
	return record["type"] == "response" && record["id"] == id && record["success"] == true
}

func isUISelect(record rpcRecord, title string) bool {
	return record["type"] == "extension_ui_request" && record["method"] == "select" && record["title"] == title
}

func TestRPCModeStartsWithUnknownModelAndRejectsPromptBeforeAcceptance(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcess(t, []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent")}, "--no-session")

	p.send(`{"id":"state","type":"get_state"}`)
	p.await("unknown state", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "state" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		model, _ := data["model"].(map[string]any)
		if model["id"] != "unknown" || model["provider"] != "unknown" || model["api"] != "unknown" {
			t.Fatalf("unknown model = %#v", model)
		}
		if data["thinkingLevel"] != "off" {
			t.Fatalf("unknown model thinking level = %#v", data["thinkingLevel"])
		}
		input, ok := model["input"].([]any)
		if !ok || len(input) != 0 {
			t.Fatalf("unknown model input = %#v", model["input"])
		}
		return true
	})

	p.send(`{"id":"prompt","type":"prompt","message":"hello"}`)
	p.await("prompt preflight error", func(record rpcRecord) bool {
		if record["type"] == "agent_start" {
			t.Fatal("unauthenticated prompt reached agent_start")
		}
		if record["type"] != "response" || record["id"] != "prompt" {
			return false
		}
		message, _ := record["error"].(string)
		if record["success"] != false || !strings.HasPrefix(message, "No API key found for the selected model.") {
			t.Fatalf("prompt preflight response = %#v", record)
		}
		return true
	})

	p.closeAndWait("after the prompt preflight error")
}

func TestRPCModeEOFStopsSessionTransitionWaitingOnExtensionUI(t *testing.T) {
	p := startRPCUIFixture(t, []string{"PIG_HOME=" + t.TempDir()}, "--model", "test-faux/echo", "--no-session")

	p.send(`{"id":"arm","type":"prompt","message":"/block-session-switch"}`)
	p.await("session-switch arm response", func(record rpcRecord) bool { return isSuccessResponse(record, "arm") })

	p.send(`{"id":"new","type":"new_session"}`)
	p.await("blocked Session transition", func(record rpcRecord) bool { return isUISelect(record, "Session switching") })

	p.closeAndWait("while a Session transition waited on extension UI")
}

func TestRPCModePromptRejectsWhileCompactionWaitsOnExtensionUI(t *testing.T) {
	home := t.TempDir()
	sessionDir := filepath.Join(home, "sessions")
	manager := codingagent.NewSessionManagerWithDir(home, sessionDir)
	session, err := manager.Create("rpc-compaction", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := session.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{
			Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: fmt.Sprintf("question %d", i)}}, Timestamp: int64(i*2 + 1),
		}}); err != nil {
			t.Fatal(err)
		}
		if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
			Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d", i)}},
			Usage: &ai.Usage{Input: 100, Output: 10}, StopReason: "stop", Timestamp: int64(i*2 + 2),
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agent", "settings.json"), []byte(`{"compaction":{"enabled":true,"reserveTokens":100,"keepRecentTokens":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	p := startRPCUIFixture(t, []string{"PIG_HOME=" + home},
		"--model", "test-faux/echo", "--session", session.Path(), "--session-dir", sessionDir,
	)

	p.send(`{"id":"arm","type":"prompt","message":"/block-compaction"}`)
	p.await("compaction arm response", func(record rpcRecord) bool { return isSuccessResponse(record, "arm") })

	p.send(`{"id":"compact","type":"compact"}`)
	var requestID string
	p.await("blocked compaction", func(record rpcRecord) bool {
		if isUISelect(record, "Compacting") {
			requestID, _ = record["id"].(string)
		}
		return requestID != ""
	})

	p.send(`{"id":"prompt","type":"prompt","message":"must reject"}`)
	p.await("compaction prompt error", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "prompt" {
			return false
		}
		message, _ := record["error"].(string)
		if record["success"] != false || message != "Cannot submit a prompt while compaction is in progress. Wait for compaction to finish and retry." {
			t.Fatalf("prompt during compaction = %#v", record)
		}
		return true
	})

	p.sendJSON(map[string]any{"type": "extension_ui_response", "id": requestID, "value": "continue"})
	p.await("compaction response", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "compact" {
			return false
		}
		if record["success"] != true {
			t.Fatalf("compact response = %#v", record)
		}
		return true
	})

	p.closeAndWait("after compaction completed")
}

func TestRPCModeExtensionUIRoundTrip(t *testing.T) {
	p := startRPCUIFixture(t, []string{"PIG_HOME=" + t.TempDir()}, "--model", "test-faux/echo", "--no-session")

	p.send(`{"id":"ask","type":"prompt","message":"/ask"}`)
	var requestID string
	p.await("UI request", func(record rpcRecord) bool {
		if record["type"] == "extension_ui_request" && record["method"] == "select" {
			requestID, _ = record["id"].(string)
		}
		return requestID != ""
	})
	p.sendJSON(map[string]any{"type": "extension_ui_response", "id": requestID, "value": "B"})

	gotNotify, gotWidget, gotSuccess := false, false, false
	p.awaitProgress(func() string {
		return fmt.Sprintf("UI completion: notify=%v widget=%v success=%v", gotNotify, gotWidget, gotSuccess)
	}, func(record rpcRecord) bool {
		if record["type"] == "extension_ui_request" && record["method"] == "notify" && record["message"] == "selected:B" {
			gotNotify = true
		}
		if record["type"] == "extension_ui_request" && record["method"] == "setWidget" && record["widgetKey"] == "choice" && record["widgetPlacement"] == "belowEditor" {
			gotWidget = true
		}
		if isSuccessResponse(record, "ask") {
			gotSuccess = true
		}
		return gotNotify && gotWidget && gotSuccess
	})

	p.send(`{"id":"handled","type":"prompt","message":"handled input"}`)
	gotInputNotify, gotInputSuccess := false, false
	p.awaitProgress(func() string {
		return fmt.Sprintf("handled input completion: notify=%v success=%v", gotInputNotify, gotInputSuccess)
	}, func(record rpcRecord) bool {
		if record["type"] == "agent_start" {
			t.Fatal("handled RPC input started the agent")
		}
		if record["type"] == "extension_ui_request" && record["method"] == "notify" && record["message"] == "input:rpc:idle" {
			gotInputNotify = true
		}
		if isSuccessResponse(record, "handled") {
			gotInputSuccess = true
		}
		return gotInputNotify && gotInputSuccess
	})

	p.send(`{"id":"session-events","type":"prompt","message":"/session-events"}`)
	var appendedEntry map[string]any
	gotSessionName, gotSessionEventsSuccess := false, false
	p.awaitProgress(func() string {
		return fmt.Sprintf("session events: entry=%v name=%v success=%v", appendedEntry != nil, gotSessionName, gotSessionEventsSuccess)
	}, func(record rpcRecord) bool {
		switch record["type"] {
		case "entry_appended":
			appendedEntry, _ = record["entry"].(map[string]any)
		case "session_info_changed":
			if record["name"] == "extension-name" {
				gotSessionName = true
			}
		case "response":
			if record["id"] == "session-events" && record["success"] == true {
				if appendedEntry == nil || !gotSessionName {
					t.Fatal("session event response preceded its entry or name event")
				}
				gotSessionEventsSuccess = true
			}
		}
		return gotSessionName && gotSessionEventsSuccess && appendedEntry != nil
	})
	if appendedEntry["type"] != "custom" || appendedEntry["customType"] != "rpc-entry" {
		t.Fatalf("entry_appended entry = %#v", appendedEntry)
	}
	data, _ := appendedEntry["data"].(map[string]any)
	if data["value"] != "hello" {
		t.Fatalf("entry_appended data = %#v", data)
	}

	p.send(`{"id":"entries","type":"get_entries"}`)
	p.await("persisted entry query", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "entries" {
			return false
		}
		responseData, _ := record["data"].(map[string]any)
		entries, _ := responseData["entries"].([]any)
		for _, value := range entries {
			entry, _ := value.(map[string]any)
			if entry["type"] == "custom" && entry["customType"] == "rpc-entry" {
				return true
			}
		}
		return false
	})

	p.send(`{"id":"clear-name","type":"prompt","message":"/clear-session-name"}`)
	gotClearedName, gotClearSuccess := false, false
	p.awaitProgress(func() string {
		return fmt.Sprintf("name clear: event=%v success=%v", gotClearedName, gotClearSuccess)
	}, func(record rpcRecord) bool {
		if record["type"] == "session_info_changed" {
			if _, hasName := record["name"]; !hasName {
				gotClearedName = true
			}
		}
		if isSuccessResponse(record, "clear-name") {
			if !gotClearedName {
				t.Fatal("clear-name response preceded session_info_changed")
			}
			gotClearSuccess = true
		}
		return gotClearedName && gotClearSuccess
	})

	p.send(`{"id":"direct-name","type":"set_session_name","name":"  direct-name  "}`)
	gotDirectName, gotDirectNameSuccess := false, false
	p.awaitProgress(func() string {
		return fmt.Sprintf("direct name change: event=%v success=%v", gotDirectName, gotDirectNameSuccess)
	}, func(record rpcRecord) bool {
		if record["type"] == "session_info_changed" && record["name"] == "direct-name" {
			gotDirectName = true
		}
		if isSuccessResponse(record, "direct-name") {
			if !gotDirectName {
				t.Fatal("set_session_name response preceded session_info_changed")
			}
			gotDirectNameSuccess = true
		}
		return gotDirectName && gotDirectNameSuccess
	})

	p.closeAndWait("after the UI round trip")
}

func TestRPCModeEOFStopsModelSelectionWaitingOnExtensionUI(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"fixture":{"baseUrl":"http://127.0.0.1:1/v1","apiKey":"fixture-key","api":"openai-completions","models":[{"id":"model-one","name":"Model One"}]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCUIFixture(t, []string{"PIG_HOME=" + home}, "--model", "test-faux/echo", "--no-session")

	p.send(`{"id":"arm","type":"prompt","message":"/block-model-select"}`)
	p.await("model-select arm response", func(record rpcRecord) bool { return isSuccessResponse(record, "arm") })

	p.send(`{"id":"cycle","type":"cycle_model"}`)
	p.await("blocked model selection", func(record rpcRecord) bool { return isUISelect(record, "Model selected") })

	p.closeAndWait("while model selection waited on extension UI")
}
