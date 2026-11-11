package views

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The bridge script and the tool's texts are the text Pi's modules have (they are copied, so a pin bump that changes them fails
// here): Node evaluates the vendored modules.
func nodeValue(t *testing.T, program string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("comparing with Pi's texts needs node (run in the tern-ci image): %v", err)
	}
	out, err := exec.Command(node, "--input-type=module", "-e", program).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	return string(out)
}

func TestTextsAreUpstreams(t *testing.T) {
	upstream := filepath.Join(oracle.Root(), "apps/p2/web/upstream/plugins/views")
	if got := nodeValue(t, `import { VIEW_BRIDGE_SCRIPT } from `+quote(filepath.Join(upstream, "bridge.ts"))+`; process.stdout.write(VIEW_BRIDGE_SCRIPT);`); got != viewBridgeScript {
		t.Errorf("the bridge script differs from upstream's:\n%s\n%s", got, viewBridgeScript)
	}
	extract := func(name string) string {
		return nodeValue(t, `import fs from "node:fs"; const src = fs.readFileSync(`+quote(filepath.Join(upstream, "tool.ts"))+`, "utf8");
const m = new RegExp("const `+name+` = (\\[[\\s\\S]*?\\n\\])\\.join\\(\"\\\\n\"\\)").exec(src);
process.stdout.write(Function("return " + m[1])().join("\n"));`)
	}
	if got := extract("DESCRIPTION"); got != toolDescription {
		t.Errorf("the tool's description differs from upstream's:\n%s", got)
	}
	if got := extract("PAGE_API"); got != pageAPI {
		t.Errorf("the help text differs from upstream's:\n%s", got)
	}
}

func quote(path string) string { return `"` + path + `"` }
