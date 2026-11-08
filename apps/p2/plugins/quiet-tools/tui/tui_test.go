package tui_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	codemodetui "github.com/MichaelKinsy/PiG/apps/p2/plugins/codemode/tui"
	codingtui "github.com/MichaelKinsy/PiG/apps/p2/plugins/coding-tools/tui"
	powershelltui "github.com/MichaelKinsy/PiG/apps/p2/plugins/powershell/tui"
	quiettui "github.com/MichaelKinsy/PiG/apps/p2/plugins/quiet-tools/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tools"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// context is a TranscriptContext over the dark theme whose tool renderers go through the quiet-tools resolver.
type context struct {
	themeRef *theme.ThemeRef
	settings prefs.TuiSettings
}

func (c *context) Theme() *theme.ThemeRef                             { return c.themeRef }
func (c *context) KeyText(string) string                              { return "ctrl+o" }
func (c *context) RequestRender()                                     {}
func (c *context) MarkdownTransformers() []render.MarkdownTransformer { return nil }
func (c *context) Settings() *prefs.TuiSettings                       { return &c.settings }
func (c *context) OutputPad() int                                     { return 1 }
func (c *context) Cwd() string                                        { return "/work" }
func (c *context) EntryRenderers() map[string]*components.EntryRenderer {
	return nil
}
func (c *context) ToolRenderers(name string) *tools.ToolRenderers {
	registered := map[string]*tools.ToolRenderers{"powershell": powershelltui.Renderers, "codemode": codemodetui.Card}
	for _, entry := range codingtui.Renderers() {
		registered[entry.Key] = entry.Value
	}
	return tools.ResolveToolRenderers(name, []tools.NamedResolver{{Key: "10:quiet-tools", Resolver: quiettui.QuietTools}}, registered, c.settings)
}

func plain(lines []string) string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(widthx.StripTerminalSequences(line), " ")
	}
	return strings.Join(out, "\n")
}

func entry(id int, kind string, message ai.Message) harness.TranscriptItem {
	return harness.TranscriptEntry{Entry: durable.EntryRecord{Id: durable.EntryId(id), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: kind, Model: []ai.Message{message}}}
}

func result(id int, toolCallID, toolName, text string, isError bool) harness.TranscriptItem {
	duration := int64(5)
	if toolCallID == "t1" {
		duration = 2500
	}
	return entry(id, "pi.tool-result", ai.ToolResultMessage{
		ToolCallID: toolCallID, ToolName: toolName, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, IsError: isError,
		DurationMs: &duration, Timestamp: int64(id),
	})
}

func transcriptText(t *testing.T, expanded bool, settings prefs.TuiSettings) string {
	t.Helper()
	dark, _ := theme.BuiltinTheme("dark")
	built, err := theme.CreateTheme(dark, pitui.TerminalColorModeTrueColor, theme.TerminalState{})
	if err != nil {
		t.Fatal(err)
	}
	answer := ai.AssistantMessage{
		Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: "Checking."},
			ai.ToolCall{ID: "t1", Name: "bash", Arguments: ai.JsonObject{"command": "npm run check\necho done"}},
			ai.ToolCall{ID: "t2", Name: "read", Arguments: ai.JsonObject{"path": "/work/src/a.ts", "offset": 10.0}},
			ai.ToolCall{ID: "t3", Name: "edit", Arguments: ai.JsonObject{"path": "/work/src/b.ts", "edits": []any{}}},
		},
		API: "openai-completions", Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Timestamp: 2,
	}
	view := harness.TranscriptView{Items: []harness.TranscriptItem{
		entry(1, "pi.user", ai.UserMessage{Content: ai.UserText("go"), Timestamp: 1}),
		entry(2, "pi.assistant", answer),
		result(3, "t1", "bash", "lots\nof\noutput", false),
		result(4, "t2", "read", "file text", false),
		result(5, "t3", "edit", "edit failed: no match", true),
	}}
	transcriptView := components.NewTranscriptView(&context{themeRef: theme.NewThemeRef(built), settings: settings}, components.ViewState{Expanded: expanded}, nil)
	built2 := transcript.BuildTranscript(view, nil, nil)
	transcriptView.Sync(&built2, nil)
	return plain(transcriptView.Render(80))
}

func mustMatch(t *testing.T, pattern, text string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("no match for %q in:\n%s", pattern, text)
	}
}

func mustNotMatch(t *testing.T, pattern, text string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("unexpected match for %q in:\n%s", pattern, text)
	}
}

// Pi: "collapsed: one line per call; a failed call keeps its full content"
func TestCollapsedOneLinePerCallAFailedCallKeepsItsFullContent(t *testing.T) {
	shown := transcriptText(t, false, prefs.TuiSettings{})
	mustMatch(t, `Checking\.\n\n › bash npm run check … ✓ 2\.5s\n\n › read src/a\.ts:10 ✓\n\n`, shown)
	mustNotMatch(t, `lots|file text`, shown)
	mustMatch(t, `edit src/b\.ts[\s\S]*edit failed: no match`, shown)
}

// Pi: "expanded: every call shows its full content"
func TestExpandedEveryCallShowsItsFullContent(t *testing.T) {
	shown := transcriptText(t, true, prefs.TuiSettings{})
	mustNotMatch(t, `›`, shown)
	mustMatch(t, ` \$ npm run check[\s\S]*lots\n of\n output`, shown)
	mustMatch(t, `read src/a\.ts[\s\S]*file text`, shown)
}

// Pi: "setting full: the tools' own cards, boxed"
func TestSettingFullTheToolsOwnCardsBoxed(t *testing.T) {
	shown := transcriptText(t, false, prefs.TuiSettings{Plugins: map[string]map[string]prefs.PluginSettingValue{"quiet-tools": {"toolCalls": "full"}}})
	mustNotMatch(t, `›`, shown)
	mustMatch(t, `\$ npm run check`, shown)
}

// Pi: "plugin settings: merged per setting, bounded, accepted by the launcher's patch check"
func TestPluginSettingsMergedPerSettingBoundedAcceptedByTheLaunchersPatchCheck(t *testing.T) {
	merged := prefs.MergeSettings(
		prefs.TuiSettings{Plugins: map[string]map[string]prefs.PluginSettingValue{"a": {"x": 1.0, "y": true}}},
		prefs.TuiSettings{Plugins: map[string]map[string]prefs.PluginSettingValue{"a": {"y": false}, "b": {"z": "s"}}},
	).Plugins
	if got := fmt.Sprint(merged); got != "map[a:map[x:1 y:false] b:map[z:s]]" {
		t.Fatalf("merged = %s", got)
	}
	_, invalid := prefs.SanitizeSettings(map[string]any{"plugins": map[string]any{"a": map[string]any{"x": map[string]any{"nested": 1.0}}}})
	if fmt.Sprint(invalid) != "[plugins]" {
		t.Fatalf("invalid = %v", invalid)
	}
	many := map[string]any{}
	for index := range 33 {
		many[fmt.Sprintf("k%d", index)] = true
	}
	_, invalid = prefs.SanitizeSettings(map[string]any{"plugins": map[string]any{"a": many}})
	if fmt.Sprint(invalid) != "[plugins]" {
		t.Fatalf("invalid = %v", invalid)
	}
	patch := func(value any) string {
		_, errText := host.ValidatePrefsPatch(map[string]any{"settings": map[string]any{"plugins": map[string]any{"quiet-tools": map[string]any{"toolCalls": value}}}})
		return errText
	}
	if errText := patch("full"); errText != "" {
		t.Fatalf("full refused: %s", errText)
	}
	if errText := patch(nil); errText != "" {
		t.Fatalf("null refused: %s", errText)
	}
	if errText := patch([]any{1.0}); errText == "" {
		t.Fatal("an array was accepted")
	}
}
