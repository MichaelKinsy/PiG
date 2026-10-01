package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// replaceLogLines returns the fixture's log with the per-process random instance
// identities replaced by first-seen ordinals (A, B, ...), so a test asserts which
// events shared an extension instance without depending on the random values.
func replaceLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replace log: %v", err)
	}
	ordinals := map[string]string{}
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		for i, field := range fields {
			if id, ok := strings.CutPrefix(field, "instance="); ok {
				if _, seen := ordinals[id]; !seen {
					ordinals[id] = string(rune('A' + len(ordinals)))
				}
				fields[i] = "instance=" + ordinals[id]
			}
		}
		lines = append(lines, strings.Join(fields, " "))
	}
	return lines
}

func sessionReplaceFixture(t *testing.T) string {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-replace.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

// Pi print-mode.ts binds newSession to runtimeHost.newSession, and
// agent-session-runtime.ts:164-193 tears the old Session down and builds the
// replacement, extension instances included, through the Session factory. So the
// extension that called ctx.newSession() is shut down as instance A and the
// replacement's session_start reaches a fresh instance B.
func TestPrintModeNewSessionBuildsFreshExtensionInstances(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	bin := buildPigBinaryForSignalTest(t)
	log := filepath.Join(t.TempDir(), "replace.log")
	cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--print", "-e", sessionReplaceFixture(t), "/replace-new")
	cmd.Dir = t.TempDir()
	home := t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PIG_TEST_REPLACE_LOG="+log)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pig --print: %v\n%s", err, out)
	}
	got := replaceLogLines(t, log)
	// The old instance finishes /replace-new after its own shutdown, as in Pi;
	// the replacement instance is shut down when print mode disposes the runtime.
	want := []string{
		"session_start reason=startup instance=A previous=no",
		"session_shutdown reason=new instance=A target=yes",
		"session_start reason=new instance=B previous=yes",
		"replace-new cancelled=false instance=A",
		"session_shutdown reason=quit instance=B target=no",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("print-mode lifecycle:\n got %q\nwant %q", got, want)
	}
}

// rpcResponseData returns the data object of the successful response to id.
func rpcResponseData(t *testing.T, p *rpcProcess, id string) map[string]any {
	t.Helper()
	var data map[string]any
	p.await("response "+id, func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != id {
			return false
		}
		if record["success"] != true {
			t.Fatalf("response %s = %v\n%s", id, record, p.stderr.String())
		}
		data, _ = record["data"].(map[string]any)
		return true
	})
	return data
}

// Pi rpc-mode.ts routes new_session, switch_session, fork and clone through runtimeHost, so each builds the replacement Session and its extensions through the Session factory. It then calls rebindSession() a second time after the command returns, which is why a replacement's extensions see session_start twice. Extension processes are fresh instances: A, B, C, D, E below.
func TestRPCSessionCommandsReplaceThroughTheRuntimeFactory(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	t.Parallel()
	log := filepath.Join(t.TempDir(), "replace.log")
	home := t.TempDir()
	p := startRPCProcessAt(t, t.TempDir(), []string{
		"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_TEST_REPLACE_LOG=" + log,
	}, "--model", "test-faux/faux-1", "-e", sessionReplaceFixture(t))
	p.send(`{"id":"commands","type":"get_commands"}`)
	rpcResponseData(t, p, "commands")

	// Persist a first Session: only a Session with an assistant reply exists on disk, so only it can be resumed, cloned or forked.
	p.send(`{"id":"prompt","type":"prompt","message":"reply with exactly: first"}`)
	p.await("first run to settle", func(record rpcRecord) bool { return record["type"] == "agent_settled" })
	p.send(`{"id":"state","type":"get_state"}`)
	firstFile, _ := rpcResponseData(t, p, "state")["sessionFile"].(string)
	if firstFile == "" {
		t.Fatal("first Session has no session file")
	}

	p.send(`{"id":"new","type":"new_session"}`)
	if cancelled := rpcResponseData(t, p, "new")["cancelled"]; cancelled != false {
		t.Fatalf("new_session cancelled = %v", cancelled)
	}
	p.sendJSON(map[string]any{"id": "switch", "type": "switch_session", "sessionPath": firstFile})
	if cancelled := rpcResponseData(t, p, "switch")["cancelled"]; cancelled != false {
		t.Fatalf("switch_session cancelled = %v", cancelled)
	}
	p.send(`{"id":"clone","type":"clone"}`)
	if cancelled := rpcResponseData(t, p, "clone")["cancelled"]; cancelled != false {
		t.Fatalf("clone cancelled = %v", cancelled)
	}
	p.send(`{"id":"forkable","type":"get_fork_messages"}`)
	messages, _ := rpcResponseData(t, p, "forkable")["messages"].([]any)
	if len(messages) == 0 {
		t.Fatal("cloned Session has no forkable message")
	}
	entryID, _ := messages[0].(map[string]any)["entryId"].(string)
	p.sendJSON(map[string]any{"id": "fork", "type": "fork", "entryId": entryID})
	if data := rpcResponseData(t, p, "fork"); data["cancelled"] != false || data["text"] != "reply with exactly: first" {
		t.Fatalf("fork data = %v", data)
	}
	p.closeAndWait("after the replacements")

	got := replaceLogLines(t, log)
	want := []string{
		"session_start reason=startup instance=A previous=no",
		"session_shutdown reason=new instance=A target=yes",
		"session_start reason=new instance=B previous=yes",
		"session_start reason=new instance=B previous=yes",
		"session_shutdown reason=resume instance=B target=yes",
		"session_start reason=resume instance=C previous=yes",
		"session_start reason=resume instance=C previous=yes",
		"session_shutdown reason=fork instance=C target=yes",
		"session_start reason=fork instance=D previous=yes",
		"session_start reason=fork instance=D previous=yes",
		"session_shutdown reason=fork instance=D target=yes",
		"session_start reason=fork instance=E previous=yes",
		"session_start reason=fork instance=E previous=yes",
		"session_shutdown reason=quit instance=E target=no",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("RPC lifecycle:\n got %q\nwant %q\n%s", got, want, p.stderr.String())
	}
}

// Pi's switchSession creates the replacement's services, resources, system prompt and tools for the destination Session's cwd (agent-session-runtime.ts:197-226 passes sessionManager.getCwd() to createRuntime). A resumed Session from another project therefore carries that project's context files and runs tools in its directory.
func TestRPCSwitchSessionRebuildsServicesForTheDestinationCWD(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	t.Parallel()
	home := t.TempDir()
	startup, destination := filepath.Join(home, "startup"), filepath.Join(home, "destination")
	for dir, rules := range map[string]string{startup: "STARTUP-RULES\n", destination: "DESTINATION-RULES\n"} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(rules), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Resolve symlinks (macOS temp roots) so the recorded cwd matches what the process reports.
	destination, err := filepath.EvalSymlinks(destination)
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(home, "sessions")
	other, err := codingagent.NewSessionManagerWithDir(destination, sessionDir).Create("destination-session", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "from elsewhere"}}, Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := other.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "reply"}}, Usage: &ai.Usage{Input: 1, Output: 1}, StopReason: "stop", Timestamp: 2}}); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-cwd-probe.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "cwd.log")
	p := startRPCProcessAt(t, startup, []string{
		"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PIG_TEST_CWD_LOG=" + log,
	}, "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", fixture)
	p.send(`{"id":"commands","type":"get_commands"}`)
	rpcResponseData(t, p, "commands")
	p.sendJSON(map[string]any{"id": "switch", "type": "switch_session", "sessionPath": other.Path()})
	if cancelled := rpcResponseData(t, p, "switch")["cancelled"]; cancelled != false {
		t.Fatalf("switch_session cancelled = %v", cancelled)
	}
	// The bash tool's own path syntax differs by platform (Git Bash prints /c/...), so observe its cwd through the file it creates.
	p.send(`{"id":"marker","type":"bash","command":"echo marker > cwd-marker"}`)
	rpcResponseData(t, p, "marker")
	if _, err := os.Stat(filepath.Join(destination, "cwd-marker")); err != nil {
		t.Fatalf("bash did not run in the destination cwd %q: %v", destination, err)
	}
	if _, err := os.Stat(filepath.Join(startup, "cwd-marker")); err == nil {
		t.Fatalf("bash ran in the startup cwd %q after the switch", startup)
	}
	p.closeAndWait("after the switch")

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	startupCWD, err := filepath.EvalSymlinks(startup)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"session_start reason=startup cwd=" + startupCWD + " context=STARTUP-RULES",
		"session_start reason=resume cwd=" + destination + " context=DESTINATION-RULES",
		"session_start reason=resume cwd=" + destination + " context=DESTINATION-RULES",
	}
	if got := strings.Split(strings.TrimSpace(string(data)), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("session_start records:\n got %q\nwant %q", got, want)
	}
}

const piStaleMessage = "This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload()."

// Pi agent-session-runtime.ts:167-177 invalidates the outgoing runner during teardown, so a captured pi or ctx throws runner.ts's stale message and never acts on the replacement Session (2860-replaced-session-context.test.ts:147-205). The replaced Session's extension process stays alive until its command returns, so its host calls must fail with that message. Reads a subprocess SDK answers from its own mirror, and host calls that return or raise an error or write to stderr instead of throwing at the call site, are recorded in D30.
func TestPrintModeReplacedSessionHostCallsFailWithThePiStaleMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	bin := buildPigBinaryForSignalTest(t)
	log := filepath.Join(t.TempDir(), "stale.log")
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-stale.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--mode", "json", "-e", fixture, "/stale-probe")
	cmd.Dir = t.TempDir()
	home := t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PIG_TEST_STALE_LOG="+log)
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("pig --mode json: %v", err)
	}
	got := replaceLogLines(t, log)
	want := []string{
		"pi.getActiveTools threw " + piStaleMessage,
		"pi.exec threw " + piStaleMessage,
		"pi.sendUserMessage ok",
		"pi.exec flush threw " + piStaleMessage,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stale uses:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(string(stdout), "stale message") || strings.Contains(string(stdout), `"type":"agent_start"`) {
		t.Fatalf("a stale pi.sendUserMessage reached the replacement Session:\n%s", stdout)
	}
}

// Pi agent-session.ts:2941 discovers resources with reason "startup" for every Session start that is not a reload, so a Session created by ctx.newSession() reports "startup" to resources_discover handlers, not the session_start reason.
func TestPrintModeReplacementDiscoversResourcesWithStartupReason(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	bin := buildPigBinaryForSignalTest(t)
	log := filepath.Join(t.TempDir(), "resources.log")
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-resources.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--print", "-e", fixture, "/replace-new")
	cmd.Dir = t.TempDir()
	home := t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PIG_TEST_REPLACE_LOG="+log)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pig --print: %v\n%s", err, out)
	}
	got := replaceLogLines(t, log)
	want := []string{
		"session_start reason=startup",
		"resources_discover reason=startup",
		"session_start reason=new",
		"resources_discover reason=startup",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("resources_discover reasons:\n got %q\nwant %q", got, want)
	}
}

// A replacement Session keeps the process's --session-dir, so its extensions read the same directory from ctx.sessionManager.getSessionDir() as the first Session's do (Pi's SessionManager.create(cwd, sessionDir) keeps it, agent-session-runtime.ts:240-247).
func TestPrintModeReplacementKeepsTheSessionDirForExtensionReads(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	bin := buildPigBinaryForSignalTest(t)
	log := filepath.Join(t.TempDir(), "dir.log")
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-dir.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	sessionDir := filepath.Join(home, "chosen-sessions")
	cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--print", "--session-dir", sessionDir, "-e", fixture, "/replace-new")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"),
		"PIG_TEST_FAUX=1", "PIG_TEST_REPLACE_LOG="+log)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pig --print: %v\n%s", err, out)
	}
	got := replaceLogLines(t, log)
	want := []string{
		"session_start reason=startup dir=" + sessionDir,
		"session_start reason=new dir=" + sessionDir,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("session dirs:\n got %q\nwant %q", got, want)
	}
}

// Pi's switchSession calls createRuntime for the destination cwd, and createRuntime loads the destination's settings and resolves its packages, so fileURLToPath's error for an invalid local file: URL in the destination project fails the switch (package-manager.ts:912-926). The startup project is valid, so only the replacement build can raise it.
func TestRPCSwitchSessionFailsForADestinationWithAnInvalidFileURLPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary")
	}
	home := t.TempDir()
	startup, destination := filepath.Join(home, "startup"), filepath.Join(home, "destination")
	for _, dir := range []string{startup, filepath.Join(destination, codingagent.ConfigDirName())} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(destination, codingagent.ConfigDirName(), "settings.json"), []byte(`{"packages":["file:///%2Fbad","./missing-package"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	destination, err := filepath.EvalSymlinks(destination)
	if err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(home, "sessions")
	other, err := codingagent.NewSessionManagerWithDir(destination, sessionDir).Create("destination-session", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "from elsewhere"}}, Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := other.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "reply"}}, Usage: &ai.Usage{Input: 1, Output: 1}, StopReason: "stop", Timestamp: 2}}); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, startup, []string{
		"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1",
	}, "--model", "test-faux/faux-1", "--session-dir", sessionDir, "--approve")
	p.sendJSON(map[string]any{"id": "switch", "type": "switch_session", "sessionPath": other.Path()})
	var failure string
	p.await("failed switch_session response", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "switch" {
			return false
		}
		if record["success"] == true {
			t.Fatalf("switch_session into a project with an invalid file: package succeeded: %v", record)
		}
		failure, _ = record["error"].(string)
		return true
	})
	if !strings.Contains(failure, "must not include encoded / characters") {
		t.Fatalf("switch_session error = %q, want fileURLToPath's invalid file: URL error", failure)
	}
	p.closeAndWait("after the failed switch")
	// package-manager.ts:912-926 computes every identity before installing any package, so ./missing-package is never install-attempted. Only validateConfiguredPackageSources ahead of EnsureConfiguredPackagesInstalled keeps stderr free of the install report.
	if stderr := p.stderr.String(); strings.Contains(stderr, "Could not install") || strings.Contains(stderr, "Reinstalled") {
		t.Fatalf("the replacement build attempted an install before validating package identities:\n%s", stderr)
	}
}
