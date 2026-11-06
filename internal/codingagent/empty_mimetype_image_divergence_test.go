package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestEmptyMimeTypeImageDiffersFromPi pins both sides of D266. Pi's getTextOutput (render-utils.ts) names an image block
// `img.mimeType ?? "image/unknown"`, so an empty mimeType prints `[Image: []]`, while its dimensions guard (`img.data && img.mimeType`)
// already treats "" as no type. ai.ImageContent cannot tell an empty type from an absent one, and PiG prints
// `[Image: [image/unknown]]` for both. The control with a real type must match Pi byte for byte. The test fails when Pi
// prints image/unknown for "" (D266 may be removable) and when PiG prints `[Image: []]`.
//
// pig divergence (D266): the expected PiG rows name the image image/unknown, Pi's rows name it with the empty string.
// Pi source: packages/coding-agent/src/core/tools/render-utils.ts
func TestEmptyMimeTypeImageDiffersFromPi(t *testing.T) {
	cwd := "/tmp"
	image := func(mimeType string) *toolCardResult {
		return &toolCardResult{Content: []map[string]any{{"type": "image", "data": "", "mimeType": mimeType}}}
	}
	type probeCase struct {
		name  string
		tool  string
		args  map[string]any
		mime  string
		pig   string
		piRow string
	}
	cases := []probeCase{
		{"codemode card", "codemode", map[string]any{"code": "x"}, "", "[Image: [image/unknown]]", "[Image: []]"},
		{"bash card", "bash", map[string]any{"command": "x"}, "", "[Image: [image/unknown]]", "[Image: []]"},
		{"codemode control", "codemode", map[string]any{"code": "x"}, "image/png", "[Image: [image/png]]", "[Image: [image/png]]"},
		{"bash control", "bash", map[string]any{"command": "x"}, "image/png", "[Image: [image/png]]", "[Image: [image/png]]"},
	}
	var probes []toolCardProbe
	for _, c := range cases {
		probes = append(probes, toolCardProbe{Tool: c.tool, Args: c.args, Result: image(c.mime), Expanded: true, Width: 80, Theme: "dark", CWD: cwd})
	}
	piRows := make([][]string, len(cases))
	for _, tool := range []string{"codemode", "bash"} {
		script := "testdata/codemode_cards.mjs"
		if tool == "bash" {
			script = builtinToolCardsScript
		}
		var indexes []int
		var subset []toolCardProbe
		for i, c := range cases {
			if c.tool == tool {
				indexes = append(indexes, i)
				subset = append(subset, probes[i])
			}
		}
		input, err := json.Marshal(subset)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), "node", script, pigversion.UpstreamPackageVersion)
		cmd.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("Pi oracle %s: %v\n%s", script, err, &stderr)
		}
		var rows [][]string
		if err := json.Unmarshal(output, &rows); err != nil {
			t.Fatal(err)
		}
		for k, i := range indexes {
			piRows[i] = rows[k]
		}
	}
	tui.LoadAllHighlightLanguages()
	var bashProbes, codemodeProbes []toolCardProbe
	for i, c := range cases {
		if c.tool == "bash" {
			bashProbes = append(bashProbes, probes[i])
		} else {
			codemodeProbes = append(codemodeProbes, probes[i])
		}
	}
	pigRows := renderBuiltinToolCards(t, bashProbes)
	codemodeRows := renderCodemodeCards(t, codemodeProbes)
	var bashN, codemodeN int
	for i, c := range cases {
		var pig []string
		if c.tool == "bash" {
			pig, bashN = pigRows[bashN], bashN+1
		} else {
			pig, codemodeN = codemodeRows[codemodeN], codemodeN+1
		}
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Join(piRows[i], "\n"); !strings.Contains(got, c.piRow) {
				t.Errorf("Pi rows %q lack %q: Pi renders it differently now, so D266 may be removable", piRows[i], c.piRow)
			}
			if got := strings.Join(pig, "\n"); !strings.Contains(got, c.pig) {
				t.Errorf("PiG rows %q lack %q", pig, c.pig)
			}
			if c.mime != "" && strings.Join(pig, "\n") != strings.Join(piRows[i], "\n") {
				t.Errorf("the control differs from Pi:\n  Pig %q\n  Pi  %q", pig, piRows[i])
			}
		})
	}
}
