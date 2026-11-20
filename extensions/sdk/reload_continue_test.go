package sdk

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReloadContinuationUsesOneReloadCallAndOldContextStaysStale(t *testing.T) {
	ext := New("reload-continue")
	ext.Command("reload", "Reload", func(ctx Context, _ string) error {
		if err := ctx.Reload(ReloadOptions{ContinueWith: "next turn"}); err != nil {
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

func TestReloadRejectsMultipleOptionsBeforeHostCall(t *testing.T) {
	if err := (Context{}).Reload(ReloadOptions{}, ReloadOptions{}); err == nil {
		t.Fatal("multiple options accepted")
	}
}
