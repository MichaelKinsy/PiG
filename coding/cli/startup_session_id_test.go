package cli

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestStartupSessionCreationWarningColor(t *testing.T) {
	const plain = "Warning: No project session found with id 'chosen-id'; creating a new session with that id."
	if got := formatSessionCreationWarning("chosen-id", false); got != plain {
		t.Fatalf("plain warning = %q", got)
	}
	if got := formatSessionCreationWarning("chosen-id", true); got != "\x1b[33m"+plain+"\x1b[39m" {
		t.Fatalf("colored warning = %q", got)
	}
	if got := formatStartupSessionError(&sessionAlreadyExistsError{id: "chosen-id"}, true); got != "\x1b[31mSession already exists with id 'chosen-id'\x1b[39m" {
		t.Fatalf("colored fork error = %q", got)
	}
}

// Pi main.ts:368-382 passes the requested fork ID into SessionManager.forkFrom and writes the fork, not the source.
func TestStartupForkPreservesRequestedID(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.jsonl")
	startupNameSessionFixture(t, root, source)
	selection, err := resolveStartupSessionSelection(Args{Fork: source, SessionID: "chosen-fork-id"}, root, root)
	if err != nil {
		t.Fatal(err)
	}
	header, err := codingagent.ReadSessionHeader(selection.forkPath)
	if err != nil || header == nil || header.ID != "chosen-fork-id" {
		t.Fatalf("fork header = %+v, %v", header, err)
	}
	if selection.forkPath == source {
		t.Fatal("fork replaced source")
	}
	original, err := codingagent.ReadSessionHeader(source)
	if err != nil || original == nil || original.ID != "existing-session" {
		t.Fatalf("source header = %+v, %v", original, err)
	}
}
