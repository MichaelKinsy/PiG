// SPDX-License-Identifier: MIT

package coding

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// replayReadsSession writes a Session file of turns user/assistant pairs with a system message every systemEvery turns, and opens it
// the way a resume does, so its message bodies are not resident.
func replayReadsSession(t *testing.T, turns, systemEvery int) (session *icodingagent.Session, systems int) {
	t.Helper()
	dir := t.TempDir()
	live, err := icodingagent.NewSessionManagerWithDir(dir, filepath.Join(dir, "sessions")).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range turns {
		if i%systemEvery == 0 {
			message := agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "cwd", Value: new(fmt.Sprintf("<cwd>/w%d</cwd>", i))}}, Timestamp: 1}}
			if _, err := live.AppendMessage(message); err != nil {
				t.Fatal(err)
			}
			systems++
		}
		user := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("question %d %s", i, strings.Repeat("q", 2048))}}, Timestamp: time.Now().UnixMilli()}}
		assistant := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d %s", i, strings.Repeat("a", 2048))}}, Timestamp: time.Now().UnixMilli()}}
		if _, err := live.AppendMessage(user); err != nil {
			t.Fatal(err)
		}
		if _, err := live.AppendMessage(assistant); err != nil {
			t.Fatal(err)
		}
	}
	session, err = icodingagent.NewSessionManagerWithDir(t.TempDir(), filepath.Dir(live.Path())).Open(live.Path())
	if err != nil {
		t.Fatal(err)
	}
	return session, systems
}

// The replay that restores the tool loadout when a Session is constructed (Pi's _restoreToolsFromTranscript) needs the system messages of the
// branch and nothing else. It read and decoded the whole branch: a 365 MB session held 2 GB of decoded entries and record buffers at the
// resume, where Pi peaks at 640 MB. Its reads must be the system messages, however long the branch is.
func TestReplayedTranscriptReadsOnlyTheSystemMessagesOfALongBranch(t *testing.T) {
	var reads []int64
	for _, turns := range []int{400, 1600} {
		every := turns / 4
		session, systems := replayReadsSession(t, turns, every)
		before := session.EntryReads()
		replayed, err := replayedTranscript(session)
		if err != nil {
			t.Fatal(err)
		}
		if len(replayed) != systems {
			t.Fatalf("%d turns: %d system messages replayed, want %d", turns, len(replayed), systems)
		}
		for i, message := range replayed {
			want := fmt.Sprintf("<cwd>/w%d</cwd>", i*every)
			if got := message.System.Sections[0].Value; got == nil || *got != want {
				t.Fatalf("%d turns: system message %d is out of order or wrong: %v, want %q", turns, i, got, want)
			}
		}
		reads = append(reads, session.EntryReads()-before)
		if got := reads[len(reads)-1]; got > int64(systems) {
			t.Fatalf("%d turns (%d entries): the replay read %d entries, want at most the %d system messages", turns, 2*turns+systems, got, systems)
		}
	}
}

// The bound the replay keeps, with the size of the session file as its denominator and no count of PiG's own reads: the bytes the replay
// allocates stay a small fraction of the file however long the branch is. The branch's entries are 4 KB each, so a replay that decodes them
// allocates at least the file's size.
func TestReplayedTranscriptAllocatesAFractionOfTheSessionFile(t *testing.T) {
	var perFile []float64
	for _, turns := range []int{300, 1200} {
		session, _ := replayReadsSession(t, turns, turns/4)
		info, err := os.Stat(session.Path())
		if err != nil {
			t.Fatal(err)
		}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		if _, err := replayedTranscript(session); err != nil {
			t.Fatal(err)
		}
		runtime.ReadMemStats(&after)
		allocated := after.TotalAlloc - before.TotalAlloc
		perFile = append(perFile, float64(allocated)/float64(info.Size()))
		t.Logf("%d turns, file %d B: the replay allocated %d B (%.3f of the file)", turns, info.Size(), allocated, perFile[len(perFile)-1])
		if allocated > uint64(info.Size())/10 {
			t.Fatalf("%d turns: the replay allocated %d B for a %d B session file; want at most a tenth", turns, allocated, info.Size())
		}
	}
	if perFile[1] > perFile[0] {
		t.Fatalf("the replay's allocation grew faster than the file: %.4f of the file at 300 turns, %.4f at 1200", perFile[0], perFile[1])
	}
}
