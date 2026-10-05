package coding

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
)

// Ports packages/coding-agent/test/suite/agent-session-codemode.test.ts (Pi 1.0.0, 21 cases: 19 here, and 2 session-free cases plus the session-free half of "declares models only for the session's own codemode tool" in coding/extension/builtin/codemode/session_free_upstream_test.go). The built-in codemode
// extension is Pi's own code in the Node cell (docs/specs/builtin-codemode-tool-search.md); the Session, the nested-tool
// pipeline (ctx.executeTool), tool exposure and the settings, classifier and usage plumbing are other families'.

const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="

var tinyPNGLabel = regexp.MustCompile(`^\[Image saved to (\S+\.png) \(image/png, \d+B\)\]$`)

// checkSavedImages is upstream's checkSavedImages: it replaces the `[Image saved to ...]` labels in text with `<saved>`
// after checking that each file holds the tiny PNG, and removes the files.
func checkSavedImages(t *testing.T, text string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		match := tinyPNGLabel.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		data, err := os.ReadFile(match[1])
		if err != nil {
			t.Fatal(err)
		}
		if got := base64.StdEncoding.EncodeToString(data); got != tinyPNGBase64 {
			t.Errorf("saved image %s = %q, want the tiny PNG", match[1], got)
		}
		if err := os.Remove(match[1]); err != nil {
			t.Fatal(err)
		}
		lines[i] = "<saved>"
	}
	return strings.Join(lines, "\n")
}

func codemodeCall(code string) scriptedResponse {
	return boundaryToolReply("codemode", ai.JsonObject{"code": code}, ai.StopReasonToolUse)
}

func codemodeDone() scriptedResponse { return boundaryReply("done", ai.StopReasonStop, 0) }

// codemodeResult is upstream's getToolResult(harness, "codemode").
func codemodeResult(t *testing.T, h *codemodeHarness) agent.ToolResultMessage {
	t.Helper()
	messages := h.session.Messages()
	for _, message := range slices.Backward(messages) {
		if result := message.ToolResult; result != nil && result.ToolName == "codemode" {
			return *result
		}
	}
	t.Fatal("no codemode tool result")
	return agent.ToolResultMessage{}
}

var scriptHeader = regexp.MustCompile(`^Script (completed|failed)\nWall time \d+\.\d seconds\nOutput:\n$`)

// codemodeResultText is upstream's resultText: the output after the script header, which is checked on the way.
func codemodeResultText(t *testing.T, message agent.ToolResultMessage) string {
	t.Helper()
	if len(message.Content) == 0 {
		t.Fatal("empty tool result")
	}
	header, ok := message.Content[0].(ai.TextContent)
	if !ok || !scriptHeader.MatchString(header.Text) {
		t.Fatalf("header = %#v", message.Content[0])
	}
	var items []string
	for _, block := range message.Content[1:] {
		if text, ok := block.(ai.TextContent); ok {
			items = append(items, text.Text)
		} else {
			items = append(items, "<image>")
		}
	}
	return strings.Join(items, "\n")
}

type codemodeNestedCall struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Args     string   `json:"args"`
	Status   string   `json:"status"`
	Error    string   `json:"error"`
	Cost     *float64 `json:"cost"`
	Duration *float64 `json:"durationMs"`
}

type codemodeDetails struct {
	Calls          []codemodeNestedCall `json:"calls"`
	FullOutputPath string               `json:"fullOutputPath"`
}

func codemodeDetailsOf(t *testing.T, message agent.ToolResultMessage) codemodeDetails {
	t.Helper()
	raw, err := json.Marshal(message.Details)
	if err != nil {
		t.Fatal(err)
	}
	var details codemodeDetails
	if err := json.Unmarshal(raw, &details); err != nil {
		t.Fatal(err)
	}
	return details
}

func codemodeRun(t *testing.T, h *codemodeHarness, code string) agent.ToolResultMessage {
	t.Helper()
	h.provider.mu.Lock()
	h.provider.responses = append(h.provider.responses, codemodeCall(code), codemodeDone())
	h.provider.mu.Unlock()
	boundaryPrompt(t, h.recoveryHarness, "go")
	return codemodeResult(t, h)
}

func codemodeUsage(input int, cost float64) ai.Usage {
	return ai.Usage{Input: input, TotalTokens: input, Cost: ai.UsageCost{Input: cost, Total: cost}}
}

func TestUpstreamAgentSessionCodemodeTool(t *testing.T) {
	setup := func(t *testing.T, fixtures ...string) *codemodeHarness {
		t.Helper()
		return newCodemodeHarness(t, codemodeHarnessOptions{fixtures: append([]string{"nested-tools"}, fixtures...), activeTools: []string{"codemode"}})
	}

	t.Run("presents callable tools per codemode.mode", func(t *testing.T) {
		h := setup(t)
		description := func(name string) string {
			for _, tool := range h.session.Tools() {
				if tool.Name() == name {
					return tool.Schema().Description
				}
			}
			return ""
		}
		var requestTools [][]string
		var requestPrompts []string
		record := func(messages []ai.Message) *ai.AssistantMessage {
			names := []string{}
			for _, tool := range ai.GetCurrentTools(messages) {
				names = append(names, tool.Name)
			}
			requestTools = append(requestTools, names)
			requestPrompts = append(requestPrompts, ai.GetCurrentSystemPrompt(messages))
			return boundaryReply("ok", ai.StopReasonStop, 0)(messages)
		}
		contains := func(list []string, name string) bool {
			return slices.Contains(list, name)
		}

		// on: declared tools say how scripts call them and are not listed again in codemode.
		h.session.SetActiveToolsByName([]string{"read", "echo", "codemode"})
		if !strings.Contains(description("echo"), "Codemode: `tools.echo(args)` resolves to") {
			t.Errorf("echo description = %q", description("echo"))
		}
		if strings.Contains(description("echo"), "codemode tool declaration:") {
			t.Errorf("echo description repeats the codemode declaration: %q", description("echo"))
		}
		if strings.Contains(description("codemode"), "### `echo`") {
			t.Error("codemode lists echo in mode on")
		}
		h.provider.responses = append(h.provider.responses, record)
		boundaryPrompt(t, h.recoveryHarness, "on")
		if !contains(requestTools[0], "read") || !contains(requestTools[0], "echo") || !contains(requestTools[0], "codemode") {
			t.Errorf("request tools = %v", requestTools[0])
		}
		if !strings.Contains(requestPrompts[0], "\n- read: ") {
			t.Errorf("prompt lacks the read tool: %q", requestPrompts[0])
		}

		// only: codemode lists echo, which stays active but is left out of requests.
		h.setCodemodeMode(t, "only")
		h.session.SetActiveToolsByName([]string{"read", "echo", "codemode"})
		if strings.Contains(description("echo"), "Codemode: `tools.echo") {
			t.Error("echo says how scripts call it in mode only")
		}
		if !strings.Contains(description("codemode"), "### `echo`") || strings.Contains(description("codemode"), "### `stats`") {
			t.Errorf("codemode description = %q", description("codemode"))
		}
		h.provider.responses = append(h.provider.responses, record)
		boundaryPrompt(t, h.recoveryHarness, "only")
		if !contains(requestTools[1], "codemode") || contains(requestTools[1], "echo") || contains(requestTools[1], "read") {
			t.Errorf("request tools = %v", requestTools[1])
		}
		// The prompt's tool list matches the declarations: hidden tools are not listed (#10192).
		if strings.Contains(requestPrompts[1], "\n- read: ") || !strings.Contains(requestPrompts[1], "\n- codemode: ") || strings.Contains(h.session.SystemPrompt(), "\n- read: ") {
			t.Errorf("prompt = %q, session prompt = %q", requestPrompts[1], h.session.SystemPrompt())
		}

		// Without codemode, tools keep their plain descriptions.
		h.session.SetActiveToolsByName([]string{"echo"})
		if got := description("echo"); got != "Echo text back.\n\nSecond paragraph." {
			t.Errorf("echo description = %q", got)
		}
	})

	t.Run("runs nested calls in parallel and returns only the script result", func(t *testing.T) {
		h := setup(t)
		result := codemodeRun(t, h, `
							const [a, b, stats] = await Promise.all([
								tools.echo({ text: "one" }),
								tools.echo({ text: "two" }),
								tools.stats({}),
							]);
							console.log("files", stats.files);
							text(ALL_TOOLS.map((tool) => tool.name).join(","));
							return { a, b, names: stats.names };
						`)
		if result.IsError {
			t.Error("isError")
		}
		if got, want := codemodeResultText(t, result), `files 2`+"\n"+`echo,stats,screenshot`+"\n"+`{"a":"echo: one","b":"echo: two","names":["a","b"]}`; got != want {
			t.Errorf("result = %q, want %q", got, want)
		}
		details := codemodeDetailsOf(t, result)
		var pairs [][2]string
		for _, call := range details.Calls {
			pairs = append(pairs, [2]string{call.Name, call.Status})
			if !strings.HasPrefix(call.ID, result.ToolCallID+"/") {
				t.Errorf("nested call id %q does not start with %q", call.ID, result.ToolCallID+"/")
			}
		}
		if want := [][2]string{{"echo", "ok"}, {"echo", "ok"}, {"stats", "ok"}}; !reflect.DeepEqual(pairs, want) {
			t.Errorf("calls = %v, want %v", pairs, want)
		}
		// Nested calls never become transcript tool results; their events carry the parent id.
		results := 0
		for _, message := range h.session.Messages() {
			if message.ToolResult != nil {
				results++
			}
		}
		if results != 1 {
			t.Errorf("transcript tool results = %d, want 1", results)
		}
	})

	t.Run("routes nested calls through extension hooks", func(t *testing.T) {
		h := setup(t, "forbidden-echo-hooks")
		result := codemodeRun(t, h, `
							let blocked;
							try {
								await tools.echo({ text: "forbidden" });
							} catch (error) {
								blocked = error.message;
							}
							const stats = await tools.stats({});
							return { blocked, stats };
						`)
		// Replacing content without replacing structured content drops the structured result.
		var value map[string]any
		if err := json.Unmarshal([]byte(codemodeResultText(t, result)), &value); err != nil {
			t.Fatal(err)
		}
		if want := map[string]any{"blocked": "echo of forbidden text is blocked", "stats": "redacted"}; !reflect.DeepEqual(value, want) {
			t.Errorf("value = %v, want %v", value, want)
		}
		var statuses []string
		for _, call := range codemodeDetailsOf(t, result).Calls {
			statuses = append(statuses, call.Status)
		}
		if want := []string{"error", "ok"}; !reflect.DeepEqual(statuses, want) {
			t.Errorf("statuses = %v, want %v", statuses, want)
		}
	})

	t.Run("adds the usage of nested results to the codemode result", func(t *testing.T) {
		h := setup(t, "billed-tool")
		result := codemodeRun(t, h, `await tools.billed({}); await tools.billed({}); await tools.echo({ text: "x" });`)
		if result.Usage == nil || result.Usage.Input != 200 || result.Usage.TotalTokens != 200 || result.Usage.Cost.Total != 0.5 {
			t.Fatalf("usage = %+v, want input 200, total tokens 200, cost 0.5", result.Usage)
		}
		// The usage is persisted with the result, so session totals count it.
		var persisted *ai.Usage
		for _, entry := range h.entries("message") {
			var fields struct {
				Message struct {
					Role  string    `json:"role"`
					Usage *ai.Usage `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal(entry.Raw(), &fields); err != nil {
				t.Fatal(err)
			}
			if fields.Message.Role == "toolResult" {
				persisted = fields.Message.Usage
				break
			}
		}
		if persisted == nil || !reflect.DeepEqual(*persisted, *result.Usage) {
			t.Errorf("persisted usage = %+v, want %+v", persisted, result.Usage)
		}
		if got := h.session.GetSessionStats().Cost; got != 0.5 {
			t.Errorf("session cost = %v, want 0.5", got)
		}
	})

	t.Run("keeps structured content that tool_result handlers replace along with the content", func(t *testing.T) {
		h := setup(t, "structured-result-hooks")
		result := codemodeRun(t, h, "return await tools.stats({});")
		var value map[string]any
		if err := json.Unmarshal([]byte(codemodeResultText(t, result)), &value); err != nil {
			t.Fatal(err)
		}
		if want := map[string]any{"files": float64(0), "names": []any{}}; !reflect.DeepEqual(value, want) {
			t.Errorf("value = %v, want %v", value, want)
		}
	})

	// Saved images: https://github.com/earendil-works/pi/issues/10310
	t.Run("attaches only the images the script passes to image(), in output order, each after its saved path", func(t *testing.T) {
		h := setup(t)
		result := codemodeRun(t, h, `
							// Tools without an outputSchema resolve to their text; images are not passed on.
							const shot = await tools.screenshot({});
							text(shot);
							image("data:image/png;base64,`+tinyPNGBase64+`");
							image("data:image/png;base64,`+tinyPNGBase64+`");
							text("after");
						`)
		// The same image shown twice is saved once, so both labels name one file.
		lines := strings.Split(codemodeResultText(t, result), "\n")
		if want := []string{"captured", lines[1], "<image>", lines[1], "<image>", "after"}; !slices.Equal(lines, want) || !tinyPNGLabel.MatchString(lines[1]) {
			t.Fatalf("result lines = %q, want %q", lines, want)
		}
		if got := checkSavedImages(t, lines[1]); got != "<saved>" {
			t.Errorf("saved label = %q", got)
		}
		if image, ok := result.Content[3].(ai.ImageContent); !ok || image.Data != tinyPNGBase64 || image.MimeType != "image/png" {
			t.Errorf("content[3] = %#v", result.Content[3])
		}
	})

	t.Run("reports script failures as results that keep partial output and the calls that ran", func(t *testing.T) {
		h := setup(t)
		result := codemodeRun(t, h, "text(\"partial\");\nawait tools.echo({ text: \"x\" });\nthrow new Error(\"boom\");")
		if !result.IsError {
			t.Error("isError = false")
		}
		if header, _ := result.Content[0].(ai.TextContent); !strings.HasPrefix(header.Text, "Script failed\n") {
			t.Errorf("header = %q", header.Text)
		}
		text := codemodeResultText(t, result)
		if !regexp.MustCompile(`^partial\nScript error:\nError: boom\n`).MatchString(text) {
			t.Errorf("text = %q", text)
		}
		for _, want := range []string{"codemode.js:3", "Tool calls made before the failure (they are not undone): echo (ok)"} {
			if !strings.Contains(text, want) {
				t.Errorf("text %q lacks %q", text, want)
			}
		}
		var names []string
		for _, call := range codemodeDetailsOf(t, result).Calls {
			names = append(names, call.Name)
		}
		if !reflect.DeepEqual(names, []string{"echo"}) {
			t.Errorf("calls = %v", names)
		}
	})
}

func TestUpstreamCodemodeOptionsAndStore(t *testing.T) {
	// No tools override: the session builds its own codemode tool, including the store writer.
	setup := func(t *testing.T) *codemodeHarness {
		t.Helper()
		return newCodemodeHarness(t, codemodeHarnessOptions{activeTools: []string{"codemode"}})
	}
	storeEntries := func(t *testing.T, h *codemodeHarness) []any {
		t.Helper()
		var out []any
		for _, entry := range h.session.Inner().GetBranch() {
			var fields struct {
				Type       string `json:"type"`
				CustomType string `json:"customType"`
				Data       any    `json:"data"`
			}
			if err := json.Unmarshal(entry.Raw(), &fields); err != nil {
				t.Fatal(err)
			}
			if fields.Type == "custom" && fields.CustomType == "codemode-store" {
				out = append(out, fields.Data)
			}
		}
		return out
	}
	const increment = "const next = (load(\"count\") ?? 0) + 1;\nstore(\"count\", next);\nreturn next;"

	t.Run("applies the timeout_ms option and rejects invalid options", func(t *testing.T) {
		h := setup(t)
		timedOut := codemodeRun(t, h, "// @options: {\"timeout_ms\": 200}\nwhile (true) {}")
		if !timedOut.IsError || !strings.Contains(codemodeResultText(t, timedOut), "Script error:\nScript timed out") {
			t.Errorf("timed out result = %#v", timedOut)
		}
		invalid := codemodeRun(t, h, "// @options: {\"yield\": 1}\ntext(1)")
		want := []ai.ToolResultMessageContent{ai.TextContent{Text: "@options only supports `max_output_tokens` and `timeout_ms`; got `yield`"}}
		if !invalid.IsError || !reflect.DeepEqual(invalid.Content, want) {
			t.Errorf("invalid options result = %#v", invalid)
		}
	})

	t.Run("limits script memory so runaway allocations fail inside the script", func(t *testing.T) {
		h := setup(t)
		result := codemodeRun(t, h, "// @options: {\"timeout_ms\": 30000}\nlet a = [];\ntry { while (true) a.push(\"x\".repeat(1 << 20) + a.length); } catch (error) { const n = a.length; a = null; return { n, error: String(error) }; }")
		if result.IsError {
			t.Fatalf("isError: %s", codemodeResultText(t, result))
		}
		var value struct {
			N     int    `json:"n"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(codemodeResultText(t, result)), &value); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(value.Error, "out of memory") {
			t.Errorf("error = %q", value.Error)
		}
		// Each entry holds at least 1 MiB, so the limit stops the script well before wasm32's 4 GiB.
		if value.N >= 512 {
			t.Errorf("n = %d, want fewer than 512", value.N)
		}
	})

	t.Run("truncates output to the token budget and spills the full text", func(t *testing.T) {
		h := setup(t)
		result := codemodeRun(t, h, "// @options: {\"max_output_tokens\": 10}\nfor (let i = 0; i < 100; i++) text(\"row \" + i);\nimage(\"data:image/png;base64,"+tinyPNGBase64+"\");")
		path := codemodeDetailsOf(t, result).FullOutputPath
		if path == "" {
			t.Fatal("No spill file")
		}
		defer os.Remove(path)
		text := codemodeResultText(t, result)
		if !strings.HasPrefix(text, "Warning: truncated output") {
			t.Errorf("text = %.60q", text)
		}
		for _, want := range []string{"row 0\n", "tokens truncated", "row 99\n", "[Full output: " + path + " (read with offset/limit)]"} {
			if !strings.Contains(text, want) {
				t.Errorf("text lacks %q", want)
			}
		}
		if strings.Contains(text, "row 50\n") {
			t.Error("text keeps row 50")
		}
		// Images follow the truncated text, each after the path it was saved to.
		if got := checkSavedImages(t, strings.Split(text, "\n")[len(strings.Split(text, "\n"))-2]); got != "<saved>" {
			t.Errorf("label before the image = %q", got)
		}
		if last, ok := result.Content[len(result.Content)-1].(ai.ImageContent); !ok || last.Data != tinyPNGBase64 || last.MimeType != "image/png" {
			t.Errorf("last block = %#v", result.Content[len(result.Content)-1])
		}
		if runtime.GOOS != "windows" {
			if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0o077 != 0 {
				t.Errorf("spill file %s: %v, %v, want user-only", path, info, err)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for i := range 100 {
			want = append(want, "row "+strconv.Itoa(i))
		}
		if string(data) != strings.Join(want, "\n") {
			t.Errorf("spill file = %.80q", data)
		}

		small := codemodeRun(t, h, "return { ok: true };")
		if got := codemodeDetailsOf(t, small).FullOutputPath; got != "" {
			t.Errorf("fullOutputPath = %q for a small result", got)
		}
	})

	t.Run("resolves bash calls to structured results, also for non-zero exit codes", func(t *testing.T) {
		h := newCodemodeHarness(t, codemodeHarnessOptions{activeTools: []string{"codemode", "bash"}})
		result := codemodeRun(t, h, "const r = await tools.bash({ command: \"echo out; exit 3\" });\ntext(JSON.stringify([r.output, r.exit_code, typeof r.wall_time_seconds]));")
		if result.IsError {
			t.Fatalf("isError: %s", codemodeResultText(t, result))
		}
		if got, want := codemodeResultText(t, result), `["out\n",3,"number"]`; got != want {
			t.Errorf("result = %q, want %q", got, want)
		}
	})

	t.Run("persists store() writes as custom entries for later calls", func(t *testing.T) {
		h := setup(t)
		if got := codemodeResultText(t, codemodeRun(t, h, increment)); got != "1" {
			t.Errorf("first run = %q", got)
		}
		if got := codemodeResultText(t, codemodeRun(t, h, increment)); got != "2" {
			t.Errorf("second run = %q", got)
		}
		want := []any{
			map[string]any{"set": map[string]any{"count": float64(1)}, "delete": []any{}},
			map[string]any{"set": map[string]any{"count": float64(2)}, "delete": []any{}},
		}
		if got := storeEntries(t, h); !reflect.DeepEqual(got, want) {
			t.Errorf("store entries = %v, want %v", got, want)
		}
		appended := 0
		for _, event := range h.settle(t) {
			if entry, ok := event.(agent.EntryAppendedEvent); ok {
				var fields struct {
					Type       string `json:"type"`
					CustomType string `json:"customType"`
				}
				if err := json.Unmarshal(entry.Entry, &fields); err != nil {
					t.Fatal(err)
				}
				if fields.Type == "custom" && fields.CustomType == "codemode-store" {
					appended++
				}
			}
		}
		if appended != 2 {
			t.Errorf("entry_appended custom codemode-store events = %d, want 2", appended)
		}

		if got := codemodeResultText(t, codemodeRun(t, h, "store(\"count\", undefined);\nreturn load(\"count\") === undefined;")); got != "true" {
			t.Errorf("delete run = %q", got)
		}
		entries := storeEntries(t, h)
		if last := entries[len(entries)-1]; !reflect.DeepEqual(last, map[string]any{"set": map[string]any{}, "delete": []any{"count"}}) {
			t.Errorf("last store entry = %v", last)
		}
	})

	t.Run("appends nothing for failed scripts or scripts without writes", func(t *testing.T) {
		h := setup(t)
		if !codemodeRun(t, h, "store(\"count\", 5);\nthrow new Error(\"boom\");").IsError {
			t.Error("failed script is not an error result")
		}
		if codemodeRun(t, h, "return load(\"count\") ?? \"missing\";").IsError {
			t.Error("read-only script is an error result")
		}
		if got := storeEntries(t, h); len(got) != 0 {
			t.Errorf("store entries = %v, want none", got)
		}
	})

	t.Run("loads the values written on the current branch", func(t *testing.T) {
		h := setup(t)
		codemodeRun(t, h, increment)
		var firstPrompt string
		for _, entry := range h.session.Inner().GetBranch() {
			if entry.Base.Type == "message" {
				firstPrompt = entry.Base.ID
				break
			}
		}
		if firstPrompt == "" {
			t.Fatal("No first prompt entry")
		}
		if got := codemodeResultText(t, codemodeRun(t, h, increment)); got != "2" {
			t.Errorf("second run = %q", got)
		}

		// Branch from the first prompt: the store entries written after it are on another path.
		// upstream: harness.sessionManager.branch(firstPrompt.id) moves the leaf (session-manager.ts:1579-1584); Go's Branch only reads the path.
		if err := h.session.Inner().SetLeafID(&firstPrompt); err != nil {
			t.Fatal(err)
		}
		if got := codemodeResultText(t, codemodeRun(t, h, increment)); got != "1" {
			t.Errorf("branched run = %q", got)
		}
	})

}

// setupWithImages is the "codemode models" setup (agent-session-codemode.test.ts:580-649, v1.0.0): a Session with the
// built-in codemode extension and the scorer provider's classifier and image models.
func setupWithImages(t *testing.T) (*codemodeHarness, func() []classifierCall, func() int, func() []imagesCall) {
	t.Helper()
	h := newCodemodeHarness(t, codemodeHarnessOptions{activeTools: []string{"codemode"}})
	observed, maxActive, imageRequests := h.registerScorerProvider(t)
	h.session.SetActiveToolsByName([]string{"codemode"})
	return h, observed, maxActive, imageRequests
}

func TestUpstreamCodemodeModels(t *testing.T) {
	const questions = `{ approved: { type: "bool", instructions: "Approval?", criteria: { true: "yes", false: "no" } } }`
	setup := func(t *testing.T) (*codemodeHarness, func() []classifierCall, func() int) {
		t.Helper()
		h, observed, maxActive, _ := setupWithImages(t)
		return h, observed, maxActive
	}

	t.Run("declares models only for the session's own codemode tool", func(t *testing.T) {
		h, _, _ := setup(t)
		var description string
		for _, tool := range h.session.Tools() {
			if tool.Name() == "codemode" {
				description = tool.Schema().Description
			}
		}
		// The description names the models globals and points to the docs for the API.
		if !strings.Contains(description, "`models`: classifiers and image generation") {
			t.Errorf("description lacks the models global: %q", description)
		}
		// upstream CODEMODE_DOCS_PATH is join(getDocsPath(), "codemode.md"), never empty; an empty path would make the
		// containment check below pass vacuously.
		if docs := codemode.DocsPath(); docs == "" || !strings.Contains(description, docs) {
			t.Errorf("description lacks the codemode docs path %q: %q", docs, description)
		}

		// upstream: createHarness({ tools: [createCodemodeTool() as AgentTool] }): a codemode tool created without model
		// access, with no built-in codemode extension.
		plainDefinition := codemode.Definition(codemode.Options{})
		plainExtension := extension.Extension{Name: "plain-codemode", Path: "<inline:plain-codemode>", ResolvedPath: "<inline:plain-codemode>",
			Tools: map[string]extension.RegisteredTool{plainDefinition.Name: {Definition: plainDefinition}}, ToolOrder: []string{plainDefinition.Name}}
		overridden := newCodemodeHarness(t, codemodeHarnessOptions{noExtension: true, goExtensions: []extension.Extension{plainExtension}})
		var plain string
		found := false
		for _, tool := range overridden.session.Tools() {
			if tool.Name() == "codemode" {
				plain, found = tool.Schema().Description, true
			}
		}
		if !found {
			t.Fatal("the overridden harness has no codemode tool")
		}
		if strings.Contains(plain, "`models`") {
			t.Errorf("a codemode tool created without model access names `models`: %q", plain)
		}
	})

	t.Run("lists models and classifies with catalog auth, ignoring script-supplied fields", func(t *testing.T) {
		h, observed, maxActive := setup(t)
		result := codemodeRun(t, h, `
			const [model] = await models.getAvailableOfType("classifier", "scorer");
			const listed = await models.getModelsOfType("classifier");
			const same = await models.getModelOfType("classifier", "scorer", "judge");
			const texts = ["good", "bad", "good", "bad", "good", "bad"];
			const results = await Promise.all(
				texts.map((text) => models.classify({ ...model, baseUrl: "https://evil.test" }, { state: { text }, questions: `+questions+` })),
			);
			return {
				id: model.id,
				headers: "headers" in model,
				listed: listed.some((entry) => entry.provider === "scorer" && entry.id === "judge"),
				same: same.id,
				missing: (await models.getModelOfType("classifier", "scorer", "nope")) === undefined,
				probabilities: results.map((r) => r.answers.approved.probability),
				cost: results[0].usage.cost.total,
			};
		`)
		if result.IsError {
			t.Fatalf("isError: %s", codemodeResultText(t, result))
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(codemodeResultText(t, result)), &value); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"id": "judge", "headers": false, "listed": true, "same": "judge", "missing": true,
			"probabilities": []any{0.9, 0.1, 0.9, 0.1, 0.9, 0.1}, "cost": 0.001,
		}
		if !reflect.DeepEqual(value, want) {
			t.Errorf("value = %v, want %v", value, want)
		}
		calls := observed()
		if len(calls) != 6 {
			t.Fatalf("classifier calls = %d, want 6", len(calls))
		}
		for _, call := range calls {
			if call.BaseURL != "https://classifier.test/v1" || call.APIKey != "secret-key" {
				t.Errorf("call = %+v, want the catalog base URL and key", call)
			}
		}
		// Six classifications with at most four in flight.
		if got := maxActive(); got != 4 {
			t.Errorf("max concurrent classifications = %d, want 4", got)
		}
		details := codemodeDetailsOf(t, result)
		if len(details.Calls) != 6 {
			t.Fatalf("nested calls = %d, want 6", len(details.Calls))
		}
		for _, call := range details.Calls {
			if call.Name != "models.classify" || call.Args != "scorer/judge" || call.Status != "ok" || call.Cost == nil || *call.Cost != 0.001 {
				t.Errorf("nested call = %+v", call)
			}
		}
		// The classifications' usage becomes the codemode result's usage.
		if result.Usage == nil || result.Usage.Input != 1800 || math.Abs(result.Usage.Cost.Total-0.006) > 1e-10 {
			t.Errorf("usage = %+v, want input 1800, cost 0.006", result.Usage)
		}
		if got := h.session.GetSessionStats().Cost; math.Abs(got-0.006) > 1e-10 {
			t.Errorf("session cost = %v, want 0.006", got)
		}
	})

	t.Run("generates images with catalog auth and attaches them through image()", func(t *testing.T) {
		h, _, _, imageRequests := setupWithImages(t)
		result := codemodeRun(t, h, `
			const [model] = await models.getAvailableOfType("image", "scorer");
			const reference = { type: "image", data: "`+tinyPNGBase64+`", mimeType: "image/png" };
			const generated = await models.generateImages(
				{ ...model, baseUrl: "https://evil.test" },
				{ input: [{ type: "text", text: "a fox" }, reference] },
			);
			for (const block of generated.output) {
				if (block.type === "image") image(block);
				else text(block.text);
			}
			const failed = await models.generateImages(model, { input: [{ type: "text", text: "explode" }] });
			const attempt = async (fn) => { try { await fn(); return "ok"; } catch (error) { return error.message; } };
			return {
				id: model.id,
				stopReason: generated.stopReason,
				failed: [failed.stopReason, failed.errorMessage],
				wrongType: await attempt(() => models.generateImages({ provider: "scorer", id: "judge" }, { input: [] })),
			};
		`)
		if result.IsError {
			t.Errorf("isError: %s", codemodeResultText(t, result))
		}
		lines := strings.Split(checkSavedImages(t, codemodeResultText(t, result)), "\n")
		if lines[0] != "painted a fox" {
			t.Errorf("text = %q, want %q", lines[0], "painted a fox")
		}
		if len(lines) < 3 || lines[1] != "<saved>" || lines[2] != "<image>" {
			t.Fatalf("result lines = %q, want the text, then <saved>, <image>, then the returned value", lines)
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(strings.Join(lines[3:], "\n")), &value); err != nil {
			t.Fatalf("returned value %q: %v", strings.Join(lines[3:], "\n"), err)
		}
		want := map[string]any{
			"id":         "painter",
			"stopReason": "stop",
			"failed":     []any{"error", "painter exploded"},
			"wrongType":  `"scorer/judge" is a classifier model, not an image model. List the image models you can use with models.getAvailableOfType("image").`,
		}
		if !reflect.DeepEqual(value, want) {
			t.Errorf("value = %v, want %v", value, want)
		}
		if len(result.Content) < 4 || !reflect.DeepEqual(result.Content[3], ai.ToolResultMessageContent(ai.ImageContent{Data: tinyPNGBase64, MimeType: "image/png"})) {
			t.Errorf("content = %#v, want the generated image at index 3", result.Content)
		}
		requests := imageRequests()
		var pairs [][2]string
		for _, request := range requests {
			pairs = append(pairs, [2]string{request.BaseURL, request.APIKey})
		}
		if want := [][2]string{{"https://images.test/v1", "secret-key"}, {"https://images.test/v1", "secret-key"}}; !reflect.DeepEqual(pairs, want) {
			t.Errorf("image requests = %v, want %v", pairs, want)
		}
		if len(requests) > 0 {
			want := []ai.ContentBlock{ai.TextContent{Text: "a fox"}, ai.ImageContent{Data: tinyPNGBase64, MimeType: "image/png"}}
			if !reflect.DeepEqual(requests[0].Input, want) {
				t.Errorf("first request input = %#v, want %#v", requests[0].Input, want)
			}
		}
		type row struct {
			Name, Args, Status string
			Cost               *float64
			Error              string
		}
		var rows []row
		for _, call := range codemodeDetailsOf(t, result).Calls {
			rows = append(rows, row{call.Name, call.Args, call.Status, call.Cost, call.Error})
		}
		cost := 0.04
		if want := []row{
			{"models.generateImages", "scorer/painter", "ok", &cost, ""},
			{"models.generateImages", "scorer/painter", "error", nil, "painter exploded"},
		}; !reflect.DeepEqual(rows, want) {
			t.Errorf("calls = %+v, want %+v", rows, want)
		}
		if result.Usage == nil || math.Abs(result.Usage.Cost.Total-0.04) > 1e-10 {
			t.Errorf("usage = %+v, want cost 0.04", result.Usage)
		}
		if got := h.session.GetSessionStats().Cost; math.Abs(got-0.04) > 1e-10 {
			t.Errorf("session cost = %v, want 0.04", got)
		}
	})

	t.Run("notes generated images that the script did not show", func(t *testing.T) {
		h, _, _ := setup(t)
		result := codemodeRun(t, h, `
			const [model] = await models.getAvailableOfType("image", "scorer");
			const generated = await models.generateImages(model, { input: [{ type: "text", text: "a fox" }] });
			return generated.stopReason;
		`)
		if result.IsError {
			t.Errorf("isError: %s", codemodeResultText(t, result))
		}
		if got, want := codemodeResultText(t, result), "stop\nNote: models.generateImages() returned 1 image that the script did not show. Show each image block of result.output with image(block)."; got != want {
			t.Errorf("result = %q, want %q", got, want)
		}
	})

	t.Run("reports provider errors as results and invalid arguments as exceptions", func(t *testing.T) {
		h, _, _ := setup(t)
		result := codemodeRun(t, h, `
			const model = await models.getModelOfType("classifier", "scorer", "judge");
			const failed = await models.classify(model, { state: { text: "explode" }, questions: `+questions+` });
			const attempt = async (fn) => { try { await fn(); return "ok"; } catch (error) { return error.message; } };
			return {
				failed: [failed.stopReason, failed.errorMessage],
				badType: await attempt(() => models.getModelsOfType("video")),
				unknown: await attempt(() => models.classify({ provider: "scorer", id: "nope" }, {})),
				noModel: await attempt(() => models.classify("judge", {})),
				undefinedModel: await attempt(() => models.classify(undefined, {})),
				noState: await attempt(() => models.classify(model, { questions: `+questions+` })),
				badQuestion: await attempt(() =>
					models.classify(model, { state: {}, questions: { kind: { type: "choice", instructions: "Kind?", criteria: ["a", "b"] } } }),
				),
				badImage: await attempt(() => models.generateImages({ provider: "scorer", id: "painter" }, { prompt: "a fox" })),
				badSplit: await attempt(() => models.getModelOfType("classifier", "scorer/judge")),
			};
		`)
		if result.IsError {
			t.Fatalf("isError: %s", codemodeResultText(t, result))
		}
		var value struct {
			Failed         []string `json:"failed"`
			BadType        string   `json:"badType"`
			Unknown        string   `json:"unknown"`
			NoModel        string   `json:"noModel"`
			UndefinedModel string   `json:"undefinedModel"`
			NoState        string   `json:"noState"`
			BadQuestion    string   `json:"badQuestion"`
			BadImage       string   `json:"badImage"`
			BadSplit       string   `json:"badSplit"`
		}
		if err := json.Unmarshal([]byte(codemodeResultText(t, result)), &value); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(value.Failed, []string{"error", "classifier exploded"}) {
			t.Errorf("failed = %v", value.Failed)
		}
		if !strings.Contains(value.BadType, `Unknown model type "video"`) {
			t.Errorf("badType = %q", value.BadType)
		}
		if want := `Unknown classifier model "scorer/nope". List the classifier models you can use with models.getAvailableOfType("classifier").`; value.Unknown != want {
			t.Errorf("unknown = %q, want %q", value.Unknown, want)
		}
		for _, check := range []struct{ name, got, want string }{
			{"noModel", value.NoModel, "models.classify() expects a classifier model as its first argument, got a string."},
			{"undefinedModel", value.UndefinedModel, "models.getModelOfType() returns undefined for an unknown provider or id."},
			{"noState", value.NoState, "models.classify() context.state must be an object, got undefined."},
			{"noState", value.NoState, "codemode.md"},
			{"badQuestion", value.BadQuestion, `context.questions.kind is a "choice" question, so criteria must map each label to its meaning.`},
			{"badImage", value.BadImage, "models.generateImages() context.input must be a non-empty array of blocks, got undefined."},
			{"badSplit", value.BadSplit, "The provider and the id are separate arguments"},
		} {
			if !strings.Contains(check.got, check.want) {
				t.Errorf("%s = %q, want it to contain %q", check.name, check.got, check.want)
			}
		}
		calls := codemodeDetailsOf(t, result).Calls
		if len(calls) != 1 || calls[0].Name != "models.classify" || calls[0].Status != "error" || calls[0].Error != "classifier exploded" {
			t.Errorf("nested calls = %+v", calls)
		}
		if result.Usage != nil {
			t.Errorf("usage = %+v, want none", result.Usage)
		}
	})
}
