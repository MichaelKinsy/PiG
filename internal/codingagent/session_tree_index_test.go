package codingagent

import (
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/tui"
)

type treeRowView struct {
	id, label, branchLabel, labelTime string
	tags                              []string
	search                            string
	copyText                          *string
	children                          []treeRowView
}

func viewTreeRows(nodes []tui.TreeNode) []treeRowView {
	var out []treeRowView
	for _, n := range nodes {
		a := n.(*treeNodeAdapter)
		out = append(out, treeRowView{
			id: a.NodeID(), label: a.NodeLabel(), branchLabel: a.NodeBranchLabel(), labelTime: a.NodeLabelTimestamp(),
			tags: a.NodeFilterTags(), search: jsstring.ToLower(a.NodeSearchableText()), copyText: a.NodeCopyText(), children: viewTreeRows(a.NodeChildren()),
		})
	}
	return out
}

// The /tree selector over a resumed session that keeps only its active context resident shows the rows the same file loaded wholly resident
// shows (Pi's getTree over its in-memory entries, tree-selector.ts flattenTree): ids, child order, labels, filter tags, row text, search text
// and copy text, with tool results named by their calls. Opening it and classifying every row reads no entry that is not resident.
func TestLazyTreeRowsMatchTheResidentTree(t *testing.T) {
	for seed := range uint64(10) {
		live, _ := randomStoreSession(t, seed, 260)
		path := live.Path()
		resident := residentCopy(t, path)
		indexed, _, err := loadPersistedSession(path)
		if err != nil {
			t.Fatal(err)
		}
		leaf := indexed.GetLeafID()
		roots, lt := indexed.lazyTree()
		lazyRoot := &treeNodeAdapter{n: &SessionTreeNode{Children: roots}, f: newLazyTreeFormatter(lt, leaf)}
		evicted := 0
		var classify func([]tui.TreeNode)
		classify = func(nodes []tui.TreeNode) {
			for _, n := range nodes {
				n.(*treeNodeAdapter).NodeFilterTags()
				classify(n.NodeChildren())
			}
		}
		classify(lazyRoot.NodeChildren())
		for _, ln := range lt.nodes {
			if !ln.loaded {
				evicted++
			}
		}
		if evicted == 0 {
			t.Fatalf("seed %d: no entry is evicted; the comparison would not exercise the lazy rows", seed)
		}
		if loaded := len(lt.nodes) - evicted; loaded > residentSlots(indexed) {
			t.Fatalf("seed %d: classifying the rows read %d entries that are not resident", seed, loaded-residentSlots(indexed))
		}
		if got, want := userMessageSelectorItems(indexed), userMessageSelectorItems(resident); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: /fork items %+v, want %+v", seed, got, want)
		}
		tree := resident.GetTree()
		eagerRoot := &treeNodeAdapter{n: &SessionTreeNode{Children: tree}, f: newTreeRowFormatterFromTree(tree, leaf)}
		got, want := viewTreeRows(lazyRoot.NodeChildren()), viewTreeRows(eagerRoot.NodeChildren())
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: lazy tree rows differ:\n%s", seed, firstRowDiff(got, want, ""))
		}
	}
}

func residentSlots(s *Session) int {
	n := 0
	for _, slot := range s.slots {
		if slot.entry != nil {
			n++
		}
	}
	return n
}

func firstRowDiff(got, want []treeRowView, at string) string {
	if len(got) != len(want) {
		return fmt.Sprintf("%s: %d rows, want %d", at, len(got), len(want))
	}
	for i := range got {
		g, w := got[i], want[i]
		if !reflect.DeepEqual(g.children, w.children) && reflect.DeepEqual(treeRowView{id: g.id, label: g.label, branchLabel: g.branchLabel, labelTime: g.labelTime, tags: g.tags, search: g.search, copyText: g.copyText}, treeRowView{id: w.id, label: w.label, branchLabel: w.branchLabel, labelTime: w.labelTime, tags: w.tags, search: w.search, copyText: w.copyText}) {
			return firstRowDiff(g.children, w.children, at+"/"+g.id)
		}
		if !reflect.DeepEqual(g, w) {
			g.children, w.children = nil, nil
			return fmt.Sprintf("%s/%d: got %+v\nwant %+v", at, i, g, w)
		}
	}
	return at + ": equal"
}

func checkTreeFactsScan(t *testing.T, line []byte) bool {
	t.Helper()
	if !json.Valid(line) {
		return false
	}
	scanned, ok := scanTreeFacts(line)
	if !ok {
		return false
	}
	if want := decodeTreeFacts(line); !reflect.DeepEqual(scanned, want) {
		t.Fatalf("scan of %s = %+v, decode = %+v", line, *scanned, *want)
	}
	return true
}

// The tree facts a scan of a message line decides equal the ones the line's decode gives: blank and visible text (escapes, tool_result
// wrappers), stop reasons, call ids of toolCall and tool_use blocks, duplicate members; shapes it cannot decide go to the decode.
func TestScanTreeFactsMatchesTheDecode(t *testing.T) {
	blocks := []string{`{"type":"text","text":"hi"}`, `{"type":"text","text":" \n\t"}`, `{"type":"text","text":"\u00a0"}`, `{"type":"text","text":"\\"}`,
		`{"type":"text","text":"\b"}`, `{"type":"text"}`, `{"type":"text","text":null}`, `{"type":"toolCall","id":"c1","name":"read","arguments":{"text":"x"}}`,
		`{"type":"tool_use","id":"c2","input":{}}`, `{"type":"toolCall","id":""}`, `{"type":"tool_result","content":"  "}`, `{"type":"tool_result","content":"r"}`,
		`{"type":"tool_result","content":[1]}`, `{"type":"thinking","thinking":"deep"}`, `{"type":"image","data":"AA"}`, `null`, `{"type":"text","text":"a","text":" "}`,
		`{"Type":"text","text":"a"}`, `{"type":"text","text":5}`, `{"type":"toolCall","id":7}`, `{"type":"text","text":"é"}`, `{}`}
	stops := []string{``, `,"stopReason":"stop"`, `,"stopReason":"toolUse"`, `,"stopReason":"aborted"`, `,"stopReason":null`, `,"stopReason":1`}
	roles := []string{`"assistant"`, `"user"`, `null`, `5`}
	rng := rand.New(rand.NewPCG(3, 4))
	decided := 0
	for range 20000 {
		var content []string
		for range rng.IntN(4) {
			content = append(content, blocks[rng.IntN(len(blocks))])
		}
		line := fmt.Sprintf(`{"type":"message","id":"a","message":{"role":%s,"content":[%s]%s}}`, roles[rng.IntN(len(roles))], strings.Join(content, ","), stops[rng.IntN(len(stops))])
		if checkTreeFactsScan(t, []byte(line)) {
			decided++
		}
	}
	if decided < 2500 {
		t.Fatalf("scan decided only %d of 20000 lines", decided)
	}
}

func FuzzScanTreeFacts(f *testing.F) {
	for _, seed := range []string{`{"message":{"role":"assistant","content":[{"type":"toolCall","id":"x"}],"stopReason":"toolUse"}}`, `{"message":{"role":"assistant","content":[{"type":"text","text":" \n"}]}}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, line []byte) { checkTreeFactsScan(t, line) })
}

func checkSearchBodyScan(t *testing.T, line []byte) bool {
	t.Helper()
	if !json.Valid(line) {
		return false
	}
	scanned, ok := scanSearchBody(line)
	if !ok {
		return false
	}
	entry := RawEntry{SessionEntryBase: SessionEntryBase{Type: "message"}}
	if want := treeSearchBody(entry, line); scanned != want {
		t.Fatalf("scan of %s = %+v, decode = %+v", line, scanned, want)
	}
	return true
}

// The search text a scan of a message line gives equals the decode's: role, the first 200 UTF-16 units of string or text-block content
// (escapes, surrogate pairs at the cut, several blocks), a bash command; shapes it cannot decide go to the decode.
func TestScanSearchBodyMatchesTheDecode(t *testing.T) {
	long := strings.Repeat("ab\\n\\u00e9😀", 60)
	contents := []string{`"hi"`, `""`, `null`, `5`, `{"a":1}`, `"` + long + `"`, `[]`, `[{"type":"text","text":"one "},{"type":"image","data":"x"},{"type":"text","text":"` + long + `"}]`,
		`[{"type":"toolCall","id":"c","arguments":{"text":"x"}}]`, `[{"type":"thinking","text":"hidden"},{"type":"text","text":"shown"}]`, `[null,{"type":"text"}]`, `[{"type":"text","text":5}]`, `[{"Type":"text","text":"x"}]`, `[1]`,
		`[{"type":"text","text":"\ud83d\ude00` + long + `"}]`, `"x\ud800y"`, `[{"type":"text"},{"type":"\u0074ext","text":"t"}]`, `[1,"s",[],null,{"type":"text","text":"a"}]`,
		`[{"type":5,"text":"x"},{"type":"text","text":null},{"type":"text","text":{"a":1}},{"type":"text","text":[1,"b"]}]`, `0`, `0.0`, `-0`, `true`, `false`, `{}`}
	roles := []string{`"user"`, `"assistant"`, `"bashExecution"`, `"bash\u0045xecution"`, `null`, `5`, `{"a":1}`, `[1,2]`, `true`}
	commands := []string{``, `,"command":"ls -la \"x\""`, `,"command":null`, `,"command":7`, `,"command":0`, `,"command":""`, `,"command":{}`}
	rng := rand.New(rand.NewPCG(5, 6))
	decided := 0
	for range 20000 {
		line := fmt.Sprintf(`{"type":"message","id":"a","message":{"role":%s,"content":%s%s}}`, roles[rng.IntN(len(roles))], contents[rng.IntN(len(contents))], commands[rng.IntN(len(commands))])
		if checkSearchBodyScan(t, []byte(line)) {
			decided++
		}
	}
	if decided < 2500 {
		t.Fatalf("scan decided only %d of 20000 lines", decided)
	}
}

func FuzzScanSearchBody(f *testing.F) {
	for _, seed := range []string{`{"message":{"role":"user","content":"hi"}}`, `{"message":{"role":"bashExecution","command":"ls","content":[{"type":"text","text":"a\u00e9"}]}}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, line []byte) { checkSearchBodyScan(t, line) })
}

// treeSearchNodeScript is tree-selector.ts getSearchableText's message case and extractContent/extractFullContent (562-581, 887-902)
// verbatim, without the label, over each JSON line on stdin.
const treeSearchNodeScript = `
function extractFullContent(content) {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return "";
	let result = "";
	for (const block of content) {
		if (typeof block === "object" && block !== null && "type" in block && block.type === "text") {
			result += block.text;
		}
	}
	return result;
}
function extractContent(content) { return extractFullContent(content).slice(0, 200); }
const out = [];
for (const line of require("fs").readFileSync(0, "utf8").split("\n")) {
	if (!line) continue;
	const msg = JSON.parse(line).message;
	const parts = [];
	parts.push(msg.role);
	if ("content" in msg && msg.content) parts.push(extractContent(msg.content));
	if (msg.role === "bashExecution") { if (msg.command) parts.push(msg.command); }
	out.push(parts.join(" "));
}
process.stdout.write(JSON.stringify(out));
`

// The message search text equals Pi's getSearchableText run in Node on the same lines, JavaScript coercions included.
func TestMessageSearchTextMatchesPiInNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	long := strings.Repeat("ab\\n\\u00e9😀", 60)
	contents := []string{`"hi"`, `""`, `null`, `5`, `0`, `0.0`, `true`, `false`, `{}`, `[]`, `"` + long + `"`,
		`[{"type":"text","text":"one "},{"type":"image","data":"x"},{"type":"text","text":"` + long + `"}]`,
		`[{"type":"text"},{"type":"\u0074ext","text":"t"}]`, `[1,"s",[],null,{"type":"text","text":"a"}]`,
		`[{"type":5,"text":"x"},{"type":"text","text":null},{"type":"text","text":{"a":1}},{"type":"text","text":[1,"b",null,[2]]},{"type":"text","text":1e21},{"type":"text","text":-0}]`,
		`[{"type":"text","text":"a","type":"image"},{"type":"image","type":"text","text":"b","text":"c"}]`}
	roles := []string{`"user"`, `"bashExecution"`, `"bash\u0045xecution"`, `null`, `5`, `{"a":1}`, `[1,2]`, `true`}
	commands := []string{``, `,"command":"ls -la \"x\""`, `,"command":null`, `,"command":7`, `,"command":0`, `,"command":""`, `,"command":{}`, `,"command":[1]`}
	var lines []string
	for _, role := range roles {
		for _, content := range contents {
			for _, command := range commands {
				lines = append(lines, fmt.Sprintf(`{"type":"message","id":"a","message":{"role":%s,"content":%s%s}}`, role, content, command))
			}
		}
		lines = append(lines, fmt.Sprintf(`{"type":"message","id":"a","message":{"role":%s}}`, role))
	}
	cmd := exec.Command(node, "-e", treeSearchNodeScript)
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var want []string
	if err := json.Unmarshal(stdout, &want); err != nil || len(want) != len(lines) {
		t.Fatalf("node output: %v (%d of %d)", err, len(want), len(lines))
	}
	entry := RawEntry{SessionEntryBase: SessionEntryBase{Type: "message"}}
	for i, line := range lines {
		if got := treeSearchBody(entry, []byte(line)); got.text != want[i] {
			t.Fatalf("line %s: PiG %q, Pi %q", line, got.text, want[i])
		}
		if scanned, ok := scanSearchBody([]byte(line)); ok && scanned.text != want[i] {
			t.Fatalf("line %s: scan %q, Pi %q", line, scanned.text, want[i])
		}
	}
}

// jsTruthy is JavaScript truthiness of the value JSON.parse gives: every spelling of zero is falsy.
func TestJSTruthyOfEveryZeroSpelling(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "null": false, "false": false, `""`: false, "0": false, "-0": false, "0.0": false, "0e5": false, "-0.0E-3": false,
		"true": true, `" "`: true, "1e-400": false, "0.1": true, "{}": true, "[]": true, `"0"`: true} {
		if got := jsTruthy(json.RawMessage(raw)); got != want {
			t.Errorf("jsTruthy(%s) = %v, want %v", raw, got, want)
		}
	}
}

func checkIndexProbeWalk(t *testing.T, line []byte) bool {
	t.Helper()
	if !json.Valid(line) {
		return false
	}
	walked, ok := walkIndexProbe(line)
	if !ok {
		return false
	}
	var want indexProbe
	if err := json.Unmarshal(line, &want); err != nil {
		t.Fatalf("walk read %s, which the decode rejects: %v", line, err)
	}
	if !reflect.DeepEqual(walked, want) {
		t.Fatalf("walk of %s = %+v, decode = %+v", line, walked, want)
	}
	return true
}

// The one-walk index probe of a line equals json.Unmarshal into indexProbe whenever it answers, and it answers for Pi's own lines.
func TestIndexProbeWalkMatchesTheDecode(t *testing.T) {
	members := []string{`"type":"message"`, `"type":"custom_message"`, `"id":"a1"`, `"id":"\u00e9"`, `"parentId":null`, `"parentId":"p"`, `"timestamp":"2026-01-01T00:00:00Z"`,
		`"timestamp":5`, `"provider":"x"`, `"model":"m"`, `"usage":{"input":1,"output":2,"cost":{"total":0.5}}`, `"usage":null`, `"kind":"k"`, `"Type":"x"`, `"type":5`,
		`"message":null`, `"message":5`, `"extra":[1,{"a":"b"}]`,
		`"message":{"role":"assistant","content":[{"type":"toolCall","id":"c"},{"type":"text","text":"x"}],"usage":{"input":3},"provider":"p","model":"m","api":"openai-completions","responseModel":"r","timestamp":17}`,
		`"message":{"role":"user","content":"hi","timestamp":1.5}`, `"message":{"role":"toolResult","content":null,"api":{"a":1},"responseModel":null}`,
		`"message":{"role":5}`, `"message":{"Role":"user"}`, `"message":{"role":"a","role":"b"}`}
	rng := rand.New(rand.NewPCG(7, 8))
	decided := 0
	for range 20000 {
		n := 1 + rng.IntN(6)
		picked := make([]string, n)
		for i := range picked {
			picked[i] = members[rng.IntN(len(members))]
		}
		if checkIndexProbeWalk(t, []byte("{"+strings.Join(picked, ",")+"}")) {
			decided++
		}
	}
	if decided < 2000 {
		t.Fatalf("walk decided only %d of 20000 lines", decided)
	}
}

func FuzzIndexProbeWalk(f *testing.F) {
	f.Add([]byte(`{"type":"message","id":"a","parentId":null,"timestamp":"t","message":{"role":"user","content":"hi","usage":{"input":1}}}`))
	f.Fuzz(func(t *testing.T, line []byte) { checkIndexProbeWalk(t, line) })
}

func checkJSPrefix(t *testing.T, text string, units int) {
	t.Helper()
	if got, want := jsPrefix(text, units), jsstring.Slice(text, 0, units); got != want {
		t.Fatalf("jsPrefix(%q, %d) = %q, want %q", text, units, got, want)
	}
}

// jsPrefix is String.prototype.slice(0, n) as jsstring.Slice computes it: a cut through a surrogate pair, a lone surrogate and invalid UTF-8
// take the conversion.
func TestJSPrefixIsTheSlice(t *testing.T) {
	for _, text := range []string{"", "abc", "é漢字", "a😀b", "😀😀", "\xff\xfeab", "a\xed\xa0\x80b", "\uFFFDx", strings.Repeat("ab😀", 90)} {
		for units := range 8 {
			checkJSPrefix(t, text, units)
		}
		checkJSPrefix(t, text, 200)
	}
}

func FuzzJSPrefix(f *testing.F) {
	f.Add("a😀b\xff", 3)
	f.Fuzz(func(t *testing.T, text string, units int) {
		if units < 0 || units > 1<<12 {
			return
		}
		checkJSPrefix(t, text, units)
	})
}
