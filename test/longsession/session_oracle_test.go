// SPDX-License-Identifier: MIT

package longsession

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// sessionView is what a caller of a SessionManager observes after opening one file; the Pi oracle reports the same members.
type sessionView struct {
	Header         string     `json:"header"`
	Entries        []string   `json:"entries"`
	LeafID         *string    `json:"leafId"`
	SessionName    *string    `json:"sessionName"`
	Labels         []*string  `json:"labels"`
	Tree           string     `json:"tree"`
	Branch         []string   `json:"branch"`
	LeafBranches   [][]string `json:"leafBranches"`
	ContextEntries []string   `json:"contextEntries"`
	Projection     string     `json:"projection"`
	Context        string     `json:"context"`
}

// sessionReport is one scenario's observations: the opened view and file, and the files branching, forking and appending write.
type sessionReport struct {
	Open         sessionView `json:"open"`
	OpenedFile   string      `json:"openedFile"`
	BranchedFile *string     `json:"branchedFile"`
	ForkView     sessionView `json:"forkView"`
	ForkFile     string      `json:"forkFile"`
	AppendView   sessionView `json:"appendView"`
	AppendFile   string      `json:"appendFile"`
}

type sessionScenario struct {
	Dir      string `json:"dir"`
	Source   string `json:"source"`
	MiddleID string `json:"middleId"`
	Cwd      string `json:"cwd"`
}

// stringify is JSON.stringify of the value PiG marshals, spelled as PiG writes session lines (jsonstringify.Canonicalize).
func stringify(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := jsonstringify.Canonicalize(data)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}

type treeRow struct {
	ID             string   `json:"id"`
	Label          *string  `json:"label"`
	LabelTimestamp *string  `json:"labelTimestamp"`
	Children       []string `json:"children"`
}

// treeRows lists the tree's nodes in depth-first order, each with its children's ids, as the Pi oracle does.
func treeRows(roots []*codingagent.SessionTreeNode) []treeRow {
	rows := []treeRow{}
	pending := make([]*codingagent.SessionTreeNode, 0, len(roots))
	for _, root := range slices.Backward(roots) {
		pending = append(pending, root)
	}
	for len(pending) > 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		row := treeRow{ID: node.Entry.Base().ID, Children: []string{}}
		if node.Label != "" {
			row.Label = &node.Label
		}
		if node.LabelTimestamp != "" {
			row.LabelTimestamp = &node.LabelTimestamp
		}
		for _, child := range node.Children {
			row.Children = append(row.Children, child.Entry.Base().ID)
		}
		rows = append(rows, row)
		for _, v := range slices.Backward(node.Children) {
			pending = append(pending, v)
		}
	}
	return rows
}

func entryIDs(entries []codingagent.SessionEntry) []string {
	ids := []string{}
	for _, entry := range entries {
		ids = append(ids, entry.Base().ID)
	}
	return ids
}

func pigView(t *testing.T, session *codingagent.Session) sessionView {
	t.Helper()
	entries := session.GetEntries()
	view := sessionView{Header: stringify(t, session.GetHeader()), Entries: []string{}, Labels: []*string{}, LeafBranches: [][]string{}}
	// GetLabel and GetChildren each scan the log (as Pi's getChildren does; Pi's getLabel is a map read), so a per-entry call over a 46,000-entry session does not finish. Above this size the labels and the leaves come from one walk of the tree, which resolves them the same way.
	const perEntryLimit = 5_000
	tree := session.GetTree()
	var labelOf map[string]*string
	var hasChildren map[string]bool
	if len(entries) > perEntryLimit {
		labelOf, hasChildren = map[string]*string{}, map[string]bool{}
		pending := append([]*codingagent.SessionTreeNode{}, tree...)
		for len(pending) > 0 {
			node := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			id := node.Entry.Base().ID
			hasChildren[id] = len(node.Children) > 0
			if node.Label != "" {
				label := node.Label
				labelOf[id] = &label
			}
			pending = append(pending, node.Children...)
		}
	}
	for _, entry := range entries {
		id := entry.Base().ID
		view.Entries = append(view.Entries, stringify(t, entry))
		var leaf bool
		if labelOf != nil {
			view.Labels = append(view.Labels, labelOf[id])
			leaf = !hasChildren[id]
		} else {
			if label, ok := session.GetLabel(id); ok {
				view.Labels = append(view.Labels, &label)
			} else {
				view.Labels = append(view.Labels, nil)
			}
			leaf = len(session.GetChildren(id)) == 0
		}
		if leaf {
			view.LeafBranches = append(view.LeafBranches, entryIDs(session.GetBranch(id)))
		}
	}
	view.LeafID = session.GetLeafID()
	if name := session.GetSessionName(); name != "" {
		view.SessionName = &name
	}
	view.Tree = stringify(t, treeRows(tree))
	view.Branch = entryIDs(session.GetBranch())
	view.ContextEntries = entryIDs(session.BuildContextEntries())
	projection := session.BuildSessionProjection()
	type projected struct {
		SourceID string               `json:"sourceId"`
		Messages []agent.AgentMessage `json:"messages"`
	}
	rows := []projected{}
	for _, entry := range projection.Entries {
		rows = append(rows, projected{SourceID: entry.SourceEntry.Base().ID, Messages: append([]agent.AgentMessage{}, entry.Messages...)})
	}
	view.Projection = stringify(t, struct {
		Entries       []projected                      `json:"entries"`
		Messages      []agent.AgentMessage             `json:"messages"`
		ThinkingLevel string                           `json:"thinkingLevel"`
		Model         *codingagent.SessionContextModel `json:"model"`
	}{rows, append([]agent.AgentMessage{}, projection.Messages...), projection.ThinkingLevel, projection.Model})
	view.Context = stringify(t, session.BuildSessionContext())
	return view
}

func copyFile(t *testing.T, from, to string) string {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return to
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func pigReport(t *testing.T, scenario sessionScenario) sessionReport {
	t.Helper()
	var report sessionReport
	fresh := func(name string) string { return copyFile(t, scenario.Source, filepath.Join(scenario.Dir, name)) }
	open := func(path, dir string) *codingagent.Session {
		session, err := codingagent.NewSessionManagerWithDir(scenario.Cwd, dir).Open(path, scenario.Cwd)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	opened := fresh("open.jsonl")
	report.Open = pigView(t, open(opened, scenario.Dir))
	report.OpenedFile = readFile(t, opened)

	branchedDir := filepath.Join(scenario.Dir, "branched")
	branched, err := open(fresh("branch-source.jsonl"), branchedDir).CreateBranchedSession(scenario.MiddleID)
	if err != nil {
		t.Fatal(err)
	}
	if branched != "" {
		file := readFile(t, branched)
		report.BranchedFile = &file
	}

	forkDir := filepath.Join(scenario.Dir, "fork")
	forked, err := codingagent.NewSessionManagerWithDir(scenario.Cwd, forkDir).ForkFromFile(fresh("fork-source.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	report.ForkView = pigView(t, forked)
	report.ForkFile = readFile(t, *forked.GetSessionFile())

	appended := fresh("append.jsonl")
	session := open(appended, scenario.Dir)
	if entries := session.GetEntries(); len(entries) > 0 {
		label := "first <label> & é"
		if _, err := session.AppendLabelChange(entries[0].Base().ID, &label); err != nil {
			t.Fatal(err)
		}
	}
	if err := session.Branch(scenario.MiddleID); err != nil {
		t.Fatal(err)
	}
	// The message the oracle appends, decoded from the same JSON.
	var message agent.AgentMessage
	if err := json.Unmarshal([]byte(`{"role":"user","content":[{"type":"text","text":"after the branch \u2028 <b>"}],"timestamp":1700000000123}`), &message); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(message); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendSessionInfo(`renamed "session"`); err != nil {
		t.Fatal(err)
	}
	report.AppendView = pigView(t, session)
	report.AppendFile = readFile(t, appended)
	return report
}

// The placeholders accept only the forms Pi creates, so a harness that spells a new id or clock differently still differs: an entry id is eight hex digits (randomUUID().slice(0, 8)), a session id is a UUIDv7 (session-manager.ts uuidv7), and a clock is Date.toISOString (milliseconds, Z).
var (
	idPattern    = regexp.MustCompile(`\b[0-9a-f]{8}(-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})?\b`)
	stampPattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z`)
)

// normalize replaces what each run creates anew, so the two harnesses' reports compare: the scenario directory, ids the source file does not hold (numbered in order of appearance), and ISO timestamps it does not hold (one placeholder: two appends within one millisecond share a clock reading).
func normalize(report string, scenario sessionScenario, source string) string {
	report = strings.ReplaceAll(report, scenario.Dir, "<dir>")
	known := map[string]bool{}
	for _, id := range idPattern.FindAllString(source, -1) {
		known[id] = true
	}
	for _, stamp := range stampPattern.FindAllString(source, -1) {
		known[stamp] = true
	}
	fresh := map[string]string{}
	replace := func(prefix string) func(string) string {
		return func(value string) string {
			if known[value] {
				return value
			}
			if _, ok := fresh[value]; !ok {
				fresh[value] = fmt.Sprintf("<%s%d>", prefix, len(fresh))
			}
			return fresh[value]
		}
	}
	report = idPattern.ReplaceAllStringFunc(report, replace("id"))
	return stampPattern.ReplaceAllStringFunc(report, func(stamp string) string {
		if known[stamp] {
			return stamp
		}
		return "<ts>"
	})
}

// TestSessionManagerMatchesPi opens linear and branching sessions in Pi 1.1.0's SessionManager and in PiG's, and compares every read accessor, the file the open leaves, and the files that createBranchedSession, forkFrom and appending a label, a branch, a message and a name write.
func TestSessionManagerMatchesPi(t *testing.T) {
	root := t.TempDir()
	var scenarios []sessionScenario
	sources := map[string]string{}
	add := func(name string, write func(*os.File) error) {
		cwd := filepath.Join(root, name, "work")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(root, name, "source.jsonl")
		file, err := os.Create(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := write(file); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		data := readFile(t, source)
		lines := strings.Split(strings.TrimSpace(data), "\n")
		var middle struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(lines[len(lines)/2]), &middle); err != nil {
			t.Fatal(err)
		}
		sources[name] = data
		for _, side := range []string{"pi", "pig"} {
			if err := os.MkdirAll(filepath.Join(root, name, side), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		scenarios = append(scenarios, sessionScenario{Dir: filepath.Join(root, name, "pi"), Source: source, MiddleID: middle.ID, Cwd: cwd})
	}
	// Lines Pi's loader reads without failing (session-manager.ts parseSessionEntryLine, loadEntriesFromFile): blank lines and syntax errors are skipped, null is skipped as falsy. The scenario lists what each kind of line leaves in a session that Pi and PiG both open.
	add("tree-garbage", func(file *os.File) error {
		var tree bytes.Buffer
		if err := GenerateTree(&tree, TreeOptions{Entries: 120, Seed: 7, Cwd: filepath.Join(root, "tree-garbage", "work")}); err != nil {
			return err
		}
		lines := strings.Split(strings.TrimSuffix(tree.String(), "\n"), "\n")
		garbage := map[int][]string{
			3:  {"", "   ", "\t"},
			10: {"not json", "{broken", `{"type":"message","id":`},
			20: {"null"},
			40: {lines[30]},
		}
		var out []string
		for i, line := range lines {
			out = append(out, line)
			out = append(out, garbage[i]...)
		}
		_, err := file.WriteString(strings.Join(out, "\n") + "\n")
		return err
	})

	// A 46,000-entry session with a few branches: the size of the owner's file, with the leaves of a long conversation so the comparison is not dominated by one branch read per leaf. Pi's own getChildren scans the log per call, so this scenario takes about six minutes and runs only when PIG_LONGSESSION_46K is set.
	if os.Getenv("PIG_LONGSESSION_46K") != "" {
		add("tree-46k", func(file *os.File) error {
			return GenerateTree(file, TreeOptions{Entries: 46_000, Seed: 46, ForkOneIn: 3_000, Cwd: filepath.Join(root, "tree-46k", "work")})
		})
	}

	// Messages with a member of another type than the one Pi writes, at the top level and in nested blocks and records, each on the path to the leaf: Pi's loader keeps any JSON object (session-manager.ts parseSessionEntryLine), so every message stays in the entries, the branch and the context.
	add("poisoned-messages", func(file *os.File) error {
		messages := strings.Split(strings.TrimSpace(readFile(t, "../../agent/testdata/poisoned-messages.jsonl")), "\n")
		cwd, err := json.Marshal(filepath.Join(root, "poisoned-messages", "work"))
		if err != nil {
			return err
		}
		lines := []string{`{"type":"session","version":3,"id":"00000000-0000-4000-8000-00000000000a","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + string(cwd) + `}`}
		parent := "null"
		for i, message := range messages {
			id := fmt.Sprintf("%08x", i+1)
			lines = append(lines, fmt.Sprintf(`{"type":"message","id":"%s","parentId":%s,"timestamp":"2026-01-01T00:00:%02d.000Z","message":%s}`, id, parent, i%60, message))
			parent = `"` + id + `"`
		}
		_, err = file.WriteString(strings.Join(lines, "\n") + "\n")
		return err
	})
	add("linear", func(file *os.File) error {
		_, err := Generate(file, Options{Tokens: 60_000, Window: 200_000, ResultBytes: 2048, Seed: 7, Cwd: filepath.Join(root, "linear", "work")})
		return err
	})
	// One path of 800-1,500 entries (Generate writes no compaction). Both sides walk the tree iteratively, so the path length is bounded by run time only; tree-46k holds a far longer path.
	add("linear-deep", func(file *os.File) error {
		_, err := Generate(file, Options{Tokens: 250_000, Window: 200_000, ResultBytes: 2048, Seed: 11, Cwd: filepath.Join(root, "linear-deep", "work")})
		return err
	})
	for seed := range uint64(6) {
		name := fmt.Sprintf("tree-%d", seed)
		add(name, func(file *os.File) error {
			return GenerateTree(file, TreeOptions{Entries: 60 + int(seed)*40, Seed: seed, Cwd: filepath.Join(root, name, "work")})
		})
	}
	// Larger branchy sessions of every entry type, with the compactions and hand-ordered members GenerateTree writes. GenerateTree branches
	// often, so even tree-4000's longest root-to-leaf path is 120-200 entries; linear-deep holds the long path.
	for _, entries := range []int{700, 1_500, 4_000} {
		name := fmt.Sprintf("tree-%d", entries)
		add(name, func(file *os.File) error {
			return GenerateTree(file, TreeOptions{Entries: entries, Seed: 100 + uint64(entries), Cwd: filepath.Join(root, name, "work")})
		})
	}

	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/pi-session-oracle.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []sessionReport
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(scenarios) {
		t.Fatalf("Pi reported %d scenarios, want %d", len(want), len(scenarios))
	}
	for i, scenario := range scenarios {
		name := filepath.Base(filepath.Dir(scenario.Dir))
		t.Run(name, func(t *testing.T) {
			piScenario := scenario
			pigScenario := scenario
			pigScenario.Dir = filepath.Join(filepath.Dir(scenario.Dir), "pig")
			got := normalize(stringify(t, pigReport(t, pigScenario)), pigScenario, sources[name])
			expected := normalize(stringify(t, want[i]), piScenario, sources[name])
			if got == expected {
				return
			}
			var gotReport, wantReport map[string]json.RawMessage
			_ = json.Unmarshal([]byte(got), &gotReport)
			_ = json.Unmarshal([]byte(expected), &wantReport)
			for _, key := range slices.Sorted(maps.Keys(wantReport)) {
				if bytes.Equal(gotReport[key], wantReport[key]) {
					continue
				}
				var gotView, wantView map[string]json.RawMessage
				if json.Unmarshal(wantReport[key], &wantView) != nil || json.Unmarshal(gotReport[key], &gotView) != nil {
					t.Errorf("%s differs from Pi:\n%s", key, firstDifference(string(gotReport[key]), string(wantReport[key])))
					continue
				}
				for _, member := range slices.Sorted(maps.Keys(wantView)) {
					if !bytes.Equal(gotView[member], wantView[member]) {
						t.Errorf("%s.%s differs from Pi:\n%s", key, member, firstDifference(string(gotView[member]), string(wantView[member])))
					}
				}
			}
		})
	}
}

// firstDifference shows both values around the first byte where they differ.
func firstDifference(got, want string) string {
	at := 0
	for at < len(got) && at < len(want) && got[at] == want[at] {
		at++
	}
	window := func(s string) string {
		from, to := max(0, at-160), min(len(s), at+160)
		return s[from:to]
	}
	return fmt.Sprintf("  at byte %d\n  pig: %s\n  pi:  %s", at, window(got), window(want))
}
