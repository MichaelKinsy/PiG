package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

func checkStreamedWriteCardAgainstPi(t *testing.T, path, content string, expanded bool) {
	// The deltas are 16 characters, as a provider's stream decodes them: never a part of a character.
	const width, deltaChars = 120, 16
	var encoded strings.Builder
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(content); err != nil {
		t.Fatal(err)
	}
	encodedPath, err := json.Marshal(path)
	if err != nil {
		t.Fatal(err)
	}
	text := `{"path":` + string(encodedPath) + `,"content":` + strings.TrimSuffix(encoded.String(), "\n") + `}`
	// prefixes are byte offsets into text; piPrefixes are the same offsets in UTF-16 code units, as JavaScript slices.
	var prefixes, piPrefixes []int
	chars, units := 0, 0
	for offset, char := range text {
		if chars > 0 && chars%deltaChars == 0 {
			prefixes, piPrefixes = append(prefixes, offset), append(piPrefixes, units)
		}
		chars++
		units += len(utf16.Encode([]rune{char}))
	}
	prefixes, piPrefixes = append(prefixes, len(text)), append(piPrefixes, units)

	m, _ := newTickRenderProbe(t, "regular")
	m.toolsExpanded = expanded
	type input struct {
		Text     string `json:"text"`
		Prefixes []int  `json:"prefixes"`
		Width    int    `json:"width"`
		Cwd      string `json:"cwd"`
		Expanded bool   `json:"expanded"`
	}
	var want []string
	pioracle.Run(t, `
const th = await load("pi-coding-agent/modes/interactive/theme/theme.js");
th.initTheme("dark", false);
const tui = await load("../pi-tui.mjs");
tui.setCapabilities({ images: null, trueColor: true, hyperlinks: true });
const { KeybindingsManager } = await load("pi-coding-agent/core/keybindings.js");
tui.setKeybindings(new KeybindingsManager({}));
const { parseStreamingJson } = await load("pi-ai/utils/json-parse.js");
const { createWriteToolDefinition } = await load("pi-coding-agent/core/tools/write.js");
const { ToolExecutionComponent } = await load("pi-coding-agent/modes/interactive/components/tool-execution.js");
const card = new ToolExecutionComponent("write", "call_0", {}, {}, createWriteToolDefinition(input.cwd), { requestRender() {} }, input.cwd);
card.setExpanded(input.expanded);
const frames = [card.render(input.width).join("\n")];
for (const end of input.prefixes) {
	card.updateArgs(parseStreamingJson(input.text.slice(0, end)));
	frames.push(card.render(input.width).join("\n"));
}
card.setArgsComplete();
frames.push(card.render(input.width).join("\n"));
emit(frames);`, input{Text: text, Prefixes: piPrefixes, Width: width, Cwd: m.opts.CWD, Expanded: expanded}, &want, "FORCE_COLOR=3", "COLORTERM=truecolor")

	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call_0", Name: "write"}}}
	message := func(arguments string) agent.AgentMessage {
		call := ai.ToolCall{ID: "call_0", Name: "write"}
		call.SetStreamingArguments(arguments)
		return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{call}, StopReason: ai.StopReasonPending}}
	}
	m.handleAgentEvent(agent.MessageStartEvent{Message: message("")})
	m.handleAgentEvent(agent.MessageUpdateEvent{Message: message(""), AssistantMessageEvent: ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial}})
	card := m.toolByID["call_0"]
	if card == nil {
		t.Fatal("toolcall_start made no tool card")
	}
	got := []string{strings.Join(card.Render(width), "\n")}
	start := 0
	for _, end := range prefixes {
		m.handleAgentEvent(agent.MessageUpdateEvent{Message: message(string(text[:end])), AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: string(text[start:end]), Partial: partial}})
		got = append(got, strings.Join(card.Render(width), "\n"))
		start = end
	}
	final := message(string(text))
	finalCall := final.Assistant.Content[0].(ai.ToolCall)
	m.handleAgentEvent(agent.MessageUpdateEvent{Message: final, AssistantMessageEvent: ai.ToolCallEndEvent{ContentIndex: 0, ToolCall: finalCall, Partial: partial}})
	final.Assistant.StopReason = ai.StopReasonToolUse
	m.handleAgentEvent(agent.MessageEndEvent{Message: final})
	got = append(got, strings.Join(card.Render(width), "\n"))

	if len(got) != len(want) {
		t.Fatalf("Pig drew %d frames, Pi %d", len(got), len(want))
	}
	mismatches := 0
	for i := range want {
		if got[i] != want[i] {
			if mismatches++; mismatches <= 4 {
				label := "after setArgsComplete"
				switch {
				case i == 0:
					label = "at toolcall_start"
				case i <= len(prefixes):
					label = fmt.Sprintf("after %d argument bytes", prefixes[i-1])
				}
				t.Errorf("frame %d %s:\n Pig %q\n Pi  %q", i, label, got[i], want[i])
			}
		}
	}
	if mismatches > 4 {
		t.Errorf("%d of %d frames differ from Pi", mismatches, len(want))
	}
}
