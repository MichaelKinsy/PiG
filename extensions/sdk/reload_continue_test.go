package sdk

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReloadPreservesStockMethodTypeAndCall(t *testing.T) {
	ext := New("plain-reload")
	ext.Command("reload", "Reload", func(ctx Context, _ string) error {
		if _, ok := any(ctx).(interface{ Reload() error }); !ok {
			t.Errorf("Context does not implement Reload() error")
		}
		reload, ok := any(ctx.Reload).(func() error)
		if !ok {
			t.Errorf("Reload method value has type %T, want func() error", ctx.Reload)
			return ctx.Reload()
		}
		return reload()
	})
	host, done := startBusExtension(t, ext)
	readyBusExtension(t, host)
	calls, response := runSurfaceRequest(t, host, "reload-request", &requestMsg{Method: "command", Tool: "reload"}, func(call *callMsg) *callResultMsg {
		if call.Method != "reload" || string(call.Args) != "null" {
			t.Fatalf("host call = %s(%s), want reload with no args", call.Method, call.Args)
		}
		return &callResultMsg{}
	})
	if len(calls) != 1 || response.Error != nil {
		t.Fatalf("calls=%d response=%+v", len(calls), response)
	}
	stopBusExtension(t, host, done)
}

func TestReloadContinuationUsesOneReloadCallAndOldContextStaysStale(t *testing.T) {
	ext := New("reload-continue")
	ext.Command("reload", "Reload", func(ctx Context, _ string) error {
		if err := ctx.ReloadWithOptions(ReloadOptions{ContinueWith: "next turn"}); err != nil {
			return err
		}
		err := ctx.SendUserMessage("old ctx must not send", "followUp")
		if err == nil || !strings.Contains(err.Error(), "stale") {
			t.Errorf("old context send = %v", err)
		}
		return nil
	})
	host, done := startBusExtension(t, ext)
	readyBusExtension(t, host)
	calls, response := runSurfaceRequest(t, host, "reload-request", &requestMsg{Method: "command", Tool: "reload"}, func(call *callMsg) *callResultMsg {
		if call.Method == "sendUserMessage" {
			return &callResultMsg{Error: &errorInfo{Message: "context stale after reload"}}
		}
		if call.Method != "reload" {
			t.Fatalf("unexpected call %s", call.Method)
		}
		var options ReloadOptions
		if err := json.Unmarshal(call.Args, &options); err != nil || options.ContinueWith != "next turn" {
			t.Fatalf("reload args %s, %v", call.Args, err)
		}
		host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "invalidate", Args: json.RawMessage(`{"message":"context stale after reload"}`)}})
		return &callResultMsg{}
	})
	if len(calls) != 2 || calls[0].Method != "reload" || calls[1].Method != "sendUserMessage" || response.Error != nil {
		t.Fatalf("calls=%d response=%+v", len(calls), response)
	}
	stopBusExtension(t, host, done)
}
