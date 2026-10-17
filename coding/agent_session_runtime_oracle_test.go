package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type runtimeFileSpec struct {
	Messages []string `json:"messages"`
	CWD      string   `json:"cwd,omitempty"`
}

type runtimeOp struct {
	Op     string `json:"op"`
	Index  int    `json:"index"`
	Parent bool   `json:"parent"`
}

type runtimeScenario struct {
	Files   []runtimeFileSpec `json:"files"`
	Imports []runtimeFileSpec `json:"imports"`
	Ops     []runtimeOp       `json:"ops"`
}

type runtimeRecord struct {
	Initial map[string]any   `json:"initial"`
	Outs    []map[string]any `json:"outs"`
}

func runtimeScenarios() []runtimeScenario {
	rng := rand.New(rand.NewPCG(14, 3))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	var scenarios []runtimeScenario
	for range 150 {
		scenario := runtimeScenario{Imports: []runtimeFileSpec{}}
		for range 1 + rng.IntN(3) {
			spec := runtimeFileSpec{Messages: []string{}}
			for range rng.IntN(3) {
				spec.Messages = append(spec.Messages, pick([]string{"a", "b", "日本語", "two words", "x"}))
			}
			if rng.IntN(5) == 0 {
				spec.CWD = "missing"
			}
			scenario.Files = append(scenario.Files, spec)
		}
		for range 1 + rng.IntN(2) {
			scenario.Imports = append(scenario.Imports, runtimeFileSpec{Messages: []string{pick([]string{"i1", "i2"})}})
		}
		for range 1 + rng.IntN(8) {
			op := runtimeOp{Index: rng.IntN(6), Parent: rng.IntN(2) == 0}
			op.Op = pick([]string{"switch", "switch", "new", "new", "import", "importStored", "switchMissing", "importMissing", "rebindOff", "invalidateOff"})
			scenario.Ops = append(scenario.Ops, op)
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

func runRuntimeWithPig(t *testing.T, scenario runtimeScenario, dir string) runtimeRecord {
	cwd, agentDir, sessionDir := filepath.Join(dir, "cwd"), filepath.Join(dir, "agent"), filepath.Join(dir, "sessions")
	for _, path := range []string{cwd, agentDir, sessionDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	appendMessages := func(manager *icodingagent.SessionManager, spec runtimeFileSpec, base string) string {
		sessionCWD := base
		if spec.CWD == "missing" {
			sessionCWD = filepath.Join(dir, "gone")
		}
		created, err := icodingagent.NewSessionManagerWithDir(sessionCWD, manager.SessionDir()).Create("", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range spec.Messages {
			if _, err := created.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: 1}}); err != nil {
				t.Fatal(err)
			}
		}
		if len(spec.Messages) == 0 {
			if _, err := created.AppendSessionInfo("empty"); err != nil {
				t.Fatal(err)
			}
		}
		return *created.GetSessionFile()
	}
	sessionManager := icodingagent.NewSessionManagerWithDir(cwd, sessionDir)
	var files, importable []string
	for _, spec := range scenario.Files {
		files = append(files, appendMessages(sessionManager, spec, cwd))
	}
	external := filepath.Join(dir, "external")
	if err := os.Mkdir(external, 0o755); err != nil {
		t.Fatal(err)
	}
	externalManager := icodingagent.NewSessionManagerWithDir(cwd, external)
	for _, spec := range scenario.Imports {
		importable = append(importable, appendMessages(externalManager, spec, cwd))
	}
	factory := func(_ context.Context, options CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: options.CWD, AgentDir: options.AgentDir})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		session, err := NewSession(services, SessionOptions{SessionManager: options.SessionManager, SkipBuiltinTools: true})
		if err != nil {
			services.Close()
			return CreateAgentSessionRuntimeResult{}, err
		}
		return CreateAgentSessionRuntimeResult{Session: session, Services: services, Dispose: func(string) { services.Close() }}, nil
	}
	first, err := icodingagent.NewSessionManagerWithDir(cwd, sessionDir).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(context.Background(), factory, CreateAgentSessionRuntimeOptions{CWD: cwd, AgentDir: agentDir, SessionManager: first})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	var calls []string
	runtime.SetRebindSession(func(context.Context, *Session) error { calls = append(calls, "rebind"); return nil })
	runtime.SetBeforeSessionInvalidate(func() { calls = append(calls, "invalidate") })
	processDir, _ := os.Getwd()
	physical, _ := filepath.EvalSymlinks(processDir)
	sessionName := regexp.MustCompile(`\d{4}-\d\d-\d\dT[\d-]+Z_[0-9a-f-]+\.jsonl`)
	norm := func(text string) string {
		text = strings.ReplaceAll(text, dir, "<dir>")
		text = strings.ReplaceAll(text, physical, "<proc>")
		text = strings.ReplaceAll(text, processDir, "<proc>")
		return sessionName.ReplaceAllString(text, "<session>.jsonl")
	}
	state := func() map[string]any {
		session := runtime.Session()
		manager := session.SessionManager()
		file := any(nil)
		if path := manager.GetSessionFile(); path != nil {
			switch {
			case strings.HasPrefix(*path, sessionDir):
				file = "<sessions>/new"
				for i, candidate := range files {
					if candidate == *path {
						file = fmt.Sprintf("<sessions>/f%d", i)
					}
				}
			default:
				file = norm(*path)
			}
		}
		entries := []string{}
		for _, entry := range session.Entries() {
			raw, _ := json.Marshal(entry)
			var fields map[string]any
			_ = json.Unmarshal(raw, &fields)
			if fields["type"] == "message" {
				message, _ := fields["message"].(map[string]any)
				text := "?"
				if content, ok := message["content"].(string); ok {
					text = content
				} else if blocks, ok := message["content"].([]any); ok && len(blocks) == 1 {
					if block, ok := blocks[0].(map[string]any); ok {
						text, _ = block["text"].(string)
					}
				}
				entries = append(entries, fmt.Sprintf("message:%v:%s", message["role"], text))
				continue
			}
			entries = append(entries, fmt.Sprint(fields["type"]))
		}
		header := manager.GetHeader()
		parent := any(nil)
		if header.ParentSession != "" {
			parent = norm(header.ParentSession)
			for i, candidate := range files {
				if candidate == header.ParentSession {
					parent = fmt.Sprintf("f%d", i)
				}
			}
		}
		listing, _ := os.ReadDir(sessionDir)
		names := []string{}
		for _, entry := range listing {
			name := entry.Name()
			label := "other"
			for i, candidate := range files {
				if filepath.Base(candidate) == name {
					label = fmt.Sprintf("f%d", i)
				}
			}
			for i, candidate := range importable {
				stem := strings.TrimSuffix(filepath.Base(candidate), ".jsonl")
				if filepath.Base(candidate) == name {
					label = fmt.Sprintf("imp%d", i)
				} else if strings.HasPrefix(name, stem+"-") {
					label = fmt.Sprintf("imp%d-%s", i, name[len(filepath.Base(candidate))-5:])
				}
			}
			names = append(names, label)
		}
		slices.Sort(names)
		return map[string]any{"cwd": norm(runtime.CWD()), "file": file, "entries": entries, "header": map[string]any{"cwd": norm(header.CWD), "parentSession": parent}, "dirFiles": names}
	}
	record := runtimeRecord{Initial: state()}
	for _, op := range scenario.Ops {
		calls = nil
		var result any
		var err error
		var cancelled extension.CancelledResult
		switch op.Op {
		case "switch":
			cancelled, err = runtime.SwitchSession(context.Background(), files[op.Index%len(files)])
		case "switchMissing":
			cancelled, err = runtime.SwitchSession(context.Background(), filepath.Join(sessionDir, "nope.jsonl"))
		case "new":
			var options *extension.NewSessionOptions
			if op.Parent {
				options = &extension.NewSessionOptions{ParentSession: files[op.Index%len(files)]}
			}
			cancelled, err = runtime.NewSession(context.Background(), options)
		case "import":
			cancelled, err = runtime.ImportFromJsonl(context.Background(), importable[op.Index%len(importable)])
		case "importStored":
			cancelled, err = runtime.ImportFromJsonl(context.Background(), files[op.Index%len(files)])
		case "importMissing":
			cancelled, err = runtime.ImportFromJsonl(context.Background(), filepath.Join(dir, "nope.jsonl"))
		case "rebindOff":
			runtime.SetRebindSession(nil)
			result = "off"
		case "invalidateOff":
			runtime.SetBeforeSessionInvalidate(nil)
			result = "off"
		}
		switch {
		case err != nil:
			result = map[string]any{"thrown": norm(err.Error())}
		case result == nil:
			result = map[string]any{"cancelled": cancelled.Cancelled}
		default:
			result = nil
		}
		record.Outs = append(record.Outs, map[string]any{"result": result, "calls": append([]string{}, calls...), "state": state()})
	}
	return record
}

// AgentSessionRuntime (agent-session-runtime.ts) against pinned Pi over 150 seeded scenarios on persisted session files: switchSession to a stored file or a missing path, newSession with or without a parent session, importFromJsonl of an external file, an already stored one, or a missing one, with the rebind and before-invalidate callbacks installed or removed; after each op the result or error, the callbacks that ran and in which order, the current session file, cwd, entries and header, and the session directory's file count must agree.
func TestAgentSessionRuntimeMatchesPi(t *testing.T) {
	scenarios := runtimeScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/agent_session_runtime.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []runtimeRecord
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	failures := 0
	for i, scenario := range scenarios {
		dir := filepath.Join(root, fmt.Sprint(i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		got := runRuntimeWithPig(t, scenario, dir)
		raw, _ := json.Marshal(got)
		var normalized runtimeRecord
		_ = json.Unmarshal(raw, &normalized)
		if reflect.DeepEqual(normalized, expected[i]) {
			continue
		}
		if failures++; failures > 5 && os.Getenv("ORACLE_ALL") == "" {
			continue
		}
		report := fmt.Sprintf("scenario %d files %+v imports %+v", i, scenario.Files, scenario.Imports)
		if !reflect.DeepEqual(normalized.Initial, expected[i].Initial) {
			g, _ := json.Marshal(normalized.Initial)
			e, _ := json.Marshal(expected[i].Initial)
			report += fmt.Sprintf("\n  initial\n  Pig %s\n  Pi  %s", g, e)
		}
		for k := range scenario.Ops {
			if k < len(normalized.Outs) && !reflect.DeepEqual(normalized.Outs[k], expected[i].Outs[k]) {
				ops, _ := json.Marshal(scenario.Ops[:k+1])
				g, _ := json.Marshal(normalized.Outs[k])
				e, _ := json.Marshal(expected[i].Outs[k])
				report += fmt.Sprintf("\n  ops %s\n  Pig %.800s\n  Pi  %.800s", ops, g, e)
				break
			}
		}
		t.Error(report)
	}
	if failures > 5 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}
