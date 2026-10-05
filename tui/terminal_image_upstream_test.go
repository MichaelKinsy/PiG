package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestRenderImageITerm2DefaultAspectRatio(t *testing.T) {
	// packages/tui/src/terminal-image.ts:643-648 defaults the omitted option to true.
	preserveCapabilityState(t)
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolITerm2})
	got := RenderImage("AAAA", ImageDimensions{20, 20}, ImageRenderOptions{MaxWidthCells: 2})
	if got == nil || strings.Contains(got.Sequence, "preserveAspectRatio=0") {
		t.Fatalf("omitted aspect ratio must remain enabled: %#v", got)
	}
}

func BenchmarkImageFallbackRender(b *testing.B) {
	preserveCapabilityState(b)
	SetCapabilities(TerminalCapabilities{})
	image := NewImage("AAAA", "image/png", ImageOptions{Filename: "/images/" + strings.Repeat("long-name", 20) + ".png"}, &ImageDimensions{1280, 720})
	b.ReportAllocs()
	for b.Loop() {
		image.Invalidate()
		image.Render(80)
	}
}

func TestUpstreamTerminalImageCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   map[string]string
		probe bool
		want  TerminalCapabilities
	}{
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:219
		{"defaults to hyperlinks: false for unknown terminals", nil, false, TerminalCapabilities{}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:284
		{"enables hyperlinks under tmux when the client forwards them", map[string]string{"TMUX": "/tmp/tmux-1000/default,1234,0", "TERM_PROGRAM": "ghostty"}, true, TerminalCapabilities{Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:292
		{"disables hyperlinks under tmux when the client does not forward them", map[string]string{"TMUX": "/tmp/tmux-1000/default,1234,0", "TERM_PROGRAM": "ghostty"}, false, TerminalCapabilities{}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:311
		{"forces hyperlinks: false when TERM starts with 'screen'", map[string]string{"TERM": "screen-256color"}, false, TerminalCapabilities{}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:319
		{"enables hyperlinks for Ghostty", map[string]string{"TERM_PROGRAM": "ghostty"}, false, TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:326
		{"does not disable Ghostty images solely because cmux is present", map[string]string{"TERM_PROGRAM": "ghostty", "CMUX_WORKSPACE_ID": "workspace"}, false, TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:334
		{"enables hyperlinks for Kitty", map[string]string{"KITTY_WINDOW_ID": "1"}, false, TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:341
		{"enables hyperlinks for WezTerm", map[string]string{"WEZTERM_PANE": "0"}, false, TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:348
		{"enables images and hyperlinks for Warp via TERM_PROGRAM", map[string]string{"TERM_PROGRAM": "WarpTerminal"}, false, TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:357
		{"enables images and hyperlinks for Warp via WARP_SESSION_ID", map[string]string{"WARP_SESSION_ID": "some-session-id"}, false, TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:366
		{"enables images and hyperlinks for Warp via WARP_TERMINAL_SESSION_UUID", map[string]string{"WARP_TERMINAL_SESSION_UUID": "d0e1a2e5-7ca7-44cd-9037-ac7222011161"}, false, TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:375
		{"disables images for Warp inside tmux", map[string]string{"TERM_PROGRAM": "WarpTerminal", "TMUX": "/tmp/tmux-1000/default,1234,0", "TERM": "tmux-256color"}, true, TerminalCapabilities{Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:390
		{"enables hyperlinks for iTerm2", map[string]string{"TERM_PROGRAM": "iterm.app"}, false, TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:397
		{"enables hyperlinks for VSCode", map[string]string{"TERM_PROGRAM": "vscode"}, false, TerminalCapabilities{TrueColor: true, Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:404
		{"enables Alacritty capabilities for Zed", map[string]string{"TERM_PROGRAM": "zed"}, false, TerminalCapabilities{TrueColor: true, Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:410
		{"enables truecolor and hyperlinks for Windows Terminal outside multiplexers", map[string]string{"WT_SESSION": "session", "TERM": "xterm-256color"}, false, TerminalCapabilities{TrueColor: true, Hyperlinks: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:419
		{"enables truecolor without hyperlinks for JetBrains terminal", map[string]string{"TERMINAL_EMULATOR": "JetBrains-JediTerm", "TERM": "xterm-256color"}, false, TerminalCapabilities{TrueColor: true}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:428
		{"does not inherit Windows Terminal truecolor through tmux", map[string]string{"WT_SESSION": "session", "TMUX": "/tmp/tmux-1000/default,1234,0", "TERM": "tmux-256color"}, false, TerminalCapabilities{}},
		// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:437
		{"trusts explicit truecolor hints through tmux", map[string]string{"COLORTERM": "truecolor", "TMUX": "/tmp/tmux-1000/default,1234,0", "TERM": "tmux-256color"}, false, TerminalCapabilities{TrueColor: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateCapabilityEnv(t)
			t.Setenv("CMUX_WORKSPACE_ID", "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if got := DetectCapabilities(func() bool { return tc.probe }); got != tc.want {
				t.Fatalf("caps=%#v want %#v", got, tc.want)
			}
		})
	}
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:227
	t.Run("applies environment overrides", func(t *testing.T) {
		isolateCapabilityEnv(t)
		setenvs(t, "PI_HYPERLINKS", "1", "PI_IMAGE_PROTOCOL", "kitty", "PI_TRUE_COLOR", "1")
		if got := DetectCapabilities(nil); got != (TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}) {
			t.Fatalf("enabled=%#v", got)
		}
		setenvs(t, "TERM_PROGRAM", "iterm.app", "PI_HYPERLINKS", "0", "PI_IMAGE_PROTOCOL", "none", "PI_TRUE_COLOR", "0")
		if got := DetectCapabilities(nil); got != (TerminalCapabilities{}) {
			t.Fatalf("disabled=%#v", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:240
	t.Run("preserves auto-detection for auto environment overrides", func(t *testing.T) {
		isolateCapabilityEnv(t)
		setenvs(t, "TERM_PROGRAM", "ghostty", "PI_HYPERLINKS", "auto", "PI_IMAGE_PROTOCOL", "auto", "PI_TRUE_COLOR", "auto")
		if got := DetectCapabilities(nil); got != (TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}) {
			t.Fatalf("auto=%#v", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:255
	t.Run("applies and clears programmatic overrides", func(t *testing.T) {
		isolateCapabilityEnv(t)
		setenvs(t, "PI_HYPERLINKS", "1", "PI_IMAGE_PROTOCOL", "kitty", "PI_TRUE_COLOR", "1")
		SetCapabilityOverrides(CapabilityOverrides{Images: new(ImageProtocol("")), TrueColor: new(false), Hyperlinks: new(false)})
		if got := GetCapabilities(); got != (TerminalCapabilities{}) {
			t.Fatalf("overrides=%#v", got)
		}
		SetCapabilityOverrides(CapabilityOverrides{})
		if got := GetCapabilities(); got != (TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}) {
			t.Fatalf("cleared=%#v", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:269
	t.Run("bypasses the tmux probe when hyperlinks are overridden", func(t *testing.T) {
		isolateCapabilityEnv(t)
		setenvs(t, "TMUX", "/tmp/tmux-1000/default,1234,0", "PI_HYPERLINKS", "1", "PI_IMAGE_PROTOCOL", "kitty")
		probed := false
		got := DetectCapabilities(func() bool { probed = true; return false })
		if probed || !got.Hyperlinks || got.Images != ImageProtocolKitty {
			t.Fatalf("probed=%v caps=%#v", probed, got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:300
	t.Run("checks tmux capability when TERM starts with 'tmux'", func(t *testing.T) {
		isolateCapabilityEnv(t)
		setenvs(t, "TERM", "tmux-256color", "TERM_PROGRAM", "iterm.app")
		yes := DetectCapabilities(func() bool { return true })
		no := DetectCapabilities(func() bool { return false })
		if !yes.Hyperlinks || yes.Images != "" || no.Hyperlinks {
			t.Fatalf("yes=%#v no=%#v", yes, no)
		}
	})
}

func upstreamImageState(t *testing.T, dimensions CellDimensions) {
	t.Helper()
	preserveCapabilityState(t)
	old := GetCellDimensions()
	t.Cleanup(func() { SetCellDimensions(old) })
	SetCapabilities(TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true})
	SetCellDimensions(dimensions)
}

func TestUpstreamTerminalImageEncoding(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:448
	t.Run("includes the decoded payload size in OSC 1337 metadata", func(t *testing.T) {
		if got := EncodeITerm2("AAAA", 2, "auto", "", true); got != "\x1b]1337;File=inline=1;size=3;width=2;height=auto:AAAA\x07" {
			t.Fatalf("OSC 1337=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:455
	t.Run("can request no terminal-side cursor movement", func(t *testing.T) {
		if got := EncodeKitty("AAAA", 2, 2, 0, false); !strings.HasPrefix(got, "\x1b_Ga=T,f=100,q=2,C=1,c=2,r=2;") {
			t.Fatalf("Kitty=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:460
	t.Run("suppresses Kitty replies for delete commands", func(t *testing.T) {
		if got := DeleteKittyImage(42); got != "\x1b_Ga=d,d=I,i=42,q=2\x1b\\" {
			t.Fatalf("delete=%q", got)
		}
		if got := DeleteAllKittyImages(); got != "\x1b_Ga=d,d=A,q=2\x1b\\" {
			t.Fatalf("delete images=%q", got)
		}
		if got := DeleteAllKittyPlacements(); got != "\x1b_Ga=d,d=a,q=2\x1b\\" {
			t.Fatalf("delete placements=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:466
	t.Run("preserves renderImage's default terminal-side cursor movement", func(t *testing.T) {
		upstreamImageState(t, CellDimensions{10, 10})
		result := RenderImage("AAAA", ImageDimensions{20, 20}, ImageRenderOptions{MaxWidthCells: 2})
		if result == nil || strings.Contains(result.Sequence, ",C=1") || result.Rows != 2 {
			t.Fatalf("rendered=%#v", result)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:480
	t.Run("can opt renderImage into no terminal-side cursor movement", func(t *testing.T) {
		upstreamImageState(t, CellDimensions{10, 10})
		result := RenderImage("AAAA", ImageDimensions{20, 20}, ImageRenderOptions{MaxWidthCells: 2, MoveCursor: new(false)})
		if result == nil || !strings.Contains(result.Sequence, ",C=1,") || result.Rows != 2 {
			t.Fatalf("rendered=%#v", result)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:494
	t.Run("registers metadata and crops a partially visible placement", func(t *testing.T) {
		upstreamImageState(t, CellDimensions{10, 10})
		result := RenderImage("AAAA", ImageDimensions{100, 100}, ImageRenderOptions{MaxWidthCells: 3, ImageID: 42, MoveCursor: new(false)})
		if result == nil {
			t.Fatal("missing image")
		}
		meta := GetKittyImageMetadata(result.Sequence)
		if meta == nil || *meta != (KittyImageMetadata{ImageID: 42, Columns: 3, Rows: 3, WidthPx: 100, HeightPx: 100}) {
			t.Fatalf("metadata=%#v", meta)
		}
		if got := CropKittyImageLine(result.Sequence, 2, 1); !strings.Contains(got, "y=66,h=34,r=1") {
			t.Fatalf("crop=%q", got)
		}
	})
	// .upstream/v1.0.1/packages/tui/test/terminal-image.test.ts:467
	t.Run("reads explicit placement rows without registered metadata", func(t *testing.T) {
		if rows, ok := GetKittyImagePlacementRows(EncodeKitty("AAAA", 2, 3, 0, false)); !ok || rows != 3 {
			t.Fatalf("rows = %d, %v", rows, ok)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:518
	t.Run("creates placement-only commands for uploaded and cropped images", func(t *testing.T) {
		RegisterKittyImageMetadata(KittyImageMetadata{ImageID: 42, Columns: 3, Rows: 3, WidthPx: 100, HeightPx: 100})
		transmission := EncodeKitty(strings.Repeat("A", 8192), 3, 3, 42, false)
		line := "left " + CropKittyImageLine(transmission, 2, 1) + " right"
		placement, ok := GetKittyImagePlacement(line)
		if !ok {
			t.Fatal("missing placement")
		}
		if placement.TransmissionBytes != len(line)-len("left ")-len(" right") || placement.EstimatedDecodedBytes != 100*100*4 {
			t.Fatalf("placement accounting=%#v", placement)
		}
		want := "\x1b_Ga=p,q=2,C=1,c=3,i=42,y=66,h=34,r=1\x1b\\"
		// .upstream/v1.0.1/packages/tui/test/terminal-image.test.ts:541: the crop's explicit rows.
		if rows, ok := GetKittyImagePlacementRows(line); !ok || rows != 1 || placement.Rows != 1 {
			t.Fatalf("placement rows = %d, %v; placement.Rows = %d", rows, ok, placement.Rows)
		}
		if placement.Sequence != want || placement.ReplacementLine != "left "+want+" right" || strings.Contains(placement.ReplacementLine, "AAAA") {
			t.Fatalf("placement=%#v", placement)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:536
	t.Run("honors maxHeightCells by reducing rendered width", func(t *testing.T) {
		upstreamImageState(t, CellDimensions{10, 10})
		result := RenderImage("AAAA", ImageDimensions{10, 100}, ImageRenderOptions{MaxWidthCells: 10, MaxHeightCells: 5})
		if result == nil || result.Rows != 5 || !strings.Contains(result.Sequence, ",c=1,r=5") {
			t.Fatalf("rendered=%#v", result)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:550
	t.Run("caps Image component height to a square pixel box by default", func(t *testing.T) {
		upstreamImageState(t, CellDimensions{10, 20})
		image := NewImage("AAAA", "image/png", ImageOptions{MaxWidthCells: 10}, &ImageDimensions{10, 100})
		image.Theme.FallbackColor = func(s string) string { return s }
		lines := image.Render(12)
		if len(lines) != 5 || !strings.Contains(lines[0], ",c=1,r=5") {
			t.Fatalf("lines=%q", lines)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:570
	t.Run("places image sequence on first line with empty padding rows", func(t *testing.T) {
		upstreamImageState(t, CellDimensions{10, 10})
		image := NewImage("AAAA", "image/png", ImageOptions{MaxWidthCells: 2}, &ImageDimensions{20, 20})
		image.Theme.FallbackColor = func(s string) string { return s }
		lines := image.Render(4)
		id := image.GetImageID()
		if len(lines) == 0 || !strings.HasPrefix(lines[0], "\x1b_G") || !strings.Contains(lines[0], ",C=1,") || !strings.Contains(lines[0], fmt.Sprintf(",i=%d", id)) || !strings.HasSuffix(lines[0], "\x1b\\") || !slices.Equal(lines[1:], []string{""}) {
			t.Fatalf("id=%d lines=%q", id, lines)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:595
	t.Run("truncates long image fallback lines to render width", func(t *testing.T) {
		preserveCapabilityState(t)
		SetCapabilities(TerminalCapabilities{})
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(home, "images", strings.Repeat("generated-image-with-a-very-long-absolute-path", 4)+".png")
		image := NewImage("AAAA", "image/png", ImageOptions{Filename: path}, &ImageDimensions{1280, 720})
		image.Theme.FallbackColor = func(s string) string { return "\x1b[33m" + s + "\x1b[0m" }
		lines := image.Render(40)
		if len(lines) != 1 || widthx.VisibleWidth(lines[0]) > 40 || !strings.Contains(lines[0], "...") || !strings.Contains(lines[0], "~") {
			t.Fatalf("fallback=%q", lines)
		}
	})
}

func TestUpstreamImageFallback(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:626
	t.Run("shortens home-prefixed absolute paths without hyperlinks", func(t *testing.T) {
		preserveCapabilityState(t)
		SetCapabilities(TerminalCapabilities{})
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		got := ImageFallback("image/png", &ImageDimensions{1280, 720}, filepath.Join(home, ".pi", "agent", "shot.png"))
		// Pi keeps the path's own separators after "~" (path.join yields
		// backslashes on win32).
		if want := "[Image: " + filepath.Join("~", ".pi", "agent", "shot.png") + " [image/png] 1280x720]"; got != want {
			t.Fatalf("fallback=%q, want %q", got, want)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:637
	t.Run("wraps shortened absolute paths in OSC 8 file links when hyperlinks are enabled", func(t *testing.T) {
		preserveCapabilityState(t)
		SetCapabilities(TerminalCapabilities{Hyperlinks: true})
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(home, ".pi", "agent", "shot.png")
		got := ImageFallback("image/png", &ImageDimensions{10, 10}, path)
		if !strings.Contains(got, "\x1b]8;;file://") || (!strings.Contains(got, strings.ReplaceAll(path, "\\", "/")) && !strings.Contains(got, path)) {
			t.Fatalf("link=%q", got)
		}
		if visible, want := widthx.StripTerminalSequences(got), "[Image: "+filepath.Join("~", ".pi", "agent", "shot.png")+" [image/png] 10x10]"; visible != want {
			t.Fatalf("visible=%q, want %q", visible, want)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:655
	t.Run("leaves bare basenames unchanged and does not hyperlink them", func(t *testing.T) {
		preserveCapabilityState(t)
		SetCapabilities(TerminalCapabilities{Hyperlinks: true})
		got := ImageFallback("image/png", &ImageDimensions{1, 1}, "clankolas.png")
		if got != "[Image: clankolas.png [image/png] 1x1]" || strings.Contains(got, "\x1b]8;") {
			t.Fatalf("fallback=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:666
	t.Run("omits filename segment when not provided", func(t *testing.T) {
		preserveCapabilityState(t)
		SetCapabilities(TerminalCapabilities{})
		if got := ImageFallback("image/png", &ImageDimensions{8, 6}, ""); got != "[Image: [image/png] 8x6]" {
			t.Fatalf("fallback=%q", got)
		}
	})
}

func TestUpstreamImageHyperlink(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:677
	t.Run("wraps text in OSC 8 open and close sequences", func(t *testing.T) {
		if got := Hyperlink("click me", "https://example.com"); got != "\x1b]8;;https://example.com\x1b\\click me\x1b]8;;\x1b\\" {
			t.Fatalf("link=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:682
	t.Run("preserves ANSI styling inside the hyperlink", func(t *testing.T) {
		styled := "\x1b[4m\x1b[34mclick me\x1b[0m"
		got := Hyperlink(styled, "https://example.com")
		if !strings.HasPrefix(got, "\x1b]8;;https://example.com\x1b\\") || !strings.Contains(got, styled) || !strings.HasSuffix(got, "\x1b]8;;\x1b\\") {
			t.Fatalf("link=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:690
	t.Run("works with empty text", func(t *testing.T) {
		if got := Hyperlink("", "https://example.com"); got != "\x1b]8;;https://example.com\x1b\\\x1b]8;;\x1b\\" {
			t.Fatalf("link=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:695
	t.Run("works with file:// URIs", func(t *testing.T) {
		got := Hyperlink("README.md", "file:///home/user/README.md")
		if !strings.Contains(got, "file:///home/user/README.md") || !strings.Contains(got, "README.md") {
			t.Fatalf("link=%q", got)
		}
	})
}
