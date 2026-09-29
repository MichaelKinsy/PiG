package codingagent

import (
	"errors"
	"reflect"
	"testing"
)

func TestPairReviewRenameFailurePreservesHeader(t *testing.T) {
	sessions := []SessionInfo{{Path: "rename-failure.jsonl", ID: "rename-failure", Name: "Old"}}
	loader := func() ([]SessionInfo, error) { return sessions, nil }
	denied := errors.New("rename denied")
	selector := newLoadedSessionSelector(loader, loader, func(string, string) error { return denied }, nil, "", nil)
	defer selector.close()
	before := sessionSelectorHeader(selector, 120)
	selector.enterRenameMode()
	if err := selector.confirmRename("New"); !errors.Is(err, denied) {
		t.Fatalf("rename error=%v, want original rejection", err)
	}
	if selector.renameMode {
		t.Fatal("rename mode remained active after rejection")
	}
	if got := sessionSelectorHeader(selector, 120); !reflect.DeepEqual(got, before) {
		t.Fatalf("rename rejection changed header: got %q want %q", got, before)
	}
}
