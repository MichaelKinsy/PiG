package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type treeOracleNode struct {
	Entry          json.RawMessage   `json:"entry"`
	Children       []*treeOracleNode `json:"children"`
	Label          string            `json:"label,omitempty"`
	LabelTimestamp string            `json:"labelTimestamp,omitempty"`
}

type treeOracleProbe struct {
	Theme           string            `json:"theme"`
	Width           int               `json:"width"`
	Height          int               `json:"height"`
	Tree            []*treeOracleNode `json:"tree"`
	Leaf            string            `json:"leaf,omitempty"`
	InitialSelected string            `json:"initialSelected,omitempty"`
	Filter          string            `json:"filter,omitempty"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type treeOracleResult struct {
	Steps  [][]string `json:"steps"`
	Frames [][]string `json:"frames"`
}

func treeOracleFixtures() map[string][]*treeOracleNode {
	user := func(id, parent, text string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2018-02-01T10:00:00.000Z","message":{"role":"user","content":%q,"timestamp":1517479200000}}`, id, jsonParent(parent), text))
	}
	assistant := func(id, parent, text string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2018-02-01T10:01:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":%q}],"api":"openai-responses","provider":"openai","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1517479260000}}`, id, jsonParent(parent), text))
	}
	toolResult := func(id, parent, text string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2018-02-01T10:02:00.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":%q}],"isError":false,"timestamp":1517479320000}}`, id, jsonParent(parent), text))
	}
	modelChange := func(id, parent string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"type":"model_change","id":%q,"parentId":%s,"timestamp":"2018-02-01T10:03:00.000Z","provider":"openai","modelId":"gpt"}`, id, jsonParent(parent)))
	}
	node := func(entry json.RawMessage, kids ...*treeOracleNode) *treeOracleNode {
		return &treeOracleNode{Entry: entry, Children: append([]*treeOracleNode{}, kids...)}
	}
	labeled := func(n *treeOracleNode, label string) *treeOracleNode {
		n.Label, n.LabelTimestamp = label, "2018-02-01T10:30:00.000Z"
		return n
	}
	linear := node(user("u1", "", "first question about parsers"), node(assistant("a1", "u1", "answer about parsers"),
		node(user("u2", "a1", "second question on deploy"), node(assistant("a2", "u2", "answer on deploy")))))
	branched := node(user("u1", "", "start project"), node(assistant("a1", "u1", "plan"),
		labeled(node(user("u2", "a1", "branch one request"), node(assistant("a2", "u2", "branch one done"), node(user("u3", "a2", "follow up one"), node(assistant("a3", "u3", "follow up answer")))),
			node(toolResult("t1", "u2", "tool output text"))), "milestone"),
		node(modelChange("m1", "a1"), node(user("u4", "m1", "branch two request"), node(assistant("a4", "u4", "branch two done"))),
			node(user("u5", "m1", "branch two alternative"))),
		node(user("u6", "a1", "branch three"))))
	long := node(user("l0", "", "long chain start"))
	cur := long
	for i := 1; i <= 14; i++ {
		var e json.RawMessage
		if i%2 == 1 {
			e = assistant(fmt.Sprintf("l%d", i), fmt.Sprintf("l%d", i-1), fmt.Sprintf("assistant line %d", i))
		} else {
			e = user(fmt.Sprintf("l%d", i), fmt.Sprintf("l%d", i-1), fmt.Sprintf("user line %d", i))
		}
		next := node(e)
		cur.Children = append(cur.Children, next)
		cur = next
	}
	// Every level forks, so the indent grows past a narrow terminal and the selected row's anchor scrolls the body sideways.
	deep := node(user("d0", "", "deep chain start with a long enough text to overflow a narrow terminal"))
	tip := deep
	for i := 1; i <= 12; i++ {
		fork := node(user(fmt.Sprintf("f%d", i), fmt.Sprintf("d%d", i-1), fmt.Sprintf("side branch %d with its own long request text", i)))
		next := node(assistant(fmt.Sprintf("d%d", i), fmt.Sprintf("d%d", i-1), fmt.Sprintf("chain answer %d that keeps going for a long while", i)))
		tip.Children = append(tip.Children, fork, next)
		tip = next
	}
	return map[string][]*treeOracleNode{
		"deep":     {deep},
		"linear":   {linear},
		"branched": {branched},
		"multi":    {linear, node(user("x1", "", "second root"), node(assistant("x2", "x1", "second root answer")))},
		"long":     {long},
		"empty":    {},
	}
}

func jsonParent(parent string) string {
	if parent == "" {
		return "null"
	}
	return fmt.Sprintf("%q", parent)
}

// tree-selector.ts handleInput against pinned Pi: the TreeList key chain (up/down wrap, fold/unfold and branch jumps, page
// moves, confirm, copy, cancel clearing the search first, filter modes, backspace, label edit, label timestamps, type-to-search)
// and the LabelInput (confirm saves a trimmed label, cancel hides it, everything else edits). Every frame is compared byte for byte, colours,
// the selected row's background and the unpadded rows included.
func TestTreeSelectorInputMatchesPi(t *testing.T) {
	fixtures := treeOracleFixtures()
	scripts := [][]string{
		{},
		{"\x1b[A", "\x1b[A", "\x1b[B"},
		{"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B"},
		{"\x1b[5~", "\x1b[5~", "\x1b[6~", "\x1b[6~", "\x1b[6~"},
		// four keys get the 12-row terminal (six visible lines), so on the 15-entry chain each page move lands inside the list instead of clamping at an end
		{"\x1b[5~", "\x1b[6~", "\x1b[5~", "\x1b[5~"},
		{"\x1b[D", "\x1b[C", "\x1b[C"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\r"},
		{"\x1b[A", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5C", "\x1b[1;5C"},
		{"\x1b[A", "\x1b[A", "\x1b[1;5D", "\x1b[1;5C", "\x1b[1;5C", "\x1b[1;5C"},
		{"\x1b[A", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5C"},
		{"\x04", "\x14", "\x15", "\x0c", "\x01"},
		{"\x1b[1;3D", "\x1b[1;3C"},
		{"\x1bd", "\x1b[Z", "\t"},
		{"d", "e", "p", "l", "\x1b[A", "\x7f", "\x7f", "\x1b", "\x1b"},
		{"b", "r", "a", "n", "c", "h", "\x1b[B", "\r"},
		{"\x1b", "\x1b"},
		{"u", "\x1b[5~", "\x1b[1;5D", "\x1b[B", "\x1b", "\x1b[B"},
		{"s", "\x1b[5~", "\x1b[1;5D", "\x7f", "\x1b[B"},
		{"z", "z", "z", "\x1b[A", "\x1b[B", "\r"},
		{"\x7f", "\x7f", "\x1b[A"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "L", "h", "i", "\r"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "L", "h", "i", "\x1b", "\r"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "L", "\x7f", "\x7f", "\x1b[D", "x", "\r"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "L", " ", "a", " ", "\r"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "L", "L", "\x1b[B", "\x1b"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "L", "q", "\r"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x0f", "\x0f", "\x0f", "\x0f"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x18"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[1;3D", "\x1b[1;3D"},
		// Label timestamps of a label saved now would show the wall clock, so timestamps are toggled off again before any save.
		{"T", "\x1b[A", "T", "L", "x", "\r"},
		{"\x0c", "T", "\x1b[A", "T", "\x1b[A", "L", "\r", "\x0f", "\x0f"},
	}
	bindings := []map[string][]string{
		nil,
		{"tui.select.up": {"k"}, "tui.select.down": {"j"}},
		{"app.tree.foldOrUp": {"ctrl+x"}, "tui.select.pageUp": {"ctrl+x"}, "app.tree.filter.cycleForward": {"tab"}},
		{"tui.select.confirm": {"ctrl+x"}, "app.message.copy": {"ctrl+x"}},
		{"tui.select.cancel": {"ctrl+x"}, "tui.editor.deleteCharBackward": {"ctrl+x"}},
		{"app.tree.editLabel": {"ctrl+x"}, "tui.select.confirm": {"ctrl+y"}},
	}
	probeSets := []struct {
		fixture, leaf, initial, filter string
	}{
		{"linear", "a2", "", ""},
		{"branched", "a2", "", ""},
		{"branched", "u2", "", "all"},
		{"branched", "a4", "u6", "no-tools"},
		{"multi", "x2", "", "user-only"},
		{"long", "l14", "", ""},
		{"empty", "", "", ""},
		{"deep", "d12", "", ""},
	}
	var probes []treeOracleProbe
	for _, theme := range []string{"dark", "light"} {
		for _, set := range probeSets {
			for _, b := range bindings {
				for _, keys := range scripts {
					probes = append(probes, treeOracleProbe{Theme: theme, Width: 100, Height: map[bool]int{true: 12, false: 40}[len(keys)%2 == 0], Tree: fixtures[set.fixture], Leaf: set.leaf, InitialSelected: set.initial, Filter: set.filter, Bindings: b, Keys: keys})
				}
			}
		}
	}
	// Narrow terminals clip the help, search, label and tree rows and pan deep trees; the key chains run with the default bindings only.
	for _, width := range []int{24, 38, 64} {
		for _, set := range probeSets {
			for _, keys := range scripts {
				probes = append(probes, treeOracleProbe{Theme: "dark", Width: width, Height: map[bool]int{true: 12, false: 40}[len(keys)%2 == 0], Tree: fixtures[set.fixture], Leaf: set.leaf, InitialSelected: set.initial, Filter: set.filter, Keys: keys})
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/tree_selector.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []treeOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousTheme, previousCaps, previousKeys := tui.ActiveTheme(), tui.GetCapabilities(), tui.GetTUIKeybindings()
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetTUIKeybindings(previousKeys)
	})
	var toSession func(n *treeOracleNode) *SessionTreeNode
	toSession = func(n *treeOracleNode) *SessionTreeNode {
		var base SessionEntryBase
		if err := json.Unmarshal(n.Entry, &base); err != nil {
			t.Fatal(err)
		}
		out := &SessionTreeNode{Entry: sessionentry.DecodeSessionEntry(n.Entry), Label: n.Label, LabelTimestamp: n.LabelTimestamp}
		for _, c := range n.Children {
			out.Children = append(out.Children, toSession(c))
		}
		return out
	}
	// The scripts must reach every event kind in Pi, or the comparison below would pass vacuously.
	reached := map[string]bool{}
	for _, result := range expected {
		for _, event := range result.Steps[len(result.Steps)-1] {
			reached[event[:strings.IndexByte(event, ':')+1]] = true
		}
		if last := result.Steps[len(result.Steps)-1]; len(last) > 0 && last[len(last)-1] == "cancel" {
			reached["cancel:"] = true
		}
	}
	for _, kind := range []string{"select:", "cancel:", "label:", "copy:"} {
		if !reached[kind] {
			t.Fatalf("no probe reaches a %q event in Pi", kind)
		}
	}
	failures := 0
	for i, probe := range probes {
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
		tui.SetTheme(probe.Theme)
		bindingMap := map[string][]KeyID{}
		for action, keys := range probe.Bindings {
			for _, key := range keys {
				bindingMap[action] = append(bindingMap[action], KeyID(key))
			}
		}
		km := DefaultKeybindingsManager()
		km.SetUserBindings(bindingMap)
		km.syncToTUI()
		root := &SessionTreeNode{}
		for _, r := range probe.Tree {
			root.Children = append(root.Children, toSession(r))
		}
		var events []string
		selector := tui.NewTreeSelectWithInitialFilter("Session tree", &treeNodeAdapter{n: root, f: newTreeRowFormatter(nil)}, probe.Filter)
		selector.MaxVisibleLines = tui.TreeVisibleLines(probe.Height)
		// testdata/tree_selector.mjs focuses Pi's component, so a label input draws the IME cursor marker; focus Pig's alike.
		selector.SetFocused(true)
		selector.SetInitialCursor(probe.Leaf, probe.InitialSelected)
		selector.OnCopy = func(text *string) {
			if text == nil {
				events = append(events, "copy:")
			} else {
				events = append(events, "copy:"+*text)
			}
		}
		selector.OnLabelEdit = func(id, label string) { events = append(events, "label:"+id+":"+label) }
		got := treeOracleResult{Steps: [][]string{append([]string{}, events...)}, Frames: [][]string{selector.Render(probe.Width)}}
		completedAt := len(probe.Keys)
		for keyIndex, key := range probe.Keys {
			selector.HandleInput(key)
			if selector.Done() {
				if selector.Cancelled() {
					events = append(events, "cancel")
				} else {
					events = append(events, "select:"+selector.SelectedID())
				}
			}
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, selector.Render(probe.Width))
			if selector.Done() {
				completedAt = keyIndex + 1
				break
			}
		}
		want := expected[i]
		want.Steps, want.Frames = want.Steps[:completedAt+1], want.Frames[:completedAt+1]
		if !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Frames, want.Frames) {
			failures++
			if failures <= 6 {
				t.Errorf("probe %d keys=%q bindings=%v fixture leaf=%q filter=%q h=%d:\nsteps = %q\nPi    = %q\n%s", i, probe.Keys, probe.Bindings, probe.Leaf, probe.Filter, probe.Height, got.Steps, want.Steps, firstFrameDifference(got.Frames, want.Frames))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

var styleSGR = regexp.MustCompile("\x1b\\[(?:[0-6]|[8-9]|[1-9][0-9]|[34]8;[0-9;]+|2[0-5])?m")

// visibleText drops every style sequence except the cursor's inverse video and ignores trailing blanks. Only the first-time-setup oracle uses it; the tree, scoped-model, settings and submenu oracles compare frames byte for byte.
func visibleText(frames [][]string) [][]string {
	out := make([][]string, len(frames))
	for i, frame := range frames {
		out[i] = make([]string, len(frame))
		for j, line := range frame {
			out[i][j] = strings.TrimRight(styleSGR.ReplaceAllString(line, ""), " ")
		}
	}
	return out
}
