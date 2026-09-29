package codingagent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestEarendilAnnouncementMatchesPinnedPi(t *testing.T) {
	caps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(caps) })
	tui.SetCapabilities(tui.TerminalCapabilities{})
	colors := map[string]string{}
	for _, token := range []string{"accent", "muted", "mdLink"} {
		colors[token] = tui.ActiveTheme().Fg(token)
	}
	input, err := json.Marshal(colors)
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/earendil-oracle.mjs", string(input)).CombinedOutput()
	if err != nil {
		t.Fatalf("pinned Pi oracle: %v: %s", err, output)
	}
	var want [][]string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	component := newEarendilAnnouncementComponent()
	for i, width := range []int{1, 12, 32, 56, 80, 120} {
		got := component.Render(width)
		if !slices.Equal(got, want[i]) {
			t.Errorf("width %d\ngot  %q\nwant %q", width, got, want[i])
		}
	}
}

func TestEarendilEmbeddedAssetAndImageProtocols(t *testing.T) {
	upstream, err := os.ReadFile("../../.upstream/current/packages/coding-agent/src/modes/interactive/assets/clankolas.png")
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(upstream) != sha256.Sum256(clankolasPNG) {
		t.Fatal("embedded PNG differs from pinned Pi")
	}
	caps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(caps) })
	for _, protocol := range []tui.ImageProtocol{tui.ImageProtocolKitty, tui.ImageProtocolITerm2} {
		t.Run(string(protocol), func(t *testing.T) {
			tui.SetCapabilities(tui.TerminalCapabilities{Images: protocol})
			announcement := newEarendilAnnouncementComponent()
			children := announcement.body.Children()
			image, ok := children[len(children)-2].(*tui.Image)
			if !ok {
				t.Fatal("announcement did not use the shared terminal Image component")
			}
			if image.Options.MaxWidthCells != 56 || image.Options.Filename != "clankolas.png" || image.MIMEType != "image/png" || image.Base64Data != loadAnnouncementImage() {
				t.Fatal("announcement changed Pi's image contract")
			}
			for _, width := range []int{12, 80, 120} {
				lines := announcement.Render(width)
				expectedImage := image.Render(width)
				start := len(lines) - len(expectedImage) - 2
				if !slices.Equal(lines[start:start+len(expectedImage)], expectedImage) {
					t.Fatal("image rows changed by announcement container")
				}
				joined := strings.Join(expectedImage, "\n")
				if strings.Contains(joined, "[image:") {
					t.Fatal("capable terminal got fallback")
				}
				switch protocol {
				case tui.ImageProtocolKitty:
					if !strings.Contains(joined, "\x1b_G") || image.GetImageID() == 0 {
						t.Fatal("missing Kitty transmission")
					}
				case tui.ImageProtocolITerm2:
					if !strings.Contains(joined, "\x1b]1337;File=") {
						t.Fatalf("missing iTerm2 transmission: %.100s", joined)
					}
				}
			}
		})
	}
}

func TestEarendilInvalidationRefreshesImageCapabilities(t *testing.T) {
	caps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(caps) })
	tui.SetCapabilities(tui.TerminalCapabilities{})
	component := newEarendilAnnouncementComponent()
	if got := strings.Join(component.Render(80), "\n"); !strings.Contains(got, "clankolas.png") {
		t.Fatal("missing fallback before capability negotiation")
	}
	tui.SetCapabilities(tui.TerminalCapabilities{Images: tui.ImageProtocolKitty})
	component.Invalidate()
	if got := strings.Join(component.Render(80), "\n"); !strings.Contains(got, "\x1b_G") {
		t.Fatal("announcement retained fallback after image capability invalidation")
	}
}

func BenchmarkEarendilAnnouncementRender(b *testing.B) {
	caps := tui.GetCapabilities()
	b.Cleanup(func() { tui.SetCapabilities(caps) })
	tui.SetCapabilities(tui.TerminalCapabilities{})
	component := newEarendilAnnouncementComponent()
	component.Render(100)
	b.ReportAllocs()
	for b.Loop() {
		component.Render(100)
	}
}

func TestEarendilAssetRecordedHash(t *testing.T) {
	const recorded = "169acd0dfe6fbb8d8742ed24a3fc654fd0b2e2d4223c733249c5493723f1b72d"
	if got := fmt.Sprintf("%x", sha256.Sum256(clankolasPNG)); got != recorded {
		t.Fatalf("asset changed: %s; review assets/README.md attribution and upstream pin", got)
	}
}
