package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type editorScrollProbe struct {
	Rows     int `json:"rows"`
	Ups      int `json:"ups"`
	PaddingX int `json:"paddingX"`
	Width    int `json:"width"`
}

func editorScrollProbes(t testing.TB) []editorScrollProbe {
	t.Helper()
	data, err := os.ReadFile("testdata/editor_scroll_probes.json")
	if err != nil {
		t.Fatal(err)
	}
	var probes []editorScrollProbe
	if err := json.Unmarshal(data, &probes); err != nil {
		t.Fatal(err)
	}
	return probes
}

func renderEditorScrollProbe(probe editorScrollProbe) []string {
	rows := make([]string, probe.Rows)
	for i := range rows {
		rows[i] = fmt.Sprintf("row %d", i)
	}
	e := NewEditor()
	e.BorderColor = func(s string) string { return s }
	e.SetFocused(true)
	e.SetPaddingX(probe.PaddingX)
	e.HandleInput("\x1b[200~" + strings.Join(rows, "\n") + "\x1b[201~")
	for range probe.Ups {
		e.HandleInput("\x1b[A")
	}
	return e.Render(probe.Width)
}

// TestEditorScrollBordersMatchPi: Editor.renderTopBorder and renderBottomBorder (components/editor.ts createScrollBorder) after a pasted block of 6 to 40 rows
// scrolled by 0 to 20 Up keys, at paddings 0, 1 and 3 and widths from 6 (ellipsis) to 40 (centred label): both borders and every visible row equal Pi's.
func TestEditorScrollBordersMatchPi(t *testing.T) {
	probes := editorScrollProbes(t)
	input, err := os.ReadFile("testdata/editor_scroll_probes.json")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/editor_scroll_borders.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures, indicators := 0, 0
	for i, probe := range probes {
		got := renderEditorScrollProbe(probe)
		if strings.Contains(got[0], "more") || strings.Contains(got[len(got)-1], "more") {
			indicators++
		}
		if !reflect.DeepEqual(got, expected[i]) {
			if failures++; failures <= 5 {
				t.Errorf("%+v:\n  Pig %q\n  Pi  %q", probe, got, expected[i])
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	if indicators < len(probes)/3 {
		t.Errorf("only %d of %d probes show a scroll indicator", indicators, len(probes))
	}
}

// TestEditorScrollBordersParity prints Pig's renders of the corpus, one JSON line per probe, for the editor-scroll-borders parity scenario.
func TestEditorScrollBordersParity(t *testing.T) {
	for _, probe := range editorScrollProbes(t) {
		line, err := json.Marshal(renderEditorScrollProbe(probe))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("editor-scroll-observation:%s\n", line)
	}
}
