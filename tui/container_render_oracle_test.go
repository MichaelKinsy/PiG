package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type containerOp struct {
	Op    string `json:"op"`
	Text  string `json:"text,omitempty"`
	PadX  int    `json:"padX"`
	N     int    `json:"n"`
	Index int    `json:"index"`
	Width int    `json:"width"`
}

// Container.render and MouseRegion.render against the pinned pi-tui (tui.ts Container, components/mouse-region.ts): the children's rows in order at the given
// width, after children are added, edited, removed and cleared, with a nested container and a mouse region, across widths and an explicit invalidate.
// Pig's Container caches the flattened rows; Pi concatenates on every render, so every edit sequence must show the same rows.
// Pi source: packages/tui/src/tui.ts, packages/tui/src/components/mouse-region.ts
func TestContainerRenderMatchesPi(t *testing.T) {
	scripts := loadContainerScripts(t)
	input, err := json.Marshal(scripts)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/container_render.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, ops := range scripts {
		frames := replayContainerScript(ops)
		if len(frames) != len(expected[i]) {
			t.Fatalf("script %d: %d frames, Pi %d", i, len(frames), len(expected[i]))
		}
		for f := range frames {
			if !reflect.DeepEqual(nonNil(frames[f]), nonNil(expected[i][f])) {
				t.Errorf("script %d frame %d\n pi: %q\n go: %q", i, f, expected[i][f], frames[f])
			}
		}
	}
}

// loadContainerScripts reads the edit scripts both Pi and Pig replay (testdata/container_scripts.json).
func loadContainerScripts(t *testing.T) [][]containerOp {
	t.Helper()
	data, err := os.ReadFile("testdata/container_scripts.json")
	if err != nil {
		t.Fatal(err)
	}
	var scripts [][]containerOp
	if err := json.Unmarshal(data, &scripts); err != nil {
		t.Fatal(err)
	}
	return scripts
}

// replayContainerScript applies one script to a Container and returns the rows of every render op.
func replayContainerScript(ops []containerOp) [][]string {
	root, nested := NewContainer(), NewContainer()
	var leaves []*Text
	region := NewMouseRegion(NewPaddedText("region child", 1, 0, nil), nil)
	nestedAttached := false
	frames := [][]string{}
	for _, op := range ops {
		switch op.Op {
		case "add":
			leaf := NewPaddedText(op.Text, op.PadX, 0, nil)
			leaves = append(leaves, leaf)
			root.AddChild(leaf)
		case "spacer":
			root.AddChild(NewSpacer(op.N))
		case "setText":
			if op.Index < len(leaves) {
				leaves[op.Index].SetText(op.Text)
			}
		case "remove":
			if op.Index < len(leaves) {
				root.RemoveChild(leaves[op.Index])
			}
		case "clear":
			root.Clear()
			leaves = nil
			nestedAttached = false
		case "attachNested":
			if !nestedAttached {
				root.AddChild(nested)
				nestedAttached = true
			}
		case "nestedAdd":
			nested.AddChild(NewPaddedText(op.Text, 0, 0, nil))
		case "nestedClear":
			nested.Clear()
		case "attachRegion":
			root.AddChild(region)
		case "invalidate":
			root.Invalidate()
		case "render":
			frames = append(frames, nonNil(root.Render(op.Width)))
		}
	}
	return frames
}

// TestContainerRenderParity emits the observations test/parity/scenarios/tui-components/28-container-render.toml compares with Pi 1.1.0's Container and
// MouseRegion (test/parity/testdata/container-render-pi.mjs): the rows of every render op of every script, one JSON line per script.
func TestContainerRenderParity(t *testing.T) {
	for _, ops := range loadContainerScripts(t) {
		line, err := json.Marshal(replayContainerScript(ops))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("container-observation:%s\n", line)
	}
}
