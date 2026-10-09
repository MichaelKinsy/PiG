package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi session-manager.ts:1202-1211 and :439-451 expose bashExecution as an ordinary message entry both before and after reload.
func TestBashMessageEntryDiscriminatorSurvivesAppendAndRestore(t *testing.T) {
	for _, excluded := range []bool{false, true} {
		t.Run(fmt.Sprint(excluded), func(t *testing.T) {
			session := NewSession("bash-entry", t.TempDir())
			id, err := session.AppendBashExecution(BashExecutionMessage{Command: "echo hi", Output: "hi\n", ExitCode: new(0), Timestamp: 1234, ExcludeFromContext: excluded})
			if err != nil {
				t.Fatal(err)
			}
			check := func(t *testing.T, session *Session) {
				t.Helper()
				entries := session.GetEntries()
				if len(entries) != 1 || entries[0].Base().Type != "message" {
					t.Fatalf("entries=%+v; want one message entry", entries)
				}
				entry, ok := session.GetEntry(id)
				if !ok || entry.Base().Type != "message" {
					t.Fatalf("indexed entry=%+v, found=%v", entry, ok)
				}
				branch := session.GetBranch()
				if len(branch) != 1 || branch[0].Base().Type != "message" {
					t.Fatalf("branch=%+v", branch)
				}
				message, ok := entry.(MessageEntry)
				if !ok || message.Message.Role() != agent.RoleBashExecution {
					t.Fatalf("decoded message=%+v, ok=%v", message, ok)
				}
				var bash BashExecutionEntry
				if err := json.Unmarshal(entry.Raw(), &bash); err != nil {
					t.Fatal(err)
				}
				if bash.Type != "message" || bash.MessageTimestamp != 1234 || bash.ExcludeFromContext != excluded {
					t.Fatalf("bash entry=%+v", bash)
				}
				projection := session.BuildSessionProjection()
				if len(projection.Entries) != 1 || projection.Entries[0].SourceEntry.Base().Type != "message" || len(projection.Messages) != 1 || projection.Messages[0].Role() != agent.RoleBashExecution {
					t.Fatalf("projection=%+v", projection)
				}
				if stats := session.Accounting(); stats.TotalMessages != 1 || stats.UserMessages != 0 || stats.AssistantMessages != 0 {
					t.Fatalf("accounting=%+v", stats)
				}
			}
			check(t, session)
			restored, err := newSessionFromEntries(session.CWD(), session.ID(), []FileEntry{session.GetEntries()[0]})
			if err != nil {
				t.Fatal(err)
			}
			check(t, restored)
		})
	}
}

// Pi tree-selector.ts:808-810 renders the command without a bang prefix or a command-length cap, regardless of excludeFromContext.
func TestBashMessageTreeRowUsesCommand(t *testing.T) {
	for _, command := range []string{"", "echo hi", "echo first\nsecond", strings.Repeat("界", 100)} {
		for _, excluded := range []bool{false, true} {
			session := NewSession("bash-tree", t.TempDir())
			id, err := session.AppendBashExecution(BashExecutionMessage{Command: command, ExcludeFromContext: excluded})
			if err != nil {
				t.Fatal(err)
			}
			entry, _ := session.GetEntry(id)
			got := newTreeRowFormatter(session).FormatTreeRow(entry)
			want := fg(tui.ActiveTheme().Dim, "[bash]: "+strings.ReplaceAll(command, "\n", " "))
			if got != want {
				t.Fatalf("command=%q excluded=%v: row=%q; want=%q", command, excluded, got, want)
			}
		}
	}
}
