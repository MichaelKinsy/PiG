// SPDX-License-Identifier: MIT

package coding

import (
	"fmt"
	"path/filepath"
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
		user := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("question %d", i)}}, Timestamp: time.Now().UnixMilli()}}
		assistant := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("answer %d", i)}}, Timestamp: time.Now().UnixMilli()}}
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
