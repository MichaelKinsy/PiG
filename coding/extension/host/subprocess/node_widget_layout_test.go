package subprocess

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 0.87.1 interactive-mode.ts:2322-2331 renders the first ten string
// entries as Text(line, 1, 0), with a muted truncation notice for overflow.
func TestNodeStringWidgetsUsePiTextLayout(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	root := findModuleRoot(t)
	textModule := filepath.Join(root, "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-tui/dist/components/text.js")
	entry := filepath.Join(t.TempDir(), "widget.mjs")
	write(t, entry, `export default function(pi) {
  pi.registerCommand("widget", {handler: (args, ctx) => ctx.ui.setWidget("list", JSON.parse(args))});
}`)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			h := newTestHost(t)
			h.SetMode("tui")
			bridge := NewUIBridge(func() {})
			h.SetUIBridge(bridge)
			t.Cleanup(func() { h.Shutdown("test done") })
			exts, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "widget", Source: entry, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 || len(exts) != 1 {
				t.Fatalf("LoadAll = %v, %v", exts, errs)
			}
			bridge.SetUIContext(&themedTestUI{UIContext: extension.NoopUIContext, theme: map[string]any{
				"foregrounds": map[string]string{"muted": "\x1b[31m"},
				"backgrounds": map[string]string{},
			}})
			for _, content := range [][]string{{}, {"ordinary"}, {"", "wide 界 and words wrap at a narrow width", "\x1b[31mred\x1b[39m"}, slices.Repeat([]string{"entry"}, 11)} {
				data, err := json.Marshal(content)
				if err != nil {
					t.Fatal(err)
				}
				for i, width := range []int{20, 40} {
					h.NotifyWidth(width)
					if i == 0 {
						if err := exts[0].Commands["widget"].Handler(t.Context(), string(data)); err != nil {
							t.Fatal(err)
						}
					}
					pollUntil(t, 5*time.Second, "widget not published", func() bool { return bridge.GetWidget("widget", "list") != nil })
					script := `import {pathToFileURL} from "node:url";
const {Text} = await import(pathToFileURL(process.argv[1]));
const content = JSON.parse(process.argv[2]);
const entries = content.slice(0, 10);
if (content.length > 10) entries.push("\x1b[31m... (widget truncated)\x1b[39m");
const lines = entries.flatMap(line => new Text(line, 1, 0).render(Number(process.argv[3])));
process.stdout.write(JSON.stringify(lines));`
					out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script, textModule, string(data), strconv.Itoa(width)).Output()
					if err != nil {
						t.Fatalf("Pi Text: %v", err)
					}
					var want []string
					if err := json.Unmarshal(out, &want); err != nil {
						t.Fatal(err)
					}
					if len(want) > 0 {
						pollUntil(t, 5*time.Second, "widget not rerendered at new width", func() bool {
							return len(bridge.GetWidget("widget", "list").Render(width)) > 0
						})
					}
					got := bridge.GetWidget("widget", "list").Render(width)
					if !slices.Equal(got, want) {
						t.Fatalf("width %d content %q: got %q, Pi Text %q", width, content, got, want)
					}
				}
			}
		})
	}
}
