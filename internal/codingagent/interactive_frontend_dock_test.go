package codingagent

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type dockEntry struct {
	id   string
	node frontend.Node
}

// dockTree replays the session's dock ops into the nodes it holds.
func dockTree(t *testing.T, session *fakeFrontendSession) []dockEntry {
	t.Helper()
	var dock []dockEntry
	for _, frame := range session.frames {
		for _, op := range frame.Ops {
			if op.Region != frontend.RegionDock {
				continue
			}
			switch op.Kind {
			case frontend.Insert:
				dock = append(dock[:op.Index], append([]dockEntry{{op.ID, op.Node}}, dock[op.Index:]...)...)
			case frontend.Update:
				if op.Index >= len(dock) || dock[op.Index].id != op.ID {
					t.Fatalf("update %s at %d: not there", op.ID, op.Index)
				}
				dock[op.Index].node = op.Node
			case frontend.Remove:
				if op.Index >= len(dock) || dock[op.Index].id != op.ID {
					t.Fatalf("remove %s at %d: not there", op.ID, op.Index)
				}
				dock = append(dock[:op.Index], dock[op.Index+1:]...)
			}
		}
	}
	return dock
}

func dockNode(t *testing.T, session *fakeFrontendSession, id string) (frontend.Node, bool) {
	t.Helper()
	for _, entry := range dockTree(t, session) {
		if entry.id == id {
			return entry.node, true
		}
	}
	return nil, false
}

func dockLinesText(t *testing.T, session *fakeFrontendSession) string {
	t.Helper()
	var text []string
	for _, entry := range dockTree(t, session) {
		if lines, ok := entry.node.(frontend.Lines); ok {
			text = append(text, widthx.StripAnsi(strings.Join(lines.Lines, "\n")))
		}
	}
	return strings.Join(text, "\n")
}

func dockOpsSince(session *fakeFrontendSession, n int) []frontend.Op {
	var ops []frontend.Op
	for _, frame := range session.frames[n:] {
		for _, op := range frame.Ops {
			if op.Region == frontend.RegionDock {
				ops = append(ops, op)
			}
		}
	}
	return ops
}

// The footer node carries what Pi's footer shows, from the same helpers
// renderFooter draws with: the abbreviated cwd, branch and session name,
// the session's usage totals and cache-hit rate, the context usage against
// the limits model's window, the model with the provider only while several
// are available, the thinking level only for a model that reasons ("off"
// when unset), the routed model, and the extension statuses in key order.
func TestFooterNodeCarriesThePiFooterData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PI_EXPERIMENTAL", "")
	t.Setenv("PIG_EXPERIMENTAL", "")
	provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "anthropic"})
	reasoning := &ai.Model{ID: "claude", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 200_000, MaxThinking: ai.ThinkingLevelHigh}}
	plain := &ai.Model{ID: "gpt-4o", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}}
	rate := 60.0
	data := footerData{
		model:              reasoning,
		cwd:                filepath.Join(home, "src", "pig"),
		gitBranch:          "main",
		sessionName:        "fix",
		usage:              footerUsageTotals{input: 1200, output: 340, cacheRead: 900, cacheWrite: 10, cost: 0.0125, latestCacheHitRate: &rate},
		contextTokens:      50_000,
		autoCompactEnabled: true,
		providerCount:      2,
		usingSubscription:  true,
		extensionStatuses:  map[string]string{"b": "two\n  words", "a": "one", "c": " \t"},
	}
	want := frontend.Footer{
		Cwd:                filepath.Join("~", "src", "pig"),
		GitBranch:          "main",
		SessionName:        "fix",
		UsageTotals:        frontend.UsageTotals{Input: 1200, Output: 340, CacheRead: 900, CacheWrite: 10, Cost: 0.0125},
		LatestCacheHitRate: &rate,
		UsingSubscription:  true,
		ContextUsage:       frontend.ContextUsage{Tokens: 50_000, Percent: 25, ContextWindow: 200_000},
		AutoCompact:        true,
		Model:              "claude",
		Provider:           "anthropic",
		ThinkingLevel:      "off",
		ExtensionStatuses:  []string{"one", "two words"},
	}
	if got := footerNode(data); !reflect.DeepEqual(got, want) {
		t.Fatalf("footer = %#v\nwant %#v", got, want)
	}
	lines := renderFooter(data, 120)
	if line := stripANSI(strings.Join(lines, "\n")); !strings.Contains(line, "(anthropic) claude • thinking off") || !strings.Contains(line, "25.0%/200k (auto)") || !strings.Contains(line, "one two words") {
		t.Fatalf("drawn footer = %q", line)
	}

	for name, tc := range map[string]struct {
		change func(*footerData)
		check  func(frontend.Footer) bool
	}{
		"thinking level": {func(d *footerData) { d.thinkingLevel = "high" }, func(f frontend.Footer) bool { return f.ThinkingLevel == "high" }},
		"no reasoning, one provider": {func(d *footerData) { d.model, d.providerCount = plain, 1 }, func(f frontend.Footer) bool {
			return f.Model == "gpt-4o" && f.Provider == "" && f.ThinkingLevel == "" && f.ContextUsage.ContextWindow == 128_000
		}},
		"routed": {func(d *footerData) {
			d.routed = &RoutedModelSelection{Model: &ai.Model{ID: "haiku", Capabilities: ai.ModelCapabilities{ContextWindow: 100_000}}, ThinkingLevel: ai.ThinkingLow}
		}, func(f frontend.Footer) bool {
			return f.Routed == frontend.RoutedModel{Model: "haiku", ThinkingLevel: "low"} && f.ContextUsage.ContextWindow == 100_000 && f.ContextUsage.Percent == 50
		}},
		"unknown context": {func(d *footerData) { d.contextTokens, d.contextUnknown = 0, true }, func(f frontend.Footer) bool {
			return f.ContextUsage == frontend.ContextUsage{Unknown: true, ContextWindow: 200_000}
		}},
		"no model, no cwd": {func(d *footerData) { d.model, d.cwd = nil, "" }, func(f frontend.Footer) bool {
			return f.Model == "unknown" && f.Provider == "" && f.ThinkingLevel == "" && f.Cwd == "."
		}},
		"no statuses": {func(d *footerData) { d.extensionStatuses = map[string]string{"a": " "} }, func(f frontend.Footer) bool { return f.ExtensionStatuses == nil }},
	} {
		changed := data
		tc.change(&changed)
		if got := footerNode(changed); !tc.check(got) {
			t.Fatalf("%s: footer = %#v", name, got)
		}
	}
	t.Setenv("PI_EXPERIMENTAL", "1")
	if !footerNode(data).Experimental {
		t.Fatal("experimental features are not reported")
	}
}

// A turn shows the working indicator as its own dock node while the agent
// works, never as ANSI border lines; spinner ticks send nothing, a working
// message or an extension's frames update the node, other indicators report
// their kind, and the node goes when the turn ends.
func TestFrontendDockReportsTheWorkingIndicator(t *testing.T) {
	m, _, session := newFrontendEditorProbe(t)
	m.editor.EmbedWorkingStatus = true
	m.agent = mustNewAgent(agent.AgentOptions{})
	m.tuiInst.Render()
	if _, ok := dockNode(t, session, "working"); ok {
		t.Fatal("an idle dock reports a working indicator")
	}

	m.handleAgentEvent(agent.AgentStartEvent{})
	m.tuiInst.Render()
	working, ok := dockNode(t, session, "working")
	if want := (frontend.Working{Kind: frontend.WorkingAgent, Message: "Working", Interval: 80 * time.Millisecond}); !ok || !reflect.DeepEqual(working, want) {
		t.Fatalf("working = %#v, want %#v", working, want)
	}
	ids := []string{}
	for _, entry := range dockTree(t, session) {
		ids = append(ids, entry.id)
	}
	if strings.Join(ids, ",") != "dock,working,editor,dock.below,footer" {
		t.Fatalf("dock = %q", ids)
	}
	if text := dockLinesText(t, session); strings.Contains(text, "─") || strings.Contains(text, "Working") {
		t.Fatalf("dock lines carry the editor border or status: %q", text)
	}

	frames := len(session.frames)
	for i := range 3 {
		m.tickStatusIndicators(time.Now().Add(time.Duration(i+1) * time.Second))
		m.tuiInst.Render()
	}
	if ops := dockOpsSince(session, frames); len(ops) != 0 {
		t.Fatalf("spinner ticks sent %#v", ops)
	}

	m.setWorkingMessage("Reading files")
	m.tuiInst.Render()
	ops := dockOpsSince(session, frames)
	if len(ops) != 1 || ops[0].ID != "working" || ops[0].Node.(frontend.Working).Message != "Reading files" {
		t.Fatalf("ops for a working message = %#v", ops)
	}
	m.setWorkingIndicator(&extension.WorkingIndicatorOptions{Frames: new([]string{"◐", "◑"}), IntervalMs: new(120.0)})
	m.tuiInst.Render()
	working, _ = dockNode(t, session, "working")
	if got := working.(frontend.Working); !reflect.DeepEqual(got.Frames, []string{"◐", "◑"}) || got.Interval != 120*time.Millisecond {
		t.Fatalf("working with an extension's frames = %#v", got)
	}

	m.handleAgentEvent(agent.AgentEndEvent{})
	m.tuiInst.Render()
	if node, ok := dockNode(t, session, "working"); ok {
		t.Fatalf("working after the turn = %#v", node)
	}

	m.showCompactionStatusIndicator("manual")
	m.tuiInst.Render()
	working, _ = dockNode(t, session, "working")
	if got, ok := working.(frontend.Working); !ok || got.Kind != frontend.WorkingCompaction || got.Message != "Compacting context... (escape to cancel)" {
		t.Fatalf("compaction indicator = %#v", working)
	}
	m.clearStatusIndicator("")
	m.tuiInst.Render()
	if _, ok := dockNode(t, session, "working"); ok {
		t.Fatal("cleared indicator still reported")
	}
}

// The footer is the dock's last node. A thinking-level change is one update
// of it, an extension status joins it, and an extension's footer replaces it
// with lines until the extension clears it.
func TestFrontendDockReportsTheFooterUntilAnExtensionReplacesIt(t *testing.T) {
	m, _, session := newFrontendEditorProbe(t)
	provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "anthropic"})
	m.statusLine.SetModel(&ai.Model{ID: "claude", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 200_000, MaxThinking: ai.ThinkingLevelHigh}})
	m.statusLine.SetThinkingLevel("medium")
	m.tuiInst.Render()
	tree := dockTree(t, session)
	last := tree[len(tree)-1]
	footer, ok := last.node.(frontend.Footer)
	if last.id != "footer" || !ok || footer.Model != "claude" || footer.ThinkingLevel != "medium" || footer.ContextUsage.ContextWindow != 200_000 {
		t.Fatalf("last dock node = %q %#v", last.id, last.node)
	}
	if text := dockLinesText(t, session); strings.Contains(text, "claude") {
		t.Fatalf("the footer also draws as lines: %q", text)
	}

	frames := len(session.frames)
	m.statusLine.SetThinkingLevel("high")
	m.tuiInst.Render()
	ops := dockOpsSince(session, frames)
	if len(ops) != 1 || ops[0].Kind != frontend.Update || ops[0].ID != "footer" || ops[0].Node.(frontend.Footer).ThinkingLevel != "high" {
		t.Fatalf("ops for a thinking change = %#v", ops)
	}
	m.statusLine.SetExtensionStatus("lint", "lint ok")
	m.tuiInst.Render()
	if node, _ := dockNode(t, session, "footer"); !reflect.DeepEqual(node.(frontend.Footer).ExtensionStatuses, []string{"lint ok"}) {
		t.Fatalf("footer with a status = %#v", node)
	}

	ui := &ExtUIContext{m: m}
	ui.SetFooter(extension.FrameFooter([]string{"custom footer"}, 0))
	m.tuiInst.Render()
	if node, ok := dockNode(t, session, "footer"); ok {
		t.Fatalf("footer node beside an extension's footer: %#v", node)
	}
	if text := dockLinesText(t, session); !strings.Contains(text, "custom footer") {
		t.Fatalf("the extension's footer is missing: %q", text)
	}
	ui.SetFooter(nil)
	m.tuiInst.Render()
	if _, ok := dockNode(t, session, "footer"); !ok || strings.Contains(dockLinesText(t, session), "custom footer") {
		t.Fatalf("the footer did not come back: %q", dockLinesText(t, session))
	}
}
