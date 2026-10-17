package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type findTrimCase struct {
	Names []string `json:"names"`
}

// core/tools/find.ts:270 trims each fd output line with String.prototype.trim, which strips U+FEFF and U+00A0 and leaves U+0085 alone; strings.TrimSpace strips U+0085 and
// U+00A0 and leaves U+FEFF. A file name that begins or ends with one of them must be listed as Pi lists it. Pi's find and PiG's FindTool run over the same directories.
func TestFindTrimsOutputLinesLikeJavaScript(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names with these characters are only portable on Linux")
	}
	if _, err := exec.LookPath("fd"); err != nil {
		t.Skip("fd is not installed")
	}
	cases := []findTrimCase{
		{Names: []string{"plain.txt", "\ufeffbom_lead.txt"}},
		{Names: []string{"trail_bom\ufeff", "plain.txt"}},
		{Names: []string{"\u0085nel_lead.txt", "plain.txt"}},
		{Names: []string{"trail_nel\u0085", "plain.txt"}},
		{Names: []string{"\u00a0nbsp_lead.txt", "trail_nbsp\u00a0"}},
		{Names: []string{" space_lead.txt", "trail_space "}},
		{Names: []string{"\ufeff", "\u0085"}},
	}
	var want []string
	pioracle.Run(t, `
const { createFindTool } = await load("pi-coding-agent/core/tools/find.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const c of input) {
	const dir = fs.mkdtempSync(path.join(os.tmpdir(), "findtrim-"));
	for (const name of c.names) fs.writeFileSync(path.join(dir, name), "");
	try {
		const result = await createFindTool(dir).execute("call", { pattern: "*" });
		out.push(result.content.map((b) => b.text).join(""));
	} catch (error) { out.push("THROWN " + String(error && error.message)); }
	fs.rmSync(dir, { recursive: true, force: true });
}
emit(out);`, cases, &want)
	for i, c := range cases {
		dir := t.TempDir()
		for _, name := range c.Names {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
				t.Skipf("file system refuses %q: %v", name, err)
			}
		}
		result, err := (&FindTool{CWD: dir}).Execute(context.Background(), "call", json.RawMessage(`{"pattern":"*"}`), nil)
		got := "THROWN "
		if err == nil && len(result.Content) > 0 {
			got = result.Content[0].(ai.TextContent).Text
		} else if err != nil {
			got += err.Error()
		}
		if got != want[i] {
			t.Errorf("case %d names %q\n  PiG %q\n  Pi  %q", i, c.Names, got, want[i])
		}
	}
}
