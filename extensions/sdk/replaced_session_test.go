package sdk

import (
	"errors"
	"strings"
	"testing"
)

// A withSession callback stays in the extension: the call's arguments carry only its handle, and the caller's options keep the callback.
func TestWithSessionOptionSeparatesTheCallback(t *testing.T) {
	called := errors.New("called")
	opts := map[string]any{"parentSession": "/parent.jsonl", "withSession": WithSessionFunc(func(ReplacedSessionContext) error { return called })}
	args, callback, err := withSessionOption(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := args["withSession"]; ok || args["parentSession"] != "/parent.jsonl" {
		t.Fatalf("args = %v", args)
	}
	if _, ok := opts["withSession"]; !ok {
		t.Fatal("the caller's options lost their callback")
	}
	if callback == nil || !errors.Is(callback(ReplacedSessionContext{}), called) {
		t.Fatal("callback not returned")
	}
	plain := func(ReplacedSessionContext) error { return nil }
	if _, callback, err := withSessionOption(map[string]any{"withSession": plain}); err != nil || callback == nil {
		t.Fatalf("plain func: callback=%v err=%v", callback != nil, err)
	}
	if args, callback, err := withSessionOption(map[string]any{"position": "at"}); err != nil || callback != nil || args["position"] != "at" {
		t.Fatalf("no callback: args=%v callback=%v err=%v", args, callback != nil, err)
	}
	if _, _, err := withSessionOption(map[string]any{"withSession": "handle"}); err == nil || !strings.Contains(err.Error(), "withSession must be a func(ReplacedSessionContext) error") {
		t.Fatalf("non-function withSession: %v", err)
	}
}
