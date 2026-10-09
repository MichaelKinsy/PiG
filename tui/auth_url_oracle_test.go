package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type authURLProbe struct {
	Theme      string              `json:"theme"`
	Width      int                 `json:"width"`
	URL        string              `json:"url"`
	Hyperlinks bool                `json:"hyperlinks"`
	Bindings   map[string][]string `json:"bindings,omitempty"`
}

// auth-url.ts (the sign-in URL as an accent OSC 8 hyperlink, then the click hint linked to the URL, a dim bullet and the copy key hint)
// against pinned Pi: raw frames with and without hyperlink support, long, wide-character and escape-bearing URLs, narrow widths and a
// rebound or unbound copy key.
func TestAuthURLRendersLikePi(t *testing.T) {
	urls := []string{
		"https://auth.example.invalid/authorize?state=1",
		"https://auth.example.invalid/authorize?client_id=abc&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&scope=openid+profile&state=" + string(bytes.Repeat([]byte("x"), 120)),
		"https://例え.jp/パス?q=日本語",
		"http://a/😀",
		"x",
		"",
		"https://e.example/a b",
		"https://e.example/\x1b]8;;evil\x1b\\",
	}
	bindings := []map[string][]string{nil, {"app.message.copy": {}}, {"app.message.copy": {"ctrl+y", "alt+c"}}}
	var probes []authURLProbe
	for _, theme := range []string{"dark", "light"} {
		for _, hyperlinks := range []bool{true, false} {
			for _, width := range []int{80, 40, 12, 3, 1} {
				for _, url := range urls {
					for _, b := range bindings {
						probes = append(probes, authURLProbe{Theme: theme, Width: width, URL: url, Hyperlinks: hyperlinks, Bindings: b})
					}
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/auth_url.mjs", pigversion.UpstreamVersion)
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
	previousTheme, previousCaps := ActiveTheme(), GetCapabilities()
	restoreKeybindingsAfterTest(t)
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		SetTheme(previousTheme.Name)
	})
	failures := 0
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: probe.Hyperlinks})
		SetTheme(probe.Theme)
		defs := TUIKeybindingDefinitionsFor(KeybindingPlatformFor("linux", func(string) string { return "" }))
		defs["app.message.copy"] = TUIKeybindingDef{DefaultKeys: []string{"ctrl+x"}}
		SetKeybindings(NewKeybindingsManager(defs, probe.Bindings))
		got := NewAuthURL(probe.URL, func(string) error { return nil }, func() {}).Render(probe.Width)
		if !reflect.DeepEqual(got, expected[i]) {
			if failures++; failures <= 4 {
				t.Errorf("probe %d %+v:\n  Pig %q\n  Pi  %q", i, probe, got, expected[i])
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
