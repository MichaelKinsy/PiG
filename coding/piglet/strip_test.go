package piglet

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// TestStripRejectsUnknownIDsByName pins that every strip list rejects an ID
// the registrations do not define, and that the error names the ID.
func TestStripRejectsUnknownIDsByName(t *testing.T) {
	for _, tc := range []struct{ field, id string }{
		{"tools", "grepx"},
		{"commands", "/shared"},
		{"commands", "share"},
		{"extensions", "toolSearch"},
		{"features", "mcp-servers"},
	} {
		t.Run(tc.field+"/"+tc.id, func(t *testing.T) {
			_, err := ParseBytes([]byte("name: lean\nstrip:\n  " + tc.field + ": [" + `"` + tc.id + `"` + "]\n"))
			if err == nil || !strings.Contains(err.Error(), `"`+tc.id+`"`) || !strings.Contains(err.Error(), "strip."+tc.field) {
				t.Fatalf("error = %v, want one naming %q", err, tc.id)
			}
		})
	}
}

// TestStripAcceptsKnownIDs pins that each kind accepts a registered ID and
// that StrippedIDs reports them in canonical order with runtime disposition.
func TestStripAcceptsKnownIDs(t *testing.T) {
	p, err := ParseBytes([]byte("name: lean\nstrip:\n  tools: [grep, find]\n  commands: [/share]\n  extensions: [mcp]\n  features: [themes]\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []StrippedID{
		{StripKindTool, "find", StripDispositionRuntime},
		{StripKindTool, "grep", StripDispositionRuntime},
		{StripKindCommand, "/share", StripDispositionRuntime},
		{StripKindExtension, "mcp", StripDispositionBinary},
		{StripKindFeature, "themes", StripDispositionRuntime},
	}
	if got := p.Strip.StrippedIDs(); !slices.Equal(got, want) {
		t.Fatalf("StrippedIDs = %v, want %v", got, want)
	}
	undo := p.Strip.Record()
	if !pigstrip.Has(pigstrip.ListCommands, "/share") || !pigstrip.Has(pigstrip.ListTools, "grep") || !pigstrip.Has(pigstrip.ListFeatures, "themes") || pigstrip.Has(pigstrip.ListCommands, "/export") {
		undo()
		t.Fatalf("Record left the one strip state at commands %v, tools %v, features %v", pigstrip.IDs(pigstrip.ListCommands), pigstrip.IDs(pigstrip.ListTools), pigstrip.IDs(pigstrip.ListFeatures))
	}
	undo()
	if pigstrip.Has(pigstrip.ListCommands, "/share") || pigstrip.Has(pigstrip.ListTools, "grep") {
		t.Fatal("undoing Record left /share or grep stripped")
	}
	if _, err := ParseBytes([]byte("name: lean\nstrip:\n  tools: [grep, grep]\n")); err == nil || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate error = %v", err)
	}
}

// TestStripSkillsFeatureConflictsWithDeclaredSkills pins that stripping the
// skills feature cannot silently drop the Piglet's own skills.
func TestStripSkillsFeatureConflictsWithDeclaredSkills(t *testing.T) {
	if !slices.Contains(pigstrip.Known(pigstrip.ListFeatures), pigstrip.Skills) {
		t.Fatal("the skills feature ID left the generated table; update the conflict check in Validate")
	}
	_, err := ParseBytes([]byte("name: lean\nskills:\n  - name: review\n    content: hi\nstrip:\n  features: [skills]\n"))
	if err == nil || !strings.Contains(err.Error(), `"skills"`) {
		t.Fatalf("error = %v", err)
	}
}

// TestStripFloorRefusesEveryToolAndQuit pins the functional floor: a Piglet
// can't strip every built-in tool and /quit together, on its own or through
// its extends lineage, and the error names the entries. Stripping /quit alone
// or every tool alone stays allowed.
func TestStripFloorRefusesEveryToolAndQuit(t *testing.T) {
	tools := pigstrip.Known(pigstrip.ListTools)
	if !slices.Contains(pigstrip.Known(pigstrip.ListCommands), "/quit") {
		t.Fatal("/quit left the generated commands table; update the floor check in Validate")
	}
	allTools := "[" + strings.Join(tools, ", ") + "]"
	want := "a Piglet can't strip every tool and /quit (strip.tools: " + strings.Join(tools, ", ") + "; strip.commands: /quit); keep at least one tool or /quit"
	refused := func(t *testing.T, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want one containing %q", err, want)
		}
	}
	t.Run("one Piglet", func(t *testing.T) {
		_, err := ParseBytes([]byte("name: lean\nstrip:\n  tools: " + allTools + "\n  commands: [/share, /quit]\n"))
		refused(t, err)
	})
	t.Run("quit alone", func(t *testing.T) {
		if _, err := ParseBytes([]byte("name: lean\nstrip:\n  commands: [/quit]\n")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("quit and all tools but one", func(t *testing.T) {
		if _, err := ParseBytes([]byte("name: lean\nstrip:\n  tools: [" + strings.Join(tools[1:], ", ") + "]\n  commands: [/quit]\n")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("every tool alone", func(t *testing.T) {
		if _, err := ParseBytes([]byte("name: chat\nstrip:\n  tools: " + allTools + "\n  commands: [/share]\n")); err != nil {
			t.Fatal(err)
		}
	})
	dir := t.TempDir()
	writePigletSource(t, dir, "chat", "name: chat\nstrip:\n  tools: "+allTools+"\n")
	t.Run("through extends", func(t *testing.T) {
		_, err := ResolveEffective(writePigletSource(t, dir, "noquit", "name: noquit\nextends:\n  source: local:./chat.yaml\nstrip:\n  commands: [/quit]\n"))
		refused(t, err)
	})
	t.Run("child keeps a tool", func(t *testing.T) {
		path := writePigletSource(t, dir, "keepsread", "name: keepsread\nextends:\n  source: local:./chat.yaml\n  allowWiden: true\n  remove:\n    strip:\n      tools: [read]\nstrip:\n  commands: [/quit]\n")
		resolved, err := ResolveEffective(path)
		if err != nil {
			t.Fatal(err)
		}
		if strip := resolved.Piglet.Strip; strip == nil || slices.Contains(strip.Tools, "read") || !slices.Contains(strip.Commands, "/quit") {
			t.Fatalf("effective strip = %#v", strip)
		}
	})
}

// TestStripUnionsDownTheExtendsChain pins that a child inherits its base's
// strip list, adds its own, and needs allowWiden to re-enable an entry.
func TestStripUnionsDownTheExtendsChain(t *testing.T) {
	dir := t.TempDir()
	writePigletSource(t, dir, "base", "name: base\nstrip:\n  tools: [grep]\n  extensions: [mcp]\n")
	writePigletSource(t, dir, "middle", "name: middle\nextends:\n  source: local:./base.yaml\n")
	child := writePigletSource(t, dir, "child", "name: child\nextends:\n  source: local:./middle.yaml\nstrip:\n  tools: [grep, find]\n  commands: [/share]\n")

	resolved, err := ResolveEffective(child)
	if err != nil {
		t.Fatal(err)
	}
	strip := resolved.Piglet.Strip
	if strip == nil || !slices.Equal(strip.Tools, []string{"grep", "find"}) || !slices.Equal(strip.Commands, []string{"/share"}) || !slices.Equal(strip.Extensions, []string{"mcp"}) {
		t.Fatalf("effective strip = %#v", strip)
	}

	t.Run("re-enable without allowWiden", func(t *testing.T) {
		path := writePigletSource(t, dir, "narrow", "name: narrow\nextends:\n  source: local:./base.yaml\n  remove:\n    strip:\n      tools: [grep]\n")
		if _, err := ResolveEffective(path); err == nil || !strings.Contains(err.Error(), "strip.tools/grep") || !strings.Contains(err.Error(), "allowWiden") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("re-enable with allowWiden", func(t *testing.T) {
		path := writePigletSource(t, dir, "wide", "name: wide\nextends:\n  source: local:./base.yaml\n  allowWiden: true\n  remove:\n    strip:\n      tools: [grep]\n")
		resolved, err := ResolveEffective(path)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Piglet.Strip == nil || slices.Contains(resolved.Piglet.Strip.Tools, "grep") || !slices.Contains(resolved.Piglet.Strip.Extensions, "mcp") {
			t.Fatalf("effective strip = %#v", resolved.Piglet.Strip)
		}
	})
	t.Run("re-enable an absent entry", func(t *testing.T) {
		path := writePigletSource(t, dir, "absent", "name: absent\nextends:\n  source: local:./base.yaml\n  allowWiden: true\n  remove:\n    strip:\n      tools: [find]\n")
		if _, err := ResolveEffective(path); err == nil || !strings.Contains(err.Error(), `remove.strip.tools: "find" is absent`) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("keep, replace and strip in one remove", func(t *testing.T) {
		if err := os.Mkdir(filepath.Join(dir, "tern"), 0o755); err != nil {
			t.Fatal(err)
		}
		writePigletSource(t, dir, "slotted", "name: slotted\nslots:\n  frontend:\n    member: ./tern\nstrip:\n  tools: [grep]\n  extensions: [mcp]\n")
		inherited, err := ResolveEffective(writePigletSource(t, dir, "inherits", "name: inherits\nextends:\n  source: local:./slotted.yaml\n"))
		if err != nil {
			t.Fatal(err)
		}
		if !inherited.Piglet.HasFrontend() || inherited.Piglet.Strip == nil || !slices.Contains(inherited.Piglet.Strip.Extensions, "mcp") {
			t.Fatalf("child does not inherit the frontend and the strip list: slots=%#v strip=%#v", inherited.Piglet.Slots, inherited.Piglet.Strip)
		}
		path := writePigletSource(t, dir, "restored", "name: restored\nextends:\n  source: local:./slotted.yaml\n  allowWiden: true\n  remove:\n    slots: [frontend]\n    strip:\n      extensions: [mcp]\n")
		resolved, err := ResolveEffective(path)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Piglet.HasFrontend() || resolved.Piglet.Strip == nil || !slices.Contains(resolved.Piglet.Strip.Tools, "grep") || slices.Contains(resolved.Piglet.Strip.Extensions, "mcp") {
			t.Fatalf("effective slots=%#v strip=%#v, want PiG's frontend, mcp back and grep still stripped", resolved.Piglet.Slots, resolved.Piglet.Strip)
		}
	})
}

// TestStripChangesEffectiveDigest pins that the strip list is part of the
// effective Piglet identity.
func TestStripChangesEffectiveDigest(t *testing.T) {
	dir := t.TempDir()
	plain, err := ResolveEffective(writePigletSource(t, dir, "plain", "name: lean\n"))
	if err != nil {
		t.Fatal(err)
	}
	stripped, err := ResolveEffective(writePigletSource(t, dir, "stripped", "name: lean\nstrip:\n  tools: [grep]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if plain.EffectiveDigest == stripped.EffectiveDigest {
		t.Fatal("strip list does not change the effective digest")
	}
}

// TestShowListsSlotsAsOneModel pins that `pig piglet show` lists the replaced
// frontend and each stripped ID in one Slots section, named by manifest path.
func TestShowListsSlotsAsOneModel(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "tern"), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "lean.yaml")
	if err := os.WriteFile(source, []byte("name: lean\nslots:\n  frontend:\n    member: ./tern\nstrip:\n  tools: [grep]\n  commands: [/share]\n  extensions: [mcp]\n  features: [experimental-server]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	if code := RunCommand([]string{"piglet", "show", source}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	_, slots, found := strings.Cut(out, "\nSlots:\n  frontend  replaced(")
	if !found || strings.Count(out, "Slots:") != 1 {
		t.Fatalf("show output lacks one Slots section that starts with the frontend:\n%s", out)
	}
	_, slots, _ = strings.Cut(slots, "tern)\n")
	want := "  tools.grep  stripped(runtime)\n  commands./share  stripped(runtime)\n  extensions.mcp  stripped(binary)\n  features.experimental-server  stripped(binary)\n"
	if !strings.HasPrefix(slots, want) {
		t.Errorf("Slots after the frontend = %q, want prefix %q", slots, want)
	}
}

// TestRecordStripConvertsBinaryEntries pins the record-to-show conversion.
func TestRecordStripConvertsBinaryEntries(t *testing.T) {
	got := recordStrip([]pigletartifact.StripEntry{{Kind: "tool", ID: "grep", Disposition: "runtime"}})
	if !slices.Equal(got, []StrippedID{{StripKindTool, "grep", StripDispositionRuntime}}) {
		t.Fatalf("recordStrip = %v", got)
	}
}

// TestStrippedPigletCommandIsNotRegistered pins strip.commands /piglet: the
// in-session extension registers no /piglet command, so the runner that
// feeds autocomplete, getCommands, RPC get_commands and dispatch has none,
// while the extension keeps its --piglet flag and scoping handlers. Without
// the strip /piglet is registered.
func TestStrippedPigletCommandIsNotRegistered(t *testing.T) {
	if !slices.Contains(pigstrip.Known(pigstrip.ListCommands), "/"+InspectCommand) {
		t.Fatalf("/%s is not a generated command strip ID: %v", InspectCommand, pigstrip.Known(pigstrip.ListCommands))
	}
	stock := inproc.NewRunner([]extension.Extension{BuildExtensionWithPiglet(&Piglet{Name: "lean"})}, t.TempDir())
	if _, ok := stock.Command(InspectCommand); !ok {
		t.Fatal("stock /piglet is not registered")
	}
	p, err := ParseBytes([]byte("name: lean\nstrip:\n  commands: [/piglet]\n"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Strip.Record())
	ext := BuildExtensionWithPiglet(p)
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	if _, ok := runner.Command(InspectCommand); ok {
		t.Fatal("stripped /piglet is still registered")
	}
	for _, command := range runner.Commands() {
		if command.Name == InspectCommand {
			t.Fatal("stripped /piglet is still listed")
		}
	}
	if _, ok := ext.Flags["piglet"]; !ok {
		t.Error("stripping /piglet dropped the --piglet flag")
	}
	for _, event := range []string{"session_start", "before_agent_start"} {
		if len(ext.Handlers[event]) == 0 {
			t.Errorf("stripping /piglet dropped the %s handler", event)
		}
	}
}
