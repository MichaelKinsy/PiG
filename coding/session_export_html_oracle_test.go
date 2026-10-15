package coding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type exportHTMLScenario struct {
	Dir           string `json:"dir"`
	Theme         string `json:"theme"`
	SettingsTheme string `json:"settingsTheme"`
	ViaServices   bool   `json:"viaServices"`
}

// exportHTMLSessionFile is a scripted session log: user, assistant (text, thinking, tool calls), tool result, custom and bash-like messages, compaction, branch summary, model and thinking changes, labels and names, with ids and timestamps fixed so both sides read the same file.
func exportHTMLSessionFile(rng *rand.Rand, dir string) string {
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	lines := []string{fmt.Sprintf(`{"type":"session","version":3,"id":"sess-%d","timestamp":"2025-01-02T03:04:05.000Z","cwd":%q}`, rng.IntN(1000), dir)}
	var ids []string
	parent := "null"
	add := func(kind, body string) {
		id := fmt.Sprintf("e%07x", len(ids)+1)
		line := fmt.Sprintf(`{"type":%q,"id":%q,"parentId":%s,"timestamp":"2025-01-02T03:04:%02d.000Z",%s}`, kind, id, parent, 6+len(ids)%50, body)
		lines = append(lines, line)
		ids = append(ids, id)
		if rng.IntN(7) != 0 {
			parent = fmt.Sprintf("%q", id)
		}
	}
	texts := []string{"hello", "Ünï 日本語 😀", "line1\\nline2", "<b>html</b> & \\\"quotes\\\"", "```go\\nfunc main() {}\\n```", "", "</script><script>alert(1)</script>", "\\u2028 sep"}
	for range 1 + rng.IntN(14) {
		switch rng.IntN(13) {
		case 0, 1, 2:
			add("message", fmt.Sprintf(`"message":{"role":"user","content":[{"type":"text","text":"%s"}],"timestamp":1735787045000}`, pick(texts)))
		case 3, 4, 5:
			call := ""
			if rng.IntN(2) == 0 {
				call = fmt.Sprintf(`,{"type":"toolCall","id":"call-%d","name":"%s","arguments":{"path":"a.txt","command":"echo hi"}}`, rng.IntN(5), pick([]string{"read", "bash", "edit", "custom_tool"}))
			}
			add("message", fmt.Sprintf(`"message":{"role":"assistant","content":[{"type":"thinking","thinking":"%s"},{"type":"text","text":"%s"}%s],"api":"anthropic-messages","provider":"anthropic","model":"claude-sonnet-4-5","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":15,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"%s","timestamp":1735787046000}`, pick(texts), pick(texts), call, pick([]string{"stop", "toolUse", "error"})))
		case 6:
			add("message", fmt.Sprintf(`"message":{"role":"toolResult","toolCallId":"call-%d","toolName":"%s","content":[{"type":"text","text":"%s"}],"isError":%v,"timestamp":1735787047000}`, rng.IntN(5), pick([]string{"read", "bash", "custom_tool"}), pick(texts), rng.IntN(2) == 0))
		case 7:
			add("model_change", `"provider":"anthropic","modelId":"claude-sonnet-4-5"`)
		case 8:
			add("thinking_level_change", fmt.Sprintf(`"thinkingLevel":"%s"`, pick([]string{"off", "low", "high"})))
		case 9:
			add("custom_message", fmt.Sprintf(`"customType":"note","content":"%s","display":%v`, pick(texts), rng.IntN(2) == 0))
		case 10:
			if len(ids) > 0 {
				add("compaction", fmt.Sprintf(`"summary":"%s","firstKeptEntryId":%q,"tokensBefore":1234`, pick(texts), ids[rng.IntN(len(ids))]))
			}
		case 11:
			if len(ids) > 0 {
				add("label", fmt.Sprintf(`"targetId":%q,"label":"%s"`, ids[rng.IntN(len(ids))], pick([]string{"L1", "Ünï"})))
			}
		default:
			add("session_info", fmt.Sprintf(`"name":"%s"`, pick([]string{"named", "Ünï 日本"})))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// AgentSession.exportToHtml (agent-session.ts) against pinned Pi over 150 scripted session files: the page (template, theme variables, scripts) byte for byte, and the embedded session data (header, entries, leaf) with ids and timestamps turned into indices. The live system prompt and tool schemas differ by design (D22, tool sets) and are dropped from the data. Themes: none, dark, light, unknown, and a settings theme.
func TestSessionExportToHTMLMatchesPi(t *testing.T) {
	root := t.TempDir()
	rng := rand.New(rand.NewPCG(19, 37))
	var scenarios []exportHTMLScenario
	for i := range 150 {
		dir := filepath.Join(root, fmt.Sprint(i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "source.jsonl"), []byte(exportHTMLSessionFile(rng, dir)), 0o600); err != nil {
			t.Fatal(err)
		}
		scenario := exportHTMLScenario{Dir: dir, ViaServices: rng.IntN(2) == 0, Theme: []string{"", "", "dark", "light", "nope"}[rng.IntN(5)]}
		if rng.IntN(4) == 0 {
			scenario.SettingsTheme = []string{"light", "dark", "nope"}[rng.IntN(3)]
		}
		scenarios = append(scenarios, scenario)
	}
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/session_export_html.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []map[string]any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	exported, mismatches := 0, 0
	for i, scenario := range scenarios {
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: scenario.Dir, AgentDir: scenario.Dir})
		if err != nil {
			t.Fatal(err)
		}
		if err := services.ModelRuntime().SetRuntimeApiKey(t.Context(), "anthropic", "k1"); err != nil {
			t.Fatal(err)
		}
		if scenario.SettingsTheme != "" {
			if err := services.SettingsManager().UpdateGlobal(func(s *Settings) { s.Theme = scenario.SettingsTheme }); err != nil {
				t.Fatal(err)
			}
		}
		model, err := BuildModel("anthropic/claude-sonnet-4-5", services)
		if err != nil {
			t.Fatal(err)
		}
		sessionFile := filepath.Join(scenario.Dir, "go-session.jsonl")
		data, err := os.ReadFile(filepath.Join(scenario.Dir, "source.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sessionFile, data, 0o600); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		manager, err := SessionManagerFor(services, SessionStartOptions{ResumePath: sessionFile})
		var created CreateAgentSessionResult
		if err == nil {
			// Pi's createAgentSession over the opened session log, as the oracle script calls it.
			if scenario.ViaServices {
				created, err = CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, SessionManager: manager, Model: model, NoTools: "all"})
			} else {
				created, err = CreateAgentSession(CreateAgentSessionOptions{CWD: scenario.Dir, AgentDir: scenario.Dir, ModelRuntime: services.ModelRuntime(), SettingsManager: services.SettingsManager(), SessionManager: manager, Model: model, NoTools: "all"})
			}
		}
		if err == nil {
			var written string
			written, err = created.Session.ExportToHTML(filepath.Join(scenario.Dir, "go-export.html"), ExportToHTMLOptions{ThemeName: scenario.Theme})
			if err == nil {
				got = normalizedHTMLExport(t, written, scenario.Dir)
			}
			_ = created.Session.Close()
		}
		if err != nil {
			got = map[string]any{"thrown": strings.ReplaceAll(err.Error(), scenario.Dir, "<cwd>")}
		}
		services.Close()
		if _, ok := want[i]["html"]; ok {
			exported++
		}
		if !reflect.DeepEqual(roundTripJSON(t, got), want[i]) {
			if mismatches++; mismatches <= 3 {
				t.Errorf("scenario %d (theme %q, settings %q): %s", i, scenario.Theme, scenario.SettingsTheme, describeExportDiff(roundTripJSON(t, got), want[i]))
			}
		}
	}
	if mismatches > 3 {
		t.Errorf("%d of %d exports differ from Pi", mismatches, len(scenarios))
	}
	if exported < 100 {
		t.Errorf("only %d of %d exports succeeded in Pi", exported, len(scenarios))
	}
}

func roundTripJSON(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func describeExportDiff(got, want map[string]any) string {
	if got["thrown"] != nil || want["thrown"] != nil {
		return fmt.Sprintf("Go %v, Pi %v", got["thrown"], want["thrown"])
	}
	gotHTML, _ := got["html"].(string)
	wantHTML, _ := want["html"].(string)
	if gotHTML != wantHTML {
		at := 0
		for at < len(gotHTML) && at < len(wantHTML) && gotHTML[at] == wantHTML[at] {
			at++
		}
		clip := func(s string) string { return s[max(0, at-40):min(len(s), at+60)] }
		return fmt.Sprintf("page differs at byte %d: Go %q, Pi %q (%d vs %d bytes)", at, clip(gotHTML), clip(wantHTML), len(gotHTML), len(wantHTML))
	}
	kinds := func(value map[string]any) string {
		data, _ := value["data"].(map[string]any)
		entries, _ := data["entries"].([]any)
		var out []string
		for _, entry := range entries {
			fields, _ := entry.(map[string]any)
			label, _ := fields["type"].(string)
			if level, ok := fields["thinkingLevel"].(string); ok {
				label += ":" + level
			}
			out = append(out, label)
		}
		return strings.Join(out, ",")
	}
	if kinds(got) != kinds(want) {
		return fmt.Sprintf("entries differ:\n Go %s\n Pi %s", kinds(got), kinds(want))
	}
	gotJSON, _ := json.Marshal(got["data"])
	wantJSON, _ := json.Marshal(want["data"])
	return fmt.Sprintf("session data differs:\n Go %.700s\n Pi %.700s", gotJSON, wantJSON)
}
