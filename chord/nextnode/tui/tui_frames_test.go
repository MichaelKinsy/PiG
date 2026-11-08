package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

// Tests of frames for the terminal (Pi's test/tui/frames.test.ts; slice 11 §4, fix round 1, Fable L9): a write over the launcher's
// 64 KiB arrives as frames each under it, cut only between escape sequences and surrogate pairs, the filter keeping every one
// whole. `splitFrames` is the sandbox's own TypeScript (the vendored client's src/tui/frames.ts), run under Node on the input.

// splitFrames runs the vendored frames.ts on data and returns its frames and its FRAME_UNITS.
func splitFrames(t *testing.T, data string) ([]string, int) {
	t.Helper()
	node := tuiNode(t)
	source, err := filepath.Abs(filepath.Join(clientRoot, "src", "tui", "frames.ts"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "run.mjs")
	body := `import { FRAME_UNITS, splitFrames } from ` + jsonString(t, "file://"+source) + `;
let data = "";
for await (const chunk of process.stdin) data += chunk;
process.stdout.write(JSON.stringify({ units: FRAME_UNITS, frames: splitFrames(JSON.parse(data)) }));
`
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, script)
	command.Stdin = strings.NewReader(string(input))
	out, err := command.Output()
	if err != nil {
		t.Fatalf("running frames.ts: %v", err)
	}
	var result struct {
		Units  int      `json:"units"`
		Frames []string `json:"frames"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	return result.Frames, result.Units
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSplittingFramesKeepsASmallWriteWhole(t *testing.T) {
	frames, _ := splitFrames(t, "hello")
	if len(frames) != 1 || frames[0] != "hello" {
		t.Fatalf("frames = %q, want [hello]", frames)
	}
}

func TestSplittingFramesCutsALargeTrueColourPaintBetweenSequencesEachFrameUnder64KiBAndPassingTheFilterWhole(t *testing.T) {
	var paint strings.Builder
	for cell := 0; cell < 20_000; cell++ {
		paint.WriteString("\x1b[38;2;" + strconv.Itoa(cell%256) + ";12;200m")
		if cell%7 == 0 {
			paint.WriteString("😀")
		} else {
			paint.WriteString("é")
		}
	}
	frames, units := splitFrames(t, paint.String())
	if len(frames) <= 1 {
		t.Fatalf("frames = %d, want more than 1", len(frames))
	}
	if strings.Join(frames, "") != paint.String() {
		t.Fatal("the frames do not join to the paint")
	}
	for i, frame := range frames {
		if n := len(utf16.Encode([]rune(frame))); n > units {
			t.Fatalf("frame %d has %d units, over %d", i, n, units)
		}
		if len(frame) > MaxFrame {
			t.Fatalf("frame %d has %d bytes, over %d", i, len(frame), MaxFrame)
		}
		filtered, err := FilterOutput(frame)
		if err != nil || filtered != frame {
			t.Fatalf("frame %d does not pass the filter whole: %v", i, err)
		}
	}
}
