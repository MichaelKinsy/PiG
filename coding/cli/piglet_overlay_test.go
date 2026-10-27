package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet"
)

// bakedPigletBinary is a Piglet Binary named name whose baked Piglet is bakedYAML; it accepts overlays when the baked
// Piglet's build block says so (D202).
func bakedPigletBinary(t *testing.T, name, bakedYAML string) pigletBinary {
	t.Helper()
	baked, err := piglet.ParseBytes([]byte(bakedYAML))
	if err != nil {
		t.Fatal(err)
	}
	return pigletBinary{
		Is:   true,
		Name: func() string { return name },
		OverlayBase: func() *piglet.OverlayBase {
			if baked.Build == nil || !baked.Build.AllowOverlays {
				return nil
			}
			return &piglet.OverlayBase{Piglet: baked, Digest: "sha256:" + strings.Repeat("ab", 32)}
		},
	}
}

func clearPigletEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PIG_PIGLET_PATH", "")
	t.Setenv("PIG_PIGLET_NAME", "")
	t.Setenv("PIG_HOME", t.TempDir())
}

func TestPigletBinaryWithoutAllowOverlaysRefusesEverySelector(t *testing.T) {
	clearPigletEnv(t)
	binary := bakedPigletBinary(t, "pig-tern", "name: pig-tern\nstrip:\n  tools: [edit]\n")
	if got := pigletBinaryOverlayRefusal(map[string]any{}, binary); got != "" {
		t.Fatalf("no selector refused: %q", got)
	}
	selectors := map[string]func(){
		"--piglet":        func() {},
		"PIG_PIGLET_PATH": func() { t.Setenv("PIG_PIGLET_PATH", "extra.yaml") },
		"PIG_PIGLET_NAME": func() { t.Setenv("PIG_PIGLET_NAME", "extra") },
	}
	for name, selected := range selectors {
		t.Run(name, func(t *testing.T) {
			selected()
			flags := map[string]any{}
			if name == "--piglet" {
				flags["piglet"] = "extra.yaml"
			}
			got := pigletBinaryOverlayRefusal(flags, binary)
			for _, want := range []string{"Piglet Binary (pig-tern)", "does not accept --piglet overlays", "build.allowOverlays: true", "stock pig"} {
				if !strings.Contains(got, want) {
					t.Fatalf("refusal %q lacks %q", got, want)
				}
			}
		})
	}
}

func TestPigletBinaryWithAllowOverlaysAcceptsEverySelector(t *testing.T) {
	clearPigletEnv(t)
	binary := bakedPigletBinary(t, "pig-tern", "name: pig-tern\nstrip:\n  tools: [edit]\nbuild:\n  allowOverlays: true\n")
	for _, flags := range []map[string]any{{"piglet": "extra.yaml"}, {}} {
		t.Setenv("PIG_PIGLET_NAME", "extra")
		if got := pigletBinaryOverlayRefusal(flags, binary); got != "" {
			t.Fatalf("an accepting Binary refused %v: %q", flags, got)
		}
	}
}

func TestResolvePigletLaysAnOverlayOnAnAcceptingBinary(t *testing.T) {
	clearPigletEnv(t)
	binary := bakedPigletBinary(t, "pig-tern", "name: pig-tern\nextensions:\n  - name: core\nstrip:\n  tools: [edit]\nbuild:\n  allowOverlays: true\n")
	dir := t.TempDir()
	overlay := filepath.Join(dir, "extra.yaml")
	if err := os.WriteFile(overlay, []byte("name: extra\nextensions:\n  - name: extra\n    origins: [local:./extra]\nstrip:\n  tools: [grep]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func() (*piglet.Piglet, bool){
		"flag": func() (*piglet.Piglet, bool) { return resolvePigletOn(map[string]any{"piglet": overlay}, nil, binary) },
		"path": func() (*piglet.Piglet, bool) {
			t.Setenv("PIG_PIGLET_PATH", overlay)
			return resolvePigletOn(map[string]any{}, nil, binary)
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, baked := run()
			if p == nil || !baked || p.Name != "pig-tern" {
				t.Fatalf("overlay = %#v baked=%t, want the Binary's Piglet with the overlay laid over it", p, baked)
			}
			if len(p.Extensions) != 2 || p.Extensions[0].Name != "core" || p.Extensions[1].Name != "extra" || len(p.Strip.Tools) != 2 {
				t.Fatalf("effective Piglet = %#v", p)
			}
		})
	}
}

func TestResolvePigletNeverFallsBackToTheBinaryWhenAnOverlayDoesNotResolve(t *testing.T) {
	clearPigletEnv(t)
	binary := bakedPigletBinary(t, "pig-tern", "name: pig-tern\nextensions:\n  - name: core\nbuild:\n  allowOverlays: true\n")
	dir := t.TempDir()
	conflict := filepath.Join(dir, "conflict.yaml")
	if err := os.WriteFile(conflict, []byte("name: conflict\nextensions:\n  - name: core\n    origins: [local:./other]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, _ := resolvePigletOn(map[string]any{"piglet": conflict}, nil, binary); p != nil {
		t.Fatalf("a refused overlay resolved to %#v", p)
	}
	if p, _ := resolvePigletOn(map[string]any{"piglet": filepath.Join(dir, "missing.yaml")}, nil, binary); p != nil {
		t.Fatalf("a missing overlay resolved to %#v", p)
	}
	t.Setenv("PIG_PIGLET_NAME", "no-such-piglet")
	if p, _ := resolvePigletOn(map[string]any{}, nil, binary); p != nil {
		t.Fatalf("an unresolvable PIG_PIGLET_NAME fell back to %#v", p)
	}
}
