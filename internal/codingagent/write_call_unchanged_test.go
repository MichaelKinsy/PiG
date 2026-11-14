// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package codingagent

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// unchangedStreamSource is a file of lines source text whose highlighting of early lines depends on later ones: block comments and template strings open on one line and close many lines later, or never; some lines end in CR LF, some are empty, some hold tabs.
func unchangedStreamSource(r *rand.Rand, lines int) string {
	var b strings.Builder
	for i := range lines {
		switch r.IntN(12) {
		case 0:
			fmt.Fprintf(&b, "/* opened on line %d", i)
		case 1:
			b.WriteString("closed here */ const x = 1;")
		case 2:
			fmt.Fprintf(&b, "const t%d = `template opened", i)
		case 3:
			b.WriteString("closed` + 'x';")
		case 4:
		case 5:
			fmt.Fprintf(&b, "\tif (a%d) {\treturn \"s\"; }", i)
		default:
			fmt.Fprintf(&b, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }", i, i%97, i)
		}
		if r.IntN(5) == 0 {
			b.WriteString("\r")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// unchangedStreamCuts are the lengths of the content after each delta: random pieces of 1 to 40 bytes, and around every sixth newline a piece that ends right before it, one that ends with it and one that ends one byte after it.
func unchangedStreamCuts(r *rand.Rand, content string) []int {
	var cuts []int
	for at := 0; at < len(content); {
		at = min(len(content), at+1+r.IntN(40))
		if next := strings.IndexByte(content[at:], '\n'); next >= 0 && r.IntN(6) == 0 {
			cuts = append(cuts, at+next, at+next+1)
			at = min(len(content), at+next+2)
		}
		cuts = append(cuts, at)
	}
	return cuts
}

// A write card that is drawn only when writeCallShowsNoChange does not say that nothing changed shows, after every delta, the lines of a card that is drawn after every delta: a wrong "nothing changed" would be a frame that should have been drawn and was not. Collapsed cards of a file with a language skip draws once the content is past the leading block; an expanded card, a path without a language and a path that arrives after the content never skip one.
func TestWriteCardSkippedDrawsShowTheSameLines(t *testing.T) {
	const width = 120
	for _, tc := range []struct {
		name      string
		path      string
		expanded  bool
		pathAfter int // the path argument arrives with this delta
		skips     bool
	}{
		{"collapsed", "big/source.ts", false, 0, true},
		{"expanded", "big/source.ts", true, 0, false},
		{"no language", "big/notes", false, 0, false},
		{"path arrives late", "big/source.ts", false, 120, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			skipped, drawn := 0, 0
			for seed := range uint64(6) {
				r := rand.New(rand.NewPCG(seed, 625))
				content := unchangedStreamSource(r, 140)
				always := extension.ToolRenderContext{State: map[string]any{builtInRenderStateKey: &builtInRenderState{writeWarmOff: true}}, Cwd: "/work", Expanded: tc.expanded}
				onChange := extension.ToolRenderContext{State: map[string]any{builtInRenderStateKey: &builtInRenderState{writeWarmOff: true}}, Cwd: "/work", Expanded: tc.expanded}
				var shown []string
				for step, cut := range unchangedStreamCuts(r, content) {
					fields := map[string]any{"content": content[:cut]}
					if step >= tc.pathAfter {
						fields["path"] = tc.path
					}
					always.Args, onChange.Args = fields, fields
					always.LastComponent = writeRenderCall(nil, nil, always)
					want := always.LastComponent.Render(width)
					if writeCallShowsNoChange(fields, onChange) {
						skipped++
					} else {
						drawn++
						onChange.LastComponent = writeRenderCall(nil, nil, onChange)
						shown = slices.Clone(onChange.LastComponent.Render(width))
					}
					if !slices.Equal(shown, want) {
						t.Fatalf("seed %d, delta %d (content of %d bytes ending %q): the card drawn on change shows\n%s\nthe card drawn after every delta shows\n%s", seed, step, cut, content[max(0, cut-12):cut], strings.Join(shown, "\n"), strings.Join(want, "\n"))
					}
				}
			}
			t.Logf("%d draws skipped, %d drawn", skipped, drawn)
			if tc.skips != (skipped > 0) {
				t.Errorf("%d draws were skipped, want some = %v", skipped, tc.skips)
			}
		})
	}
}

// frameRequests is the renderer of an interactive mode that counts the frames asked for and says whether the adaptive policy paces them.
type frameRequests struct {
	tui.TUI
	requests int
	active   bool
}

func (r *frameRequests) RequestRender(force ...bool) {
	r.requests++
	r.TUI.RequestRender(force...)
}

func (r *frameRequests) FramePacingActive() bool { return r.active }

// A write call's arguments stream 16 characters at a time, as the benchmark's model sends them. Under Pi's interval every update asks for a frame twice, as Pi's handler does. While the adaptive policy paces the frames, an update that leaves the collapsed card's lines as they are asks for none, and the card shows after every update what the last frame asked for drew.
func TestToolCallDeltaAsksForNoFrameWhenTheCardShowsNoChange(t *testing.T) {
	const width, deltaChars = 120, 16
	var source strings.Builder
	for i := range 200 {
		fmt.Fprintf(&source, "export function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", i, i%97, i)
	}
	encoded, err := json.Marshal(source.String())
	if err != nil {
		t.Fatal(err)
	}
	text := `{"path":"big/source.ts","content":` + string(encoded) + `}`
	for _, tc := range []struct {
		name            string
		installed, open bool
		active          bool
		skips           bool
	}{
		{"Pi's interval", false, false, false, false},
		{"policy installed, gate closed", true, false, true, false},
		{"policy installed, terminal's conditions not met", true, true, false, false},
		{"adaptive policy in force", true, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTickRenderProbe(t, "regular")
			renderer := &frameRequests{TUI: m.tuiInst, active: tc.active}
			m.tuiInst = renderer
			m.framePacingInstalled = tc.installed
			m.framePacingAllowed.Store(tc.open)
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
			shown := slices.Clone(card.Render(width))
			deltas, skipped := 0, 0
			for start := 0; start < len(text); start += deltaChars {
				end := min(len(text), start+deltaChars)
				before := renderer.requests
				m.handleAgentEvent(agent.MessageUpdateEvent{Message: message(text[:end]), AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: text[start:end], Partial: partial}})
				deltas++
				now := card.Render(width)
				switch asked := renderer.requests - before; {
				case asked == 0:
					skipped++
					if !slices.Equal(shown, now) {
						t.Fatalf("delta %d (arguments of %d bytes) asked for no frame, and the card would draw\n%s\nwhere the last frame asked for drew\n%s", deltas, end, strings.Join(now, "\n"), strings.Join(shown, "\n"))
					}
				case !tc.skips && asked != 2:
					t.Fatalf("delta %d asked for a frame %d times, want 2, as Pi's handler does", deltas, asked)
				}
				shown = slices.Clone(now)
			}
			t.Logf("%d deltas, %d asked for no frame", deltas, skipped)
			if tc.skips != (skipped > 0) {
				t.Errorf("%d of %d deltas asked for no frame, want some = %v", skipped, deltas, tc.skips)
			}
			if tc.skips && skipped < deltas/2 {
				t.Errorf("%d of %d deltas asked for no frame: past the leading block only a new line changes the collapsed card", skipped, deltas)
			}
		})
	}
}
