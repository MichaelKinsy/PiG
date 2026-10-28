package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

// overlayBaseYAML is what a Piglet Binary that accepts overlays bakes: no origins, inlined prompt and skill, the strip
// list, and the one build setting Bake keeps.
const overlayBaseYAML = `name: pig-base
description: the Binary's own
tools: [read, bash, grep]
systemPrompt:
  text: base prompt
extensions:
  - name: core
    tools: [core_tool]
skills:
  - name: guide
    content: guide body
strip:
  tools: [edit]
  extensions: [mcp]
build:
  allowOverlays: true
`

func overlayBase(t *testing.T) *OverlayBase {
	t.Helper()
	base, err := ParseBytes([]byte(overlayBaseYAML))
	if err != nil {
		t.Fatal(err)
	}
	return &OverlayBase{Piglet: base, Digest: "sha256:" + strings.Repeat("ab", 32)}
}

func resolveOverlay(t *testing.T, dir, body string) (*EffectiveResolution, error) {
	t.Helper()
	path := writePigletSource(t, dir, "overlay", body)
	return ResolveEffectiveWithOptions(path, ResolveOptions{Base: overlayBase(t)})
}

func TestOverlayAddsToTheBinaryPigletAndKeepsItsIdentity(t *testing.T) {
	dir := t.TempDir()
	resolved, err := resolveOverlay(t, dir, `name: extra
description: the overlay's own
model:
  provider: test-faux
  name: faux-1
tools: [read]
packages:
  helpers: npm:@example/helpers@1.0.0
extensions:
  - name: extra
    origins: [local:./extra]
    tools: [extra_tool]
skills:
  - name: extra-skill
    origins: [local:./skills/extra]
strip:
  tools: [grep]
  commands: [/share]
`)
	if err != nil {
		t.Fatal(err)
	}
	p := resolved.Piglet
	if p.Name != "pig-base" {
		t.Fatalf("effective name = %q, want the Binary's name", p.Name)
	}
	if p.Description != "the overlay's own" {
		t.Fatalf("description = %q, want the overlay's (extends rule)", p.Description)
	}
	if names := extensionNames(p.Extensions); len(names) != 2 || names[0] != "core" || names[1] != "extra" {
		t.Fatalf("extensions = %v, want the Binary's then the overlay's", names)
	}
	if core := p.Extensions[0]; len(core.Origins) != 0 || core.Tools == nil || (*core.Tools)[0] != "core_tool" {
		t.Fatalf("baked extension changed: %#v", core)
	}
	// Stock --piglet resolution (and so the overlay's) anchors origins at the canonical overlay path: the real directory,
	// not the symlinked /var or 8.3 short-named temp directory t.TempDir returns on macOS and Windows.
	if extra := p.Extensions[1]; len(extra.Origins) != 1 || extra.Origins[0] != "local:"+filepath.Join(evalDir(t, dir), "extra") {
		t.Fatalf("overlay extension origin not anchored at the overlay file: %#v", extra)
	}
	if got := skillNames(p.Skills); len(got) != 2 || got[0] != "guide" || p.Skills[0].Content != "guide body" {
		t.Fatalf("skills = %v", got)
	}
	if p.SystemPrompt == nil || p.SystemPrompt.Text != "base prompt" {
		t.Fatalf("system prompt = %#v", p.SystemPrompt)
	}
	if p.BuiltinTools == nil || len(*p.BuiltinTools) != 1 || (*p.BuiltinTools)[0] != "read" {
		t.Fatalf("tools = %v, want the overlay's narrower list", p.BuiltinTools)
	}
	if p.Model == nil || p.Model.Name != "faux-1" || p.Packages["helpers"] == "" {
		t.Fatalf("model/packages not merged: %#v %#v", p.Model, p.Packages)
	}
	strip := p.Strip
	if len(strip.Tools) != 2 || strip.Tools[0] != "edit" || strip.Tools[1] != "grep" || len(strip.Commands) != 1 || len(strip.Extensions) != 1 || strip.Extensions[0] != "mcp" {
		t.Fatalf("strip = %#v, want the union", strip)
	}
	if p.Build != nil || p.Extends != nil {
		t.Fatalf("an effective Piglet carries no derivation metadata: build=%#v extends=%#v", p.Build, p.Extends)
	}
	if len(resolved.Lineage) != 2 || resolved.Lineage[0].Source != "binary:pig-base" || resolved.Lineage[0].Digest != overlayBase(t).Digest || resolved.Lineage[1].Source != "local:overlay.yaml" {
		t.Fatalf("lineage = %#v", resolved.Lineage)
	}
	if got := formatActivePiglet(p); !strings.Contains(got, "Piglet: pig-base") || !strings.Contains(got, "Overlay of: pig-base (Piglet Binary)") {
		t.Fatalf("/piglet = %q", got)
	}
}

func TestOverlayResolutionLeavesTheBakedPigletUnchanged(t *testing.T) {
	base := overlayBase(t)
	path := writePigletSource(t, t.TempDir(), "overlay", "name: extra\nextensions:\n  - name: extra\n    origins: [local:./extra]\nstrip:\n  tools: [grep]\n")
	for range 2 {
		if _, err := ResolveEffectiveWithOptions(path, ResolveOptions{Base: base}); err != nil {
			t.Fatal(err)
		}
	}
	if len(base.Piglet.Extensions) != 1 || len(base.Piglet.Strip.Tools) != 1 || base.Piglet.Name != "pig-base" {
		t.Fatalf("resolution modified the baked Piglet: %#v", base.Piglet)
	}
}

func TestOverlayChainResolvesOnTheBinary(t *testing.T) {
	dir := t.TempDir()
	writePigletSource(t, dir, "shared", "name: shared\nextensions:\n  - name: shared\n    origins: [local:./shared]\n")
	resolved, err := resolveOverlay(t, dir, "name: extra\nextends:\n  source: local:./shared.yaml\nextensions:\n  - name: extra\n    origins: [local:./extra]\n")
	if err != nil {
		t.Fatal(err)
	}
	if names := extensionNames(resolved.Piglet.Extensions); len(names) != 3 || names[0] != "core" || names[1] != "shared" || names[2] != "extra" {
		t.Fatalf("extensions = %v", names)
	}
	if len(resolved.Lineage) != 3 || resolved.Lineage[0].Source != "binary:pig-base" || resolved.Lineage[1].Source != "local:shared.yaml" || resolved.Lineage[2].Source != "local:overlay.yaml" {
		t.Fatalf("lineage = %#v", resolved.Lineage)
	}
}

// overlayBaseWithFrontend is overlayBase for a Binary with a frontend member compiled in, as binarypiglet.OverlayBase names it.
func overlayBaseWithFrontend(t *testing.T) *OverlayBase {
	t.Helper()
	base := overlayBase(t)
	base.Piglet.Slots = &Slots{Frontend: &SlotMember{Member: "compiled-in"}}
	return base
}

func resolveOverlayFiles(t *testing.T, base *OverlayBase, files map[string]string) (*EffectiveResolution, string, error) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		writePigletSource(t, dir, name, body)
	}
	path := filepath.Join(dir, "overlay.yaml")
	resolved, err := ResolveEffectiveWithOptions(path, ResolveOptions{Base: base})
	return resolved, path, err
}

// The overlay refuses only what is compiled into the Binary. Each message names the field and the Binary, and says why.
func TestOverlayRefusesWhatChangesTheBinary(t *testing.T) {
	const binary = `Piglet Binary "pig-base"`
	for _, tc := range []struct {
		name     string
		frontend bool
		files    map[string]string
		want     []string
	}{
		{"sets a frontend on a Binary built without one", false, map[string]string{"overlay": "name: x\nslots:\n  frontend:\n    member: ./tern\n"},
			[]string{"slots.frontend", binary, "built without a frontend member", "compiled into the Binary"}},
		{"replaces the frontend the Binary compiled in", true, map[string]string{"overlay": "name: x\nslots:\n  frontend:\n    member: ./tern\n"},
			[]string{"slots.frontend", binary, "compiled in", "cannot replace it", "extends.remove.slots"}},
		{"replaces a fused member with another under its name", false, map[string]string{"overlay": "name: x\nextends:\n  allowWiden: true\nextensions:\n  - name: core\n    origins: [local:./other]\n"},
			[]string{"extensions[core]", binary, "compiled in (fused or embedded)", "cannot replace it with another one under the same name"}},
		{"removes a fused member and adds another under its name", false, map[string]string{"overlay": "name: x\nextends:\n  allowWiden: true\n  remove:\n    extensions: [core]\nextensions:\n  - name: core\n    origins: [local:./other]\n"},
			[]string{"extensions[core]", binary, "cannot replace it"}},
		{"replaces a fused member through its chain", false, map[string]string{
			"mid":     "name: mid\nextends:\n  allowWiden: true\nextensions:\n  - name: core\n    origins: [local:./other]\n",
			"overlay": "name: x\nextends:\n  source: local:./mid.yaml\n"},
			[]string{"extensions[core]", binary, "cannot replace it"}},
		{"re-enables an extension a build tag compiled out", false, map[string]string{"overlay": "name: x\nextends:\n  allowWiden: true\n  remove:\n    strip:\n      extensions: [mcp]\n"},
			[]string{`strip.extensions: "mcp"`, binary, "compiled out", "pig_strip_mcp", "cannot re-enable it"}},
		{"re-enables a compiled-out extension through its chain", false, map[string]string{
			"mid":     "name: mid\n",
			"overlay": "name: x\nextends:\n  source: local:./mid.yaml\n  allowWiden: true\n  remove:\n    strip:\n      extensions: [mcp]\n"},
			[]string{`strip.extensions: "mcp"`, binary, "cannot re-enable it"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := overlayBase(t)
			if tc.frontend {
				base = overlayBaseWithFrontend(t)
			}
			resolved, path, err := resolveOverlayFiles(t, base, tc.files)
			if err == nil {
				t.Fatalf("overlay was accepted: %#v", resolved.Piglet)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q lacks %q", err, want)
				}
			}
			if !strings.Contains(err.Error(), "overlay "+path) && !strings.Contains(err.Error(), "overlay "+filepath.Join(evalDir(t, filepath.Dir(path)), "overlay.yaml")) {
				t.Fatalf("error %q does not name the overlay file", err)
			}
		})
	}
}

// An overlay merges onto the Binary as an extends child merges onto its base, so what extends allows an overlay does too.
func TestOverlayMergesAsAnExtendsChild(t *testing.T) {
	for _, tc := range []struct {
		name     string
		frontend bool
		files    map[string]string
		check    func(t *testing.T, p *Piglet)
	}{
		{"replaces the system prompt", false, map[string]string{"overlay": "name: x\nsystemPrompt:\n  text: overlay prompt\n"}, func(t *testing.T, p *Piglet) {
			if p.SystemPrompt == nil || p.SystemPrompt.Text != "overlay prompt" {
				t.Fatalf("system prompt = %#v", p.SystemPrompt)
			}
		}},
		{"removes a baked extension", false, map[string]string{"overlay": "name: x\nextends:\n  remove:\n    extensions: [core]\n"}, func(t *testing.T, p *Piglet) {
			if len(p.Extensions) != 0 {
				t.Fatalf("extensions = %v", extensionNames(p.Extensions))
			}
		}},
		{"removes a baked extension through its chain", false, map[string]string{
			"mid":     "name: mid\n",
			"overlay": "name: x\nextends:\n  source: local:./mid.yaml\n  remove:\n    extensions: [core]\n"}, func(t *testing.T, p *Piglet) {
			if len(p.Extensions) != 0 {
				t.Fatalf("extensions = %v", extensionNames(p.Extensions))
			}
		}},
		{"removes a baked skill", false, map[string]string{"overlay": "name: x\nextends:\n  remove:\n    skills: [guide]\n"}, func(t *testing.T, p *Piglet) {
			if len(p.Skills) != 0 {
				t.Fatalf("skills = %v", skillNames(p.Skills))
			}
		}},
		{"replaces a baked skill", false, map[string]string{"overlay": "name: x\nskills:\n  - name: guide\n    content: other\n"}, func(t *testing.T, p *Piglet) {
			if len(p.Skills) != 1 || p.Skills[0].Content != "other" {
				t.Fatalf("skills = %#v", p.Skills)
			}
		}},
		{"removes the frontend slot", true, map[string]string{"overlay": "name: x\nextends:\n  remove:\n    slots: [frontend]\n"}, func(t *testing.T, p *Piglet) {
			if p.HasFrontend() {
				t.Fatalf("slots = %#v", p.Slots)
			}
		}},
		{"keeps the frontend it does not remove", true, map[string]string{"overlay": "name: x\nsystemPrompt:\n  text: other\n"}, func(t *testing.T, p *Piglet) {
			if !p.HasFrontend() {
				t.Fatal("the compiled-in frontend is gone")
			}
		}},
		{"widens the built-in tools", false, map[string]string{"overlay": "name: x\nextends:\n  allowWiden: true\ntools: [read, write, edit]\n"}, func(t *testing.T, p *Piglet) {
			if p.BuiltinTools == nil || len(*p.BuiltinTools) != 3 {
				t.Fatalf("tools = %v", p.BuiltinTools)
			}
		}},
		{"widens ambient discovery", false, map[string]string{"overlay": "name: x\nextends:\n  allowWiden: true\ndiscovery:\n  extensions: [workspace]\n  skills: [user]\n"}, func(t *testing.T, p *Piglet) {
			if p.Discovery == nil || len(p.Discovery.Extensions) != 1 || len(p.Discovery.Skills) != 1 {
				t.Fatalf("discovery = %#v", p.Discovery)
			}
		}},
		{"widens a baked extension's tools without replacing it", false, map[string]string{"overlay": "name: x\nextends:\n  allowWiden: true\nextensions:\n  - name: core\n    tools: [core_tool, more]\n"}, func(t *testing.T, p *Piglet) {
			if len(p.Extensions) != 1 || p.Extensions[0].Tools == nil || len(*p.Extensions[0].Tools) != 2 || len(p.Extensions[0].Origins) != 0 {
				t.Fatalf("extensions = %#v", p.Extensions)
			}
		}},
		{"re-enables a runtime strip entry", false, map[string]string{"overlay": "name: x\nextends:\n  allowWiden: true\n  remove:\n    strip:\n      tools: [edit]\n"}, func(t *testing.T, p *Piglet) {
			if len(p.Strip.Tools) != 0 || len(p.Strip.Extensions) != 1 {
				t.Fatalf("strip = %#v", p.Strip)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := overlayBase(t)
			if tc.frontend {
				base = overlayBaseWithFrontend(t)
			}
			resolved, _, err := resolveOverlayFiles(t, base, tc.files)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Piglet.Name != "pig-base" {
				t.Fatalf("name = %q", resolved.Piglet.Name)
			}
			tc.check(t, resolved.Piglet)
		})
	}
}

// What extends refuses without allowWiden an overlay refuses too, with the extends message.
func TestOverlayWideningNeedsAllowWidenAsExtendsDoes(t *testing.T) {
	for name, body := range map[string]string{
		"tools":     "name: x\ntools: [read, write]\n",
		"discovery": "name: x\ndiscovery:\n  extensions: [workspace]\n",
		"strip":     "name: x\nextends:\n  remove:\n    strip:\n      tools: [edit]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolveOverlayFiles(t, overlayBase(t), map[string]string{"overlay": body})
			if err == nil || !strings.Contains(err.Error(), "requires extends.allowWiden: true") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestOverlayRemovesOnlyWhatTheBinaryHas(t *testing.T) {
	_, _, err := resolveOverlayFiles(t, overlayBase(t), map[string]string{"overlay": "name: x\nextends:\n  remove:\n    slots: [frontend]\n"})
	if err == nil || !strings.Contains(err.Error(), "remove.slots") || !strings.Contains(err.Error(), `Piglet Binary "pig-base"`) {
		t.Fatalf("err = %v", err)
	}
}

// An extends block without a source extends the Piglet Binary; stock pig has none, so it refuses the Piglet by name.
func TestExtendsWithoutSourceNeedsAPigletBinary(t *testing.T) {
	path := writePigletSource(t, t.TempDir(), "overlay", "name: x\nextends:\n  remove:\n    extensions: [core]\n")
	if _, err := ResolveEffective(path); err == nil || !strings.Contains(err.Error(), "extends.source is required unless a Piglet Binary") {
		t.Fatalf("err = %v", err)
	}
	if _, err := ParseBytes([]byte("name: x\nextends:\n  version: '>=1'\n")); err == nil || !strings.Contains(err.Error(), "extends.version requires extends.source") {
		t.Fatalf("err = %v", err)
	}
}

func TestOverlayRecordsItsPigletBinary(t *testing.T) {
	resolved, _, err := resolveOverlayFiles(t, overlayBase(t), map[string]string{"overlay": "name: x\n"})
	if err != nil {
		t.Fatal(err)
	}
	if binary, ok := resolved.Piglet.OverlayOf(); !ok || binary != "pig-base" {
		t.Fatalf("OverlayOf = %q, %v", binary, ok)
	}
	if _, ok := overlayBase(t).Piglet.OverlayOf(); ok {
		t.Fatal("a baked Piglet is no overlay")
	}
}

func evalDir(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// checkOverlay judges the final Piglet: a compiled-out API or feature cannot come back, a runtime one can.
func TestCheckOverlayStripDispositions(t *testing.T) {
	apis := knownStripIDs("apis")
	features := knownStripIDs("features")
	if len(apis) == 0 || len(features) == 0 {
		t.Skip("no strip table")
	}
	var runtimeFeature string
	for _, id := range features {
		if _, binary := stripBuildTag(StripKindFeature, id); !binary {
			runtimeFeature = id
			break
		}
	}
	var binaryFeature string
	for _, id := range features {
		if _, binary := stripBuildTag(StripKindFeature, id); binary {
			binaryFeature = id
			break
		}
	}
	base := &Piglet{Name: "pig-base", Strip: &StripSpec{APIs: []string{apis[0]}, Features: []string{binaryFeature, runtimeFeature}}}
	reEnable := func(change func(*StripSpec)) *Piglet {
		p := Clone(base)
		change(p.Strip)
		return p
	}
	if err := checkOverlay(base, reEnable(func(s *StripSpec) { s.APIs = nil })); err == nil || !strings.Contains(err.Error(), "strip.apis: "+`"`+apis[0]+`"`) {
		t.Fatalf("a compiled-out API came back: %v", err)
	}
	if err := checkOverlay(base, reEnable(func(s *StripSpec) { s.Features = []string{runtimeFeature} })); err == nil || !strings.Contains(err.Error(), binaryFeature) {
		t.Fatalf("a compiled-out feature came back: %v", err)
	}
	if err := checkOverlay(base, reEnable(func(s *StripSpec) { s.Features = []string{binaryFeature} })); err != nil {
		t.Fatalf("a runtime-only feature may come back: %v", err)
	}
}

func TestOverlayOnABaseWithoutSlotsOrPromptStaysEmptyThere(t *testing.T) {
	dir := t.TempDir()
	base, err := ParseBytes([]byte("name: bare\nstrip:\n  tools: [edit]\nbuild:\n  allowOverlays: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	path := writePigletSource(t, dir, "overlay", "name: x\nextensions:\n  - name: extra\n    origins: [local:./extra]\n")
	resolved, err := ResolveEffectiveWithOptions(path, ResolveOptions{Base: &OverlayBase{Piglet: base, Digest: "sha256:" + strings.Repeat("ef", 32)}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Piglet.SystemPrompt != nil || resolved.Piglet.Slots != nil || resolved.Piglet.Name != "bare" {
		t.Fatalf("effective = %#v", resolved.Piglet)
	}
}

func TestBuildAllowOverlaysIsStrictDefaultOffAndNotInherited(t *testing.T) {
	parsed, err := ParseBytes([]byte("name: p\n"))
	if err != nil || parsed.Build != nil {
		t.Fatalf("default = %#v, %v", parsed.Build, err)
	}
	parsed, err = ParseBytes([]byte("name: p\nbuild:\n  allowOverlays: true\n"))
	if err != nil || parsed.Build == nil || !parsed.Build.AllowOverlays {
		t.Fatalf("allowOverlays: true = %#v, %v", parsed.Build, err)
	}
	if _, err := ParseBytes([]byte("name: p\nbuild:\n  allowOverlays: yes please\n")); err == nil {
		t.Fatal("a non-boolean allowOverlays parsed")
	}
	dir := t.TempDir()
	writePigletSource(t, dir, "base", "name: base\nbuild:\n  allowOverlays: true\n  outputName: pig-base\n")
	child := writePigletSource(t, dir, "child", "name: child\nextends:\n  source: local:./base.yaml\n")
	resolved, err := ResolveEffective(child)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Piglet.Build != nil {
		t.Fatalf("a child inherited the base's build block: %#v", resolved.Piglet.Build)
	}
	authored, err := Parse(child)
	if err != nil || authored.Build != nil {
		t.Fatalf("child's authored build = %#v, %v", authored.Build, err)
	}
}

func TestBakeKeepsOnlyAllowOverlaysFromBuild(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		build string
		want  string
	}{
		{"build:\n  allowOverlays: true\n  outputName: pig-x\n  targets: [linux/amd64]\n", "build:\n    allowOverlays: true\n"},
		{"build:\n  allowOverlays: false\n  outputName: pig-x\n", ""},
		{"build:\n  outputName: pig-x\n", ""},
		{"", ""},
	} {
		p, err := ParseBytes([]byte("name: x\nmodel:\n  provider: test-faux\n  name: faux-1\n" + tc.build))
		if err != nil {
			t.Fatal(err)
		}
		baked, err := Bake(p, dir)
		if err != nil {
			t.Fatal(err)
		}
		got := string(baked)
		if tc.want == "" {
			if strings.Contains(got, "build:") || strings.Contains(got, "allowOverlays") {
				t.Fatalf("Bake wrote build for %q:\n%s", tc.build, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) || strings.Contains(got, "outputName") || strings.Contains(got, "targets") {
			t.Fatalf("Bake of %q:\n%s", tc.build, got)
		}
		reparsed, err := ParseBytes(baked)
		if err != nil || reparsed.Build == nil || !reparsed.Build.AllowOverlays {
			t.Fatalf("baked Piglet does not parse back with the setting: %#v %v", reparsed, err)
		}
	}
}

func TestShowPrintsAllowOverlays(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.yaml"), []byte("name: x\nbuild:\n  allowOverlays: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if code := cmdShow([]string{filepath.Join(dir, "x.yaml")}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "Build defaults:\n  allowOverlays: true") {
		t.Fatalf("show: exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
}

// A Binary's signed manifest names the digest of its baked Piglet, which carries build.allowOverlays. `pig piglet verify
// --source` recomputes that digest from the source as the build does, so a source that sets the setting still matches and
// one that changed it no longer does.
func TestProvenanceCoversAllowOverlays(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	source := filepath.Join(t.TempDir(), "overlayable.yaml")
	if err := os.WriteFile(source, []byte("name: overlayable\nmodel:\n  provider: test-faux\n  name: faux-1\nbuild:\n  allowOverlays: true\n  outputName: pig-overlayable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The build reattaches the authored build block to the effective Piglet and bakes it.
	resolution, err := ResolveEffective(source)
	if err != nil {
		t.Fatal(err)
	}
	authored, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	resolution.Piglet.Build = authored.Build
	baked, err := Bake(resolution.Piglet, filepath.Dir(source))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(baked), "allowOverlays: true") {
		t.Fatalf("baked Piglet lacks the setting:\n%s", baked)
	}
	key := filepath.Join(t.TempDir(), "author.key")
	if code, _, stderr := runSigningCommand(t, "keygen", key); code != 0 {
		t.Fatalf("keygen: %s", stderr)
	}
	private, err := signature.ReadPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pig-overlayable")
	if err := os.WriteFile(binary, []byte("a Piglet Binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := signature.Manifest{Piglet: "overlayable", ReleaseVersion: "1.0.0", Target: "test/test", PigVersion: "test", PigletDigest: digestPigletBytes(baked), SourceDigest: resolution.SourceDigest}
	if _, err := signature.Sign(binary, manifest, private); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr := runSigningCommand(t, "verify", binary, "--source", source); code != 0 || !strings.Contains(stdout, "Provenance: matches") {
		t.Fatalf("verify --source: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	data, _ := os.ReadFile(source)
	if err := os.WriteFile(source, []byte(strings.Replace(string(data), "allowOverlays: true", "allowOverlays: false", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := runSigningCommand(t, "verify", binary, "--source", source); code != 1 || !strings.Contains(stdout, "Piglet digest:") {
		t.Fatalf("a source that changed allowOverlays must fail the Piglet digest: code=%d stdout=%q", code, stdout)
	}
}

func skillNames(entries []SkillEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	return names
}
