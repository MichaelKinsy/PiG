package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestReloadOnlyContinuationRetainsRunningTools(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		command    bool
		deferStop  bool
	}{
		{name: "plain"},
		{name: "null", args: `null`},
		{name: "empty object", args: `{}`},
		{name: "empty continuation", args: `{"continueWith":""}`},
		{name: "plain command", command: true, deferStop: true},
		{name: "continuation", args: `{"continueWith":"resume"}`, deferStop: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newPushRig(t)
			stops := make(chan struct{}, 2)
			called := false
			rig.me.requests.begin() // one pending tool
			if tc.command {
				rig.me.commands.begin()
				rig.me.requests.begin()
			}
			rig.bridge.SetActions(&HostCallbacks{
				Reload: func(context.Context) error {
					called = true
					if got := rig.h.retireGeneration(rig.me, func() { stops <- struct{}{} }); got != tc.deferStop {
						t.Errorf("retirement deferred = %t, want %t", got, tc.deferStop)
					}
					return nil
				},
				SendUserMessage: func(any, SendUserMessageOptions) error { return nil },
			})
			rig.h.runCall(rig.me, rig.conn, "", &CallPayload{Method: "reload", Args: json.RawMessage(tc.args)}, nil)
			if !called {
				t.Error("reload callback did not run")
			}
			if got := len(stops); (got == 0) != tc.deferStop {
				t.Errorf("stops before request returned = %d, want deferred stop: %t", got, tc.deferStop)
			}
			if tc.command {
				rig.me.requests.end()
				rig.me.commands.end()
				rig.h.retirementTasks.Wait()
				if len(stops) != 1 {
					t.Error("plain command reload did not retire when the command returned while a tool was still pending")
				}
			}
			rig.me.requests.end()
			rig.h.retirementTasks.Wait()
			if len(stops) != 1 {
				t.Error("generation did not stop exactly once after the request returned")
			}
		})
	}
}

func TestReloadContinuationDelegatesToTheModeOwnedOperation(t *testing.T) {
	bridge := NewUIBridge(func() {})
	calls := 0
	bridge.SetActions(&HostCallbacks{
		Reload:          func(context.Context) error { t.Fatal("ordinary reload must not handle the continuation"); return nil },
		SendUserMessage: func(any, SendUserMessageOptions) error { t.Fatal("mode owns delivery"); return nil },
		ReloadContinuation: func(_ context.Context, text string) error {
			calls++
			if text != "resume" {
				t.Fatalf("continuation = %q", text)
			}
			return nil
		},
	})
	result, err := bridge.handleReload(t.Context(), bridge.actions, json.RawMessage(`{"continueWith":"resume"}`))
	if err != nil || result.Error != nil || calls != 1 {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, calls)
	}
}

func TestReloadContinuationUsesNewRuntimeExactlyOnce(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	var events []string
	b.SetActions(&HostCallbacks{
		SendUserMessage: func(any, SendUserMessageOptions) error { t.Fatal("old runtime received continuation"); return nil },
		Reload: func(context.Context) error {
			events = append(events, "reload")
			b.SetActions(&HostCallbacks{SendUserMessage: func(content any, opts SendUserMessageOptions) error {
				if content != "continue now" || opts.DeliverAs != "followUp" {
					t.Fatalf("continuation = %#v, %#v", content, opts)
				}
				events = append(events, "new:user")
				return nil
			}})
			return nil
		},
	})
	res, err := call(b, "reload", `{"continueWith":"continue now"}`)
	if err != nil || res.Error != nil {
		t.Fatalf("reload = %#v, %v", res, err)
	}
	if !reflect.DeepEqual(events, []string{"reload", "new:user"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestReloadContinuationRequiresBindings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions *HostCallbacks
		want    string
	}{
		{"no actions", nil, "reload continuation: reload is not available"},
		{"no reload", &HostCallbacks{}, "reload continuation: reload is not available"},
		{"no sender", &HostCallbacks{Reload: func(context.Context) error { return nil }}, "reload continuation: sendUserMessage is not available"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestBridge(&mockUIContext{})
			b.SetActions(tc.actions)
			res, err := call(b, "reload", `{"continueWith":"next"}`)
			if err != nil || res.Error == nil || res.Error.Message != tc.want {
				t.Fatalf("reload = %#v, %v; want %q", res, err, tc.want)
			}
		})
	}
}

func TestReloadContinuationRejectsMalformedOptionsBeforeReload(t *testing.T) {
	b := newTestBridge(&mockUIContext{})
	b.SetActions(&HostCallbacks{Reload: func(context.Context) error { t.Fatal("malformed options reloaded"); return nil }})
	for _, args := range []string{`{"continueWith":42}`, `{`} {
		if _, err := call(b, "reload", args); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
}

func TestReloadContinuationFailureAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, args, reloadError, sendError, wantError string
		wantSend                                      bool
	}{
		{name: "empty", args: `{}`},
		{name: "empty string", args: `{"continueWith":""}`},
		{name: "reload failure", args: `{"continueWith":"next"}`, reloadError: "reload failed", wantError: "reload failed"},
		{name: "delivery failure", args: `{"continueWith":"next"}`, sendError: "delivery failed", wantError: "delivery failed", wantSend: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestBridge(&mockUIContext{})
			sent := false
			b.SetActions(&HostCallbacks{
				Reload: func(context.Context) error {
					if tc.reloadError != "" {
						return errors.New(tc.reloadError)
					}
					return nil
				},
				SendUserMessage: func(any, SendUserMessageOptions) error {
					sent = true
					if tc.sendError != "" {
						return errors.New(tc.sendError)
					}
					return nil
				},
			})
			res, err := call(b, "reload", tc.args)
			if err != nil {
				t.Fatal(err)
			}
			gotError := ""
			if res.Error != nil {
				gotError = res.Error.Message
			}
			if gotError != tc.wantError || sent != tc.wantSend {
				t.Fatalf("error=%q sent=%v, want %q %v", gotError, sent, tc.wantError, tc.wantSend)
			}
		})
	}
}
