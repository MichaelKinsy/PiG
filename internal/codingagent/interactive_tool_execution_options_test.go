package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts:3516, 3594 and 3997 construct every ToolExecutionComponent with the settings manager's showImages and
// imageWidthCells, including the cards that rebuild a resumed transcript (3997). blockImages is not consulted: it only filters
// images sent to the model, so a blocked-image setting still shows them in the card.
func TestResumedToolCardsUseTheImageSettings(t *testing.T) {
	call := ai.ToolCall{ID: "read-call", Name: "read", Arguments: ai.JsonObject{"path": "pic.png"}}
	for _, tc := range []struct {
		name       string
		show       bool
		width      int
		blockImage bool
		wantShow   bool
	}{
		{"hidden", false, 33, false, false},
		{"wide", true, 90, false, true},
		{"blocked", true, 60, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := resumeThinkingMode(t, false, userMsg("question"), assistantMsg("", call), agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
				Role: agent.RoleToolResult, ToolCallID: call.ID, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}},
			}})
			dir := t.TempDir()
			sm := NewSettingsManager(dir, dir)
			if err := sm.SetShowImages(tc.show); err != nil {
				t.Fatal(err)
			}
			if err := sm.SetImageWidthCells(tc.width); err != nil {
				t.Fatal(err)
			}
			if tc.blockImage {
				if err := sm.SetBlockImages(true); err != nil {
					t.Fatal(err)
				}
			}
			m.opts.SettingsManager = sm
			m.renderSessionEntries()
			if len(m.toolOrder) != 1 {
				t.Fatalf("resumed %d tool cards, want 1", len(m.toolOrder))
			}
			card := m.toolOrder[0]
			if card.ShowImages != tc.wantShow || card.ImageWidthCells != tc.width {
				t.Fatalf("resumed card ShowImages=%v ImageWidthCells=%d, want %v and %d", card.ShowImages, card.ImageWidthCells, tc.wantShow, tc.width)
			}
		})
	}
}

// tool-execution.ts:76-77: `options.showImages ?? true` and `options.imageWidthCells ?? 60`.
func TestToolExecutionOptionsDefaultWhenUndefined(t *testing.T) {
	hide, width := false, 40
	for _, tc := range []struct {
		name      string
		options   []tui.ToolExecutionOptions
		wantShow  bool
		wantWidth int
	}{
		{"none", nil, true, 60},
		{"empty", []tui.ToolExecutionOptions{{}}, true, 60},
		{"show only", []tui.ToolExecutionOptions{{ShowImages: &hide}}, false, 60},
		{"width only", []tui.ToolExecutionOptions{{ImageWidthCells: &width}}, true, 40},
		{"both", []tui.ToolExecutionOptions{{ShowImages: &hide, ImageWidthCells: &width}}, false, 40},
	} {
		c := newToolCardForTest("read", "", tc.options...)
		if c.ShowImages != tc.wantShow || c.ImageWidthCells != tc.wantWidth {
			t.Errorf("%s: ShowImages=%v ImageWidthCells=%d, want %v and %d", tc.name, c.ShowImages, c.ImageWidthCells, tc.wantShow, tc.wantWidth)
		}
	}
}

// interactive-mode.ts:4924-4945: a showImages change updates every tool card, while onBlockImagesChange only persists the setting and
// leaves the cards' image display alone.
func TestBlockImagesChangeKeepsToolCardImagesShown(t *testing.T) {
	call := ai.ToolCall{ID: "read-call", Name: "read", Arguments: ai.JsonObject{"path": "pic.png"}}
	m := resumeThinkingMode(t, false, userMsg("question"), assistantMsg("", call), agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role: agent.RoleToolResult, ToolCallID: call.ID, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}},
	}})
	dir := t.TempDir()
	sm := NewSettingsManager(dir, dir)
	m.opts.SettingsManager = sm
	m.renderSessionEntries()
	if len(m.toolOrder) != 1 || !m.toolOrder[0].ShowImages {
		t.Fatalf("resumed cards = %d, want one card showing images", len(m.toolOrder))
	}
	card := m.toolOrder[0]
	apply := m.buildSlashContext(t.Context()).OnSettingApplied
	if err := sm.SetBlockImages(true); err != nil {
		t.Fatal(err)
	}
	apply("block-images", "true")
	if !card.ShowImages {
		t.Fatal("blocking images for the model hid them in the tool card")
	}
	if err := sm.SetShowImages(false); err != nil {
		t.Fatal(err)
	}
	apply("show-images", "false")
	if card.ShowImages {
		t.Fatal("the show-images change did not reach the tool card")
	}
}
