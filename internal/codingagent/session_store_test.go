package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// randomStoreSession builds a persisted session with compactions (kept entry on the branch, missing, and the compaction itself), tree
// navigation with and without summaries, model and thinking changes, custom entries, labels, context edits, custom messages and assistant
// messages of several APIs, and returns it with the ids of every entry.
func randomStoreSession(t *testing.T, seed uint64, steps int) (*Session, []string) {
	t.Helper()
	return randomStoreSessionWith(t, seed, steps, nil)
}

// randomStoreSessionWith is randomStoreSession that calls after, when it is not nil, with the session and the step's random source after
// every step.
func randomStoreSessionWith(t *testing.T, seed uint64, steps int, after func(*Session, *rand.Rand)) (*Session, []string) {
	t.Helper()
	dir := t.TempDir()
	session, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(seed, 7))
	var ids []string
	must := func(id string, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	apis := []ai.API{"openai-completions", "anthropic-messages", "virtual-api"}
	calls := 0
	text := func(n int) string { return strings.Repeat(fmt.Sprintf("w%d ", rng.IntN(1000)), n) }
	assistant := func() agent.AgentMessage {
		api := apis[rng.IntN(len(apis))]
		message := mkAssistantMsg(text(20))
		message.Assistant.API = api
		message.Assistant.Provider = "p-" + string(api)
		message.Assistant.ModelID = fmt.Sprintf("m%d", rng.IntN(3))
		message.Assistant.Usage = &ai.Usage{Input: rng.IntN(5000), Output: 40, CacheRead: rng.IntN(100), TotalTokens: 5000}
		message.Assistant.StopReason = ai.StopReasonStop
		switch k := rng.IntN(10); {
		case k < 4:
			// A turn of tool calls alone: the /tree selector hides it unless it is the leaf.
			calls++
			message.Assistant.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: fmt.Sprintf("call%d", calls), Name: "read", Arguments: ai.JsonObject{"path": fmt.Sprintf("f%d.go", calls)}}}
			message.Assistant.StopReason = ai.StopReasonToolUse
		case k < 5:
			message.Assistant.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "  \n"}}
			message.Assistant.StopReason = ai.StopReasonAborted
		}
		return message
	}
	must(session.AppendMessage(mkUserMsg("first " + text(5))))
	for range steps {
		var branch []SessionEntry
		switch r := rng.IntN(100); {
		case r < 30:
			must(session.AppendMessage(mkUserMsg(text(30))))
		case r < 60:
			must(session.AppendMessage(assistant()))
		case r < 66:
			must(session.AppendModelChange("prov", fmt.Sprintf("model-%d", rng.IntN(4))))
		case r < 70:
			must(session.AppendThinkingLevelChange([]string{"low", "medium", "high"}[rng.IntN(3)]))
		case r < 74:
			must(session.AppendCustomEntry("ext-state", map[string]any{"n": rng.IntN(10)}))
		case r < 77:
			must(session.AppendCustomMessageEntry("note", text(8), rng.IntN(2) == 0, nil))
		case r < 80:
			if len(ids) > 0 {
				label := fmt.Sprintf("l%d", rng.IntN(5))
				must(session.AppendLabelChange(ids[rng.IntN(len(ids))], &label))
			}
		case r < 84:
			branch = session.GetBranch()
			for _, entry := range branch[max(0, len(branch)-8):] {
				if entry.Base().Type == "message" {
					if role := contextEditTargetRole(entry); role == agent.RoleUser {
						replacement := &ContextEditReplacement{Content: []byte(`"edited"`)}
						must(session.AppendContextEdit(entry.Base().ID, replacement))
						break
					}
				}
			}
		case r < 92:
			branch = session.GetBranch()
			firstKept := ""
			switch k := rng.IntN(10); {
			case k < 7 && len(branch) > 4:
				firstKept = branch[len(branch)-1-rng.IntN(min(len(branch)-1, 12))].Base().ID
			case k < 8:
				firstKept = "missing-entry"
			}
			must(session.AppendCompaction("summary "+text(10), firstKept, 1000, nil, false, &ai.Usage{Input: 10, Output: 5}))
		case r < 96:
			branch = session.GetBranch()
			if len(branch) > 3 {
				target := branch[rng.IntN(len(branch)-1)].Base().ID
				if rng.IntN(2) == 0 {
					if err := session.Branch(target); err != nil {
						t.Fatal(err)
					}
				} else {
					must(session.BranchWithSummary(&target, "branch summary "+text(6), nil, false, nil))
				}
			}
		default:
			must(session.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
				Role: "toolResult", ToolCallID: fmt.Sprintf("call%d", rng.IntN(calls+2)), ToolName: "read",
				Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text(40)}}, Timestamp: 1,
			}}))
		}
		if after != nil {
			after(session, rng)
		}
	}
	return session, ids
}

// residentCopy loads path with every entry decoded and resident, as Pi's SessionManager holds its file.
func residentCopy(t *testing.T, path string) *Session {
	t.Helper()
	records, err := LoadEntriesFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	session, err := restoreSessionEntries(path, records, false)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func sameEntries(t *testing.T, what string, got, want []SessionEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d entries, want %d", what, len(got), len(want))
	}
	for i := range got {
		if reflect.TypeOf(got[i]) != reflect.TypeOf(want[i]) || string(got[i].Raw()) != string(want[i].Raw()) || !reflect.DeepEqual(got[i].Base(), want[i].Base()) {
			t.Fatalf("%s: entry %d: got %T %s, want %T %s", what, i, got[i], got[i].Raw(), want[i], want[i].Raw())
		}
	}
}

// Every read of a Session that keeps only its active context resident returns what the same file loaded wholly resident returns: the
// entries, every branch, the tree, children and labels, and for every leaf the projection, context entries and settings.
func TestSessionIndexReadsMatchResidentEntries(t *testing.T) {
	for seed := range uint64(12) {
		live, ids := randomStoreSession(t, seed, 400)
		reopened, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
		if err != nil {
			t.Fatal(err)
		}
		resident := residentCopy(t, live.Path())
		for _, s := range []*Session{live, reopened} {
			if s.backing == "" {
				t.Fatalf("seed %d: session has no backing file", seed)
			}
			sameEntries(t, "GetEntries", s.GetEntries(), resident.GetEntries())
			if !reflect.DeepEqual(treeShape(s.GetTree()), treeShape(resident.GetTree())) {
				t.Fatalf("seed %d: trees differ", seed)
			}
			for _, id := range append(ids, "missing") {
				sameEntries(t, "GetBranch "+id, s.GetBranch(id), resident.GetBranch(id))
				sameEntries(t, "GetChildren "+id, s.GetChildren(id), resident.GetChildren(id))
				gotLabel, gotOK := s.GetLabel(id)
				wantLabel, wantOK := resident.GetLabel(id)
				if gotLabel != wantLabel || gotOK != wantOK {
					t.Fatalf("seed %d: GetLabel(%s) = %q %v, want %q %v", seed, id, gotLabel, gotOK, wantLabel, wantOK)
				}
				if got, ok := s.GetEntry(id); ok {
					want, _ := resident.GetEntry(id)
					sameEntries(t, "GetEntry", []SessionEntry{got}, []SessionEntry{want})
				}
			}
		}
		// Every leaf: the projection of the context branch equals the projection of the whole branch.
		for _, id := range ids {
			for _, s := range []*Session{live, reopened} {
				if err := s.SetLeafID(&id); err != nil {
					t.Fatal(err)
				}
			}
			if err := resident.SetLeafID(&id); err != nil {
				t.Fatal(err)
			}
			full := resident.GetBranch()
			wantProjection := buildSessionProjection(full, asMessage, true)
			wantThinking, wantModel := GetSessionContextSettings(full)
			for _, s := range []*Session{live, reopened} {
				context := s.GetContextBranch()
				if got := s.BuildSessionProjection(); !reflect.DeepEqual(got, wantProjection) {
					t.Fatalf("seed %d leaf %s: projection differs", seed, id)
				}
				sameEntries(t, "BuildContextEntries", s.BuildContextEntries(), buildContextEntries(full, asMessage))
				if thinking, model := GetSessionContextSettings(context); thinking != wantThinking || !reflect.DeepEqual(model, wantModel) {
					t.Fatalf("seed %d leaf %s: settings %s %+v, want %s %+v", seed, id, thinking, model, wantThinking, wantModel)
				}
				if got, want := lastPromptTokens(context), lastPromptTokens(full); got != want {
					t.Fatalf("seed %d leaf %s: lastPromptTokens %d, want %d", seed, id, got, want)
				}
				if got, want := GetLatestCompactionEntry(context), GetLatestCompactionEntry(full); !reflect.DeepEqual(got, want) {
					t.Fatalf("seed %d leaf %s: latest compaction differs", seed, id)
				}
				for _, api := range []string{"openai-completions", "anthropic-messages", "virtual-api"} {
					if got, want := newestAssistantWithoutAPI(context, api), newestAssistantWithoutAPI(full, api); got != want {
						t.Fatalf("seed %d leaf %s: newest assistant outside %s is %q, want %q", seed, id, api, got, want)
					}
				}
				if s.KeptRangeOnBranch() {
					if got, want := contextTail(context), contextTail(full); got != want {
						t.Fatalf("seed %d leaf %s: kept range differs: %s vs %s", seed, id, got, want)
					}
				}
			}
		}
	}
}

// newestAssistantWithoutAPI is the id of the newest assistant message whose API is not api: GetBranchSelection's walk with api virtual.
func newestAssistantWithoutAPI(branch []SessionEntry, api string) string {
	for _, b := range slices.Backward(branch) {
		if message, ok := b.(MessageEntry); ok && message.Message.Assistant != nil && string(message.Message.Assistant.API) != api {
			return b.Base().ID
		}
	}
	return ""
}

// contextTail is the ids from the latest compaction's first kept entry on, as the transcript renders them.
func contextTail(branch []SessionEntry) string {
	parsed := make([]parsedEntry, len(branch))
	for i, entry := range branch {
		parsed[i] = parsedEntry{entry: entry}
	}
	var ids []string
	for _, entry := range branch[compactionTrimIndex(parsed):] {
		ids = append(ids, entry.Base().ID)
	}
	return strings.Join(ids, ",")
}

func treeShape(nodes []*SessionTreeNode) []string {
	var out []string
	var walk func(depth int, nodes []*SessionTreeNode)
	walk = func(depth int, nodes []*SessionTreeNode) {
		for _, node := range nodes {
			out = append(out, fmt.Sprintf("%d %T %s %s %s", depth, node.Entry, node.Entry.Raw(), node.Label, node.LabelTimestamp))
			walk(depth+1, node.Children)
		}
	}
	walk(0, nodes)
	return out
}

// compactedSessionFile writes a persisted session of turns user/assistant turns, compacting every compactEvery turns so the active context
// holds the last compaction's kept turns and what follows.
func compactedSessionFile(t *testing.T, turns, compactEvery int) string {
	t.Helper()
	dir := t.TempDir()
	session, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("lorem ipsum dolor sit amet ", 15)
	var kept string
	for i := range turns {
		id, err := session.AppendMessage(mkUserMsg(fmt.Sprintf("question %d: %s", i, text)))
		if err != nil {
			t.Fatal(err)
		}
		if i%compactEvery == compactEvery-2 {
			kept = id
		}
		if _, err := session.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d: %s", i, text))); err != nil {
			t.Fatal(err)
		}
		if i%compactEvery == compactEvery-1 {
			if _, err := session.AppendCompaction("summary", kept, 1000, nil, false, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	return session.Path()
}

// A long-lived process appends entries and compacts: entries behind the latest compaction leave memory as they would after a resume.
func TestLiveSessionReleasesEntriesBehindCompaction(t *testing.T) {
	dir := t.TempDir()
	session, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	var kept string
	for i := range 300 {
		id, err := session.AppendMessage(mkUserMsg(fmt.Sprintf("question %d", i)))
		if err != nil {
			t.Fatal(err)
		}
		if i == 297 {
			kept = id
		}
		if _, err := session.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := session.AppendCompaction("summary", kept, 1000, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	resident := 0
	session.mu.RLock()
	for i := range session.slots {
		if session.slots[i].entry != nil {
			resident++
		}
	}
	session.mu.RUnlock()
	// The kept turns (two entries each from turn 297), the compaction, and the newest assistant message before the kept range, which carries
	// the branch's model for getSessionContextSettings.
	if resident != 8 {
		t.Fatalf("%d entries resident after the compaction, want the 6 kept messages, the compaction and the newest earlier assistant", resident)
	}
	sameEntries(t, "GetEntries", session.GetEntries(), residentCopy(t, session.Path()).GetEntries())
}

// An extension's session read decodes only the entries it returns and answers as the read over every entry does.
func TestExtensionSessionReadsOfAnIndexedSessionMatchTheWholeLog(t *testing.T) {
	for seed := range uint64(4) {
		live, ids := randomStoreSession(t, 100+seed, 250)
		session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
		if err != nil {
			t.Fatal(err)
		}
		for _, leaf := range ids[len(ids)-15:] {
			if err := session.SetLeafID(&leaf); err != nil {
				t.Fatal(err)
			}
			whole, err := newSessionReadLog(session)
			if err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{"getEntries", "getLeafEntry", "getEntry", "getLabel", "getChildren", "getBranch", "getTree", "buildContextEntries", "buildSessionProjection", "buildSessionContext"} {
				probes := []*string{nil, &ids[0], &leaf, &ids[len(ids)/2], new("missing")}
				for _, entry := range session.getEntriesOfType("label") {
					var label LabelEntry
					if json.Unmarshal(entry.Raw(), &label) == nil {
						probes = append(probes, &label.TargetID)
					}
				}
				for _, id := range probes {
					log, err := sessionReadLogFor(session, method, id, id, id)
					if err != nil {
						t.Fatal(err)
					}
					got, gotErr := answerSessionRead(log, method, id, id, id)
					want, wantErr := answerSessionRead(whole, method, id, id, id)
					gotJSON, _ := json.Marshal(got)
					wantJSON, _ := json.Marshal(want)
					if !bytes.Equal(gotJSON, wantJSON) || (gotErr == nil) != (wantErr == nil) {
						t.Fatalf("seed %d leaf %s %s(%v): got %.300s, want %.300s", seed, leaf, method, id, gotJSON, wantJSON)
					}
				}
			}
		}
	}
}

// The indexed load keeps, skips and rejects the lines the whole load does: a malformed object line (cut short, trailing text, a stray brace, a
// bad escape) is skipped without a second validation, and a truthy record that is not an entry fails both loads with the same error.
func TestIndexedLoadSkipsTheLinesTheWholeLoadSkips(t *testing.T) {
	path := compactedSessionFile(t, 12, 4)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(strings.TrimSuffix(string(data), "\n"), "\n")
	noise := []string{`{"type":"message","id":"cut"`, `{"type":"label","id":"x","parentId":null} trailing`, `{"a":1}}`, `{"type":"message","id":"bad\x"}`, ` {broken`, `0`, `null`, `""`, `{}`, "\u00a0{\"type\":\"label\"}"}
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(line)
		if i > 0 {
			b.WriteString(noise[i%len(noise)] + "\n")
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	indexed, _, err := loadPersistedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if indexed.backing == "" {
		t.Fatal("the session did not take the indexed load")
	}
	whole := residentCopy(t, path)
	sameEntries(t, "entries", indexed.GetEntries(), whole.GetEntries())
	sameEntries(t, "branch", indexed.GetBranch(), whole.GetBranch())
	if got, want := indexed.Accounting(), whole.Accounting(); !reflect.DeepEqual(got, want) {
		t.Fatalf("accounting %+v, want %+v", got, want)
	}
	// Pi keeps every truthy JSON.parse result of a line (session-manager.ts parseSessionEntryLine), so both loads keep these records and agree.
	for _, record := range []string{`[1]`, `true`, `"text"`, `{"type":"custom","id":5}`} {
		kept := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(kept, append(slices.Clone(data), record+"\n"...), 0o600); err != nil {
			t.Fatal(err)
		}
		indexed, _, err := loadPersistedSession(kept)
		if err != nil {
			t.Fatalf("%s: indexed load: %v", record, err)
		}
		whole := residentCopy(t, kept)
		sameEntries(t, record+" entries", indexed.GetEntries(), whole.GetEntries())
		sameEntries(t, record+" branch", indexed.GetBranch(), whole.GetBranch())
		if got, want := indexed.GetLeafID(), whole.GetLeafID(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: indexed leaf %v, whole leaf %v", record, got, want)
		}
	}
}

// A read of more lines than one goroutine decodes (GetEntries, GetTree and the full branch of a long session) decodes them in parallel and
// returns what the whole load holds, in order.
func TestParallelHistoryReadMatchesTheWholeLoad(t *testing.T) {
	path := compactedSessionFile(t, 2*parallelReadMinLines+40, 50)
	indexed, _, err := loadPersistedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	evicted := 0
	for _, slot := range indexed.slots {
		if slot.entry == nil {
			evicted++
		}
	}
	if evicted < 2*parallelReadMinLines {
		t.Fatalf("%d of %d entries evicted: the read would not run in parallel", evicted, len(indexed.slots))
	}
	whole := residentCopy(t, path)
	sameEntries(t, "entries", indexed.GetEntries(), whole.GetEntries())
	sameEntries(t, "branch", indexed.GetBranch(), whole.GetBranch())
	if got := indexed.staleReads.Load(); got != 0 {
		t.Fatalf("%d stale reads", got)
	}
}

// Lines appended after a resume are found again by their recorded offsets once a compaction evicts them, so the reads return the appended
// entries and never fall back to their base members.
func TestAppendedLinesReadBackAfterEviction(t *testing.T) {
	path := compactedSessionFile(t, 8, 4)
	s, _, err := loadPersistedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept string
	for i := range 12 {
		id, err := s.AppendMessage(mkUserMsg(fmt.Sprintf("after resume %d %s", i, strings.Repeat("x", i*37))))
		if err != nil {
			t.Fatal(err)
		}
		if i == 10 {
			kept = id
		}
		if _, err := s.AppendMessage(mkAssistantMsg(fmt.Sprintf("reply %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AppendCompaction("summary", kept, 1000, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	evicted := 0
	for _, slot := range s.slots {
		if slot.entry == nil {
			evicted++
		}
	}
	if evicted < 20 {
		t.Fatalf("only %d entries evicted", evicted)
	}
	sameEntries(t, "entries", s.GetEntries(), residentCopy(t, path).GetEntries())
	if got := s.staleReads.Load(); got != 0 {
		t.Fatalf("%d appended lines did not read back", got)
	}
}

// GetLabel answers from the labels the Session keeps as it adds label entries (session-manager.ts labelsById), without reading any entry:
// the latest label entry for a target wins and an empty one clears it.
func TestGetLabelReadsNoEntries(t *testing.T) {
	session := NewSession("labels", t.TempDir())
	target, err := session.AppendMessage(mkUserMsg("hello"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 300 {
		label := fmt.Sprintf("l%d", i)
		if _, err := session.AppendLabelChange(target, &label); err != nil {
			t.Fatal(err)
		}
	}
	if got, ok := session.GetLabel(target); !ok || got != "l299" {
		t.Fatalf("label %q %v, want l299", got, ok)
	}
	if allocs := testing.AllocsPerRun(20, func() { session.GetLabel(target) }); allocs != 0 {
		t.Fatalf("GetLabel allocates %.0f times; it decodes label entries", allocs)
	}
	empty := ""
	if _, err := session.AppendLabelChange(target, &empty); err != nil {
		t.Fatal(err)
	}
	if got, ok := session.GetLabel(target); ok || got != "" {
		t.Fatalf("cleared label %q %v", got, ok)
	}
}

// The indexed load decodes a record of a non-message type once, as its type, and it reads the same entries as the whole load: the same member
// or RawEntry (a member that does not decode, an unknown or non-string type, a non-string id) and the same accounting.
func TestIndexedLoadDecodesOtherEntryTypesAsTheWholeLoad(t *testing.T) {
	path := compactedSessionFile(t, 6, 3)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const ts = `"timestamp":"2026-01-01T00:00:00.000Z"`
	usage := `{"input":5,"output":1,"cacheRead":9000,"cacheWrite":0,"totalTokens":9006,"cost":{"input":0.1,"output":0,"cacheRead":0.01,"cacheWrite":0,"total":0.11}}`
	records := []string{
		`{"type":"compaction","id":"o1","parentId":null,` + ts + `,"summary":"s","firstKeptEntryId":"x","tokensBefore":"many"}`,
		`{"type":"compaction","id":"o2","parentId":"o1",` + ts + `,"summary":"s","firstKeptEntryId":"x","tokensBefore":3,"usage":` + usage + `}`,
		`{"type":"branch_summary","id":"o3","parentId":"o2",` + ts + `,"fromId":"o1","summary":"b","usage":` + usage + `}`,
		`{"type":"usage","id":"o4","parentId":"o3",` + ts + `,"kind":"cache_warm","provider":"p","model":"m","usage":` + usage + `}`,
		`{"type":"usage","id":"o5","parentId":"o4",` + ts + `,"kind":"cache_warm","provider":"p","model":"m","usage":"none"}`,
		`{"type":"label","id":"o6","parentId":"o5",` + ts + `,"targetId":5,"label":"x"}`,
		`{"type":"label","id":"o7","parentId":"o6",` + ts + `,"targetId":"o1","label":"kept"}`,
		`{"type":"model_change","id":"o8","parentId":"o7",` + ts + `,"provider":null,"modelId":"m"}`,
		`{"type":"thinking_level_change","id":"o9","parentId":"o8",` + ts + `,"thinkingLevel":"high"}`,
		`{"type":"custom","id":"o10","parentId":"o9",` + ts + `,"customType":"k","data":{"b":1,"a":[2]}}`,
		`{"type":"session_info","id":"o11","parentId":"o10",` + ts + `,"name":3}`,
		`{"type":"session_info","id":"o12","parentId":"o11",` + ts + `,"name":"named"}`,
		`{"type":"bash_execution","id":"o13","parentId":"o12",` + ts + `,"command":"ls"}`,
		`{"type":"future_type","id":"o14","parentId":"o13",` + ts + `,"x":1}`,
		`{"type":7,"id":"o15","parentId":"o14",` + ts + `}`,
		`{"type":"label","id":15,"parentId":"o15",` + ts + `,"targetId":"o1","label":"numeric id"}`,
		`{"id":"o17","parentId":"o15",` + ts + `}`,
		`{"type":"session","version":3,"id":"mid","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp"}`,
		`{"type":"context_edit","id":"o18","parentId":"o17",` + ts + `,"edits":"bad"}`,
	}
	if err := os.WriteFile(path, append(data, strings.Join(records, "\n")+"\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	indexed, _, err := loadPersistedSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if indexed.backing == "" {
		t.Fatal("the session did not take the indexed load")
	}
	whole := residentCopy(t, path)
	sameEntries(t, "entries", indexed.GetEntries(), whole.GetEntries())
	for i, entry := range indexed.GetEntries() {
		if want := whole.GetEntries()[i]; reflect.TypeOf(entry) != reflect.TypeOf(want) {
			t.Fatalf("entry %d is %T, the whole load reads %T", i, entry, want)
		}
	}
	if got, want := indexed.Accounting(), whole.Accounting(); !reflect.DeepEqual(got, want) {
		t.Fatalf("accounting %+v, want %+v", got, want)
	}
	if got, want := indexed.GetSessionName(), whole.GetSessionName(); got != want {
		t.Fatalf("name %q, want %q", got, want)
	}
}

// Pi's session holds the message object the agent appended (session-manager.ts appendMessage pushes it into fileEntries), so an appended turn
// is not held twice. The session keeps the appended message when its record decodes to an equal message, and the record's decode otherwise.
func TestAppendedMessageIsHeldOnceWhenItsRecordDecodesToIt(t *testing.T) {
	session := NewSession("held-once", t.TempDir())
	usage := ai.Usage{Input: 3, Output: 5, TotalTokens: 8}
	shared := &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}}, Usage: &usage, StopReason: ai.StopReasonStop, Timestamp: 7}
	noUsage := &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}}, StopReason: ai.StopReasonStop, Timestamp: 8}
	for _, tc := range []struct {
		name  string
		msg   *agent.AssistantMessage
		share bool
	}{{"round trip", shared, true}, {"absent usage", noUsage, false}} {
		id, err := session.AppendMessage(agent.AgentMessage{Assistant: tc.msg})
		if err != nil {
			t.Fatal(err)
		}
		found, _ := session.GetEntry(id)
		entry, ok := asMessage(found)
		if !ok {
			t.Fatalf("%s: entry %T", tc.name, found)
		}
		if got := entry.Message.Assistant == tc.msg; got != tc.share {
			t.Fatalf("%s: entry holds the appended message = %v, want %v", tc.name, got, tc.share)
		}
		// An entry held as appended holds no record (Pi's session holds no encoding); compare it with its record attached.
		if decoded := sessionentry.DecodeSessionEntry(entry.Raw()); !reflect.DeepEqual(decoded, SessionEntry(sessionentry.WithRecord(entry, entry.Raw()))) {
			t.Fatalf("%s: entry differs from its record's decode:\n%#v\n%#v", tc.name, entry.Message.Assistant, decoded)
		}
	}
}

// A tool result is held as appended whatever its Details hold (a tool's typed details, an extension's map): its record decodes the Details
// to their JSON, which is then the only difference from the appended message. Pi's session holds the tool's details object
// (session-manager.ts appendMessage); every other difference still keeps the record's decode (TestAppendedMessageIsHeldOnceWhenItsRecordDecodesToIt).
func TestAppendedToolResultWithTypedDetailsIsHeldOnce(t *testing.T) {
	session := NewSession("typed-details", t.TempDir())
	text := strings.Repeat("line of a file\n", 400)
	head := tools.TruncateHead(text, tools.TruncationOptions{MaxLines: new(10)})
	tail := tools.TruncateTail(text, tools.TruncationOptions{MaxLines: new(10)})
	result := func(id, name, text string, details any) *agent.ToolResultMessage {
		return &agent.ToolResultMessage{Role: "toolResult", ToolCallID: id, ToolName: name, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: details, Timestamp: 9}
	}
	for _, tc := range []struct {
		name string
		msg  *agent.ToolResultMessage
	}{
		{"read details", result("c1", "read", head.Content, &tools.ReadDetails{Truncation: &head})},
		{"bash details", result("c2", "bash", tail.Content, &tools.BashDetails{Truncation: &tail, FullOutputPath: "/tmp/out.log"})},
		{"map details", result("c3", "ext", "x", map[string]any{"b": 1.0, "a": []any{"x"}})},
	} {
		id, err := session.AppendMessage(agent.AgentMessage{ToolResult: tc.msg})
		if err != nil {
			t.Fatal(err)
		}
		found, _ := session.GetEntry(id)
		entry, ok := asMessage(found)
		if !ok {
			t.Fatalf("%s: entry %T", tc.name, found)
		}
		if entry.Message.ToolResult != tc.msg {
			t.Fatalf("%s: the entry holds the record's decode, not the appended message", tc.name)
		}
		record := entry.Raw()
		decoded, _ := asMessage(sessionentry.DecodeSessionEntry(record))
		held := *entry.Message.ToolResult
		raw, err := json.Marshal(held.Details)
		if err != nil {
			t.Fatal(err)
		}
		if !jsonEqual(raw, decoded.Message.ToolResult.Details) {
			t.Fatalf("%s: held details %s, record's %v", tc.name, raw, decoded.Message.ToolResult.Details)
		}
		held.Details = decoded.Message.ToolResult.Details
		entry.Message.ToolResult = &held
		if !reflect.DeepEqual(SessionEntry(sessionentry.WithRecord(entry, record)), sessionentry.DecodeSessionEntry(record)) {
			t.Fatalf("%s: entry differs from its record's decode beyond its details", tc.name)
		}
	}
}

// jsonEqual reports whether the JSON a and the decoded value b (raw JSON or a Go value) are the same JSON value.
func jsonEqual(a []byte, b any) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil {
		return false
	}
	rb, ok := b.(json.RawMessage)
	if !ok {
		var err error
		if rb, err = json.Marshal(b); err != nil {
			return false
		}
	}
	if json.Unmarshal(rb, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// Resuming a long session decodes only the tail of its context (retainLocked keeps retainTailEntries resident), the first prompt's context
// read makes the context resident once (retainReadLocked), and the next read decodes nothing from the records again. Without the tail the
// resume decodes the whole file; without the resident-after-read, every turn reads and decodes the older messages again.
func TestResumeDecodesTheTailAndTheFirstContextReadOnce(t *testing.T) {
	const turns = 1000 // 2,000 message entries
	dir := t.TempDir()
	live, err := NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range turns {
		if _, err := live.AppendMessage(mkUserMsg(fmt.Sprintf("question %d", i))); err != nil {
			t.Fatal(err)
		}
		if _, err := live.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	session, err := NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(session.slots); got != 2*turns {
		t.Fatalf("%d entries, want %d", got, 2*turns)
	}
	if got := residentSlots(session); got != retainTailEntries {
		t.Fatalf("%d entries resident after the resume, want the %d-entry tail", got, retainTailEntries)
	}

	opened := session.slotReads.Load()
	if got := len(session.BuildSessionProjection().Messages); got != 2*turns {
		t.Fatalf("first context read: %d messages, want %d", got, 2*turns)
	}
	if got := session.slotReads.Load() - opened; got != 2*turns-retainTailEntries {
		t.Fatalf("first context read decoded %d entries, want the %d outside the tail", got, 2*turns-retainTailEntries)
	}
	if got := residentSlots(session); got != 2*turns {
		t.Fatalf("%d entries resident after the first context read, want the whole context (%d)", got, 2*turns)
	}

	first := session.slotReads.Load()
	if got := len(session.BuildSessionProjection().Messages); got != 2*turns {
		t.Fatalf("second context read: %d messages, want %d", got, 2*turns)
	}
	if got := session.slotReads.Load() - first; got != 0 {
		t.Fatalf("second context read decoded %d entries from the records again, want none", got)
	}
	if stale := session.staleReads.Load(); stale != 0 {
		t.Fatalf("%d stale reads", stale)
	}
}
