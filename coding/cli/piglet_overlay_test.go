package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
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

const overlayBinaryYAML = "name: pig-tern\nsystemPrompt:\n  text: baked prompt\nextensions:\n  - name: core\n  - name: other\nstrip:\n  tools: [edit]\nbuild:\n  allowOverlays: true\n"

func writeOverlay(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overlay.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolvePigletOverlayReplacesPromptAndRemovesMembers(t *testing.T) {
	clearPigletEnv(t)
	binary := bakedPigletBinary(t, "pig-tern", overlayBinaryYAML)
	overlay := writeOverlay(t, "name: lite\nsystemPrompt:\n  text: overlay prompt\nextends:\n  remove:\n    extensions: [core]\n")
	p, baked := resolvePigletOn(map[string]any{"piglet": overlay}, nil, binary)
	if p == nil || !baked {
		t.Fatalf("overlay = %v, baked=%t", p, baked)
	}
	if p.SystemPrompt == nil || p.SystemPrompt.Text != "overlay prompt" {
		t.Fatalf("system prompt = %#v", p.SystemPrompt)
	}
	if len(p.Extensions) != 1 || p.Extensions[0].Name != "other" {
		t.Fatalf("extensions = %#v", p.Extensions)
	}
	if name, ok := p.OverlayOf(); !ok || name != "pig-tern" {
		t.Fatalf("OverlayOf = %q %v", name, ok)
	}
}

func TestOverlayRemovesFrontendWhenTheBinaryHasOne(t *testing.T) {
	baked, err := piglet.ParseBytes([]byte(overlayBinaryYAML))
	if err != nil {
		t.Fatal(err)
	}
	baked.Slots = &piglet.Slots{Frontend: &piglet.SlotMember{Member: "compiled-in"}}
	binary := pigletBinary{Is: true, Name: func() string { return "pig-tern" }, OverlayBase: func() *piglet.OverlayBase {
		return &piglet.OverlayBase{Piglet: baked, Digest: "sha256:" + strings.Repeat("cd", 32)}
	}}
	clearPigletEnv(t)
	removed, _ := resolvePigletOn(map[string]any{"piglet": writeOverlay(t, "name: lite\nextends:\n  remove:\n    slots: [frontend]\n")}, nil, binary)
	if removed == nil || !overlayRemovedFrontend(removed) {
		t.Fatalf("removed = %#v", removed)
	}
	kept, _ := resolvePigletOn(map[string]any{"piglet": writeOverlay(t, "name: lite\nsystemPrompt:\n  text: p\n")}, nil, binary)
	if kept == nil || overlayRemovedFrontend(kept) {
		t.Fatalf("kept = %#v", kept)
	}
	if overlayRemovedFrontend(nil) || overlayRemovedFrontend(baked) {
		t.Fatal("a Binary run with no overlay keeps its frontend")
	}
}

func TestOverlayDropsRemovedCompiledInMembers(t *testing.T) {
	t.Cleanup(func() { droppedBinaryMembers = nil })
	droppedBinaryMembers = map[string]bool{"core": true}
	merged := withFusedExtensionConfigs(
		[]subprocess.ExtConfig{{Name: "core", Enabled: true}, {Name: "other", Enabled: true}}, nil)
	if len(merged) != 1 || merged[0].Name != "other" {
		t.Fatalf("fused configs = %#v", merged)
	}
	cells := withoutDroppedMembers([]subprocess.EmbeddedCell{
		{Key: "a", Extensions: []subprocess.EmbeddedExtension{{Name: "core"}}},
		{Key: "b", Extensions: []subprocess.EmbeddedExtension{{Name: "core"}, {Name: "other"}}},
	})
	if len(cells) != 1 || cells[0].Key != "b" || len(cells[0].Extensions) != 1 || cells[0].Extensions[0].Name != "other" {
		t.Fatalf("cells = %#v", cells)
	}
	droppedBinaryMembers = nil
	if got := withoutDroppedMembers([]subprocess.EmbeddedCell{{Key: "a", Extensions: []subprocess.EmbeddedExtension{{Name: "core"}}}}); len(got) != 1 {
		t.Fatalf("a run with no overlay drops nothing: %#v", got)
	}
}

func TestSystemPromptOnAPigletBinary(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		baked, overlays        bool
		prompt                 string
		wantRefused, wantInMsg string
	}{
		{"stock pig", false, false, "p", "", ""},
		{"Binary without allowOverlays refuses", true, false, "p", "yes", "--append-system-prompt"},
		{"Binary without allowOverlays and no flag", true, false, "", "", ""},
		{"Binary with allowOverlays accepts", true, true, "p", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pigletBinarySystemPromptRefusal(tc.baked, tc.overlays, tc.prompt)
			if (got != "") != (tc.wantRefused != "") || !strings.Contains(got, tc.wantInMsg) {
				t.Fatalf("refusal = %q", got)
			}
		})
	}
}

func TestAmbientScopesOfAPigletBinary(t *testing.T) {
	p, err := piglet.ParseBytes([]byte("name: pig-tern\ndiscovery:\n  extensions: [user]\n  skills: [user, workspace]\n"))
	if err != nil {
		t.Fatal(err)
	}
	closed := &cliRuntimeBuilder{activePiglet: p, activePigletBaked: true}
	ext, skills := closed.ambientScopes(true)
	if len(*ext) != 0 || len(*skills) != 0 {
		t.Fatalf("a Binary without allowOverlays discovers %v %v", *ext, *skills)
	}
	open := &cliRuntimeBuilder{activePiglet: p, activePigletBaked: true, activePigletOpen: true}
	ext, skills = open.ambientScopes(true)
	if len(*ext) != 1 || (*ext)[0] != "user" || len(*skills) != 2 {
		t.Fatalf("a Binary with allowOverlays discovers %v %v, want its Piglet's discovery", *ext, *skills)
	}
	ext, skills = open.ambientScopes(false)
	if len(*ext) != 1 || len(*skills) != 1 || (*skills)[0] != "user" {
		t.Fatalf("an untrusted project keeps the trusted scopes only: %v %v", *ext, *skills)
	}
}
