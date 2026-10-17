//go:build !race

package codingagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/tui"
)

// A resumed session holds the entries its active context needs and an index of the rest: the memory a resumed session retains does not grow
// with the history behind its latest compaction (Pi's SessionManager holds every entry).
func TestResumedSessionRetainsContextNotHistory(t *testing.T) {
	retained := func(path string) float64 {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		session, err := loadSessionFile(path)
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(session)
		return float64(after.HeapAlloc) - float64(before.HeapAlloc)
	}
	small, large := compactedSessionFile(t, 1000, 50), compactedSessionFile(t, 4000, 50)
	perEntry := (retained(large) - retained(small)) / float64(2*3000)
	searchPerEntry := float64(searchTextBytes(t, large)-searchTextBytes(t, small)) / float64(2*3000)
	t.Logf("retained per history entry: %.0f B, of which /tree search text %.0f B", perEntry, searchPerEntry)
	// Each history entry is about 500 bytes of JSON, and a resident one about 1,900 bytes of memory; an index entry is its id, parent, type,
	// role and file location, its id in the id map, and a message's /tree search text.
	if bound := 256 + 1.1*searchPerEntry; perEntry > bound {
		t.Fatalf("a resumed session retains %.0f B per history entry; want the index alone (at most %.0f B)", perEntry, bound)
	}
}

// A turn on a resumed session (two appends and a context build) costs the same whatever history lies behind the latest compaction.
func TestTurnCostIndependentOfHistoryBehindCompaction(t *testing.T) {
	const turns = 20
	text := strings.Repeat("lorem ipsum dolor sit amet ", 15)
	perTurn := func(history int) perfgate.Work {
		session, err := loadSessionFile(compactedSessionFile(t, history, 50))
		if err != nil {
			t.Fatal(err)
		}
		work := perfgate.Measure(func() {
			for i := range turns {
				if _, err := session.AppendMessage(mkUserMsg("question " + text)); err != nil {
					t.Fatal(err)
				}
				_ = session.BuildSessionContext()
				_ = session.GetContextBranch()
				if _, err := session.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d %s", i, text))); err != nil {
					t.Fatal(err)
				}
			}
		})
		return perfgate.Work{Allocs: work.Allocs / turns, Bytes: work.Bytes / turns}
	}
	small, large := perTurn(1000), perTurn(4000)
	perEntry := (large.Bytes - small.Bytes) / float64(2*3000)
	t.Logf("per turn: 1000 turns of history %.0f allocs %.0f B; 4000 turns %.0f allocs %.0f B; %.2f B per history entry", small.Allocs, small.Bytes, large.Allocs, large.Bytes, perEntry)
	if perEntry > 1 || large.Allocs > small.Allocs*1.05+5 {
		t.Fatalf("a turn costs %.2f B and %.0f allocs more per history entry behind the latest compaction; want none", perEntry, (large.Allocs-small.Allocs)/float64(2*3000))
	}
}

// A session without a file (--no-session) keeps the records of the entries its active context no longer holds compressed, not decoded: in a
// compacting loop it retains a fraction of each turn's JSON per turn. Pi keeps every entry of such a session decoded (session-manager.ts
// _appendEntry pushes to fileEntries whether or not it persists); what a read returns is unchanged.
func TestSessionWithoutFileRetainsCompressedHistory(t *testing.T) {
	vocabulary := strings.Fields("the session keeps its active context and compresses the records of history behind the latest compaction so a long loop of turns retains little memory per turn while every read still returns each entry")
	text := func(i, n int) string {
		var b strings.Builder
		for k := range n {
			b.WriteString(vocabulary[(i*7+k*3)%len(vocabulary)])
			b.WriteByte(' ')
		}
		return b.String()
	}
	session := NewSession("no-file", t.TempDir())
	retained := func() uint64 {
		var stats runtime.MemStats
		runtime.GC()
		runtime.GC()
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}
	// SESSIONSCALE_GATE=1 runs the 32,000-turn loop of the soak gate; the default run keeps the same shape at a tenth of the length.
	turns := 3200
	if os.Getenv("SESSIONSCALE_GATE") == "1" {
		turns = 32000
	}
	var kept string
	var recordBytes, searchBytes int
	var middle uint64
	for i := range turns {
		if i == turns/2 {
			middle = retained()
		}
		id, err := session.AppendMessage(mkUserMsg(fmt.Sprintf("question %d %s", i, text(i, 60))))
		if err != nil {
			t.Fatal(err)
		}
		if i%40 == 30 {
			kept = id
		}
		if _, err := session.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d %s", i, text(i+1, 120)))); err != nil {
			t.Fatal(err)
		}
		if i >= turns/2 {
			branch := session.GetContextBranch()
			recordBytes += len(branch[len(branch)-1].Raw()) + len(branch[len(branch)-2].Raw())
			searchBytes += piSearchTextLen(t, branch[len(branch)-1].Raw()) + piSearchTextLen(t, branch[len(branch)-2].Raw())
		}
		if i%40 == 39 {
			if _, err := session.AppendCompaction("summary", kept, 1000, nil, false, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	perTurn := float64(retained()-middle) / float64(turns/2)
	jsonPerTurn := float64(recordBytes) / float64(turns/2)
	searchPerTurn := float64(searchBytes) / float64(turns/2)
	t.Logf("retained %.0f B per turn for %.0f B of JSON and %.0f B of /tree search text per turn", perTurn, jsonPerTurn, searchPerTurn)
	// The index keeps each message's /tree search text uncompressed, so a search reads no record.
	if bound := 0.5*jsonPerTurn + 1.1*searchPerTurn; perTurn > bound {
		t.Fatalf("a session without a file retains %.0f B per turn, more than half the %.0f B of JSON a turn appends plus its %.0f B of search text", perTurn, jsonPerTurn, searchPerTurn)
	}
	if got := len(session.GetEntries()); got != 2*turns+turns/40 {
		t.Fatalf("%d entries, want %d", got, 2*turns+turns/40)
	}
}

// piSearchTextLen is the length of tree-selector.ts getSearchableText without a label for an ASCII message record: its role, a space and
// the first 200 characters of its text.
func piSearchTextLen(t *testing.T, raw []byte) int {
	t.Helper()
	var record struct {
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, block := range record.Message.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	for _, c := range []byte(text.String()) {
		if c >= utf8.RuneSelf {
			t.Fatalf("record %s is not ASCII", raw)
		}
	}
	return len(record.Message.Role) + 1 + min(200, text.Len())
}

// searchTextBytes sums piSearchTextLen over the message records of a session file.
func searchTextBytes(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.Contains(line, `"type":"message"`) {
			total += piSearchTextLen(t, []byte(line))
		}
	}
	return total
}

// The /tree search text the index keeps does not grow with a record: Pi's getSearchableText keeps a bash command and a custom message's
// string content whole, so a session of large ones keeps their text in the file, and a search reads it there and finds what Pi finds.
func TestIndexKeepsNoLargeSearchText(t *testing.T) {
	const turns = 400
	large := func(i int, tail string) string {
		return fmt.Sprintf("cat <<'EOF' > file%d\n%s\nEOF %s", i, strings.Repeat("payload line\n", 64<<10/13), tail)
	}
	retained := func() uint64 {
		var stats runtime.MemStats
		runtime.GC()
		runtime.GC()
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}
	for _, withFile := range []bool{true, false} {
		t.Run(fmt.Sprintf("file=%v", withFile), func(t *testing.T) {
			before := retained()
			dir := t.TempDir()
			session := NewSession("no-file", dir)
			if withFile {
				var err error
				if session, err = NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", ""); err != nil {
					t.Fatal(err)
				}
			}
			var kept string
			code := 0
			for i := range turns {
				id, err := session.AppendMessage(mkUserMsg(fmt.Sprintf("question %d", i)))
				if err != nil {
					t.Fatal(err)
				}
				if i%40 == 38 {
					kept = id
				}
				if _, err := session.AppendCustomMessageEntry("note", large(i, fmt.Sprintf("NoteTail%d", i)), true, nil); err != nil {
					t.Fatal(err)
				}
				if _, err := session.AppendBashExecution(BashExecutionMessage{Command: large(i, fmt.Sprintf("BashTail%d", i)), Output: "ok", ExitCode: &code, Timestamp: int64(i)}); err != nil {
					t.Fatal(err)
				}
				if _, err := session.AppendMessage(mkAssistantMsg(fmt.Sprintf("answer %d", i))); err != nil {
					t.Fatal(err)
				}
				if i%40 == 39 {
					if _, err := session.AppendCompaction("summary", kept, 1000, nil, false, nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			perTurn := float64(retained()-before) / turns
			runtime.KeepAlive(session)
			t.Logf("retained %.0f B per turn of two 64 KiB records", perTurn)
			if perTurn > 16<<10 {
				t.Fatalf("a session retains %.0f B per turn of two 64 KiB records; want their search text left in the records (at most 16 KiB)", perTurn)
			}
			roots, lt := session.lazyTree()
			found := map[string]bool{}
			var walk func([]tui.TreeNode)
			walk = func(nodes []tui.TreeNode) {
				for _, n := range nodes {
					text := n.(*treeNodeAdapter).NodeSearchableText()
					for _, tail := range []string{"NoteTail5 ", "BashTail5 ", "NoteTail395", "BashTail395"} {
						if strings.Contains(text+" ", tail) {
							found[tail] = true
						}
					}
					walk(n.NodeChildren())
				}
			}
			walk((&treeNodeAdapter{n: &SessionTreeNode{Children: roots}, f: newLazyTreeFormatter(lt, session.GetLeafID())}).NodeChildren())
			if len(found) != 4 {
				t.Fatalf("search found %v; want each record's tail", found)
			}
		})
	}
}
