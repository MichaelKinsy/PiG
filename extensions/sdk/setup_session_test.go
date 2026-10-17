package sdk

import (
	"errors"
	"strings"
	"testing"
)

// types.ts:411: newSession's setup callback stays in the extension; the call's arguments carry only its handle.
func TestSetupOptionSeparatesTheCallback(t *testing.T) {
	called := errors.New("called")
	opts := map[string]any{"parentSession": "/parent.jsonl", "setup": SetupFunc(func(SetupSessionManager) error { return called })}
	args, callback, err := setupOption(opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := args["setup"]; ok || args["parentSession"] != "/parent.jsonl" {
		t.Fatalf("args = %v", args)
	}
	if _, ok := opts["setup"]; !ok {
		t.Fatal("the caller's options lost their callback")
	}
	if callback == nil || !errors.Is(callback(SetupSessionManager{}), called) {
		t.Fatal("callback not returned")
	}
	plain := func(SetupSessionManager) error { return nil }
	if _, callback, err := setupOption(map[string]any{"setup": plain}); err != nil || callback == nil {
		t.Fatalf("plain func: callback=%v err=%v", callback != nil, err)
	}
	if args, callback, err := setupOption(map[string]any{"parentSession": "p"}); err != nil || callback != nil || args["parentSession"] != "p" {
		t.Fatalf("no callback: args=%v callback=%v err=%v", args, callback != nil, err)
	}
	if _, _, err := setupOption(map[string]any{"setup": "handle"}); err == nil || !strings.Contains(err.Error(), "setup must be a func(SetupSessionManager) error") {
		t.Fatalf("non-function setup: %v", err)
	}
}

// A setup request names a registered callback; an unknown handle is an error, and the callback's error settles the request.
func TestDispatchSetupRunsTheRegisteredCallback(t *testing.T) {
	e := &Extension{name: "ext"}
	failure := errors.New("seed failed")
	handle, entry := e.setups.add(e.name, func(SetupSessionManager) error { return failure })
	if err := e.dispatchSetup(Context{}, []byte(`{"handle":"`+handle+`"}`)); !errors.Is(err, failure) || !errors.Is(entry.failure(), failure) {
		t.Fatalf("dispatch = %v, recorded %v; want %v", err, entry.failure(), failure)
	}
	e.setups.remove(handle)
	if err := e.dispatchSetup(Context{}, []byte(`{"handle":"`+handle+`"}`)); err == nil || !strings.Contains(err.Error(), "unknown setup callback") {
		t.Fatalf("removed handle: %v", err)
	}
}
