package cli

import (
	"fmt"
	"strings"
	"testing"
)

// TestRPCUIViewReloadBreaksTheMountsOfTheReloadedExtension pins the reload rule over the carrier (D614): a reload
// replaces the extension's instance and a mount belongs to the instance that made it, so the client is sent the mount
// as broken with reason owner_gone, an intent raised on it afterwards is refused without reaching the new instance,
// and the mount the new instance makes under the same key succeeds with a new epoch. It runs the real binary over rpc
// with the Node extension testdata/rpc-ui-view.mjs, whose view-reload command calls ctx.reload().
func TestRPCUIViewReloadBreaksTheMountsOfTheReloadedExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	c := startRPCLines(t, listenTempDir(t), nil)
	c.request(`{"id":"at","type":"ui_view_attach","abilities":` + rpcViewAbilities + `}`)
	// command sends a prompt that runs an extension command and returns the ui_view events written up to its response.
	command := func(id, name string) []map[string]any {
		t.Helper()
		c.send(fmt.Sprintf(`{"id":%q,"type":"prompt","message":"/%s"}`, id, name))
		var events []map[string]any
		c.until("the prompt response to "+id, func(record map[string]any) bool {
			if record["type"] == "ui_view" {
				events = append(events, record)
			}
			return record["type"] == "response" && record["id"] == id && record["command"] == "prompt"
		})
		return events
	}
	// event returns the one ui_view event of events, or the next one written.
	event := func(what string, events []map[string]any) map[string]any {
		t.Helper()
		if len(events) > 1 {
			t.Fatalf("%s: %d ui_view events, want one: %v", what, len(events), events)
		}
		if len(events) == 1 {
			return events[0]["snapshot"].(map[string]any)
		}
		_, record := c.until(what, func(record map[string]any) bool { return record["type"] == "ui_view" })
		return record["snapshot"].(map[string]any)
	}
	epochOf := func(snapshot map[string]any) float64 { return snapshot["mount"].(map[string]any)["epoch"].(float64) }

	first := event("the mount", command("m1", "view-redraw"))
	if first["state"] != "live" || first["mount"].(map[string]any)["key"] != "redraw" {
		t.Fatalf("the mount = %v", first)
	}
	broken := event("the mount after the reload", command("rl", "view-reload"))
	if broken["state"] != "broken" || broken["reason"] != "owner_gone" || epochOf(broken) != epochOf(first) || broken["mount"].(map[string]any)["key"] != "redraw" {
		t.Fatalf("after the reload the client was sent %v, want the mount of epoch %v broken with reason owner_gone", broken, epochOf(first))
	}

	// An intent on the mount of the instance before the reload is refused; the new instance is not asked and redraws nothing.
	c.send(fmt.Sprintf(`{"id":"old","type":"ui_view_intent","attachment":1,"plugin":%q,"mount":{"key":"redraw","epoch":%v,"rev":%v},"intent":"fx.redraw","arg":{"n":7}}`, first["plugin"], epochOf(first), first["mount"].(map[string]any)["rev"]))
	var between []map[string]any
	line, reply := c.until("the reply of the intent on the old mount", func(record map[string]any) bool {
		if record["type"] == "ui_view" {
			between = append(between, record)
		}
		return record["type"] == "response" && record["id"] == "old"
	})
	if data, _ := reply["data"].(map[string]any); reply["success"] != true || data["outcome"] != "refused" || data["error"] != "mount is broken" || len(between) != 0 {
		t.Fatalf("an intent on the mount of the replaced instance replied %s after %d ui_view events, want refused (mount is broken) and none", line, len(between))
	}

	second := event("the mount of the new instance", command("m2", "view-redraw"))
	if second["state"] != "live" || epochOf(second) <= epochOf(first) || !strings.Contains(fmt.Sprint(second["view"]), "redrawn-0") {
		t.Fatalf("the new instance's mount = %v, want live with an epoch above %v", second, epochOf(first))
	}
	c.send(fmt.Sprintf(`{"id":"new","type":"ui_view_intent","attachment":1,"plugin":%q,"mount":{"key":"redraw","epoch":%v,"rev":%v},"intent":"fx.redraw","arg":{"n":3}}`, second["plugin"], epochOf(second), second["mount"].(map[string]any)["rev"]))
	line, reply = c.until("the reply of the intent on the new mount", func(record map[string]any) bool { return record["type"] == "response" && record["id"] == "new" })
	if data, _ := reply["data"].(map[string]any); reply["success"] != true || data["outcome"] != "done" {
		t.Fatalf("an intent on the new instance's mount replied %s, want done", line)
	}
	c.end()
}
