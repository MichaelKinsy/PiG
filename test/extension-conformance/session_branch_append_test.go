package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// upstream: packages/coding-agent/src/core/session-manager.ts getBranch (a walk of parentId links from the leaf to the root), getEntry (byId), getLabel (labelsById over every entry, a label entry with no label removing it) and getLeafEntry.
//
// Every SDK keeps a local copy of the session log that the host's state pushes extend, and derives the branch from it. An append below the leaf extends the branch it already holds; an append below an older entry, a leaf that moves with nothing appended, a leaf reset to the root and several entries in one push each change the branch some other way. After every one of them each SDK must read what Pi's SessionManager reads: the branch from the root, the leaf entry, an entry by id and its label.
func TestSessionBranchReadsFollowAppendsAndLeafMovesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	message := func(id, parent string) string {
		parentJSON := "null"
		if parent != "" {
			parentJSON = fmt.Sprintf("%q", parent)
		}
		return fmt.Sprintf(`{"type":"message","id":%q,"parentId":%s,"timestamp":"2026-10-11T00:00:00.000Z","message":{"role":"user","content":"text of %s"}}`, id, parentJSON, id)
	}
	label := func(id, parent, target, text string) string {
		textJSON := ""
		if text != "" {
			textJSON = fmt.Sprintf(`,"label":%q`, text)
		}
		return fmt.Sprintf(`{"type":"label","id":%q,"parentId":%q,"timestamp":"2026-10-11T00:00:01.000Z","targetId":%q%s}`, id, parent, target, textJSON)
	}
	type step struct {
		name   string
		act    func(*testing.T, *codingagent.Session)
		target string
		want   string
	}
	appendEntries := func(raws ...string) func(*testing.T, *codingagent.Session) {
		return func(t *testing.T, s *codingagent.Session) {
			t.Helper()
			for _, raw := range raws {
				if err := s.AppendEntry(json.RawMessage(raw)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	steps := []step{
		{"first entries", appendEntries(message("a", ""), message("b", "a"), message("c", "b")), "b", "branch=a>b>c leaf=c entry=b label=none"},
		{"append below the leaf", appendEntries(message("d", "c")), "b", "branch=a>b>c>d leaf=d entry=b label=none"},
		{"label entry on the branch", appendEntries(label("l", "d", "b", "mark")), "b", "branch=a>b>c>d>l leaf=l entry=b label=mark"},
		{"fork below an older entry", func(t *testing.T, s *codingagent.Session) {
			t.Helper()
			if err := s.Branch("b"); err != nil {
				t.Fatal(err)
			}
			appendEntries(message("e", "b"))(t, s)
		}, "b", "branch=a>b>e leaf=e entry=b label=mark"},
		{"leaf moves back with nothing appended", func(t *testing.T, s *codingagent.Session) {
			t.Helper()
			if err := s.Branch("d"); err != nil {
				t.Fatal(err)
			}
		}, "b", "branch=a>b>c>d leaf=d entry=b label=mark"},
		{"two entries in one push", appendEntries(message("f", "d"), message("g", "f")), "g", "branch=a>b>c>d>f>g leaf=g entry=g label=none"},
		{"leaf moves to an entry beside the branch", func(t *testing.T, s *codingagent.Session) {
			t.Helper()
			if err := s.Branch("e"); err != nil {
				t.Fatal(err)
			}
		}, "e", "branch=a>b>e leaf=e entry=e label=none"},
		{"append below the moved leaf", appendEntries(message("i", "e")), "i", "branch=a>b>e>i leaf=i entry=i label=none"},
		{"leaf reset to the root, then a new root", func(t *testing.T, s *codingagent.Session) {
			t.Helper()
			s.ResetLeaf()
			appendEntries(message("h", ""))(t, s)
		}, "h", "branch=h leaf=h entry=h label=none"},
		{"label removed", appendEntries(label("m", "h", "b", "")), "b", "branch=h>m leaf=m entry=b label=none"},
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			session := codingagent.NewSession("branch-append", "/w")
			session.SetPath(t.TempDir() + "/session.jsonl")
			view := codingagent.ExtensionSessionView{Session: session, CWD: "/w"}
			leaf := func() string {
				if id := session.GetLeafID(); id != nil {
					return *id
				}
				return ""
			}
			h.bridge.SetHostAction("sessionRead", func(method string, args json.RawMessage) (any, error) {
				return codingagent.ExtensionSessionRead(view, method, args)
			})
			h.bridge.SetHostAction("getSessionID", func() string { return session.ID() })
			h.bridge.SetHostAction("getSessionName", func() string { return "" })
			h.bridge.SetHostAction("getSessionFile", func() string { return session.Path() })
			h.bridge.SetHostAction("getLeafID", leaf)
			h.bridge.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
				all := session.GetEntries()
				if cursor < 0 || cursor > len(all) {
					cursor = 0
				}
				page := []json.RawMessage{}
				for _, entry := range all[cursor:] {
					page = append(page, entry.Raw())
				}
				return page, len(all), false, leaf()
			})
			command, ok := findCommand(h.runner, "session-branch-probe")
			if !ok {
				t.Fatal("missing session-branch-probe command")
			}
			for _, s := range steps {
				s.act(t, session)
				h.host.BroadcastStateUpdate()
				*h.notify = nil
				if err := command.Handler(context.Background(), s.target); err != nil {
					t.Fatalf("%s: %v", s.name, err)
				}
				if len(*h.notify) != 1 {
					t.Fatalf("%s: notifications %v", s.name, *h.notify)
				}
				if got := strings.TrimSuffix((*h.notify)[0], ":info"); got != s.want {
					t.Fatalf("%s:\n got  %s\n want %s", s.name, got, s.want)
				}
			}
		})
	}
}
