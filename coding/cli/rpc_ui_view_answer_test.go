package cli

import (
	"fmt"
	"strings"
	"testing"
)

// The three tests below run the real binary over rpc with a Node extension (testdata/rpc-ui-view.mjs).

// TestRPCUIViewHandlersRedrawIsWrittenBeforeTheIntentReply pins the order a client relies on (D614): a handler that
// replaces the view of its own mount and then answers settles its intent done, and the ui_view event of that update is
// written before the reply to ui_view_intent, so the client holds the new view when it learns the intent is done.
// Twenty clicks in a row each see exactly their own update before their reply.
func TestRPCUIViewHandlersRedrawIsWrittenBeforeTheIntentReply(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	c := startRPCLines(t, listenTempDir(t), nil)
	c.request(`{"id":"at","type":"ui_view_attach","abilities":` + rpcViewAbilities + `}`)
	c.send(`{"id":"show","type":"prompt","message":"/view-redraw"}`)
	_, mounted := c.until("the mount", func(record map[string]any) bool { return record["type"] == "ui_view" })
	snapshot := mounted["snapshot"].(map[string]any)
	plugin, epoch := snapshot["plugin"], snapshot["mount"].(map[string]any)["epoch"]
	c.response("show", "prompt")
	for i := 1; i <= 20; i++ {
		id := fmt.Sprintf("i%d", i)
		c.send(fmt.Sprintf(`{"id":%q,"type":"ui_view_intent","attachment":1,"plugin":%q,"mount":{"key":"redraw","epoch":%v,"rev":%d},"intent":"fx.redraw","arg":{"n":%d}}`, id, plugin, epoch, i, i))
		var before []map[string]any
		line, reply := c.until("the reply of intent "+id, func(record map[string]any) bool {
			if record["type"] == "ui_view" {
				before = append(before, record)
			}
			return record["type"] == "response" && record["id"] == id && record["command"] == "ui_view_intent"
		})
		data, _ := reply["data"].(map[string]any)
		if reply["success"] != true || data["outcome"] != "done" || fmt.Sprint(data["answer"]) != fmt.Sprintf("map[rev:%d]", i+1) {
			t.Fatalf("intent %d, whose handler redraws its own mount, replied %s, want done with rev %d", i, line, i+1)
		}
		if len(before) != 1 {
			t.Fatalf("intent %d: %d ui_view events were written before the reply, want the handler's update: %v", i, len(before), before)
		}
		drawn := before[0]["snapshot"].(map[string]any)
		if before[0]["seq"] != float64(i+1) || drawn["mount"].(map[string]any)["rev"] != float64(i+1) || !strings.Contains(fmt.Sprint(drawn["view"]), fmt.Sprintf("redrawn-%d", i)) {
			t.Fatalf("intent %d: the event before the reply = %v, want redrawn-%d at rev %d", i, before[0], i, i+1)
		}
	}
	// No intent left a stale notice on the mount.
	c.send(`{"id":"n","type":"prompt","message":"/view-notices redraw"}`)
	_, notices := c.until("the notices", func(record map[string]any) bool {
		message, _ := record["message"].(string)
		return record["type"] == "extension_ui_request" && strings.HasPrefix(message, "view-notices:")
	})
	if notices["message"] != "view-notices:" {
		t.Errorf("the redrawn mount holds %v, want no notice", notices["message"])
	}
	c.response("n", "prompt")
	c.end()
}

// TestRPCUIViewAbilitiesEventMountsForAClientThatAttachesToAnIdleSession pins the event that tells an extension a
// client arrived (D614): the client attaches to a session that ran no prompt, its attach reply carries no snapshot, and
// the mount the extension makes in its onViewAbilities handler follows as a ui_view event. The mount names the
// client's own argument limit, and when the client detaches the handler is told there is no client.
func TestRPCUIViewAbilitiesEventMountsForAClientThatAttachesToAnIdleSession(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	c := startRPCLines(t, listenTempDir(t), []string{"PIG_TEST_VIEW_ARRIVE=mount"})
	c.request(`{"id":"state","type":"get_state"}`)
	if line, _ := c.request(`{"id":"at","type":"ui_view_attach","abilities":` + rpcViewAbilities + `}`); line != `{"id":"at","type":"response","command":"ui_view_attach","success":true,"data":{"attachment":1,"seq":0,"snapshots":[]}}` {
		t.Fatalf("attach = %s", line)
	}
	_, event := c.until("the mount made in the abilities handler", func(record map[string]any) bool { return record["type"] == "ui_view" })
	snapshot := event["snapshot"].(map[string]any)
	if event["seq"] != 1.0 || snapshot["state"] != "live" || snapshot["mount"].(map[string]any)["key"] != "arrived" || !strings.Contains(fmt.Sprint(snapshot["view"]), "arrived-100-panel") {
		t.Fatalf("the event after the attach = %v, want the extension's mount arrived-100-panel", event)
	}
	if line, _ := c.request(`{"id":"dt","type":"ui_view_detach","attachment":1}`); line != `{"id":"dt","type":"response","command":"ui_view_detach","success":true,"data":{}}` {
		t.Fatalf("detach = %s", line)
	}
	c.until("the handler's report that no client is attached", func(record map[string]any) bool {
		return record["type"] == "extension_ui_request" && record["message"] == "view-arrive:none"
	})
	c.end()
}

// TestRPCUIViewBlockedAbilitiesHandlerDelaysNothing pins that the host does not wait for an onViewAbilities handler: the
// handler below never returns, and the attach is answered, a prompt runs, the extension answers a command and the
// session ends as it does without the handler.
func TestRPCUIViewBlockedAbilitiesHandlerDelaysNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	c := startRPCLines(t, listenTempDir(t), []string{"PIG_TEST_VIEW_ARRIVE=block"})
	if line, _ := c.request(`{"id":"at","type":"ui_view_attach","abilities":` + rpcViewAbilities + `}`); line != `{"id":"at","type":"response","command":"ui_view_attach","success":true,"data":{"attachment":1,"seq":0,"snapshots":[]}}` {
		t.Fatalf("attach = %s", line)
	}
	c.until("the handler's entry", func(record map[string]any) bool {
		return record["type"] == "extension_ui_request" && record["message"] == "view-arrive:client"
	})
	if _, state := c.request(`{"id":"state","type":"get_state"}`); state["success"] != true {
		t.Fatalf("get_state while the handler blocks = %v", state)
	}
	c.send(`{"id":"try","type":"prompt","message":"/view-try"}`)
	c.until("the extension's answer to a command while its handler blocks", func(record map[string]any) bool {
		return record["type"] == "extension_ui_request" && record["message"] == "view-result:none"
	})
	c.response("try", "prompt")
	if line, _ := c.request(`{"id":"dt","type":"ui_view_detach","attachment":1}`); line != `{"id":"dt","type":"response","command":"ui_view_detach","success":true,"data":{}}` {
		t.Fatalf("detach = %s", line)
	}
	for _, line := range c.end() {
		if strings.Contains(line, `"type":"ui_view"`) {
			t.Errorf("the blocked handler mounted: %s", line)
		}
	}
}
