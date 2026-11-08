package screens_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

func TestBranchesShowFromTwoOn(t *testing.T) {
	label := "first answer"
	row := &screens.Row{}
	row.ID, row.Title, row.Main = "s", "Session", 2
	row.Branches = append(row.Branches, branch(2, &label), branch(1, nil))
	got := screens.Branches(row)
	if len(got) != 2 || got[0].Label != "first answer" || !got[0].Current || got[1].Label != "Session" || got[1].Current {
		t.Fatalf("branches = %+v", got)
	}
	row.Branches = row.Branches[:1]
	if screens.Branches(row) != nil {
		t.Fatal("one branch is listed")
	}
	row.Branches = append(row.Branches, branch(1, nil))
	archived := int64(1)
	row.ArchivedAt = &archived
	if screens.Branches(row) != nil {
		t.Fatal("an archived session lists its branches")
	}
	row.ArchivedAt = nil
	items := screens.ExpandBranches([]screens.Item{{Row: row}}, map[string]bool{"s": true})
	if len(items) != 3 || items[1].Branch == nil || items[2].Branch == nil {
		t.Fatalf("items = %+v", items)
	}
	if len(screens.ExpandBranches([]screens.Item{{Row: row}}, nil)) != 1 {
		t.Fatal("collapsed rows show no branches")
	}
}

// A message branched from, the branch switched, renamed and left, against the real server with the fake provider.
func TestTreeAgainstTheServer(t *testing.T) {
	fake, zero := true, 0
	server, err := hosts.StartServer(hosts.ServerOptions{DataDir: t.TempDir(), Port: &zero, Fake: &fake, Facets: []host.HostFacet{surfacehost.ServerFacet(nil)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(p2test.Background) })
	h, err := surfacehost.Open(surfacehost.Options{Server: server.Host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	scope := h.Scope(func() {})
	t.Cleanup(scope.Dispose)
	sessions, err := screens.OpenSessions(h, scope)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the catalog", sessions.Ready)
	id, err := sessions.Create(t.TempDir(), "tree")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := screens.OpenChat(h, scope, id, 1, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.Send("hello"); err != nil {
		t.Fatal(err)
	}
	var user, answer int
	waitFor(t, "the exchange", func() bool {
		tr, _ := chat.Transcript()
		user, answer = 0, 0
		for _, item := range tr.Items {
			switch it := item.(type) {
			case transcript.UserItem:
				if it.EntryID != nil {
					user = *it.EntryID
				}
			case transcript.AssistantItem:
				if it.EntryID != nil && !tr.Busy {
					answer = *it.EntryID
				}
			}
		}
		return user > 0 && answer > 0
	})
	tree := screens.NewTree(h)
	row := func() screens.Row {
		for _, item := range sessions.Items("", false) {
			if item.Row != nil && item.Row.ID == id {
				return *item.Row
			}
		}
		return screens.Row{}
	}
	conversation, text, err := tree.Navigate(id, user)
	if err != nil || conversation <= 1 || text != "hello" {
		t.Fatalf("navigate to the message: %d %q %v", conversation, text, err)
	}
	waitFor(t, "the catalog follows", func() bool { r := row(); return r.Main == conversation && len(r.Branches) == 2 })
	if err := tree.Rename(id, conversation, "  a try "); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the label", func() bool {
		r := row()
		return len(r.Branches) == 2 && r.Branches[0].Label != nil && *r.Branches[0].Label == "a try"
	})
	if err := tree.Select(id, 1); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "back on conversation 1", func() bool { return row().Main == 1 })
	if err := tree.Rename(id, conversation, ""); err == nil {
		t.Fatal("an empty label of a branch was accepted")
	}
	if _, _, err := tree.Navigate(id, 9999); err == nil {
		t.Fatal("an unknown message was navigated to")
	}
}

func branch(conversation int, label *string) sessionscontract.Branch {
	return sessionscontract.Branch{Conversation: conversation, Label: label}
}
