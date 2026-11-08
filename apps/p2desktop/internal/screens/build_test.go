package screens

import (
	"testing"
	"time"

	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
)

func ptr[T any](v T) *T { return &v }

func TestBuildItemsGroupsOrdersAndFilters(t *testing.T) {
	sessions := []sessionscontract.Session{
		{ID: "1", Title: "old pinned", Cwd: "/home/u/a", UpdatedAt: 10, Pinned: true},
		{ID: "2", Title: "newer", Cwd: "/home/u/a", UpdatedAt: 50},
		{ID: "3", Title: "other dir newest", Cwd: "/srv/x", UpdatedAt: 90},
		{ID: "4", Title: "remote", Cwd: "/w", Env: ptr("box"), UpdatedAt: 70},
		{ID: "5", Title: "gone", Cwd: "/home/u/a", UpdatedAt: 99, ArchivedAt: ptr(int64(1))},
	}
	items := BuildItems(sessions, map[string]status{"2": {busy: true}, "3": {error: "boom"}}, nil, "", false)
	var got []string
	for _, item := range items {
		if item.Group != nil {
			got = append(got, "#"+item.Group.Label)
		} else {
			got = append(got, item.Row.ID)
		}
	}
	want := []string{"#/srv/x", "3", "#box:/w", "4", "#~/a", "1", "2"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	if !items[6].Row.Busy || items[1].Row.Error != "boom" {
		t.Fatalf("statuses not applied: %+v %+v", items[6].Row, items[1].Row)
	}
	if archived := BuildItems(sessions, nil, nil, "", true); len(archived) != 2 || archived[1].Row.ID != "5" {
		t.Fatalf("archive: %+v", archived)
	}
	if byLocation := BuildItems(sessions, nil, nil, "BOX:", false); len(byLocation) != 2 {
		t.Fatalf("query on location: %d", len(byLocation))
	}
}

func TestOperationsShowOnTheirRows(t *testing.T) {
	sessions := []sessionscontract.Session{{ID: "a", Title: "a", Cwd: "/x"}, {ID: "b", Title: "b", Cwd: "/x"}}
	ops := []operation{
		{phase: "failed", id: "o1", kind: "clone", session: "a", err: "disk full"},
		{phase: "running", id: "o2", kind: "archive", session: "a"},
		{phase: "running", id: "o3", kind: "fork", session: "z", target: "b", copying: true},
	}
	items := BuildItems(sessions, nil, ops, "", false)
	a, b := items[1].Row, items[2].Row
	if a.Operation != "Clone failed" || !a.OperationFailed || a.OperationID != "o1" || a.OperationError != "disk full" {
		t.Fatalf("a: %+v", a)
	}
	if b.Operation != "Copying files…" {
		t.Fatalf("b: %+v", b)
	}
}

func TestPathsAndAges(t *testing.T) {
	cases := map[string]string{"/home/me/proj": "~/proj", "/home/me": "~", "/srv/x": "/srv/x", "/data/data/com.termux/files/home/q": "~/q"}
	for in, want := range cases {
		if got := ShortPath(in); got != want {
			t.Errorf("ShortPath(%q) = %q, want %q", in, got, want)
		}
	}
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{30 * time.Second: "now", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 49 * time.Hour: "2d", 30 * 24 * time.Hour: "Oct 9"} {
		if got := Ago(now.Add(-d), now); got != want {
			t.Errorf("Ago(%v) = %q, want %q", d, got, want)
		}
	}
}
