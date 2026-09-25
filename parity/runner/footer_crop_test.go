//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCWDFooterCropWidthBoundary(t *testing.T) {
	// The pwd row is truncateToWidth(pwd, width, "...") in footer.ts.
	// Full-width rows fit exactly; ANSI bytes and UTF-8 bytes are not cells.
	for _, tt := range []struct {
		name   string
		cwd    string
		branch string
		cells  int
	}{
		{"ascii", "/tmp/parity-snap-cwd-0123456789", "", len("/tmp/parity-snap-cwd-0123456789")},
		{"branch", "/tmp/parity-snap-cwd-0123456789", "main", len("/tmp/parity-snap-cwd-0123456789 (main)")},
		{"wide", "/界/parity-snap-cwd-0123456789", "", 2 + len("//parity-snap-cwd-0123456789")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := TmuxDriverConfig{CaptureStart: "parity-snap-cwd-", GitBranch: tt.branch, Width: tt.cells}
			if err := validateCWDFooterCrop(tt.cwd, cfg); err != nil {
				t.Fatalf("exact fit: %v", err)
			}
			cfg.Width--
			if err := validateCWDFooterCrop(tt.cwd, cfg); err == nil {
				t.Fatal("accepted a truncated cwd row")
			}
			cfg.CaptureStart = "$0.000"
			if err := validateCWDFooterCrop(tt.cwd, cfg); err != nil {
				t.Fatalf("non-cwd crops must not constrain TMPDIR: %v", err)
			}
		})
	}
}

func TestTerminalDriversRejectUnrenderableCWDCropBeforeLaunch(t *testing.T) {
	installFakeHT(t)
	root := filepath.Join(t.TempDir(), strings.Repeat("long-root-", 12))
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "short-link")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	for _, temp := range []string{root, alias} {
		t.Run(filepath.Base(temp), func(t *testing.T) {
			t.Setenv("TMPDIR", temp)
			for _, name := range []string{"interactive-tmux", "headless-terminal"} {
				for _, label := range []string{"pig", "pi"} {
					t.Run(name+"/"+label, func(t *testing.T) {
						sc := &Scenario{Name: "reject-crop", SourcePath: filepath.Join(defaultCWDFixture(), "scenario.toml")}
						sc.Tmux = TmuxDriverConfig{Width: 100, Height: 35, CaptureStart: "parity-snap-cwd-", CaptureStartLast: true}
						bin := BinaryRef{Label: label, Path: filepath.Join(root, "must-not-launch")}
						got := DriverRegistry[name].Run(t.Context(), t, bin, sc)
						if got.Err == nil || !strings.Contains(got.Err.Error(), "footer cwd crop") || !strings.Contains(got.Err.Error(), "TMPDIR") || !strings.Contains(got.Err.Error(), "100 columns") {
							t.Fatalf("must reject an unrenderable crop before launch, got %v", got.Err)
						}
					})
				}
			}
		})
	}
}
