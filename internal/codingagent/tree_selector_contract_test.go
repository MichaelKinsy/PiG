package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
	"github.com/MichaelKinsy/PiG/tui"
)

// The tree selector contracts (coding-agent/tree-selector/*) pin one obligation per case: the case's keys run through pinned Pi's
// TreeSelectorComponent and through PiG's NewTreeSelectorComponent, every frame and every callback event must be equal byte for byte, and the
// case's own check states the rule it exercises in terms of the rendered rows, so a case cannot pass by both sides doing nothing.

type treeContractCase struct {
	name                  string
	fixture               string
	leaf, initial, filter string
	bindings              map[string][]string
	width, height         int
	zeroWidth             bool // run at width 0 instead of the default 100
	theme                 string
	keys                  []string
	check                 func(t *testing.T, tr treeContractTrace)
}

// treeContractTrace is what one run observed: index 0 is the state before any key.
type treeContractTrace struct {
	Steps  [][]string
	Frames [][]string
}

func (tr treeContractTrace) lastFrame() []string { return tr.Frames[len(tr.Frames)-1] }
func (tr treeContractTrace) events() []string    { return tr.Steps[len(tr.Steps)-1] }

var treePositionLine = regexp.MustCompile(`^  \((\d+)/(\d+)\)(.*)$`)

// treePosition is the muted "(n/total)" line of the list with its status labels, or "" when the frame has none.
func treePosition(frame []string) string {
	for _, line := range frame {
		if plain := treePlain(line); treePositionLine.MatchString(plain) {
			return strings.TrimPrefix(plain, "  ")
		}
	}
	return ""
}

// treeSelectedRow is the plain text of the highlighted row after its cursor, or "".
func treeSelectedRow(frame []string) string {
	for _, line := range frame {
		if plain := treePlain(line); strings.HasPrefix(plain, "› ") {
			return strings.TrimPrefix(plain, "› ")
		}
	}
	return ""
}

var treeAPC = regexp.MustCompile("\x1b_[^\x07]*\x07")

// treePlain drops the style sequences and the hardware-cursor markers of one line.
func treePlain(line string) string { return treeAPC.ReplaceAllString(stripANSITest(line), "") }

func treeFrameText(frame []string) string {
	plain := make([]string, len(frame))
	for i, line := range frame {
		plain[i] = treePlain(line)
	}
	return strings.Join(plain, "\n")
}

func treeSearchRow(frame []string) string {
	for _, line := range frame {
		if plain := treePlain(line); strings.HasPrefix(plain, "  Type to search:") {
			return plain
		}
	}
	return ""
}

func tcParent(parent string) string {
	if parent == "" {
		return "null"
	}
	return fmt.Sprintf("%q", parent)
}

func tcUser(id, parent, text string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2018-02-01T10:00:00.000Z","message":{"role":"user","content":%q,"timestamp":1517479200000}}`, id, tcParent(parent), text))
}

func tcAssistant(id, parent, text string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2018-02-01T10:01:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":%q}],"api":"openai-responses","provider":"openai","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1517479260000}}`, id, tcParent(parent), text))
}

func tcToolResult(id, parent, text string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2018-02-01T10:02:00.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[{"type":"text","text":%q}],"isError":false,"timestamp":1517479320000}}`, id, tcParent(parent), text))
}

func tcModelChange(id, parent string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"model_change","id":%q,"parentId":%s,"timestamp":"2018-02-01T10:03:00.000Z","provider":"openai","modelId":"gpt"}`, id, tcParent(parent)))
}

func tcCompaction(id, parent, summary string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"compaction","id":%q,"parentId":%s,"timestamp":"2018-02-01T10:04:00.000Z","summary":%q,"firstKeptEntryId":"x","tokensBefore":1500}`, id, tcParent(parent), summary))
}

func tcBranchSummary(id, parent, summary string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"branch_summary","id":%q,"parentId":%s,"timestamp":"2018-02-01T10:05:00.000Z","fromId":"x","summary":%q}`, id, tcParent(parent), summary))
}

func tcErrorAssistant(id, parent, errorMessage string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2018-02-01T10:01:00.000Z","message":{"role":"assistant","content":[],"api":"openai-responses","provider":"openai","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"error","errorMessage":%q,"timestamp":1517479260000}}`, id, tcParent(parent), errorMessage))
}

func tcNode(entry json.RawMessage, kids ...*treeOracleNode) *treeOracleNode {
	return &treeOracleNode{Entry: entry, Children: append([]*treeOracleNode{}, kids...)}
}

func tcLabeled(n *treeOracleNode, label string) *treeOracleNode {
	n.Label, n.LabelTimestamp = label, "2018-02-01T10:30:00.000Z"
	return n
}

// treeContractFixtures are the trees the contract cases run on.
//
//	fork:   r > a > { b1 > b1a > b1u, b2 > b2a, b3 }       (8 rows in default order; b1 is a foldable segment start)
//	mixed:  r > a > t(toolResult) > m(model change) > u2 > lab(label "mark")
//	rich:   one chain of every copyable entry type
//	forest: two roots, the second with a branch
//	wide:   double-width, combining and emoji text with two labelled siblings
func treeContractFixtures() map[string][]*treeOracleNode {
	fork := tcNode(tcUser("r", "", "root question"), tcNode(tcAssistant("a", "r", "root answer"),
		tcNode(tcUser("b1", "a", "branch one"), tcNode(tcAssistant("b1a", "b1", "branch one answer"), tcNode(tcUser("b1u", "b1a", "branch one follow")))),
		tcNode(tcUser("b2", "a", "branch two"), tcNode(tcAssistant("b2a", "b2", "branch two answer"))),
		tcNode(tcUser("b3", "a", "branch three"))))
	mixed := tcNode(tcUser("r", "", "first request"), tcNode(tcAssistant("a", "r", "first answer"),
		tcNode(tcToolResult("t", "a", "tool output"), tcNode(tcModelChange("m", "t"),
			tcNode(tcUser("u2", "m", "second request"), tcLabeled(tcNode(tcAssistant("lab", "u2", "labeled answer")), "mark"))))))
	rich := tcNode(tcUser("ru", "", "copy me"), tcNode(tcAssistant("ra", "ru", "answer line one"),
		tcNode(tcModelChange("rm", "ra"), tcNode(tcCompaction("rc", "rm", "the summary"),
			tcNode(tcBranchSummary("rb", "rc", "branch summary text"), tcNode(tcErrorAssistant("re", "rb", "boom")))))))
	forest := []*treeOracleNode{
		tcNode(tcUser("f1", "", "first root"), tcNode(tcAssistant("f1a", "f1", "first root answer"))),
		tcNode(tcUser("g1", "", "second root"), tcNode(tcUser("g1a", "g1", "second left"), tcNode(tcAssistant("g1x", "g1a", "left leaf"))), tcNode(tcUser("g1b", "g1", "second right"))),
	}
	single := tcNode(tcUser("only", "", "only entry"))
	wide := tcNode(tcUser("w0", "", "宽字符 text 😀 with wide cells and a long tail to force truncation at narrow widths"), tcNode(tcAssistant("w1", "w0", "ｆｕｌｌｗｉｄｔｈ ｔｅｘｔ answer"),
		tcLabeled(tcNode(tcUser("w2", "w1", "e\u0301 combining and 日本語 mixed")), "日本語ラベル"),
		tcLabeled(tcNode(tcUser("w3", "w1", "plain sibling")), "x")))
	out := treeOracleFixtures()
	out["fork"], out["mixed"], out["rich"], out["forest"], out["single"], out["wide"] = []*treeOracleNode{fork}, []*treeOracleNode{mixed}, []*treeOracleNode{rich}, forest, []*treeOracleNode{single}, []*treeOracleNode{wide}
	return out
}

const (
	tkUp         = "\x1b[A"
	tkDown       = "\x1b[B"
	tkLeft       = "\x1b[D"
	tkRight      = "\x1b[C"
	tkPageUp     = "\x1b[5~"
	tkPageDown   = "\x1b[6~"
	tkEnter      = "\r"
	tkEscape     = "\x1b"
	tkCtrlLeft   = "\x1b[1;5D"
	tkCtrlRight  = "\x1b[1;5C"
	tkAltLeft    = "\x1b[1;3D"
	tkAltRight   = "\x1b[1;3C"
	tkBackspace  = "\x7f"
	tkCtrlD      = "\x04"
	tkCtrlT      = "\x14"
	tkCtrlU      = "\x15"
	tkCtrlL      = "\x0c"
	tkCtrlA      = "\x01"
	tkCtrlO      = "\x0f"
	tkShiftCtrlO = "\x1b[111;6u"
	tkCopy       = "\x18"
)

// runTreeContracts runs every case against Pi's component in one Node process and then compares each case in its own subtest.
func runTreeContracts(t *testing.T, cases []treeContractCase) []treeContractTrace {
	t.Helper()
	fixtures := treeContractFixtures()
	probes := make([]treeOracleProbe, len(cases))
	for i, c := range cases {
		tree, ok := fixtures[c.fixture]
		if !ok {
			t.Fatalf("case %q: unknown fixture %q", c.name, c.fixture)
		}
		width, height, theme := c.width, c.height, c.theme
		if width == 0 && !c.zeroWidth {
			width = 100
		}
		if height == 0 {
			height = 40
		}
		if theme == "" {
			theme = "dark"
		}
		probes[i] = treeOracleProbe{Theme: theme, Width: width, Height: height, Tree: tree, Leaf: c.leaf, InitialSelected: c.initial, Filter: c.filter, Bindings: c.bindings, Keys: append([]string{}, c.keys...)}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/tree_selector.mjs", pigversion.UpstreamPackageVersion)
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
	if len(expected) != len(cases) {
		t.Fatalf("Pi answered %d of %d cases", len(expected), len(cases))
	}
	previousTheme, previousCaps, previousKeys := tui.ActiveTheme(), tui.GetCapabilities(), tui.GetTUIKeybindings()
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetTUIKeybindings(previousKeys)
	})
	traces := make([]treeContractTrace, len(cases))
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			probe := probes[i]
			got := replayTreeContract(t, probe)
			traces[i] = got
			want := expected[i]
			if !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Frames, want.Frames) {
				t.Fatalf("keys=%q differ from Pi:\nsteps = %q\nPi    = %q\n%s", probe.Keys, got.Steps, want.Steps, firstFrameDifference(got.Frames, want.Frames))
			}
			if c.check != nil {
				c.check(t, got)
			}
		})
	}
	return traces
}

// replayTreeContract drives PiG's production constructor the way testdata/tree_selector.mjs drives Pi's: callbacks append events and every key is followed by a render.
func replayTreeContract(t *testing.T, probe treeOracleProbe) treeContractTrace {
	t.Helper()
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme(probe.Theme)
	bindingMap := map[string][]KeyID{}
	for action, keys := range probe.Bindings {
		bindingMap[action] = []KeyID{}
		for _, key := range keys {
			bindingMap[action] = append(bindingMap[action], KeyID(key))
		}
	}
	km := DefaultKeybindingsManager()
	km.SetUserBindings(bindingMap)
	km.syncToTUI()
	var toSession func(n *treeOracleNode) *SessionTreeNode
	toSession = func(n *treeOracleNode) *SessionTreeNode {
		out := &SessionTreeNode{Entry: sessionentry.DecodeSessionEntry(n.Entry), Label: n.Label, LabelTimestamp: n.LabelTimestamp}
		for _, c := range n.Children {
			out.Children = append(out.Children, toSession(c))
		}
		return out
	}
	roots := make([]*SessionTreeNode, 0, len(probe.Tree))
	for _, r := range probe.Tree {
		roots = append(roots, toSession(r))
	}
	var events []string
	// tree-selector.ts's constructor arms setTimeout(onCancel, 100) for an empty tree. Pi's harness replays synchronously, so that callback runs on its
	// event loop after the replay and no step of Pi's trace holds it. PiG's timer fires on its own goroutine, mid-replay once a slow -race run outlasts
	// 100ms: only the goroutine that replays records an event, so the timer's cancel is neither in the trace nor a race with the replay's reads of it.
	replayer := goroutineid.Current()
	record := func(event string) {
		if goroutineid.Current() == replayer {
			events = append(events, event)
		}
	}
	var leaf, initial *string
	if probe.Leaf != "" {
		leaf = &probe.Leaf
	}
	if probe.InitialSelected != "" {
		initial = &probe.InitialSelected
	}
	selector := NewTreeSelectorComponent(roots, leaf, probe.Height,
		func(id string) { record("select:" + id) },
		func() { record("cancel") },
		func(id string, label *string) {
			text := ""
			if label != nil {
				text = *label
			}
			record("label:" + id + ":" + text)
		}, initial, probe.Filter)
	selector.OnCopy = func(text *string) {
		if text == nil {
			record("copy:")
		} else {
			record("copy:" + *text)
		}
	}
	selector.SetFocused(true)
	if probe.afterBuild != nil {
		probe.afterBuild()
	}
	got := treeContractTrace{Steps: [][]string{append([]string{}, events...)}, Frames: [][]string{piJSONLines(selector.Render(probe.Width))}}
	for _, key := range probe.Keys {
		selector.HandleInput(key)
		got.Steps = append(got.Steps, append([]string{}, events...))
		got.Frames = append(got.Frames, piJSONLines(selector.Render(probe.Width)))
	}
	return got
}

// An empty tree's setTimeout(onCancel, 100) lands in no step of Pi's replay (Pi's harness returns first). The fake clock passes the 100ms while the replay
// is between building the selector and its first key, as a slow -race run does; the timer's cancel must stay out of the trace, and recording it from the
// timer's goroutine is what raced the replay's reads of the trace (TestTreeListHandleInputContractsMatchPi/down/moves-one-row on macOS).
func TestTreeContractReplayLeavesTheEmptyTreeSelfCancelOutOfTheTrace(t *testing.T) {
	previousTheme, previousCaps, previousKeys := tui.ActiveTheme(), tui.GetCapabilities(), tui.GetTUIKeybindings()
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetTUIKeybindings(previousKeys)
	})
	synctest.Test(t, func(t *testing.T) {
		got := replayTreeContract(t, treeOracleProbe{Theme: "dark", Width: 100, Height: 40, Keys: []string{tkDown}, afterBuild: func() {
			time.Sleep(150 * time.Millisecond)
			synctest.Wait()
		}})
		if want := [][]string{{}, {}}; !reflect.DeepEqual(got.Steps, want) {
			t.Fatalf("steps = %q, want %q: the empty tree's self-cancel timer reached the trace", got.Steps, want)
		}
	})
}

func wantPosition(want string) func(t *testing.T, tr treeContractTrace) {
	return func(t *testing.T, tr treeContractTrace) {
		t.Helper()
		if got := treePosition(tr.lastFrame()); got != want {
			t.Errorf("position line = %q, want %q", got, want)
		}
	}
}

func wantTotal(total int) func(t *testing.T, tr treeContractTrace) {
	return func(t *testing.T, tr treeContractTrace) {
		t.Helper()
		got := treePosition(tr.lastFrame())
		if want := fmt.Sprintf("/%d)", total); !strings.Contains(got, want) {
			t.Errorf("position line = %q, want %d rows", got, total)
		}
	}
}

func wantPositionAndRow(position, rowPart string) func(t *testing.T, tr treeContractTrace) {
	return func(t *testing.T, tr treeContractTrace) {
		t.Helper()
		if got := treePosition(tr.lastFrame()); got != position {
			t.Errorf("position line = %q, want %q", got, position)
		}
		if row := treeSelectedRow(tr.lastFrame()); !strings.Contains(row, rowPart) {
			t.Errorf("highlighted row = %q, want it to contain %q", row, rowPart)
		}
	}
}

func wantEvents(want ...string) func(t *testing.T, tr treeContractTrace) {
	return func(t *testing.T, tr treeContractTrace) {
		t.Helper()
		if got := tr.events(); !slices.Equal(got, want) {
			t.Errorf("events = %q, want %q", got, want)
		}
	}
}

func wantAll(checks ...func(t *testing.T, tr treeContractTrace)) func(t *testing.T, tr treeContractTrace) {
	return func(t *testing.T, tr treeContractTrace) {
		t.Helper()
		for _, c := range checks {
			c(t, tr)
		}
	}
}

func wantFrameContains(parts ...string) func(t *testing.T, tr treeContractTrace) {
	return func(t *testing.T, tr treeContractTrace) {
		t.Helper()
		text := treeFrameText(tr.lastFrame())
		for _, part := range parts {
			if !strings.Contains(text, part) {
				t.Errorf("frame lacks %q:\n%s", part, text)
			}
		}
	}
}

func wantFrameLacks(parts ...string) func(t *testing.T, tr treeContractTrace) {
	return func(t *testing.T, tr treeContractTrace) {
		t.Helper()
		text := treeFrameText(tr.lastFrame())
		for _, part := range parts {
			if strings.Contains(text, part) {
				t.Errorf("frame has %q:\n%s", part, text)
			}
		}
	}
}

func wantSearch(want string) func(t *testing.T, tr treeContractTrace) {
	return func(t *testing.T, tr treeContractTrace) {
		t.Helper()
		got := treeSearchRow(tr.lastFrame())
		// tree-selector.ts SearchLine: an empty query's row ends in a space, and the row of the focused selector ends in the cursor cell (a reverse-video space; the
		// cursor marker is invisible). The label input takes the cursor while a label is edited (`searchLine.focused = value && labelInput === null`).
		if want == "  Type to search:" {
			want += " "
		}
		if !strings.Contains(treeFrameText(tr.lastFrame()), "Label (empty to remove):") {
			want += " "
		}
		if got != want {
			t.Errorf("search row = %q, want %q", got, want)
		}
	}
}

// treeListInputCases are one obligation each of TreeList.handleInput (tree-selector.ts:994-1100), in Pi's branch order, plus the boundary transitions.
func treeListInputCases() []treeContractCase {
	rows := func(n int) string { return fmt.Sprintf("(%d/8)", n) }
	return []treeContractCase{
		// 1. tui.select.up
		{name: "up/moves-one-row", fixture: "fork", leaf: "b1u", keys: []string{tkUp}, check: wantPositionAndRow(rows(4), "branch one answer")},
		{name: "up/wraps-from-first-to-last", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkUp}, check: wantPositionAndRow(rows(8), "branch three")},
		{name: "up/single-node-stays", fixture: "single", leaf: "only", keys: []string{tkUp, tkUp}, check: wantPositionAndRow("(1/1)", "only entry")},
		{name: "up/empty-list-moves-nowhere", fixture: "empty", keys: []string{tkUp, tkUp}, check: wantAll(wantPosition("(0/0)"), wantFrameContains("No entries found"), wantEvents())},
		// 2. tui.select.down
		{name: "down/moves-one-row", fixture: "fork", leaf: "b1u", keys: []string{tkDown}, check: wantPositionAndRow(rows(6), "branch two")},
		{name: "down/wraps-from-last-to-first", fixture: "fork", leaf: "b1u", initial: "b3", keys: []string{tkDown}, check: wantPositionAndRow(rows(1), "root question")},
		{name: "down/single-node-stays", fixture: "single", leaf: "only", keys: []string{tkDown, tkDown}, check: wantPositionAndRow("(1/1)", "only entry")},
		{name: "down/empty-list-moves-nowhere", fixture: "empty", keys: []string{tkDown, tkDown}, check: wantAll(wantPosition("(0/0)"), wantEvents())},
		{name: "up-then-down/empty-list-reaches-first-row-index", fixture: "empty", keys: []string{tkUp, tkDown, tkEnter}, check: wantEvents()},
		// 3. app.tree.foldOrUp
		{name: "fold/folds-a-root-and-hides-its-subtree", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft}, check: wantAll(wantPositionAndRow("(1/1)", "root question"), wantFrameContains("⊞ "))},
		{name: "fold/folds-a-segment-start", fixture: "fork", leaf: "b1u", initial: "b1", keys: []string{tkCtrlLeft}, check: wantAll(wantPositionAndRow("(3/6)", "branch one"), wantFrameContains("⊞"), wantFrameLacks("branch one follow"))},
		{name: "fold/alt-left-is-the-second-default-key", fixture: "fork", leaf: "b1u", initial: "b2", keys: []string{tkAltLeft}, check: wantAll(wantPositionAndRow("(6/7)", "branch two"), wantFrameLacks("branch two answer"))},
		{name: "fold/leaf-jumps-up-to-the-enclosing-segment-start", fixture: "fork", leaf: "b1u", initial: "b2a", keys: []string{tkCtrlLeft}, check: wantPositionAndRow(rows(6), "branch two")},
		{name: "fold/segment-start-that-is-a-leaf-climbs-to-the-root", fixture: "fork", leaf: "b1u", initial: "b3", keys: []string{tkCtrlLeft}, check: wantPositionAndRow(rows(1), "root question")},
		{name: "fold/root-without-children-is-not-foldable", fixture: "single", leaf: "only", keys: []string{tkCtrlLeft}, check: wantAll(wantPositionAndRow("(1/1)", "only entry"), wantFrameLacks("⊞"))},
		{name: "fold/mid-chain-row-is-not-foldable", fixture: "fork", leaf: "b1u", initial: "a", keys: []string{tkCtrlLeft}, check: wantAll(wantPositionAndRow(rows(1), "root question"), wantFrameLacks("⊞"))},
		{name: "fold/empty-list-does-nothing", fixture: "empty", keys: []string{tkCtrlLeft, tkCtrlLeft}, check: wantAll(wantPosition("(0/0)"), wantEvents())},
		{name: "fold/folded-row-climbs-on-the-next-press", fixture: "fork", leaf: "b1u", initial: "b2", keys: []string{tkCtrlLeft, tkCtrlLeft}, check: wantPositionAndRow("(1/7)", "root question")},
		{name: "fold/cursor-moves-over-a-single-visible-row", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft, tkUp, tkDown}, check: wantPositionAndRow("(1/1)", "root question")},
		// 4. app.tree.unfoldOrDown
		{name: "unfold/unfolds-a-folded-row-in_place", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft, tkCtrlRight}, check: wantAll(wantPositionAndRow(rows(1), "root question"), wantFrameLacks("⊞ "))},
		{name: "unfold/alt-right-is-the-second-default-key", fixture: "fork", leaf: "b1u", initial: "b2", keys: []string{tkAltLeft, tkAltRight}, check: wantPositionAndRow("(6/8)", "branch two")},
		{name: "unfold/jumps-down-to-the-first-child-of-the-next-branch-point", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlRight}, check: wantPositionAndRow(rows(3), "branch one")},
		{name: "unfold/follows-first-children-to-the-leaf", fixture: "fork", leaf: "b1u", initial: "b1", keys: []string{tkCtrlRight}, check: wantPositionAndRow(rows(5), "branch one follow")},
		{name: "unfold/leaf-stays", fixture: "fork", leaf: "b1u", initial: "b3", keys: []string{tkCtrlRight}, check: wantPositionAndRow(rows(8), "branch three")},
		{name: "unfold/single-node-stays", fixture: "single", leaf: "only", keys: []string{tkCtrlRight}, check: wantPositionAndRow("(1/1)", "only entry")},
		{name: "unfold/empty-list-does-nothing", fixture: "empty", keys: []string{tkCtrlRight}, check: wantAll(wantPosition("(0/0)"), wantEvents())},
		// 5. tui.editor.cursorLeft / tui.select.pageUp
		{name: "pageup/left-arrow-pages-by-the-window", fixture: "long", leaf: "l14", height: 12, keys: []string{tkLeft}, check: wantPosition("(9/15)")},
		{name: "pageup/page-up-key-pages-by-the-window", fixture: "long", leaf: "l14", height: 12, keys: []string{tkPageUp}, check: wantPosition("(9/15)")},
		{name: "pageup/clamps-at-the-first-row", fixture: "long", leaf: "l14", height: 12, keys: []string{tkPageUp, tkPageUp, tkPageUp}, check: wantPosition("(1/15)")},
		{name: "pageup/does-not-wrap-from-the-first-row", fixture: "long", leaf: "l14", initial: "l0", height: 12, keys: []string{tkPageUp}, check: wantPosition("(1/15)")},
		{name: "pageup/single-node-stays", fixture: "single", leaf: "only", keys: []string{tkPageUp, tkLeft}, check: wantPosition("(1/1)")},
		{name: "pageup/empty-list-stays-empty", fixture: "empty", keys: []string{tkPageUp, tkLeft}, check: wantPosition("(0/0)")},
		// 6. tui.editor.cursorRight / tui.select.pageDown
		{name: "pagedown/right-arrow-pages-by-the-window", fixture: "long", leaf: "l14", initial: "l0", height: 12, keys: []string{tkRight}, check: wantPosition("(7/15)")},
		{name: "pagedown/page-down-key-pages-by-the-window", fixture: "long", leaf: "l14", initial: "l0", height: 12, keys: []string{tkPageDown, tkPageDown}, check: wantPosition("(13/15)")},
		{name: "pagedown/clamps-at-the-last-row", fixture: "long", leaf: "l14", initial: "l0", height: 12, keys: []string{tkPageDown, tkPageDown, tkPageDown, tkPageDown}, check: wantPosition("(15/15)")},
		{name: "pagedown/does-not-wrap-from-the-last-row", fixture: "long", leaf: "l14", height: 12, keys: []string{tkPageDown}, check: wantPosition("(15/15)")},
		{name: "pagedown/single-node-stays", fixture: "single", leaf: "only", keys: []string{tkPageDown, tkRight}, check: wantPosition("(1/1)")},
		{name: "pagedown/empty-list-stays-empty", fixture: "empty", keys: []string{tkPageDown, tkRight}, check: wantPosition("(0/0)")},
		{name: "page/window-is-half-the-terminal-and-at-least-five-rows", fixture: "long", leaf: "l14", initial: "l0", height: 4, keys: []string{tkPageDown}, check: wantPosition("(6/15)")},
		{name: "page/even-height-uses-floor-of-half", fixture: "long", leaf: "l14", initial: "l0", height: 15, keys: []string{tkPageDown}, check: wantPosition("(8/15)")},
		// 7. tui.select.confirm
		{name: "confirm/selects-the-highlighted-entry", fixture: "fork", leaf: "b1u", keys: []string{tkEnter}, check: wantEvents("select:b1u")},
		{name: "confirm/selects-after-a-move", fixture: "fork", leaf: "b1u", keys: []string{tkDown, tkEnter}, check: wantEvents("select:b2")},
		{name: "confirm/empty-list-selects-nothing", fixture: "empty", keys: []string{tkEnter}, check: wantEvents()},
		{name: "confirm/empty-list-after-up-selects-nothing", fixture: "empty", keys: []string{tkUp, tkEnter}, check: wantEvents()},
		{name: "confirm/selection-does-not-close-the-component", fixture: "fork", leaf: "b1u", keys: []string{tkEnter, tkDown, tkEnter}, check: wantEvents("select:b1u", "select:b2")},
		// 8. app.message.copy
		{name: "copy/reports-the-full-text-of-the-highlighted-entry", fixture: "fork", leaf: "b1u", keys: []string{tkCopy}, check: wantEvents("copy:branch one follow")},
		{name: "copy/reports-absence-for-an-entry-without-text", fixture: "rich", leaf: "rm", filter: "all", keys: []string{tkCopy}, check: wantEvents("copy:")},
		{name: "copy/reports-absence-for-an-empty-list", fixture: "empty", keys: []string{tkCopy}, check: wantEvents("copy:")},
		{name: "copy/reports-absence-after-the-cursor-leaves-an-empty-list", fixture: "empty", keys: []string{tkUp, tkCopy}, check: wantEvents("copy:")},
		{name: "copy/compaction-summary", fixture: "rich", leaf: "rc", filter: "all", keys: []string{tkCopy}, check: wantEvents("copy:the summary")},
		{name: "copy/branch-summary", fixture: "rich", leaf: "rb", filter: "all", keys: []string{tkCopy}, check: wantEvents("copy:branch summary text")},
		{name: "copy/assistant-error-message-when-there-is-no-text", fixture: "rich", leaf: "re", filter: "all", keys: []string{tkCopy}, check: wantEvents("copy:boom")},
		{name: "copy/each-press-reports-again", fixture: "fork", leaf: "b1u", keys: []string{tkCopy, tkUp, tkCopy}, check: wantEvents("copy:branch one follow", "copy:branch one answer")},
		// 9. tui.select.cancel
		{name: "cancel/without-a-search-cancels", fixture: "fork", leaf: "b1u", keys: []string{tkEscape}, check: wantEvents("cancel")},
		{name: "cancel/with-a-search-clears-it-first", fixture: "fork", leaf: "b1u", keys: []string{"b", "r", tkEscape}, check: wantAll(wantEvents(), wantSearch("  Type to search:"), wantPosition("(5/8)"))},
		{name: "cancel/second-press-after-clearing-cancels", fixture: "fork", leaf: "b1u", keys: []string{"b", tkEscape, tkEscape}, check: wantEvents("cancel")},
		{name: "cancel/clearing-a-search-keeps-the-highlighted-entry", fixture: "fork", leaf: "b1u", keys: []string{"t", "h", "r", "e", "e", tkEscape}, check: wantPositionAndRow("(8/8)", "branch three")},
		{name: "cancel/clearing-a-search-clears-folds", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{"b", tkCtrlLeft, tkEscape}, check: wantAll(wantTotal(8), wantFrameLacks("⊞"), wantEvents())},
		{name: "cancel/empty-list-cancels", fixture: "empty", keys: []string{tkEscape}, check: wantEvents("cancel")},
		{name: "cancel/a-search-with-no_match-clears-then-cancels", fixture: "fork", leaf: "b1u", keys: []string{"z", "z", "z", tkEscape, tkEscape}, check: wantEvents("cancel")},
		// 10. app.tree.filter.default
		{name: "filter-default/shows-the-default-view", fixture: "mixed", leaf: "lab", filter: "all", keys: []string{tkCtrlD}, check: wantPosition("(5/5)")},
		{name: "filter-default/reselecting-keeps-default", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlD, tkCtrlD}, check: wantPosition("(5/5)")},
		{name: "filter-default/clears-folds", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft, tkCtrlD}, check: wantAll(wantPosition("(1/8)"), wantFrameLacks("⊞"))},
		// 11-14. toggles between a mode and default
		{name: "filter-notools/toggles-on", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlT}, check: wantPosition("(4/4) [no-tools]")},
		{name: "filter-notools/toggles-back-to-default", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlT, tkCtrlT}, check: wantPosition("(5/5)")},
		{name: "filter-notools/clears-folds", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft, tkCtrlT}, check: wantAll(wantPosition("(1/8) [no-tools]"), wantFrameLacks("⊞"))},
		{name: "filter-useronly/toggles-on", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlU}, check: wantPositionAndRow("(2/2) [user]", "second request")},
		{name: "filter-useronly/toggles-back-to-default", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlU, tkCtrlU}, check: wantPosition("(4/5)")},
		{name: "filter-useronly/switching-from-another-mode-sets-it", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlT, tkCtrlU}, check: wantPosition("(2/2) [user]")},
		{name: "filter-labeledonly/toggles-on", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlL}, check: wantPositionAndRow("(1/1) [labeled]", "mark")},
		{name: "filter-labeledonly/toggles-back-to-default", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlL, tkCtrlL}, check: wantPosition("(5/5)")},
		{name: "filter-labeledonly/cursor-walks-to-the-nearest-visible-ancestor", fixture: "mixed", leaf: "lab", initial: "a", keys: []string{tkCtrlL}, check: wantPosition("(1/1) [labeled]")},
		{name: "filter-all/toggles-on", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlA}, check: wantPosition("(6/6) [all]")},
		{name: "filter-all/toggles-back-to-default", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlA, tkCtrlA}, check: wantPosition("(5/5)")},
		{name: "filter-all/keeps-the-highlighted-entry", fixture: "mixed", leaf: "lab", initial: "t", keys: []string{tkCtrlA}, check: wantPositionAndRow("(3/6) [all]", "[bash]")},
		{name: "filter-direct/empty-labeled-view-then-back", fixture: "fork", leaf: "b1u", keys: []string{tkCtrlL, tkDown, tkCtrlD}, check: wantPosition("(5/8)")},
		{name: "filter-direct/empty-view-keeps-the-last-selection", fixture: "fork", leaf: "b1u", keys: []string{tkCtrlL, tkCtrlA}, check: wantPosition("(5/8) [all]")},
		// 15. app.tree.filter.cycleBackward
		{name: "cycle-backward/default-wraps-to-all", fixture: "mixed", leaf: "lab", keys: []string{tkShiftCtrlO}, check: wantPosition("(6/6) [all]")},
		{name: "cycle-backward/all-goes-to-labeled-only", fixture: "mixed", leaf: "lab", filter: "all", keys: []string{tkShiftCtrlO}, check: wantPosition("(1/1) [labeled]")},
		{name: "cycle-backward/labeled-only-goes-to-user-only", fixture: "mixed", leaf: "lab", filter: "labeled-only", keys: []string{tkShiftCtrlO}, check: wantPosition("(2/2) [user]")},
		{name: "cycle-backward/user-only-goes-to-no-tools", fixture: "mixed", leaf: "lab", filter: "user-only", keys: []string{tkShiftCtrlO}, check: wantPosition("(3/4) [no-tools]")},
		{name: "cycle-backward/no-tools-goes-to-default", fixture: "mixed", leaf: "lab", filter: "no-tools", keys: []string{tkShiftCtrlO}, check: wantPosition("(5/5)")},
		{name: "cycle-backward/full-circle-returns", fixture: "mixed", leaf: "lab", keys: []string{tkShiftCtrlO, tkShiftCtrlO, tkShiftCtrlO, tkShiftCtrlO, tkShiftCtrlO}, check: wantPosition("(4/5)")},
		{name: "cycle-backward/clears-folds", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft, tkShiftCtrlO}, check: wantAll(wantPosition("(1/8) [all]"), wantFrameLacks("⊞"))},
		// 16. app.tree.filter.cycleForward
		{name: "cycle-forward/default-goes-to-no-tools", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlO}, check: wantPosition("(4/4) [no-tools]")},
		{name: "cycle-forward/no-tools-goes-to-user-only", fixture: "mixed", leaf: "lab", filter: "no-tools", keys: []string{tkCtrlO}, check: wantPosition("(2/2) [user]")},
		{name: "cycle-forward/user-only-goes-to-labeled-only", fixture: "mixed", leaf: "lab", filter: "user-only", keys: []string{tkCtrlO}, check: wantPosition("(1/1) [labeled]")},
		{name: "cycle-forward/labeled-only-goes-to-all", fixture: "mixed", leaf: "lab", filter: "labeled-only", keys: []string{tkCtrlO}, check: wantPosition("(6/6) [all]")},
		{name: "cycle-forward/all-wraps-to-default", fixture: "mixed", leaf: "lab", filter: "all", keys: []string{tkCtrlO}, check: wantPosition("(5/5)")},
		{name: "cycle-forward/clears-folds", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft, tkCtrlO}, check: wantAll(wantPosition("(1/8) [no-tools]"), wantFrameLacks("⊞"))},
		{name: "cycle/forward-then-backward-is-identity", fixture: "mixed", leaf: "lab", filter: "user-only", keys: []string{tkCtrlO, tkShiftCtrlO}, check: wantPosition("(2/2) [user]")},
		// 17. tui.editor.deleteCharBackward
		{name: "backspace/trims-one-character-and-refilters", fixture: "fork", leaf: "b1u", keys: []string{"b", "r", "a", "n", "c", "h", " ", "t", tkBackspace}, check: wantAll(wantSearch("  Type to search: branch "), wantTotal(6))},
		{name: "backspace/emptying-the-query-restores-every-row", fixture: "fork", leaf: "b1u", keys: []string{"b", tkBackspace}, check: wantAll(wantSearch("  Type to search:"), wantPosition("(5/8)"))},
		{name: "backspace/empty-query-does-nothing", fixture: "fork", leaf: "b1u", keys: []string{tkBackspace, tkBackspace}, check: wantAll(wantSearch("  Type to search:"), wantPosition("(5/8)"))},
		{name: "backspace/empty-query-keeps-folds", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft, tkBackspace}, check: wantAll(wantPosition("(1/1)"), wantFrameContains("⊞"))},
		{name: "backspace/with-a-query-clears-folds", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{"o", tkCtrlLeft, tkBackspace}, check: wantAll(wantFrameLacks("⊞"))},
		{name: "backspace/removes-one-utf16-unit-of-an-astral-character", fixture: "fork", leaf: "b1u", keys: []string{"😀", tkBackspace}, check: wantPosition("(0/0)")},
		{name: "backspace/empty-list-with-a-query", fixture: "fork", leaf: "b1u", keys: []string{"z", "z", tkBackspace, tkBackspace}, check: wantPosition("(5/8)")},
		// 18. app.tree.editLabel
		{name: "label-edit/opens-the-input-for-the-highlighted-entry", fixture: "fork", leaf: "b1u", keys: []string{"L"}, check: wantFrameContains("Label (empty to remove):")},
		{name: "label-edit/prefills-the-current-label", fixture: "mixed", leaf: "lab", keys: []string{"L"}, check: wantFrameContains("Label (empty to remove):", "mark")},
		{name: "label-edit/opens-nothing-on-an-empty-list", fixture: "empty", keys: []string{"L"}, check: wantFrameLacks("Label (empty to remove):")},
		{name: "label-edit/opens-nothing-after-the-cursor-leaves-an-empty-list", fixture: "empty", keys: []string{tkUp, "L"}, check: wantFrameLacks("Label (empty to remove):")},
		{name: "label-edit/hides-the-list", fixture: "fork", leaf: "b1u", keys: []string{"L"}, check: wantFrameLacks("branch one follow", "(5/8)")},
		// 19. app.tree.toggleLabelTimestamp
		{name: "label-time/on-shows-the-status-label", fixture: "mixed", leaf: "lab", keys: []string{"T"}, check: wantPosition("(5/5) [+label time]")},
		{name: "label-time/off-again", fixture: "mixed", leaf: "lab", keys: []string{"T", "T"}, check: wantPosition("(5/5)")},
		{name: "label-time/survives-filter-changes", fixture: "mixed", leaf: "lab", keys: []string{"T", tkCtrlT}, check: wantPosition("(4/4) [no-tools] [+label time]")},
		{name: "label-time/shows-on-labeled-rows-only", fixture: "mixed", leaf: "lab", keys: []string{"T"}, check: wantFrameContains("[mark] ")},
		// 20. type-to-search
		{name: "type/appends-printable-text-and-filters", fixture: "fork", leaf: "b1u", keys: []string{"t", "h", "r"}, check: wantAll(wantSearch("  Type to search: thr"), wantPositionAndRow("(1/1)", "branch three"))},
		{name: "type/tokens-must-all-match", fixture: "fork", leaf: "b1u", keys: []string{"b", "r", " ", "t", "w", "o"}, check: wantAll(wantSearch("  Type to search: br two"), wantTotal(2))},
		{name: "type/match-is-case-insensitive", fixture: "fork", leaf: "b1u", keys: []string{"R", "O", "O"}, check: wantAll(wantSearch("  Type to search: ROO"), wantTotal(2))},
		{name: "type/no-match-shows-the-empty-list", fixture: "fork", leaf: "b1u", keys: []string{"q", "q"}, check: wantAll(wantPosition("(0/0)"), wantFrameContains("No entries found"))},
		{name: "type/clears-folds", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft, "r"}, check: wantFrameLacks("⊞")},
		{name: "type/control-code-31-is-dropped", fixture: "fork", leaf: "b1u", keys: []string{"a", "\x1f", "b"}, check: wantSearch("  Type to search: ab")},
		{name: "type/c1-code-0x80-is-dropped", fixture: "fork", leaf: "b1u", keys: []string{"a", "\u0080", "b"}, check: wantSearch("  Type to search: ab")},
		{name: "type/c1-code-0x9f-is-dropped", fixture: "fork", leaf: "b1u", keys: []string{"a", "\u009f", "b"}, check: wantSearch("  Type to search: ab")},
		{name: "type/no-break-space-is-kept", fixture: "fork", leaf: "b1u", keys: []string{"a", "\u00a0", "b"}, check: wantSearch("  Type to search: a\u00a0b")},
		{name: "type/a-chunk-holding-a-control-code-is-dropped-whole", fixture: "fork", leaf: "b1u", keys: []string{"a", "x\u0085", "b"}, check: wantSearch("  Type to search: ab")},
		{name: "type/pasted-multi-character-chunk-appends-whole", fixture: "fork", leaf: "b1u", keys: []string{"branch"}, check: wantAll(wantSearch("  Type to search: branch"), wantTotal(6))},
		{name: "type/empty-chunk-changes-nothing-and-keeps-folds", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft, ""}, check: wantAll(wantPosition("(1/1)"), wantFrameContains("⊞"))},
		{name: "type/digits-are-searched", fixture: "fork", leaf: "b1u", keys: []string{"1"}, check: wantAll(wantSearch("  Type to search: 1"), wantPosition("(0/0)"))},
		{name: "type/on-an-empty-tree-still-collects-the-query", fixture: "empty", keys: []string{"a", "b"}, check: wantSearch("  Type to search: ab")},
		{name: "type/keeps-the-selection-through-the-nearest-visible-ancestor", fixture: "fork", leaf: "b1u", keys: []string{"o", "n", "e"}, check: wantPosition("(3/3)")},
		// persistence across transitions
		{name: "persist/search-survives-cursor-moves", fixture: "fork", leaf: "b1u", keys: []string{"b", "r", tkDown, tkUp, tkPageDown}, check: wantSearch("  Type to search: br")},
		{name: "persist/filter-mode-survives-search-edits", fixture: "mixed", leaf: "lab", keys: []string{tkCtrlT, "s", tkBackspace}, check: wantPosition("(4/4) [no-tools]")},
		{name: "persist/folds-survive-cursor-moves-and-copy", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft, tkUp, tkDown, tkCopy}, check: wantFrameContains("⊞")},
		{name: "persist/folds-survive-timestamp-toggle", fixture: "fork", leaf: "b1u", initial: "r", keys: []string{tkCtrlLeft, "T"}, check: wantAll(wantFrameContains("⊞"), wantPosition("(1/1) [+label time]"))},
		{name: "persist/two-folds-stay-independent", fixture: "fork", leaf: "b1u", initial: "b1", keys: []string{tkCtrlLeft, tkDown, tkCtrlLeft}, check: wantAll(wantPosition("(4/5)"), wantFrameLacks("branch two answer", "branch one follow"))},
		{name: "persist/unfolding-one-leaves-the-other-folded", fixture: "fork", leaf: "b1u", initial: "b1", keys: []string{tkCtrlLeft, tkDown, tkCtrlLeft, tkUp, tkCtrlRight}, check: wantAll(wantFrameContains("branch one follow"), wantFrameLacks("branch two answer"))},
		// action priority when bindings collide (tree-selector.ts branch order)
		{name: "priority/confirm-wins-over-copy-on-a-shared-key", fixture: "fork", leaf: "b1u", bindings: map[string][]string{"tui.select.confirm": {"ctrl+x"}, "app.message.copy": {"ctrl+x"}}, keys: []string{"\x18"}, check: wantEvents("select:b1u")},
		{name: "priority/cancel-wins-over-filter-default-on-a-shared-key", fixture: "fork", leaf: "b1u", bindings: map[string][]string{"app.tree.filter.default": {"escape"}}, keys: []string{tkEscape}, check: wantEvents("cancel")},
		{name: "priority/fold-wins-over-page-up-on-a-shared-key", fixture: "fork", leaf: "b1u", initial: "r", bindings: map[string][]string{"app.tree.foldOrUp": {"ctrl+x"}, "tui.select.pageUp": {"ctrl+x"}}, keys: []string{"\x18"}, check: wantPosition("(1/1)")},
		{name: "priority/cursor-left-and-page-up-are-one-branch", fixture: "long", leaf: "l14", height: 12, bindings: map[string][]string{"tui.editor.cursorLeft": {"ctrl+g"}}, keys: []string{"\x07"}, check: wantPosition("(9/15)")},
		{name: "priority/up-wins-over-down-on-a-shared-key", fixture: "fork", leaf: "b1u", bindings: map[string][]string{"tui.select.down": {"up"}}, keys: []string{tkUp}, check: wantPosition("(4/8)")},
	}
}

func TestTreeListHandleInputContractsMatchPi(t *testing.T) {
	runTreeContracts(t, treeListInputCases())
}

// treeRemapCases rebinds each of the TreeList's keybinding ids to ctrl+g and checks both sides of the rebinding: the new key runs the action and the
// default key no longer does (a rebound default is typed-search noise, which the list ignores for control codes).
func treeRemapCases() []treeContractCase {
	const ctrlG = "\x07"
	type remap struct {
		id                     string
		fixture, leaf, initial string
		filter                 string
		height                 int
		before                 []string // keys run with the default bindings' meaning before the probe key
		oldKey                 string
		newCheck, oldCheck     func(t *testing.T, tr treeContractTrace)
	}
	remaps := []remap{
		{id: "tui.select.up", fixture: "fork", leaf: "b1u", oldKey: tkUp, newCheck: wantPosition("(4/8)"), oldCheck: wantPosition("(5/8)")},
		{id: "tui.select.down", fixture: "fork", leaf: "b1u", oldKey: tkDown, newCheck: wantPosition("(6/8)"), oldCheck: wantPosition("(5/8)")},
		{id: "app.tree.foldOrUp", fixture: "fork", leaf: "b1u", initial: "r", oldKey: tkCtrlLeft, newCheck: wantAll(wantPosition("(1/1)"), wantFrameContains("⊞")), oldCheck: wantAll(wantPosition("(1/8)"), wantFrameLacks("⊞"))},
		{id: "app.tree.unfoldOrDown", fixture: "fork", leaf: "b1u", initial: "r", before: []string{tkCtrlLeft}, oldKey: tkCtrlRight, newCheck: wantAll(wantPosition("(1/8)"), wantFrameLacks("⊞")), oldCheck: wantAll(wantPosition("(1/1)"), wantFrameContains("⊞"))},
		{id: "tui.editor.cursorLeft", fixture: "long", leaf: "l14", height: 12, oldKey: tkLeft, newCheck: wantPosition("(9/15)"), oldCheck: wantPosition("(15/15)")},
		{id: "tui.select.pageUp", fixture: "long", leaf: "l14", height: 12, oldKey: tkPageUp, newCheck: wantPosition("(9/15)"), oldCheck: wantPosition("(15/15)")},
		{id: "tui.editor.cursorRight", fixture: "long", leaf: "l14", initial: "l0", height: 12, oldKey: tkRight, newCheck: wantPosition("(7/15)"), oldCheck: wantPosition("(1/15)")},
		{id: "tui.select.pageDown", fixture: "long", leaf: "l14", initial: "l0", height: 12, oldKey: tkPageDown, newCheck: wantPosition("(7/15)"), oldCheck: wantPosition("(1/15)")},
		{id: "tui.select.confirm", fixture: "fork", leaf: "b1u", oldKey: tkEnter, newCheck: wantEvents("select:b1u"), oldCheck: wantEvents()},
		{id: "app.message.copy", fixture: "fork", leaf: "b1u", oldKey: tkCopy, newCheck: wantEvents("copy:branch one follow"), oldCheck: wantEvents()},
		{id: "tui.select.cancel", fixture: "fork", leaf: "b1u", oldKey: tkEscape, newCheck: wantEvents("cancel"), oldCheck: wantEvents()},
		{id: "app.tree.filter.default", fixture: "mixed", leaf: "lab", filter: "all", oldKey: tkCtrlD, newCheck: wantPosition("(5/5)"), oldCheck: wantPosition("(6/6) [all]")},
		{id: "app.tree.filter.noTools", fixture: "mixed", leaf: "lab", oldKey: tkCtrlT, newCheck: wantPosition("(4/4) [no-tools]"), oldCheck: wantPosition("(5/5)")},
		{id: "app.tree.filter.userOnly", fixture: "mixed", leaf: "lab", oldKey: tkCtrlU, newCheck: wantPosition("(2/2) [user]"), oldCheck: wantPosition("(5/5)")},
		{id: "app.tree.filter.labeledOnly", fixture: "mixed", leaf: "lab", oldKey: tkCtrlL, newCheck: wantPosition("(1/1) [labeled]"), oldCheck: wantPosition("(5/5)")},
		{id: "app.tree.filter.all", fixture: "mixed", leaf: "lab", oldKey: tkCtrlA, newCheck: wantPosition("(6/6) [all]"), oldCheck: wantPosition("(5/5)")},
		{id: "app.tree.filter.cycleBackward", fixture: "mixed", leaf: "lab", oldKey: tkShiftCtrlO, newCheck: wantPosition("(6/6) [all]"), oldCheck: wantPosition("(5/5)")},
		{id: "app.tree.filter.cycleForward", fixture: "mixed", leaf: "lab", oldKey: tkCtrlO, newCheck: wantPosition("(4/4) [no-tools]"), oldCheck: wantPosition("(5/5)")},
		{id: "tui.editor.deleteCharBackward", fixture: "fork", leaf: "b1u", before: []string{"b", "r"}, oldKey: tkBackspace, newCheck: wantSearch("  Type to search: b"), oldCheck: wantSearch("  Type to search: br")},
		{id: "app.tree.editLabel", fixture: "fork", leaf: "b1u", oldKey: "L", newCheck: wantFrameContains("Label (empty to remove):"), oldCheck: wantSearch("  Type to search: L")},
		{id: "app.tree.toggleLabelTimestamp", fixture: "fork", leaf: "b1u", oldKey: "T", newCheck: wantPosition("(5/8) [+label time]"), oldCheck: wantAll(wantSearch("  Type to search: T"))},
	}
	var cases []treeContractCase
	for _, r := range remaps {
		bindings := map[string][]string{r.id: {"ctrl+g"}}
		cases = append(cases,
			treeContractCase{name: "remap/" + r.id + "/new-key-runs-the-action", fixture: r.fixture, leaf: r.leaf, initial: r.initial, filter: r.filter, height: r.height, bindings: bindings, keys: append(slices.Clone(r.before), ctrlG), check: r.newCheck},
			treeContractCase{name: "remap/" + r.id + "/old-key-is-inert", fixture: r.fixture, leaf: r.leaf, initial: r.initial, filter: r.filter, height: r.height, bindings: bindings, keys: append(slices.Clone(r.before), r.oldKey), check: r.oldCheck},
		)
	}
	return cases
}

func TestTreeListRemappedKeysMatchPi(t *testing.T) {
	restoreTUIKeybindings(t)
	runTreeContracts(t, treeRemapCases())
}
