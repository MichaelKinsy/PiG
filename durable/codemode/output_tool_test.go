// Ports the "durable code mode output and tool search" tests of Pi 1.2.0 packages/durable/test/codemode.test.ts.

package codemode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
)

const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

var scriptHeader = regexp.MustCompile(`^Script completed\nWall time [\d.]+ seconds\nOutput:\n`)

// scriptRun is the result of one script: its content as text (images as <image>), and its details.
type scriptRun struct {
	text    string
	isError bool
	details ToolDetails
	content []ai.ToolResultMessageContent
}

// runScript runs code once in a chat with a Node environment.
func runScript(t *testing.T, code string, chat ...*codemodeFixture) scriptRun {
	t.Helper()
	f := codemodeChat(t)
	if len(chat) > 0 {
		f = chat[0]
	}
	f.holds.block.Store(false)
	f.setup.faux.SetResponses([]ai.FauxResponseStep{script(code), done()})
	opened, root := f.setup.openWith(t, openSqlite(t, filepath.Join(t.TempDir(), "session.sqlite")), envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: t.TempDir()}))
	mustSubmitDone(t, root)
	entries := resultEntries(t, root)
	if len(entries) == 0 {
		t.Fatal("no tool result entry")
	}
	message := entries[0].Model[0].(ai.ToolResultMessage)
	var text strings.Builder
	for _, item := range message.Content {
		if part, ok := item.(ai.TextContent); ok {
			text.WriteString(part.Text)
		} else {
			text.WriteString("<image>")
		}
	}
	details := detailsOf(t, root)
	mustClose(t, opened)
	return scriptRun{text: text.String(), isError: message.IsError, details: details, content: message.Content}
}

func TestDurableCodeModeOutputAndToolSearch(t *testing.T) {
	// Pi 1.2.0 packages/durable/test/codemode.test.ts:936 "lays out text items, console lines, and the returned value under a header"
	t.Run("lays out text items, console lines, and the returned value under a header", func(t *testing.T) {
		run := runScript(t, `text("one"); console.log("log", 1); text({ two: 2 }); return "three";`)
		if !scriptHeader.MatchString(run.text) {
			t.Errorf("no header in %q", run.text)
		}
		for _, want := range []string{"==> text 1/3 <==\none\n==> text 2/3 <==\n{\"two\":2}\n==> text 3/3 <==\nthree", "<console_output>\nlog 1\n</console_output>"} {
			if !strings.Contains(run.text, want) {
				t.Errorf("%q lacks %q", run.text, want)
			}
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:943 "cuts text past the token budget to its start and end, with the full text in a file"
	t.Run("cuts text past the token budget to its start and end, with the full text in a file", func(t *testing.T) {
		run := runScript(t, "// @options: {\"max_output_tokens\": 20}\ntext(\"BEGIN\" + \"x\".repeat(1000) + \"END\");")
		for _, want := range []string{"Warning: truncated output", "BEGIN", "END", "[Full output: " + run.details.FullOutputPath} {
			if !strings.Contains(run.text, want) {
				t.Errorf("%q lacks %q", run.text, want)
			}
		}
		full, err := os.ReadFile(run.details.FullOutputPath)
		if err != nil {
			t.Fatal(err)
		}
		if want := "BEGIN" + strings.Repeat("x", 1000) + "END"; string(full) != want {
			t.Errorf("full output has %d bytes, want %d", len(full), len(want))
		}
	})

	// PiG addition (no Pi case): the default budget of formatResult.
	t.Run("cuts to 10,000 tokens (4 characters each) when the script sets no budget", func(t *testing.T) {
		within := runScript(t, `return "y".repeat(39000);`)
		over := runScript(t, `return "y".repeat(41000);`)
		if strings.Contains(within.text, "truncated") || !strings.Contains(over.text, "Warning: truncated output (original token count: 10250)") {
			t.Errorf("within: %.60q, over: %.100q", within.text, over.text)
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:956 "keeps a result inside the tool's output limits whatever max_output_tokens says, spilling the rest"
	t.Run("keeps a result inside the tool's output limits whatever max_output_tokens says, spilling the rest", func(t *testing.T) {
		// 6 MiB of text: over the Harness's 4 MiB limit, which would cut it without a spill file.
		big := runScript(t, "// @options: {\"max_output_tokens\": 2000000}\nreturn \"x\".repeat(6 * 1024 * 1024);")
		if !strings.Contains(big.text, "Warning: truncated output") {
			t.Error("the 6 MiB result was not truncated")
		}
		if len(big.text) > 1024*1024+1000 {
			t.Errorf("result has %d characters", len(big.text))
		}
		full, err := os.ReadFile(big.details.FullOutputPath)
		if err != nil || len(full) != 6*1024*1024 {
			t.Errorf("full output: %d bytes, %v", len(full), err)
		}
		// 200,000 short lines: within the byte limit, over the 100,000 lines one.
		long := runScript(t, "// @options: {\"max_output_tokens\": 2000000}\nreturn Array.from({ length: 200000 }, () => \"a\").join(\"\\n\");")
		if !strings.Contains(long.text, "Warning: truncated output") {
			t.Error("the 200,000 lines result was not truncated")
		}
		if lines := strings.Count(long.text, "\n") + 1; lines >= 100_000 {
			t.Errorf("result has %d lines", lines)
		}
		if long.details.FullOutputPath == "" {
			t.Error("no full output path")
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:975 "writes the result's files to the same paths when it reruns after a crash"
	t.Run("writes the result's files to the same paths when it reruns after a crash", func(t *testing.T) {
		f := codemodeChat(t)
		f.holds.block.Store(false)
		directory := t.TempDir()
		written := newGate()
		var first string
		// The process dies after the full output was written and before the result was committed.
		crashing := &crashAfterWrite{ExecutionEnv: envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: directory}), written: written, path: &first}
		path := filepath.Join(t.TempDir(), "session.sqlite")
		f.setup.faux.SetResponses([]ai.FauxResponseStep{script("// @options: {\"max_output_tokens\": 20}\nreturn \"x\".repeat(1000);"), done()})
		opened, root := f.setup.openWith(t, openSqlite(t, path), crashing)
		id := submitGo(t, root).Id()
		opened.Resume()
		mustWait(t, written)
		mustClose(t, opened)
		opened, root = f.setup.openWith(t, openSqlite(t, path), envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: directory}))
		opened.Resume()
		submission := must(opened.Submission(testContext, id))
		if settled := must(submission.Wait(testContext)); settled.Status != durable.SubmissionDone {
			t.Fatalf("submission %s, want done", settled.Status)
		}
		if details := detailsOf(t, root); details.FullOutputPath != first {
			t.Errorf("full output path %q, the first attempt wrote %q", details.FullOutputPath, first)
		}
		files, err := os.ReadDir(filepath.Dir(first))
		if err != nil || len(files) != 1 || files[0].Name() != "output.txt" {
			t.Errorf("files %v, %v", files, err)
		}
		mustClose(t, opened)
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:1009 "saves images to files and names them next to the image"
	t.Run("saves images to files and names them next to the image", func(t *testing.T) {
		run := runScript(t, `image("data:image/png;base64,`+tinyPNG+`");`)
		saved := regexp.MustCompile(`\[Image saved to (\S+) \(image/png, \d+ bytes\)\]`).FindStringSubmatch(run.text)
		if saved == nil {
			t.Fatalf("no saved image in %q", run.text)
		}
		content, err := os.ReadFile(saved[1])
		if err != nil || base64.StdEncoding.EncodeToString(content) != tinyPNG {
			t.Errorf("saved image: %v, %v", content, err)
		}
		hasImage := false
		for _, item := range run.content {
			if _, ok := item.(ai.ImageContent); ok {
				hasImage = true
			}
		}
		if !hasImage {
			t.Error("the result has no image")
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:1018 "calls a deferred tool the model is not offered, and finds it in ALL_TOOLS"
	t.Run("calls a deferred tool the model is not offered, and finds it in ALL_TOOLS", func(t *testing.T) {
		chat := codemodeChat(t)
		later := tool("later", func(context.Context, toolArgs, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return textResult("later ran"), nil
		}, durable.ReplayUnsafe)
		later.Deferred = true
		addTool(t, chat.setup.registry, later)
		run := runScript(t, `return [await tools.later({}), ALL_TOOLS.some((tool) => tool.name === "later")];`, chat)
		if !strings.Contains(run.text, `["later ran",true]`) {
			t.Errorf("result %q", run.text)
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:1037 "reports a failed script with its error and the calls already made"
	t.Run("reports a failed script with its error and the calls already made", func(t *testing.T) {
		run := runScript(t, `await tools.echo({ text: "a" }); throw new Error("boom");`)
		if !run.isError || !strings.HasPrefix(run.text, "Script failed\n") {
			t.Errorf("result %q (error %v)", run.text, run.isError)
		}
		for _, want := range []string{"Error: boom", "Tool calls made before the failure (they are not undone): echo (ok)"} {
			if !strings.Contains(run.text, want) {
				t.Errorf("%q lacks %q", run.text, want)
			}
		}
	})

	// Pi 1.2.0 packages/durable/test/codemode.test.ts:1045 "finds tools with searchTools(), describeTool(), and describeNamespace(), and lists them in ALL_TOOLS"
	t.Run("finds tools with searchTools(), describeTool(), and describeNamespace(), and lists them in ALL_TOOLS", func(t *testing.T) {
		chat := codemodeChatWith(t, ToolOptions{Namespaces: []Namespace{{Name: "github", Description: "GitHub MCP server", Prefix: "mcp__github__"}}})
		addTool(t, chat.setup.registry, tool("mcp__github__list_issues", func(context.Context, toolArgs, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{}, nil
		}, durable.ReplayUnsafe))
		run := runScript(t, `const found = await searchTools("issues");
			const inNamespace = await searchTools("echo", { namespace: "github" });
			return {
				found: found.map((tool) => tool.name),
				inNamespace,
				described: (await describeTool("echo")).includes("echo(args:"),
				missing: (await describeTool("nope")) ?? null,
				namespace: await describeNamespace("github"),
				all: ALL_TOOLS.map((tool) => tool.name).includes("mcp__github__list_issues"),
			};`, chat)
		var got any
		if err := json.Unmarshal([]byte(scriptHeader.ReplaceAllString(run.text, "")), &got); err != nil {
			t.Fatalf("%q: %v", run.text, err)
		}
		var want any
		_ = json.Unmarshal([]byte(`{"found":["mcp__github__list_issues"],"inNamespace":[],"described":true,"missing":null,"namespace":{"name":"github","description":"GitHub MCP server","tools":["mcp__github__list_issues"]},"all":true}`), &want)
		if !equalJSON(got, want) {
			t.Errorf("got %s", run.text)
		}
	})
}

func TestDiscoveryGlobalsCheckTheirArguments(t *testing.T) {
	chat := codemodeChatWith(t, ToolOptions{Namespaces: []Namespace{{Name: "github", Prefix: "mcp__github__"}, {Name: "jira", Prefix: "mcp__jira__"}}})
	for _, name := range []string{"mcp__github__list_issues", "mcp__jira__list_issues", "my-tool"} {
		addTool(t, chat.setup.registry, tool(name, func(context.Context, toolArgs, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{}, nil
		}, durable.ReplayUnsafe))
	}
	run := runScript(t, `const fail = async (promise) => { try { await promise; return "resolved"; } catch (error) { return error.message; } };
		return {
			inGithub: (await searchTools("issues", { namespace: "github" })).map((tool) => tool.name),
			inNone: await searchTools("issues", { namespace: "nope" }),
			limited: (await searchTools("issues", { limit: 1 })).length,
			both: (await searchTools("issues", { namespace: null, limit: null })).length,
			zero: await fail(searchTools("issues", { limit: 0 })),
			fraction: await fail(searchTools("issues", { limit: 1.5 })),
			text: await fail(searchTools("issues", { limit: "2" })),
			query: await fail(searchTools(5)),
			namespace: await fail(searchTools("issues", { namespace: 5 })),
			tool: await fail(describeTool(5)),
			byName: (await describeTool("my-tool")).includes("my-tool"),
			byIdentifier: (await describeTool("my_tool")) === (await describeTool("my-tool")),
			noNamespace: await fail(describeNamespace()),
			unknown: (await describeNamespace("nope")) ?? null,
			hidden: ALL_TOOLS.some((tool) => tool.name.startsWith("pi.codemode.")),
		};`, chat)
	var got any
	if err := json.Unmarshal([]byte(scriptHeader.ReplaceAllString(run.text, "")), &got); err != nil {
		t.Fatalf("%q: %v", run.text, err)
	}
	var want any
	_ = json.Unmarshal([]byte(`{"inGithub":["mcp__github__list_issues"],"inNone":[],"limited":1,"both":2,"zero":"searchTools() limit must be a positive integer","fraction":"searchTools() limit must be a positive integer","text":"searchTools() limit must be a positive integer","query":"searchTools() expects a query string","namespace":"searchTools() namespace must be a string","tool":"describeTool() expects a tool name","byName":true,"byIdentifier":true,"noNamespace":"describeNamespace() expects a namespace name","unknown":null,"hidden":false}`), &want)
	if !equalJSON(got, want) {
		t.Errorf("got  %s\nwant %s", run.text, mustJSON(want))
	}
}

// crashAfterWrite is an environment whose first write reports its path, then waits for the call to end.
type crashAfterWrite struct {
	env.ExecutionEnv
	written *gate
	path    *string
}

func (e *crashAfterWrite) WriteFile(ctx context.Context, path string, content any) error {
	err := e.ExecutionEnv.WriteFile(ctx, path, content)
	if *e.path == "" {
		*e.path = path
	}
	e.written.resolve()
	<-ctx.Done()
	return err
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
