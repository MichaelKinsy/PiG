package durable

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Runs Pi's packages/durable/src/errors.ts (Node, types stripped) over the same constructor calls and compares name, message,
// Error-ness and cause with PiG's StorageRequestError, SessionFailed, ConversationBusy and ReadAfterWrite.
func TestStorageRequestErrorAndSessionFailedMatchPi(t *testing.T) {
	ops := []map[string]any{
		{"class": "StorageRequestError", "message": "Invalid storage cursor"},
		{"class": "StorageRequestError", "message": ""},
		{"class": "StorageRequestError", "message": "Unknown conversation: 12"},
		{"class": "SessionFailed", "cause": map[string]any{"name": "Error", "message": "disk full"}},
		{"class": "SessionFailed", "cause": map[string]any{"name": "TypeError", "message": "x is not a function"}},
		{"class": "SessionFailed", "cause": "undefined"},
		{"class": "ConversationBusy", "id": 7},
		{"class": "ReadAfterWrite", "method": "scan"},
	}
	payload, err := json.Marshal(ops)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/errors_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi []struct {
		Name            string `json:"name"`
		Message         string `json:"message"`
		InstanceofError bool   `json:"instanceofError"`
		Cause           *struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		} `json:"cause"`
	}
	if err := json.Unmarshal(output, &pi); err != nil || len(pi) != len(ops) {
		t.Fatalf("oracle output %s: %v", output, err)
	}
	got := []error{
		NewStorageRequestError("Invalid storage cursor"),
		NewStorageRequestError(""),
		NewStorageRequestError("Unknown conversation: 12"),
		NewSessionFailed(errors.New("disk full")),
		NewSessionFailed(errors.New("x is not a function")),
		NewSessionFailed(nil),
		NewConversationBusy(7),
		NewReadAfterWrite("scan"),
	}
	names := []interface{ Name() string }{
		NewStorageRequestError(""), NewStorageRequestError(""), NewStorageRequestError(""),
		NewSessionFailed(nil), NewSessionFailed(nil), NewSessionFailed(nil), NewConversationBusy(7), NewReadAfterWrite("scan"),
	}
	for i, err := range got {
		want := pi[i]
		if names[i].Name() != want.Name || err.Error() != want.Message || !want.InstanceofError {
			t.Errorf("case %d: PiG %q %q, Pi %q %q", i, names[i].Name(), err.Error(), want.Name, want.Message)
		}
		cause := errors.Unwrap(err)
		switch {
		case want.Cause == nil && cause != nil:
			t.Errorf("case %d: PiG has cause %v, Pi has none", i, cause)
		case want.Cause != nil && (cause == nil || cause.Error() != want.Cause.Message):
			t.Errorf("case %d: PiG cause %v, Pi cause %q", i, cause, want.Cause.Message)
		}
	}
}
