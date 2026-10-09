package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type toolPathProbe struct {
	Path          *string `json:"path"`
	Cwd           string  `json:"cwd"`
	Home          string  `json:"home"`
	Hyperlinks    bool    `json:"hyperlinks"`
	Theme         string  `json:"theme"`
	EmptyFallback *string `json:"emptyFallback"`
}

type toolPathResult struct {
	Out   *string `json:"out,omitempty"`
	Error string  `json:"error,omitempty"`
}

// renderToolPathProbe renders one probe with Pig in the probe's home directory, capabilities and theme.
func renderToolPathProbe(t testing.TB, probe toolPathProbe) string {
	t.Setenv("HOME", probe.Home)
	SetCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: probe.Hyperlinks})
	SetTheme(probe.Theme)
	fallback := ""
	if probe.EmptyFallback != nil {
		fallback = *probe.EmptyFallback
	}
	path := ""
	if probe.Path != nil {
		path = *probe.Path
	}
	return renderToolPathArg(path, probe.Path != nil, probe.Cwd, fallback)
}

func toolPathProbes() []toolPathProbe {
	str := func(s string) *string { return &s }
	paths := []*string{nil, str(""), str("a.txt"), str("src/main.go"), str("./x/../y"), str("/abs/file.go"), str("/home/probe/proj/f.go"), str("/home/probe"), str("/home/probex/f.go"), str("/home/probe/"), str("~"), str("~/notes.md"), str("~user/x"),
		str("with space/ü ñ 日本語.txt"), str("hash#and?query%percent"), str("quote'\"<>|\\back"), str("dir/"), str("//double//slash"), str("C:\\Users\\x\\f.txt"), str("a\tb"), str("\x1b[31mred"), str(".hidden"), str("../up/f"), str("/")}
	var probes []toolPathProbe
	for _, theme := range []string{"dark", "light"} {
		for _, hyperlinks := range []bool{false, true} {
			for _, p := range paths {
				for _, cwd := range []string{"/work/dir", "/home/probe/proj", "/"} {
					for _, fallback := range []*string{nil, str(""), str("fallback.txt")} {
						probes = append(probes, toolPathProbe{Path: p, Cwd: cwd, Home: "/home/probe", Hyperlinks: hyperlinks, Theme: theme, EmptyFallback: fallback})
					}
				}
			}
		}
	}
	return probes
}

// renderToolPath (core/tools/render-utils.ts) against pinned Pi: a null path is the invalid-arg text, an empty one the muted "..." or the fallback,
// otherwise the accent path with the home directory shortened (by prefix, as Pi does) and, with hyperlink support, an OSC 8 link to the file URL
// of the path resolved against the working directory (spaces, unicode and reserved characters percent-encoded as pathToFileURL does).
func TestRenderToolPathMatchesPi(t *testing.T) {
	probes := toolPathProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/render_tool_path.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []toolPathResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps, previousTheme := GetCapabilities(), ActiveTheme()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	failures := 0
	for i, probe := range probes {
		out := renderToolPathProbe(t, probe)
		got := toolPathResult{Out: &out}
		if !reflect.DeepEqual(got, expected[i]) {
			if failures++; failures <= 5 {
				g, _ := json.Marshal(got)
				e, _ := json.Marshal(expected[i])
				p, _ := json.Marshal(probe)
				t.Errorf("%s:\n  Pig %s\n  Pi  %s", p, g, e)
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// TestRenderToolPathProbeDump prints the corpus for the Pi side of the render-tool-path parity scenario.
func TestRenderToolPathProbeDump(t *testing.T) {
	line, err := json.Marshal(toolPathProbes())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("toolpath-probes:%s\n", line)
}

// TestRenderToolPathParity prints Pig's rendering of the corpus, one JSON line per probe, for the render-tool-path parity scenario.
func TestRenderToolPathParity(t *testing.T) {
	previousCaps, previousTheme := GetCapabilities(), ActiveTheme()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	for _, probe := range toolPathProbes() {
		out := renderToolPathProbe(t, probe)
		var line bytes.Buffer
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(toolPathResult{Out: &out}); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("toolpath-observation:%s", line.String())
	}
}
