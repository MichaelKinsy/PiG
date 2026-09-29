package codingagent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type expansionProbe struct{ values []bool }

func (p *expansionProbe) SetExpanded(value bool) { p.values = append(p.values, value) }
func (*expansionProbe) Render(int) []string      { return nil }
func (*expansionProbe) Invalidate()              {}

// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-status.test.ts:146
func TestSetToolsExpandedAppliesToHeaderAndMountedChildrenUpstream(t *testing.T) {
	m := statusBorderMode(t, false)
	m.loadedResourcesContainer = tui.NewContainer()
	loaded, chat := &expansionProbe{}, &expansionProbe{}
	m.loadedResourcesContainer.Add(loaded)
	m.chatContainer.Add(chat)
	m.setAllToolsExpanded(true)
	if !m.toolsExpanded || !m.builtInHeaderExpanded {
		t.Fatal("header and tool expansion state did not receive true")
	}
	if !reflect.DeepEqual(loaded.values, []bool{true}) || !reflect.DeepEqual(chat.values, []bool{true}) {
		t.Fatalf("mounted expansion calls loaded=%v chat=%v", loaded.values, chat.values)
	}
	_, last := m.chatContainer.LastTwoChildren()
	if !strings.Contains(strings.Join(last.Render(120), "\n"), "Tool output: expanded") {
		t.Fatal("expansion status missing")
	}
	m.setAllToolsExpanded(true)
	if !reflect.DeepEqual(loaded.values, []bool{true}) || !reflect.DeepEqual(chat.values, []bool{true}) {
		t.Fatal("unchanged expansion repeated child callbacks")
	}
	m.setAllToolsExpanded(false)
	if !reflect.DeepEqual(loaded.values, []bool{true, false}) || !reflect.DeepEqual(chat.values, []bool{true, false}) {
		t.Fatalf("collapse calls loaded=%v chat=%v", loaded.values, chat.values)
	}
}

// Pi setToolsExpanded walks mounted identities, not historical tracking lists. A tracked child must not receive a duplicate call, and a detached one must not receive any call.
func TestToolsExpansionVisitsMountedChildrenOnce(t *testing.T) {
	m := statusBorderMode(t, false)
	mounted, detached := &expansionProbe{}, &expansionProbe{}
	m.chatContainer.Add(mounted)
	m.customMessageOrder = []expandableCustomMessageComponent{mounted, detached}
	m.setAllToolsExpanded(true)
	if !reflect.DeepEqual(mounted.values, []bool{true}) || len(detached.values) != 0 {
		t.Fatalf("mounted=%v detached=%v", mounted.values, detached.values)
	}
}

// Pi renders a user skill block as a mounted SkillInvocationMessageComponent. Ctrl+O must reach it even though it is not in a tool tracking list.
func TestToolsExpansionReachesPersistedSkillInvocation(t *testing.T) {
	m := statusBorderMode(t, false)
	entry := contextFixtureEntry(t, "skill", "", "message", map[string]any{
		"message": map[string]any{"role": "user", "content": "<skill name=\"inspect\" location=\"/skills/inspect/SKILL.md\">\nSKILL_BODY_MARKER\n</skill>\n\nInspect this"},
	})
	m.renderSessionEntryList([]SessionEntry{entry}, false)
	if got := strings.Join(m.chatContainer.Render(120), "\n"); !strings.Contains(got, "[skill]") || strings.Contains(got, "SKILL_BODY_MARKER") {
		t.Fatalf("initial skill view=%q", got)
	}
	ui := &ExtUIContext{m: m}
	for _, expanded := range []bool{true, false} {
		ui.SetToolsExpanded(expanded)
		if got := strings.Join(m.chatContainer.Render(120), "\n"); strings.Contains(got, "SKILL_BODY_MARKER") != expanded {
			t.Fatalf("expanded=%v: skill view=%q", expanded, got)
		}
	}
}

func BenchmarkToolsExpansionMountedTranscript(b *testing.B) {
	for _, count := range []int{1, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			m, terminal := newTickRenderProbe(b, "regular")
			for range count {
				m.chatContainer.Add(tui.NewSkillInvocationMessage(tui.ParsedSkillBlock{Name: "inspect", Content: "skill body"}))
			}
			m.tuiInst.Render()
			terminal.take()
			b.ReportAllocs()
			for b.Loop() {
				m.setAllToolsExpanded(true)
				m.setAllToolsExpanded(false)
				terminal.take()
			}
		})
	}
}
