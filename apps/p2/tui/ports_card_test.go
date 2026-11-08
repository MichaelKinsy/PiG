package tui_test

import (
	"strings"
	"testing"

	portstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/ports/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
)

// test/p2/tui-p4.test.ts "ports card: action, port and name; Radius forward addresses only".
func TestPortsCardActionPortAndNameRadiusForwardAddressesOnly(t *testing.T) {
	url := "https://fwd--" + strings.Repeat("a", 32) + "--x.example.test/"
	tool := transcript.ToolView{
		ID: "p", Name: "ports", Args: jsvalue.Obj("action", "open", "port", 3000.0, "name", "web", "public", true), Status: transcript.ToolDone,
		Output:  "Forwarding port 3000.",
		Details: jsvalue.Obj("action", "open", "forward", jsvalue.Obj("url", url, "publicUrl", "https://evil.test/", "name", "web", "port", 3000.0)),
	}
	card := components.NewToolExecutionComponent(tool, portstui.Renderers, cardContext{darkRef(t)}, true)
	shown := plainLines(card.Render(100))
	mustMatch(t, `ports open · 3000 · web · public`, shown)
	mustMatch(t, `Forwarding port 3000\.\n +Open: https://fwd--a{32}--x\.example\.test/`, shown)
	if strings.Contains(shown, "evil.test") {
		t.Fatal("an address that is not a Radius forward's is shown")
	}
}
