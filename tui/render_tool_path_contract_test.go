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

// renderToolPathBoundaryProbes are the renderToolPath boundaries the corpus of TestRenderToolPathMatchesPi leaves out: the home directory itself, with and
// without a trailing separator, the root and a home that is a parent of the working directory, paths that share only a string prefix
// with the home directory or leave it through "..", "~" forms that the hyperlink's resolution expands, file URLs a resolution turns into paths (and the ones
// that make Pi's fileURLToPath throw), and relative paths whose working directory lies inside the home directory.
func renderToolPathBoundaryProbes() []toolPathProbe {
	str := func(s string) *string { return &s }
	paths := []*string{
		str("/home/probe/"), str("/home/probe"), str("/home/probex/f.go"), str("/home/pro"), str("/home/probe/./a"), str("/home/probe/../probe/a"), str("/home/probe/../other/a"),
		str("//home/probe/a"), str("/home/probe//a"), str("/HOME/probe/a"), str("/home/probe/a/../.."), str("/home/probe/a b/日本語.go"),
		str("~/"), str("~//x"), str("~/../x"), str("~/.."), str("~x"), str("a~/b"),
		str("file:///tmp/a%20b"), str("file:///home/probe/x"), str("file://localhost/tmp/x"), str("file://host/x"), str("file:///a%2Fb"), str("file:///a%00b"), str("file://"),
		str("@src/a.go"), str("\u00a0lead"), str("trail "), str("  "), str("."), str(".."), str("a/b/../../.."), str("x/\x00/y"),
	}
	var probes []toolPathProbe
	for _, home := range []string{"/home/probe", "/home/probe/", "/", "/home/probe/sub"} {
		for _, hyperlinks := range []bool{false, true} {
			for _, p := range paths {
				for _, cwd := range []string{"/work/dir", "/home/probe/proj", "/home/probe"} {
					probes = append(probes, toolPathProbe{Path: p, Cwd: cwd, Home: home, Hyperlinks: hyperlinks, Theme: "dark"})
				}
			}
		}
	}
	// A fallback stands in for an empty path only: it is shortened and linked like a path, and a path is never replaced by it.
	for _, fallback := range []string{"/home/probe/fb.txt", "~/fb", "fb space", ""} {
		for _, p := range []*string{str(""), str("a.txt"), nil} {
			for _, hyperlinks := range []bool{false, true} {
				probes = append(probes, toolPathProbe{Path: p, Cwd: "/home/probe/proj", Home: "/home/probe", Hyperlinks: hyperlinks, Theme: "light", EmptyFallback: str(fallback)})
			}
		}
	}
	return probes
}

// boundaryToolPathResult is what renderToolPath yields for a probe: its rendering, or the error it throws where its hyperlink cannot be named. The call
// renderers raise ToolPathLinkError in that case and the card draws its fallback.
func boundaryToolPathResult(t testing.TB, probe toolPathProbe) toolPathResult {
	out := renderToolPathProbe(t, probe)
	if probe.Path != nil && (probe.EmptyFallback == nil || *probe.Path != "") {
		if err := ToolPathLinkError(*probe.Path, probe.Cwd); err != nil {
			return toolPathResult{Error: truncateRunes(err.Error(), 80)}
		}
	}
	return toolPathResult{Out: &out}
}

// renderToolPath (core/tools/render-utils.ts) against pinned Pi over renderToolPathBoundaryProbes.
func TestRenderToolPathBoundariesMatchPi(t *testing.T) {
	probes := renderToolPathBoundaryProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/render_tool_path.mjs", pigversion.UpstreamPackageVersion)
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
	failures, thrown, shortened := 0, 0, 0
	for i, probe := range probes {
		got := boundaryToolPathResult(t, probe)
		if expected[i].Error != "" {
			thrown++
		}
		if expected[i].Out != nil && probe.Path != nil && len(*probe.Path) > 0 && bytes.Contains([]byte(*expected[i].Out), []byte("~")) {
			shortened++
		}
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
	// The counts come from Pi's results: a corpus that never reached a branch would pass it vacuously.
	if shortened < 100 {
		t.Errorf("Pi shortened %d of %d paths, want at least 100", shortened, len(probes))
	}
	if thrown < 12 {
		t.Errorf("Pi threw for %d of %d probes, want at least 12", thrown, len(probes))
	}
}

// TestRenderToolPathBoundaryProbeDump prints the boundary corpus for the Pi side of the render-tool-path-boundaries parity scenario.
func TestRenderToolPathBoundaryProbeDump(t *testing.T) {
	line, err := json.Marshal(renderToolPathBoundaryProbes())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("toolpath-probes:%s\n", line)
}

// TestRenderToolPathBoundaryParity prints Pig's rendering of the boundary corpus, one JSON line per probe, for the render-tool-path-boundaries parity scenario.
func TestRenderToolPathBoundaryParity(t *testing.T) {
	previousCaps, previousTheme := GetCapabilities(), ActiveTheme()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	for _, probe := range renderToolPathBoundaryProbes() {
		var line bytes.Buffer
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(boundaryToolPathResult(t, probe)); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("toolpath-observation:%s", line.String())
	}
}

// truncateRunes is String(message).slice(0, n) for the ASCII messages Pi's resolution throws.
func truncateRunes(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
