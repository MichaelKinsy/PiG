package rpcclient

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// childModeEnv makes the test binary act as a scripted RPC child process.
const childModeEnv = "PIG_RPCCLIENT_TEST_CHILD"

var pigBinary string

func TestMain(m *testing.M) {
	if mode := os.Getenv(childModeEnv); mode != "" {
		os.Exit(runChild(mode))
	}
	dir, err := os.MkdirTemp("", "pig-rpcclient-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve source SDK roots:", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	for _, key := range []string{"PIG_CODING_AGENT_DIR", "PIG_CODING_AGENT_SESSION_DIR"} {
		if err := os.Unsetenv(key); err != nil {
			fmt.Fprintf(os.Stderr, "clear inherited %s: %v\n", key, err)
			_ = os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	configRoot := filepath.Join(dir, "home")
	if err := os.Mkdir(configRoot, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "create isolated PIG_HOME:", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	for key, value := range map[string]string{
		"PIG_HOME":        configRoot,
		"PIG_SDK_GO_ROOT": filepath.Join(sourceRoot, "extensions", "sdk"),
		"PIG_SDK_PY_ROOT": filepath.Join(sourceRoot, "extensions", "sdk-py"),
		"PIG_SDK_RS_ROOT": filepath.Join(sourceRoot, "extensions", "sdk-rs"),
	} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "set isolated %s: %v\n", key, err)
			_ = os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	// Windows starts only files with an executable extension.
	pigBinary = filepath.Join(dir, "pig")
	if runtime.GOOS == "windows" {
		pigBinary += ".exe"
	}
	build := exec.Command("go", "build", "-o", pigBinary, "github.com/MichaelKinsy/PiG/cmd/pig")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build pig: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// The test binary and PIG_HOME must be different filesystem objects. Sharing
// dir/pig made SDK staging try to create directories below the executable.
func TestPigBinaryIsOutsidePIGHome(t *testing.T) {
	home := os.Getenv("PIG_HOME")
	if filepath.Clean(home) == filepath.Clean(pigBinary) {
		t.Fatalf("PIG_HOME and pig binary both use %q", home)
	}
	info, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("PIG_HOME %q is not a directory", home)
	}
}

// runChild implements the scripted children. "exit-on-stdin" mirrors the
// upstream child script that exits 43 on its first stdin data. "canned"
// logs every command line and answers it. "emit" writes a fixed stdout script.
// "not-reading" keeps stdin open without consuming pipe data.
func runChild(mode string) int {
	switch mode {
	case "exit-on-stdin":
		buf := make([]byte, 1)
		_, _ = os.Stdin.Read(buf)
		return 43
	case "not-reading":
		time.Sleep(time.Minute)
		return 0
	case "canned":
		return runCannedChild()
	case "emit":
		fmt.Print("not json\n\n{\"type\":\"custom\",\"n\":1}\r\nnull\n[1]\n")
		fmt.Print("{\"type\":\"response\",\"id\":\"req_99\",\"command\":\"x\",\"success\":true}\n")
		fmt.Print("{\"type\":\"custom\",\"n\":2}\n{\"type\":\"agent_settled\"}")
		_ = os.Stdout.Sync()
		_ = os.Stdout.Close() // end of stream delivers the unterminated record
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	}
	return 2
}

func runCannedChild() int {
	logFile, err := os.OpenFile(os.Getenv("PIG_RPCCLIENT_TEST_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 3
	}
	defer func() { _ = logFile.Close() }()
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(logFile, line)
		var cmd struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(line), &cmd)
		response := map[string]any{"id": cmd.ID, "type": "response", "command": cmd.Type, "success": true}
		switch cmd.Type {
		case "clear_queue":
			response["data"] = map[string]any{"steering": []string{"Change direction"}, "followUp": []string{"Summarize when finished"}}
		case "clone":
			response["data"] = map[string]any{"cancelled": false}
		case "get_entries":
			response["success"] = false
			response["error"] = "Entry not found: missing"
		}
		out, _ := json.Marshal(response)
		fmt.Println(string(out))
	}
	return 0
}

func childClient(t *testing.T, mode string) (*RpcClient, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "commands.log")
	client := NewRpcClient(RpcClientOptions{
		CliPath: os.Args[0],
		Env:     map[string]string{childModeEnv: mode, "PIG_RPCCLIENT_TEST_LOG": logPath},
	})
	t.Cleanup(client.Stop)
	return client, logPath
}

func loggedCommands(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// Upstream rpc-client-clear-queue.test.ts.
func TestRpcClientClearQueueSendsTheClearQueueCommand(t *testing.T) {
	client, logPath := childClient(t, "canned")
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	result, err := client.ClearQueue()
	if err != nil {
		t.Fatal(err)
	}
	want := ClearQueueResult{Steering: []string{"Change direction"}, FollowUp: []string{"Summarize when finished"}}
	if !slices.Equal(result.Steering, want.Steering) || !slices.Equal(result.FollowUp, want.FollowUp) {
		t.Fatalf("ClearQueue = %+v, want %+v", result, want)
	}
	if got := loggedCommands(t, logPath); !slices.Equal(got, []string{`{"type":"clear_queue","id":"req_1"}`}) {
		t.Fatalf("sent %q", got)
	}
}

// Upstream rpc-client-clone.test.ts.
func TestRpcClientCloneSendsTheCloneCommand(t *testing.T) {
	client, logPath := childClient(t, "canned")
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	result, err := client.Clone()
	if err != nil || result.Cancelled {
		t.Fatalf("Clone = %+v, %v", result, err)
	}
	if got := loggedCommands(t, logPath); !slices.Equal(got, []string{`{"type":"clone","id":"req_1"}`}) {
		t.Fatalf("sent %q", got)
	}
}

// Upstream rpc-client-process-exit.test.ts: an in-flight request is rejected
// when the child process exits.
func TestRpcClientRejectsInFlightRequestWhenChildExits(t *testing.T) {
	client, _ := childClient(t, "exit-on-stdin")
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	_, err := client.GetCommands()
	if err == nil || !regexp.MustCompile(`Agent process exited \(code=43 signal=null\)`).MatchString(err.Error()) {
		t.Fatalf("GetCommands error = %v", err)
	}
	// The exit error is sticky for later sends.
	if _, err2 := client.GetState(); err2 == nil || err2.Error() != err.Error() {
		t.Fatalf("later send error = %v, want %v", err2, err)
	}
}

func TestRpcClientSerializesCommandsLikeJSONStringify(t *testing.T) {
	client, logPath := childClient(t, "canned")
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	empty := ""
	instructions := "keep <tags> & more"
	steps := []func() error{
		func() error { return client.Prompt("a<b&c", nil) },
		func() error { return client.Steer("s", []ImageContent{}) },
		func() error {
			return client.FollowUp("f", []ImageContent{{Data: "AAA", MimeType: "image/png"}})
		},
		func() error { _, err := client.NewSession(nil); return err },
		func() error { _, err := client.NewSession(&empty); return err },
		func() error { _, err := client.Compact(&instructions); return err },
		func() error { _, err := client.SetModel("openai", "gpt-5"); return err },
		func() error { return client.SetThinkingLevel("high") },
		func() error { return client.SetAutoCompaction(false) },
		func() error { _, err := client.ExportHtml(nil); return err },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		`{"type":"prompt","message":"a<b&c","id":"req_1"}`,
		`{"type":"steer","message":"s","images":[],"id":"req_2"}`,
		`{"type":"follow_up","message":"f","images":[{"type":"image","data":"AAA","mimeType":"image/png"}],"id":"req_3"}`,
		`{"type":"new_session","id":"req_4"}`,
		`{"type":"new_session","parentSession":"","id":"req_5"}`,
		`{"type":"compact","customInstructions":"keep <tags> & more","id":"req_6"}`,
		`{"type":"set_model","provider":"openai","modelId":"gpt-5","id":"req_7"}`,
		`{"type":"set_thinking_level","level":"high","id":"req_8"}`,
		`{"type":"set_auto_compaction","enabled":false,"id":"req_9"}`,
		`{"type":"export_html","id":"req_10"}`,
	}
	if got := loggedCommands(t, logPath); !slices.Equal(got, want) {
		t.Fatalf("sent\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRpcClientSurfacesErrorResponses(t *testing.T) {
	client, _ := childClient(t, "canned")
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	missing := "missing"
	if _, err := client.GetEntries(&missing); err == nil || err.Error() != "Entry not found: missing" {
		t.Fatalf("GetEntries error = %v", err)
	}
}

// Mirrors upstream attachJsonlLineReader and handleLine: LF framing, one
// trailing CR stripped, non-JSON and null lines ignored, unmatched responses
// and non-object JSON delivered to listeners, and a final unterminated record
// delivered at end of stream.
func TestRpcClientDeliversStdoutRecordsToListeners(t *testing.T) {
	client, _ := childClient(t, "emit")
	var events []JsonAgentSessionEvent
	settled := make(chan struct{})
	client.OnEvent(func(event JsonAgentSessionEvent) {
		events = append(events, event)
		if event.Type == "agent_settled" {
			close(settled)
		}
	})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-settled:
	case <-time.After(30 * time.Second):
		t.Fatal("agent_settled not delivered")
	}
	var got []string
	for _, event := range events {
		got = append(got, event.Type+" "+string(event.Raw))
	}
	want := []string{
		`custom {"type":"custom","n":1}`,
		` [1]`,
		`response {"type":"response","id":"req_99","command":"x","success":true}`,
		`custom {"type":"custom","n":2}`,
		`agent_settled {"type":"agent_settled"}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Upstream iterates the live listener array, so a listener that unsubscribes
// itself while handling an event shifts the next listener into its slot and
// that listener misses the event.
func TestRpcClientListenerUnsubscribeDuringDispatchMatchesArrayIteration(t *testing.T) {
	client := NewRpcClient(RpcClientOptions{})
	var calls []string
	var unsubscribeA func()
	unsubscribeA = client.OnEvent(func(JsonAgentSessionEvent) { calls = append(calls, "a"); unsubscribeA() })
	client.OnEvent(func(JsonAgentSessionEvent) { calls = append(calls, "b") })
	client.OnEvent(func(JsonAgentSessionEvent) { calls = append(calls, "c") })
	client.handleLine([]byte(`{"type":"x"}`))
	client.handleLine([]byte(`{"type":"x"}`))
	if want := []string{"a", "c", "b", "c"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRpcClientLifecycleErrors(t *testing.T) {
	client, _ := childClient(t, "canned")
	if _, err := client.GetState(); err == nil || err.Error() != "Client not started" {
		t.Fatalf("send before start = %v", err)
	}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if err := client.Start(); err == nil || err.Error() != "Client already started" {
		t.Fatalf("second start = %v", err)
	}
	client.Stop()
	client.Stop()
	if _, err := client.GetState(); err == nil || err.Error() != "Client not started" {
		t.Fatalf("send after stop = %v", err)
	}
	if err := client.Start(); err != nil {
		t.Fatalf("restart after stop = %v", err)
	}
}

func TestRpcClientStartReportsEarlyExit(t *testing.T) {
	client := NewRpcClient(RpcClientOptions{CliPath: os.Args[0], Env: map[string]string{childModeEnv: "unknown-mode"}})
	t.Cleanup(client.Stop)
	// An exit inside the 100ms settle delay fails Start; a slower exit fails
	// the next send with the same error.
	err := client.Start()
	if err == nil {
		_, err = client.GetState()
	}
	if err == nil || !strings.HasPrefix(err.Error(), "Agent process exited (code=2 signal=null). Stderr: ") {
		t.Fatalf("early exit error = %v", err)
	}
}

func TestRpcClientStartReportsSpawnFailure(t *testing.T) {
	client := NewRpcClient(RpcClientOptions{CliPath: filepath.Join(t.TempDir(), "missing-pig")})
	err := client.Start()
	if err == nil || !strings.HasPrefix(err.Error(), "Agent process error: ") {
		t.Fatalf("Start = %v", err)
	}
}
