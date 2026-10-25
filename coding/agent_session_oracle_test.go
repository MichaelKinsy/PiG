package coding

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type agentSessionOp struct {
	Op         string               `json:"op"`
	Level      string               `json:"level,omitempty"`
	Persist    bool                 `json:"persist"`
	Direction  string               `json:"direction,omitempty"`
	Ref        string               `json:"ref,omitempty"`
	Mode       string               `json:"mode,omitempty"`
	Name       string               `json:"name"`
	Text       string               `json:"text"`
	Enabled    bool                 `json:"enabled"`
	CustomType string               `json:"customType"`
	Content    any                  `json:"content"`
	Display    bool                 `json:"display"`
	Details    any                  `json:"details"`
	Options    *customOptions       `json:"options"`
	Command    string               `json:"command,omitempty"`
	Exclude    bool                 `json:"exclude"`
	Output     string               `json:"output"`
	ExitCode   *int                 `json:"exitCode"`
	Cancelled  bool                 `json:"cancelled"`
	Truncated  bool                 `json:"truncated"`
	ToolName   string               `json:"tool,omitempty"`
	Event      string               `json:"event,omitempty"`
	Index      int                  `json:"index"`
	Label      string               `json:"label,omitempty"`
	Scoped     []agentSessionScoped `json:"scoped"`
}

type customOptions struct {
	TriggerTurn *bool  `json:"triggerTurn,omitempty"`
	DeliverAs   string `json:"deliverAs,omitempty"`
}

type agentSessionScoped struct {
	Model    string `json:"model"`
	Thinking string `json:"thinking,omitempty"`
}

type agentSessionScenario struct {
	Model    string               `json:"model"`
	Thinking string               `json:"thinking,omitempty"`
	Enabled  []string             `json:"enabled"`
	Scoped   []agentSessionScoped `json:"scoped"`
	Ops      []agentSessionOp     `json:"ops"`
}

func agentSessionScenarios() []agentSessionScenario {
	rng := rand.New(rand.NewPCG(9, 31))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	models := []string{"anthropic/claude-sonnet-4-5", "anthropic/claude-opus-4-8", "anthropic/claude-haiku-4-5", "anthropic/claude-opus-4-6", "openai/gpt-4o", "openai/gpt-5", "openai/gpt-4.1", "openai/o3"}
	levels := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
	// Commands that write to both stdout and stderr are left out: the two pipes interleave by event-loop timing, which Pi does not fix either.
	var scenarios []agentSessionScenario
	for range 600 {
		scenario := agentSessionScenario{Model: pick(models), Scoped: []agentSessionScoped{}}
		if rng.IntN(3) == 0 {
			scenario.Thinking = pick(levels)
		}
		if rng.IntN(2) == 0 {
			for range 1 + rng.IntN(4) {
				entry := agentSessionScoped{Model: pick(models)}
				if rng.IntN(2) == 0 {
					entry.Thinking = pick(levels)
				}
				scenario.Scoped = append(scenario.Scoped, entry)
			}
		}
		if rng.IntN(3) == 0 {
			scenario.Enabled = []string{pick(models), pick(models)}
		}
		for range 1 + rng.IntN(14) {
			persist := rng.IntN(2) == 0
			switch rng.IntN(18) {
			case 0, 1:
				scenario.Ops = append(scenario.Ops, agentSessionOp{Op: "setThinking", Level: pick(levels), Persist: persist})
			case 2, 3:
				scenario.Ops = append(scenario.Ops, agentSessionOp{Op: "cycleThinking", Persist: persist})
			case 4, 5:
				scenario.Ops = append(scenario.Ops, agentSessionOp{Op: "cycleModel", Direction: pick([]string{"forward", "backward"}), Persist: persist})
			case 6:
				scenario.Ops = append(scenario.Ops, agentSessionOp{Op: "setModel", Ref: pick(models), Persist: persist})
			case 7:
				scenario.Ops = append(scenario.Ops, agentSessionOp{Op: pick([]string{"steeringMode", "followUpMode"}), Mode: pick([]string{"all", "one-at-a-time"})})
			case 8:
				scenario.Ops = append(scenario.Ops, agentSessionOp{Op: "name", Name: pick([]string{"N", "日本語", "a b", "x\ny"})})
			case 9:
				scenario.Ops = append(scenario.Ops, agentSessionOp{Op: pick([]string{"lastText", "usage", "callable", "clearQueue"})})
			case 10, 11:
				scenario.Ops = append(scenario.Ops, agentSessionOp{Op: pick([]string{"steer", "followUp"}), Text: pick([]string{"hi", "later", "日本語", " padded ", ""})})
			case 12:
				scenario.Ops = append(scenario.Ops, agentSessionOp{Op: "autoCompaction", Enabled: rng.IntN(2) == 0})
			case 13:
				scenario.Ops = append(scenario.Ops, agentSessionOp{Op: "autoRetry", Enabled: rng.IntN(2) == 0})
			case 14:
				op := agentSessionOp{Op: "custom", CustomType: pick([]string{"note", "日本語", ""}), Display: rng.IntN(2) == 0, Content: pick2(rng, []any{"text", []any{map[string]any{"type": "text", "text": "x"}}, nil, ""}), Details: pick2(rng, []any{nil, map[string]any{"b": 1, "a": []any{1, "x"}}, "s"})}
				switch rng.IntN(4) {
				case 0:
					op.Options = &customOptions{DeliverAs: "nextTurn"}
				case 1:
					no := false
					op.Options = &customOptions{TriggerTurn: &no}
				case 2:
					op.Options = &customOptions{DeliverAs: pick([]string{"steer", "followUp"}), TriggerTurn: new(false)}
				}
				scenario.Ops = append(scenario.Ops, op)
			case 15:
				switch rng.IntN(7) {
				case 0, 1:
					scenario.Ops = append(scenario.Ops, agentSessionOp{Op: "bash", Command: pick([]string{"echo hello", "printf 'a\\nb'", "exit 3", "true", "printf ''"}), Exclude: rng.IntN(2) == 0})
				case 2:
					code := rng.IntN(3)
					scenario.Ops = append(scenario.Ops, agentSessionOp{Op: "recordBash", Command: "ls", Output: pick([]string{"out", "", "多"}), ExitCode: pick3(rng, []*int{nil, &code}), Cancelled: rng.IntN(4) == 0, Truncated: rng.IntN(4) == 0, Exclude: rng.IntN(2) == 0})
				case 3:
					scenario.Ops = append(scenario.Ops, agentSessionOp{Op: pick([]string{"abort", "toolDef", "handlers"}), ToolName: pick([]string{"read", "bash", "nope"}), Event: pick([]string{"input", "context", "tool_call"})})
				case 4:
					scenario.Ops = append(scenario.Ops, agentSessionOp{Op: pick([]string{"navigate", "navigate", "exportJsonl", "exportHtml"}), Index: rng.IntN(12), Label: pick([]string{"", "", "L"}), Ref: pick([]string{"", "dark", "light", "nope"})})
				case 5:
					op := agentSessionOp{Op: pick([]string{"setScoped", "setScoped", "refresh"}), Scoped: []agentSessionScoped{}}
					if op.Op == "setScoped" {
						for range rng.IntN(4) {
							entry := agentSessionScoped{Model: pick(models)}
							if rng.IntN(2) == 0 {
								entry.Thinking = pick(levels)
							}
							op.Scoped = append(op.Scoped, entry)
						}
					}
					scenario.Ops = append(scenario.Ops, op)
				default:
					scenario.Ops = append(scenario.Ops, agentSessionOp{Op: "cycleModel", Direction: "forward", Persist: persist})
				}
			default:
				scenario.Ops = append(scenario.Ops, agentSessionOp{Op: "cycleModel", Direction: "forward", Persist: persist})
			}
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

func pick2(rng *rand.Rand, items []any) any { return items[rng.IntN(len(items))] }

func pick3(rng *rand.Rand, items []*int) *int { return items[rng.IntN(len(items))] }

type agentSessionRecord struct {
	Initial map[string]any   `json:"initial"`
	Outs    []map[string]any `json:"outs"`
}

func runAgentSessionWithPig(t *testing.T, scenario agentSessionScenario, dir string) agentSessionRecord {
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	for provider, key := range map[string]string{"anthropic": "k1", "openai": "k2"} {
		if err := services.ModelRuntime().SetRuntimeApiKey(context.Background(), provider, key); err != nil {
			t.Fatal(err)
		}
	}
	build := func(ref string) *ai.Model {
		model, err := BuildModel(ref, services)
		if err != nil {
			t.Fatal(err)
		}
		return model
	}
	if scenario.Enabled != nil {
		if err := services.SettingsManager().UpdateGlobal(func(s *Settings) { s.EnabledModels = scenario.Enabled }); err != nil {
			t.Fatal(err)
		}
	}
	options := SessionOptions{Model: build(scenario.Model), NoSession: true, SystemPrompt: "test", SkipBuiltinTools: true}
	if scenario.Thinking != "" {
		options.ThinkingLevel = ai.ModelThinkingLevel(scenario.Thinking)
	}
	for _, entry := range scenario.Scoped {
		options.ScopedModels = append(options.ScopedModels, ScopedModel{Model: build(entry.Model), ThinkingLevel: agent.ThinkingLevel(entry.Thinking)})
	}
	session, err := NewSession(services, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	ref := func(model *ai.Model) any {
		if model == nil {
			return nil
		}
		return model.ProviderID() + "/" + model.ID
	}
	nonNil := func(list []string) []string {
		if list == nil {
			return []string{}
		}
		return list
	}
	snapshot := func() map[string]any {
		scoped := []string{}
		for _, entry := range session.ScopedModels() {
			scoped = append(scoped, fmt.Sprintf("%s@%s", ref(entry.Model), entry.ThinkingLevel))
		}
		levels := []string{}
		for _, level := range session.AvailableThinkingLevels() {
			levels = append(levels, string(level))
		}
		entries := []string{}
		var ids []string
		var parents []any
		var labels []any
		for _, entry := range session.Entries() {
			raw, _ := json.Marshal(entry)
			var fields map[string]any
			_ = json.Unmarshal(raw, &fields)
			id, _ := fields["id"].(string)
			ids = append(ids, id)
			parents = append(parents, fields["parentId"])
			if label, ok := session.inner.GetLabel(id); ok {
				labels = append(labels, label)
			} else {
				labels = append(labels, nil)
			}
			switch fields["type"] {
			case "model_change":
				entries = append(entries, fmt.Sprintf("model_change:%v/%v", fields["provider"], fields["modelId"]))
			case "thinking_level_change":
				entries = append(entries, fmt.Sprintf("thinking:%v", fields["thinkingLevel"]))
			case "session_info":
				entries = append(entries, fmt.Sprintf("session_info:%v", fields["name"]))
			case "custom_message":
				entries = append(entries, fmt.Sprintf("custom_message:%v", fields["customType"]))
			case "message":
				message, _ := fields["message"].(map[string]any)
				entries = append(entries, fmt.Sprintf("message:%v", message["role"]))
			default:
				entries = append(entries, fmt.Sprint(fields["type"]))
			}
		}
		settings := services.Settings()
		nilIfEmpty := func(text string) any {
			if text == "" {
				return nil
			}
			return text
		}
		var enabled any
		if list := services.SettingsManager().GetEnabledModels(); list != nil {
			enabled = list
		}
		indexOf := func(id any) any {
			for i, candidate := range ids {
				if text, ok := id.(string); ok && candidate == text {
					return i
				}
			}
			return nil
		}
		parentIndexes := []any{}
		for _, parent := range parents {
			parentIndexes = append(parentIndexes, indexOf(parent))
		}
		var leaf any
		if id := session.LeafID(); id != nil {
			leaf = indexOf(*id)
		}
		messages := []any{}
		for _, message := range session.Messages() {
			raw, _ := json.Marshal(message)
			var fields map[string]any
			_ = json.Unmarshal(raw, &fields)
			delete(fields, "timestamp")
			messages = append(messages, fields)
		}
		return map[string]any{
			"messages": messages, "model": ref(session.Model()), "thinking": string(session.ThinkingLevel()), "supports": session.SupportsThinking(), "levels": levels,
			"steeringMode": string(session.SteeringMode()), "followUpMode": string(session.FollowUpMode()), "name": nilIfEmpty(session.SessionName()),
			"scoped": scoped, "steering": nonNil(session.GetSteeringMessages()), "followUp": nonNil(session.GetFollowUpMessages()), "pending": session.PendingMessageCount(),
			"settings": map[string]any{"provider": nilIfEmpty(settings.DefaultProvider), "model": nilIfEmpty(settings.DefaultModel), "thinking": nilIfEmpty(settings.DefaultThinkingLevel), "enabled": enabled, "autoCompaction": services.SettingsManager().GetCompactionEnabled(), "autoRetry": services.SettingsManager().GetRetryEnabled()},
			"entries":  entries, "leaf": leaf, "parents": parentIndexes, "labels": labels, "autoCompactionEnabled": session.AutoCompactionEnabled(), "autoRetryEnabled": session.AutoRetryEnabled(),
		}
	}
	record := agentSessionRecord{Initial: snapshot()}
	for _, op := range scenario.Ops {
		var result any
		thrown := func(err error) {
			if err != nil {
				result = map[string]any{"thrown": err.Error()}
			}
		}
		persist := ModelMutationOptions{Persist: op.Persist}
		switch op.Op {
		case "setThinking":
			thrown(session.SetThinkingLevel(ai.ModelThinkingLevel(op.Level), persist))
		case "cycleThinking":
			level, err := session.CycleThinkingLevel(persist)
			thrown(err)
			if err == nil && level != "" {
				result = string(level)
			}
		case "cycleModel":
			cycled, err := session.CycleModel(op.Direction, persist)
			thrown(err)
			if err == nil && cycled != nil {
				result = map[string]any{"model": ref(cycled.Model), "thinking": string(cycled.ThinkingLevel), "scoped": cycled.IsScoped}
			}
		case "setModel":
			thrown(session.SetModel(build(op.Ref), persist))
		case "steeringMode":
			thrown(session.SetSteeringMode(agent.QueueMode(op.Mode)))
		case "followUpMode":
			thrown(session.SetFollowUpMode(agent.QueueMode(op.Mode)))
		case "name":
			thrown(session.SetSessionName(op.Name))
		case "lastText":
			if text := session.LastAssistantText(); text != nil {
				result = *text
			}
		case "usage":
			if usage := session.ContextUsage(); usage != nil {
				record := map[string]any{"tokens": nil, "window": usage.ContextWindow, "percent": nil}
				if usage.Tokens != nil {
					record["tokens"] = *usage.Tokens
				}
				if usage.Percent != nil {
					record["percent"] = *usage.Percent
				}
				result = record
			}
		case "steer":
			_, err := session.Steer(context.Background(), op.Text, nil, nil)
			thrown(err)
		case "followUp":
			_, err := session.FollowUp(context.Background(), op.Text, nil, nil)
			thrown(err)
		case "clearQueue":
			steering, followUp := session.ClearQueue()
			result = map[string]any{"steering": nonNil(steering), "followUp": nonNil(followUp)}
		case "autoCompaction":
			session.SetAutoCompactionEnabled(op.Enabled)
		case "autoRetry":
			session.SetAutoRetryEnabled(op.Enabled)
		case "custom":
			var options *extension.SendMessageOptions
			if op.Options != nil {
				options = &extension.SendMessageOptions{TriggerTurn: op.Options.TriggerTurn, DeliverAs: extension.DeliverAs(op.Options.DeliverAs)}
			}
			thrown(session.SendCustomMessage(context.Background(), extension.CustomMessageRef{CustomType: op.CustomType, Content: op.Content, Display: op.Display, Details: op.Details}, options))
		case "bash":
			bash, err := session.ExecuteBash(context.Background(), op.Command, nil, &ExecuteBashOptions{ExcludeFromContext: excludeOption(op.Exclude)})
			thrown(err)
			if err == nil {
				result = map[string]any{"output": bash.Output, "exitCode": bash.ExitCode, "cancelled": bash.Cancelled, "truncated": bash.Truncated}
			}
		case "recordBash":
			thrown(session.RecordBashResult(op.Command, BashResult{Output: op.Output, ExitCode: op.ExitCode, Cancelled: op.Cancelled, Truncated: op.Truncated}, excludeOption(op.Exclude)))
		case "abort":
			session.AbortBash()
			session.AbortCompaction()
			session.AbortRetry()
			session.AbortBranchSummary()
			thrown(session.WaitForIdle(context.Background()))
		case "toolDef":
			if _, ok := session.GetToolDefinition(op.ToolName); ok {
				result = "defined"
			}
		case "handlers":
			result = session.HasExtensionHandlers(op.Event)
		case "navigate":
			entries := session.Entries()
			target := "missing"
			if len(entries) > 0 {
				raw, _ := json.Marshal(entries[op.Index%len(entries)])
				var fields map[string]any
				_ = json.Unmarshal(raw, &fields)
				target, _ = fields["id"].(string)
			}
			navigated, err := session.NavigateTree(context.Background(), target, NavigateTreeOptions{Label: op.Label})
			thrown(err)
			if err == nil {
				record := map[string]any{"cancelled": navigated.Cancelled, "aborted": nil, "editorText": nil, "summary": nil}
				if navigated.Aborted {
					record["aborted"] = true
				}
				if navigated.EditorText != "" {
					record["editorText"] = navigated.EditorText
				}
				if navigated.SummaryEntry != nil {
					record["summary"] = true
				}
				result = record
			}
		case "exportHtml":
			written, err := session.ExportToHTML(filepath.Join(dir, "export.html"), ExportToHTMLOptions{ThemeName: op.Ref})
			thrown(err)
			if err == nil {
				result = normalizedHTMLExport(t, written, dir)
			}
		case "exportJsonl":
			written, err := session.ExportToJsonl(filepath.Join(dir, "export.jsonl"))
			thrown(err)
			if err == nil {
				data, readErr := os.ReadFile(written)
				if readErr != nil {
					t.Fatal(readErr)
				}
				ids := map[string]int{}
				var lines []map[string]any
				for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
					var fields map[string]any
					if err := json.Unmarshal([]byte(line), &fields); err != nil {
						t.Fatal(err)
					}
					if id, ok := fields["id"].(string); ok {
						ids[id] = i
					}
					lines = append(lines, fields)
				}
				normalized := []any{}
				for _, fields := range lines {
					parent := any(nil)
					if text, ok := fields["parentId"].(string); ok {
						if index, found := ids[text]; found {
							parent = index
						} else {
							parent = "?"
						}
					}
					for _, key := range []string{"id", "parentId", "timestamp", "cwd"} {
						delete(fields, key)
					}
					if message, ok := fields["message"].(map[string]any); ok {
						delete(message, "timestamp")
					}
					fields["parent"] = parent
					normalized = append(normalized, fields)
				}
				result = normalized
			}
		case "setScoped":
			var scoped []ScopedModel
			for _, entry := range op.Scoped {
				scoped = append(scoped, ScopedModel{Model: build(entry.Model), ThinkingLevel: agent.ThinkingLevel(entry.Thinking)})
			}
			session.SetScopedModels(scoped)
		case "refresh":
			session.RefreshContext()
		case "callable":
			result = nonNil(session.CallableToolNames())
		}
		record.Outs = append(record.Outs, map[string]any{"result": result, "state": snapshot()})
	}
	return record
}

// AgentSession (agent-session.ts) against pinned Pi over 600 seeded scenarios that make no model call: the start model and thinking level, scoped models with their own thinking levels, enabled-models settings, then model and thinking-level changes (persisted or not), model cycling in both directions over the scope or every available model, steering and follow-up queues and their modes, session name, context usage, auto-compaction and auto-retry flags, custom messages (nextTurn, deferred, immediate) and bash executions and records with their session entries and messages. After every op the result, the model, thinking level, queue contents, settings and session entries must agree.
func TestAgentSessionMatchesPi(t *testing.T) {
	scenarios := agentSessionScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/agent_session.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []agentSessionRecord
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
		got := runAgentSessionWithPig(t, scenario, dir)
		gotJSON, _ := json.Marshal(got)
		var normalized agentSessionRecord
		_ = json.Unmarshal(gotJSON, &normalized)
		if reflect.DeepEqual(normalized, expected[i]) {
			continue
		}
		if failures++; failures > 5 && os.Getenv("ORACLE_ALL") == "" {
			continue
		}
		header, _ := json.Marshal(map[string]any{"model": scenario.Model, "thinking": scenario.Thinking, "enabled": scenario.Enabled, "scoped": scenario.Scoped})
		report := fmt.Sprintf("scenario %d %s", i, header)
		if !reflect.DeepEqual(normalized.Initial, expected[i].Initial) {
			g, _ := json.Marshal(normalized.Initial)
			e, _ := json.Marshal(expected[i].Initial)
			report += fmt.Sprintf("\n  initial\n  Pig %s\n  Pi  %s", g, e)
		} else {
			for k, op := range scenario.Ops {
				if k >= len(normalized.Outs) || reflect.DeepEqual(normalized.Outs[k], expected[i].Outs[k]) {
					continue
				}
				opJSON, _ := json.Marshal(scenario.Ops[:k+1])
				_ = op
				for key, value := range normalized.Outs[k] {
					if !reflect.DeepEqual(value, expected[i].Outs[k][key]) {
						g, _ := json.Marshal(value)
						e, _ := json.Marshal(expected[i].Outs[k][key])
						report += fmt.Sprintf("\n  ops %.600s\n  op %d %s differs\n  Pig %.900s\n  Pi  %.900s", opJSON, k, key, g, e)
						if key == "state" {
							gm, em := value.(map[string]any), expected[i].Outs[k][key].(map[string]any)
							for field := range gm {
								if !reflect.DeepEqual(gm[field], em[field]) {
									gf, _ := json.Marshal(gm[field])
									ef, _ := json.Marshal(em[field])
									report = strings.Split(report, "\n  Pig")[0] + fmt.Sprintf("\n  field %s\n  Pig %.500s\n  Pi  %.500s", field, gf, ef)
									break
								}
							}
						}
						break
					}
				}
				break
			}
		}
		t.Error(report)
	}
	if failures > 5 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}

// excludeOption is the oracle script's options argument: `op.exclude ? { excludeFromContext: true } : undefined` (testdata/agent_session.mjs), so a false scenario value passes no option and the recorded message has no excludeFromContext member.
func excludeOption(exclude bool) *bool {
	if !exclude {
		return nil
	}
	return &exclude
}

// normalizedHTMLExport is the export with its embedded session data decoded: ids and timestamps become indices, the working directory a placeholder, and the live system prompt and tool schemas (documentation paths and tool sets that differ by design, D22) are dropped; the rest of the page is compared byte for byte.
func normalizedHTMLExport(t *testing.T, path, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const opening = `<script id="session-data" type="application/json">`
	html := string(data)
	start := strings.Index(html, opening)
	end := strings.Index(html[start:], "</script>")
	if start < 0 || end < 0 {
		t.Fatal("no session data in the export")
	}
	encoded := html[start+len(opening) : start+end]
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	delete(decoded, "systemPrompt")
	delete(decoded, "tools")
	entries, _ := decoded["entries"].([]any)
	ids := map[string]int{}
	for i, entry := range entries {
		if fields, ok := entry.(map[string]any); ok {
			if id, ok := fields["id"].(string); ok {
				ids[id] = i
			}
		}
	}
	index := func(id any) any {
		text, ok := id.(string)
		if !ok {
			return nil
		}
		if position, found := ids[text]; found {
			return position
		}
		return "?"
	}
	normalized := []any{}
	for _, entry := range entries {
		fields, _ := entry.(map[string]any)
		parent := index(fields["parentId"])
		delete(fields, "id")
		delete(fields, "parentId")
		delete(fields, "timestamp")
		if message, ok := fields["message"].(map[string]any); ok {
			delete(message, "timestamp")
		}
		fields["parent"] = parent
		normalized = append(normalized, fields)
	}
	decoded["entries"] = normalized
	decoded["leafId"] = index(decoded["leafId"])
	if header, ok := decoded["header"].(map[string]any); ok {
		delete(header, "id")
		delete(header, "timestamp")
		delete(header, "cwd")
	}
	page := strings.ReplaceAll(strings.Replace(html, encoded, "<DATA>", 1), dir, "<cwd>")
	// The template script is Pi's file (TestExportHTMLPinnedAssets pins its hash); everything around it is compared byte for byte.
	const scriptOpen, scriptClose = "\n  <script>\n", "\n  </script>"
	if open := strings.LastIndex(page, scriptOpen); open >= 0 {
		if closing := strings.Index(page[open:], scriptClose); closing >= 0 {
			page = page[:open+len(scriptOpen)] + "<TEMPLATE_JS>" + page[open+closing:]
		}
	}
	return map[string]any{"html": page, "data": decoded}
}
