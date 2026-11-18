package subprocess

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// declinedRegionScript builds a card whose MouseRegion closure declines every click (it returns undefined) and prints what Pi's dispatch answers for a click on the
// card, and the rectangles the runtime sends with the frame for it.
const declinedRegionScript = `
import {createRequire} from "node:module";
import {pathToFileURL} from "node:url";
const {mouseRegions} = await import(pathToFileURL(%q));
const require = createRequire(%q);
const tui = require("./shims/pi-dist/pi-tui/sdk-bundle/index.js");
const {Container, dispatchMouseEvent} = require("./shims/pi-dist/pi-tui/tui.js");
let asked = 0;
const card = new tui.MouseRegion({render: () => ["card", "body"], invalidate() {}}, () => { asked++; return undefined; });
const root = new Container();
root.addChild(card);
root.render(10);
asked = 0;
const click = {type: "click", button: "left", x: 2, y: 0, screenX: 2, screenY: 0, width: 10, height: 2, shift: false, alt: false, ctrl: false, clickCount: 1};
const answer = dispatchMouseEvent(root, click);
const askedByPi = asked;
const regions = mouseRegions(root, 10, 2);
console.log(JSON.stringify({piHandled: answer !== undefined, askedByPi, askedByTheWalk: asked - askedByPi, regions}));
`

// D277, claim 3: a Node MouseRegion whose closure declines a left click is still answered handled by the host, where Pi leaves the click unhandled. Pi's dispatch asks
// the closure and gets no answer for it; the runtime cannot ask it for the rectangles it sends the host, so the region is one, and the host answers a click inside
// a region at once, so the selection is cleared and copy-on-select is skipped.
func TestNodeMouseRegionThatDeclinesAClickIsAnsweredHandledByTheHost(t *testing.T) {
	nodeCellRequireNode(t)
	mouse, err := filepath.Abs("runtime-node/mouse.mjs")
	if err != nil {
		t.Fatal(err)
	}
	runtimes, err := filepath.Abs("runtime-node/runtime-modules.mjs")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", fmt.Sprintf(declinedRegionScript, mouse, runtimes)).Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var probe struct {
		PiHandled      bool
		AskedByPi      int
		AskedByTheWalk int
		Regions        []RenderMouseRegion
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatalf("probe output %q: %v", out, err)
	}
	if probe.PiHandled || probe.AskedByPi != 1 {
		t.Fatalf("Pi's dispatch of the click: handled %v after asking the closure %d times; want it unhandled after one question", probe.PiHandled, probe.AskedByPi)
	}
	if probe.AskedByTheWalk != 0 || len(probe.Regions) != 1 {
		t.Fatalf("the runtime's walk asked the closure %d times and found regions %+v; want no question and the card's one rectangle", probe.AskedByTheWalk, probe.Regions)
	}
	rig := newMouseRig(t, extension.NewRenderIdentity())
	rig.shown(t, RenderResult{Lines: []string{"card", "body"}, Regions: probe.Regions})
	if result := rig.component.HandleMouse(clickAt(2, 0)); result == nil || !result.Handled || result.Target.Component != rig.component {
		t.Fatalf("the host's answer to the click the closure declined = %+v, want handled", result)
	}
}
