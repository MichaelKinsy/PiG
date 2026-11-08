package components

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
)

// padContext is the oracle context with another output padding.
type padContext struct {
	oracleContext
	pad int
}

func (c padContext) OutputPad() int { return c.pad }

const markdownText = "# Heading\n\nSome **bold** and *italic* text with `code` and a [link](https://example.com/very/long/path). 日本語のテキスト\n\n- item one\n- item two with a long line that wraps around the narrow widths of the terminal\n  - nested\n\n1. first\n2. second\n\n> quote line\n\n```\nplain code block\n  indented\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n---\n\nfinal paragraph\x1b]52;c;evil\x07"

const skillText = "<skill name=\"deploy\" location=\"/s/deploy.md\">\n# Deploy\n\nStep one\nStep **two** with a long line that wraps over several rows at narrow widths\n</skill>\n\nplease go 日本語"

var testImage = ai.ImageContent{MimeType: "image/png"}

func userItem(edit func(*transcript.UserItem)) transcript.UserItem {
	item := transcript.UserItem{Key: "u", Text: "hello **there**"}
	if edit != nil {
		edit(&item)
	}
	return item
}

func assistantOf(stop ai.StopReason, errText string, content ...ai.AssistantContentBlock) *ai.AssistantMessage {
	return &ai.AssistantMessage{Content: content, API: "x", Provider: "faux", Model: "faux-1", StopReason: stop, ErrorMessage: errText, Timestamp: 1}
}

// toggles are the expand and collapse steps of a component.
func toggles(expand, collapse func()) map[string]func() {
	return map[string]func(){"expand": expand, "collapse": collapse}
}

func toolArgs() *jsvalue.Object {
	return jsvalue.Obj("path", "src/app.ts", "content", "line one\nline two\ttab\x1b[31mred\x1b[0m",
		"nested", jsvalue.Obj("a", []any{1.0, 2.5, true, nil, "s"}, "b", jsvalue.Obj("c", "日本語")),
		"long", strings.Repeat("x", 300), "count", 42.0)
}

func outputLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("output-%d \x1b[32mgreen\x1b[0m 日本語 %s", i, strings.Repeat("w", i*3))
	}
	return strings.Join(lines, "\n")
}

func bashLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%d %s 日本語\x1b[31m", i, strings.Repeat("c", i%7))
	}
	return strings.Join(lines, "\r\n")
}

func init() {
	// userScene is a user message that expands (a skill block); the others never do.
	userScene := func(item transcript.UserItem) func(t testing.TB, ctx oracleContext) oracleScene {
		return func(t testing.TB, ctx oracleContext) oracleScene {
			component := NewUserMessageComponent(item, ctx)
			return oracleScene{component: component, inputs: []string{"@expand", "@collapse"}, actions: toggles(func() { component.SetExpanded(true) }, func() { component.SetExpanded(false) })}
		}
	}
	// plainUser is a user message with an output padding (0: the default).
	plainUser := func(item transcript.UserItem, pad int) func(t testing.TB, ctx oracleContext) oracleScene {
		return func(t testing.TB, ctx oracleContext) oracleScene {
			var mc MessageContext = ctx
			if pad != 0 {
				mc = padContext{ctx, pad}
			}
			return oracleScene{component: NewUserMessageComponent(item, mc)}
		}
	}
	owner := &runtime.Author{Kind: runtime.AuthorOwner, ID: "owner", Name: "Owner"}
	registerScene("user-plain", plainUser(userItem(func(i *transcript.UserItem) { i.Text = markdownText }), 0))
	registerScene("user-short", plainUser(userItem(nil), 0))
	registerScene("user-pad3", plainUser(userItem(func(i *transcript.UserItem) {
		i.Author = &runtime.Author{Kind: runtime.AuthorGuest, ID: "g", Name: "Mallory 日本語"}
		i.Note = "3 chat lines passed"
		i.Files = []string{"a.txt", "dir/b\x1b[31m.txt"}
	}), 3))
	registerScene("user-guest", plainUser(userItem(func(i *transcript.UserItem) {
		i.Author = &runtime.Author{Kind: runtime.AuthorGuest, ID: "g", Name: "Alice\x1b]0;x\x07"}
		i.ReplyTo = &transcript.ReplyQuote{EntryID: 4, Name: "Bob", Excerpt: "an excerpt of the quoted message that is long"}
		i.Images = []ai.ImageContent{testImage, testImage}
	}), 0))
	registerScene("user-peer", plainUser(userItem(func(i *transcript.UserItem) {
		i.Peer = &transcript.ConversationPeer{ID: "p", Title: "Other session"}
		i.Text = ""
		i.Images = []ai.ImageContent{testImage}
	}), 0))
	registerScene("user-chat", plainUser(userItem(func(i *transcript.UserItem) { i.Chat, i.Author = true, owner }), 0))
	registerScene("user-owner-note", plainUser(userItem(func(i *transcript.UserItem) {
		i.Author, i.Note, i.Images = owner, "a note", []ai.ImageContent{testImage}
	}), 0))
	registerScene("user-skill", userScene(userItem(func(i *transcript.UserItem) { i.Text = skillText })))
	registerScene("user-skill-only", plainUser(userItem(func(i *transcript.UserItem) { i.Text = "<skill name=\"x\" location=\"/s\">\nbody\n</skill>" }), 0))
	registerScene("user-skill-files", plainUser(userItem(func(i *transcript.UserItem) {
		i.Text, i.Files = "<skill name=\"x\" location=\"/s\">\nbody\n</skill>", []string{"f.txt"}
	}), 0))
	registerScene("skill-invocation", func(t testing.TB, ctx oracleContext) oracleScene {
		component := NewSkillInvocationComponent(*ParseSkillBlock(skillText), ctx, 2)
		return oracleScene{component: component, inputs: []string{"@expand", "@collapse"}, actions: toggles(func() { component.SetExpanded(true) }, func() { component.SetExpanded(false) })}
	})

	assistantScene := func(message *ai.AssistantMessage, hide bool, label string) func(t testing.TB, ctx oracleContext) oracleScene {
		return func(t testing.TB, ctx oracleContext) oracleScene {
			component := NewAssistantMessageComponent(ctx, message, hide, label)
			return oracleScene{component: component, inputs: []string{"@hide", "@show"}, actions: map[string]func(){
				"hide": func() { component.SetHideThinkingBlock(true) }, "show": func() { component.SetHideThinkingBlock(false) },
			}}
		}
	}
	text := func(s string) ai.TextContent { return ai.TextContent{Text: s} }
	thinking := func(s string) ai.ThinkingContent { return ai.ThinkingContent{Thinking: s} }
	registerScene("assistant-markdown", assistantScene(assistantOf(ai.StopReasonStop, "", thinking("private **reasoning**\n\nsecond"), thinking("more"), text(markdownText), thinking("trailing thought")), false, ""))
	registerScene("assistant-hidden", assistantScene(assistantOf(ai.StopReasonStop, "", thinking("private reasoning"), text("Answer"), thinking("again")), true, "Pondering..."))
	registerScene("assistant-empty-thinking", assistantScene(assistantOf(ai.StopReasonStop, "", thinking("   "), text("   ")), false, ""))
	registerScene("assistant-error", assistantScene(assistantOf(ai.StopReasonError, "fixture error\x1b]0;x\x07", text("partial")), false, ""))
	registerScene("assistant-error-unknown", assistantScene(assistantOf(ai.StopReasonError, ""), false, ""))
	registerScene("assistant-aborted", assistantScene(assistantOf(ai.StopReasonAborted, ""), false, ""))
	registerScene("assistant-aborted-message", assistantScene(assistantOf(ai.StopReasonAborted, "Stopped by the owner", text("x")), false, ""))
	registerScene("assistant-aborted-request", assistantScene(assistantOf(ai.StopReasonAborted, "Request was aborted", text("x")), false, ""))
	registerScene("assistant-length", assistantScene(assistantOf(ai.StopReasonLength, "", text("cut")), false, ""))
	registerScene("assistant-tool-calls", assistantScene(assistantOf(ai.StopReasonError, "tool failed", text("running"), ai.ToolCall{ID: "c1", Name: "bash"}), false, ""))
	registerScene("assistant-none", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{component: NewAssistantMessageComponent(ctx, nil, false, "")}
	})
	highlights, err := os.ReadFile("testdata/oracle_highlight.json")
	if err != nil {
		panic(err)
	}
	var snippets map[string]string
	if err := json.Unmarshal(highlights, &snippets); err != nil {
		panic(err)
	}
	for lang, code := range snippets {
		registerScene("highlight-"+lang, assistantScene(assistantOf(ai.StopReasonStop, "", text("```"+lang+"\n"+code+"\n```")), false, ""))
	}
	registerScene("compaction-collapsed", func(t testing.TB, ctx oracleContext) oracleScene {
		component := NewCompactionSummaryComponent("Summary of **earlier** work\n\n- a\n- b", false, ctx, CompactionSummaryOptions{})
		return oracleScene{component: component, inputs: []string{"@expand", "@collapse"}, actions: toggles(func() { component.SetExpanded(true) }, func() { component.SetExpanded(false) })}
	})
	registerScene("compaction-options", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{component: NewCompactionSummaryComponent("   ", true, ctx, CompactionSummaryOptions{Label: "[branch summary]", Collapsed: "Branch summarized"})}
	})
	registerScene("compaction-pad", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{component: NewCompactionSummaryComponent("Expanded summary", true, padContext{ctx, 4}, CompactionSummaryOptions{})}
	})
	for _, color := range []string{"muted", "error", "warning"} {
		registerScene("notice-"+color, func(t testing.TB, ctx oracleContext) oracleScene {
			component := NewNoticeComponent("The model could not answer.\x1b]0;x\x07 日本語 and a very long text that wraps at narrow widths", color, ctx)
			return oracleScene{component: component, inputs: []string{"@update"}, actions: map[string]func(){"update": func() { component.Update("Updated") }}}
		})
	}

	toolView := func(edit func(*transcript.ToolView)) transcript.ToolView {
		view := transcript.ToolView{ID: "call", Name: "mcp_thing", Args: toolArgs(), Status: transcript.ToolDone}
		if edit != nil {
			edit(&view)
		}
		return view
	}
	toolScene := func(view transcript.ToolView, argsComplete bool, pad int) func(t testing.TB, ctx oracleContext) oracleScene {
		return func(t testing.TB, ctx oracleContext) oracleScene {
			var tc ToolCardContext = ctx
			if pad != 0 {
				tc = padContext{ctx, pad}
			}
			component := NewToolExecutionComponent(view, nil, tc, argsComplete)
			return oracleScene{component: component, inputs: []string{"@expand", "@collapse", "@update"}, actions: map[string]func(){
				"expand": func() { component.SetExpanded(true) }, "collapse": func() { component.SetExpanded(false) },
				"update": func() {
					updated := view
					updated.Status, updated.Output = transcript.ToolError, "failed\nbadly"
					component.Update(updated, true)
				},
			}}
		}
	}
	registerScene("tool-done-long", toolScene(toolView(func(v *transcript.ToolView) { v.Output = outputLines(15) }), true, 0))
	registerScene("tool-done-short", toolScene(toolView(func(v *transcript.ToolView) { v.Output, v.DurationMs = "ok", new(1234.0) }), true, 0))
	registerScene("tool-done-exactly-ten", toolScene(toolView(func(v *transcript.ToolView) { v.Output = outputLines(10) }), true, 0))
	registerScene("tool-pending", toolScene(toolView(func(v *transcript.ToolView) { v.Status = transcript.ToolPending }), false, 0))
	registerScene("tool-running", toolScene(toolView(func(v *transcript.ToolView) { v.Status, v.Output = transcript.ToolRunning, "partial\noutput" }), true, 0))
	registerScene("tool-error", toolScene(toolView(func(v *transcript.ToolView) { v.Status, v.Output = transcript.ToolError, "boom: failed\n  at line 3" }), true, 3))
	registerScene("tool-interrupted", toolScene(toolView(func(v *transcript.ToolView) { v.Status = transcript.ToolInterrupted }), true, 0))
	registerScene("tool-images", toolScene(toolView(func(v *transcript.ToolView) {
		v.Output, v.Images = "see image", []ai.ImageContent{testImage, testImage}
	}), true, 0))
	registerScene("tool-empty-args", toolScene(toolView(func(v *transcript.ToolView) { v.Args, v.Output = jsvalue.NewObject(), "x" }), true, 0))
	registerScene("tool-nested-calls", toolScene(toolView(func(v *transcript.ToolView) {
		v.Status = transcript.ToolPending
		v.Calls = []transcript.NestedCall{{ID: "n", Name: "read", Args: jsvalue.Obj("path", "a"), Status: transcript.ToolRunning}}
	}), true, 0))

	bashScene := func(command string, options BashExecutionOptions, live bool, steps func(c *BashExecutionComponent) ([]string, map[string]func())) func(t testing.TB, ctx oracleContext) oracleScene {
		return func(t testing.TB, ctx oracleContext) oracleScene {
			if live {
				screen, _ := newScreen(t, 80, 24)
				options.TUI = screen
			}
			component := NewBashExecutionComponent(command, ctx, options)
			order, actions := steps(component)
			names := make([]string, len(order))
			for i, name := range order {
				names[i] = "@" + name
			}
			return oracleScene{component: component, inputs: names, actions: actions, cleanup: component.Dispose}
		}
	}
	zero, two := new(0.0), new(2.0)
	registerScene("bash-running", bashScene("sleep 5 && echo 日本語", BashExecutionOptions{}, true, func(c *BashExecutionComponent) ([]string, map[string]func()) {
		return []string{"out", "done"}, map[string]func(){"out": func() { c.SetOutput("partial\r\nline", false) }, "done": func() { c.SetComplete(zero, false, BashCompletion{}) }}
	}))
	registerScene("bash-long", bashScene("seq 1 45", BashExecutionOptions{}, false, func(c *BashExecutionComponent) ([]string, map[string]func()) {
		return []string{"out", "done", "expand", "collapse"}, map[string]func(){
			"out": func() { c.SetOutput(bashLines(45), false) }, "done": func() { c.SetComplete(zero, false, BashCompletion{}) },
			"expand": func() { c.SetExpanded(true) }, "collapse": func() { c.SetExpanded(false) },
		}
	}))
	registerScene("bash-truncated", bashScene("cat big", BashExecutionOptions{ExcludeFromContext: true, OutputPad: new(2), By: "Alice (guest)"}, false, func(c *BashExecutionComponent) ([]string, map[string]func()) {
		return []string{"out", "done"}, map[string]func(){
			"out": func() { c.SetOutput(bashLines(30), true) }, "done": func() { c.SetComplete(zero, false, BashCompletion{Truncated: true, FullOutputPath: "/tmp/full.log"}) },
		}
	}))
	registerScene("bash-fail", bashScene("false", BashExecutionOptions{}, false, func(c *BashExecutionComponent) ([]string, map[string]func()) {
		return []string{"out", "done"}, map[string]func(){"out": func() { c.SetOutput("nope", false) }, "done": func() { c.SetComplete(two, false, BashCompletion{}) }}
	}))
	registerScene("bash-cancel", bashScene("sleep 9", BashExecutionOptions{}, true, func(c *BashExecutionComponent) ([]string, map[string]func()) {
		return []string{"out", "done"}, map[string]func(){"out": func() { c.SetOutput("x", false) }, "done": func() { c.SetComplete(nil, true, BashCompletion{}) }}
	}))
	registerScene("bash-error", bashScene("weird", BashExecutionOptions{}, false, func(c *BashExecutionComponent) ([]string, map[string]func()) {
		return []string{"done"}, map[string]func(){"done": func() { c.SetComplete(nil, false, BashCompletion{Error: new("spawn failed\x1b]0;x\x07")}) }}
	}))
	registerScene("bash-no-output", bashScene("true", BashExecutionOptions{}, false, func(c *BashExecutionComponent) ([]string, map[string]func()) {
		return []string{"done", "out"}, map[string]func(){"done": func() { c.SetComplete(zero, false, BashCompletion{}) }, "out": func() { c.SetOutput("", false) }}
	}))
	registerScene("bash-huge", bashScene("yes", BashExecutionOptions{}, false, func(c *BashExecutionComponent) ([]string, map[string]func()) {
		huge := make([]string, 3000)
		for i := range huge {
			huge[i] = fmt.Sprintf("y%d%s", i, strings.Repeat("z", 30))
		}
		return []string{"out", "done", "expand"}, map[string]func(){
			"out": func() { c.SetOutput(strings.Join(huge, "\n"), false) }, "done": func() { c.SetComplete(zero, false, BashCompletion{}) },
			"expand": func() { c.SetExpanded(true) },
		}
	}))
}
