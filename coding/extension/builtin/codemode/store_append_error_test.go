package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type emptyBranch struct{}

func (emptyBranch) GetBranch() []codingagent.SessionEntry { return nil }

// Upstream execute.ts:298-301 calls `options.appendEntry` (index.ts:35 binds it to pi.appendEntry) without a catch, so a failing append rejects the tool's execute with that error as it is; the agent loop reports its message.
func TestStoreAppendFailureRejectsWithTheAppendError(t *testing.T) {
	appendErr := errors.New("This extension ctx is stale after session replacement or reload.")
	base := extension.NewContext(t.TempDir(), nil, func() error { return nil }, extension.ContextActions{SessionManager: emptyBranch{}})
	ctx := extension.WithToolContext(context.Background(), extension.NewToolContext(base, "call-1", context.Background(), extension.ToolActions{
		AppendEntry: func(string, any) error { return appendErr },
	}))
	params, _ := json.Marshal(map[string]string{"code": "store(\"count\", 1);\nreturn 1;"})
	_, err := Execute(ctx, "call-1", params, nil, Options{})
	if !errors.Is(err, appendErr) || err.Error() != appendErr.Error() {
		t.Fatalf("Execute error = %v, want the append error %q as it is", err, appendErr)
	}
}
