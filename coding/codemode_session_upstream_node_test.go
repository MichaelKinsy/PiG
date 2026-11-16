//go:build !pig_strip_codemode && !pig_strip_node_extensions

package coding

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

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
		// Hidden tools' guidelines move from the rules to their codemode sections (#10343).
		if strings.Contains(requestPrompts[1], "Use read to examine files") || !strings.Contains(description("codemode"), "- Use read to examine files instead of cat or sed.") {
			t.Errorf("read guideline in the prompt %q, codemode description %q", requestPrompts[1], description("codemode"))
		}

		// Without codemode, tools keep their plain descriptions.
		h.session.SetActiveToolsByName([]string{"echo"})
		if got := description("echo"); got != "Echo text back.\n\nSecond paragraph." {
			t.Errorf("echo description = %q", got)
		}
	})

	// .upstream/v1.0.4/packages/coding-agent/test/suite/agent-session-codemode.test.ts:187 (#10343)
	t.Run("shows the guidelines of tools that do not fit the inline budget through describeTool()", func(t *testing.T) {
		h := setup(t)
		h.setCodemodeMode(t, "only")
		h.setCodemodeInlineBudget(t, 0)
		h.session.SetActiveToolsByName([]string{"read", "codemode"})
		for _, tool := range h.session.Tools() {
			if tool.Name() == "codemode" && strings.Contains(tool.Schema().Description, "### `read`") {
				t.Fatalf("codemode description lists read despite the zero budget: %q", tool.Schema().Description)
			}
		}
		result := codemodeRun(t, h, `text(await describeTool("read"))`)
		if got := codemodeResultText(t, result); !strings.Contains(got, "- Use read to examine files instead of cat or sed.") {
			t.Errorf("describeTool(read) = %q", got)
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
		if got, want := codemodeResultText(t, result), "==> text 1/2 <==\necho,stats,screenshot\n==> text 2/2 <==\n"+`{"a":"echo: one","b":"echo: two","names":["a","b"]}`+"\n<console_output>\nfiles 2\n</console_output>"; got != want {
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
		if want := []string{"==> text 1/2 <==", "captured", lines[2], "<image>", lines[2], "<image>", "==> text 2/2 <==", "after"}; !slices.Equal(lines, want) || !tinyPNGLabel.MatchString(lines[2]) {
			t.Fatalf("result lines = %q, want %q", lines, want)
		}
		if got := checkSavedImages(t, lines[2]); got != "<saved>" {
			t.Errorf("saved label = %q", got)
		}
		if image, ok := result.Content[2].(ai.ImageContent); !ok || image.Data != tinyPNGBase64 || image.MimeType != "image/png" {
			t.Errorf("content[2] = %#v", result.Content[2])
		}
	})

	// agent-session-codemode.test.ts (v1.1.0) "marks where each text item starts and puts console lines last in one text
	// block": providers join adjacent text blocks with nothing or a newline, so the output is one block.
	t.Run("marks where each text item starts and puts console lines last in one text block", func(t *testing.T) {
		h := setup(t)
		result := codemodeRun(t, h, "text(\"one\\ntwo\");\nconsole.log(\"a\");\nconsole.log(\"b\");\ntext(\"three\\n\");\nreturn 4;")
		if len(result.Content) != 2 {
			t.Fatalf("content = %#v, want the header and one text block", result.Content)
		}
		if got, want := codemodeResultText(t, result), "==> text 1/3 <==\none\ntwo\n==> text 2/3 <==\nthree\n==> text 3/3 <==\n4\n<console_output>\na\nb\n</console_output>"; got != want {
			t.Errorf("result = %q, want %q", got, want)
		}
	})

	// https://github.com/earendil-works/pi/issues/10251
	t.Run("resolves read calls to text for text files and to image blocks that image() shows", func(t *testing.T) {
		h := setup(t)
		pixel, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
		if err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{"notes.txt": []byte("hello"), "pixel.png": pixel} {
			if err := os.WriteFile(filepath.Join(h.session.CWD(), name), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		h.session.SetActiveToolsByName([]string{"read", "codemode"})
		// The harness's extension runner has a cwd of its own, so the paths are absolute where upstream's are relative.
		notes, _ := json.Marshal(filepath.Join(h.session.CWD(), "notes.txt"))
		shotPath, _ := json.Marshal(filepath.Join(h.session.CWD(), "pixel.png"))
		result := codemodeRun(t, h, `text(await tools.read({ path: `+string(notes)+` }));
const shot = await tools.read({ path: `+string(shotPath)+` });
text(shot.note);
image(shot);`)
		if result.IsError {
			t.Fatalf("result failed: %+v", result.Content)
		}
		if got, want := checkSavedImages(t, codemodeResultText(t, result)), "==> text 1/2 <==\nhello\n==> text 2/2 <==\nRead image file [image/png]\n<saved>\n<image>"; got != want {
			// The harness model does not declare image input; read's note says nothing of it (Pi 4be4cb4f5e9c tools.test.ts:240: the text is "Read image file [image/png]").
			t.Errorf("result = %q, want %q", got, want)
		}
		if image, ok := result.Content[len(result.Content)-1].(ai.ImageContent); !ok || image.Data != tinyPNGBase64 || image.MimeType != "image/png" {
			t.Errorf("last content = %#v", result.Content[len(result.Content)-1])
		}
	})

	t.Run("reports script failures as results that keep partial output and the calls that ran", func(t *testing.T) {
		h := setup(t)
		result := codemodeRun(t, h, "text(\"partial\");\nconsole.log(\"log\");\nawait tools.echo({ text: \"x\" });\nthrow new Error(\"boom\");")
		if !result.IsError {
			t.Error("isError = false")
		}
		if header, _ := result.Content[0].(ai.TextContent); !strings.HasPrefix(header.Text, "Script failed\n") {
			t.Errorf("header = %q", header.Text)
		}
		text := codemodeResultText(t, result)
		if !regexp.MustCompile(`^partial\n<console_output>\nlog\n</console_output>\nScript error:\nError: boom\n`).MatchString(text) {
			t.Errorf("text = %q", text)
		}
		for _, want := range []string{"codemode.js:4", "Tool calls made before the failure (they are not undone): echo (ok)"} {
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
