package piglet

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// withSyntheticStripIDs adds IDs to the strip table for one test, as a later
// core that ports new built-ins would. Tests that call it must not run in
// parallel.
func withSyntheticStripIDs(t *testing.T, add map[string][]string) {
	t.Helper()
	previous := knownStripIDs
	knownStripIDs = func(list string) []string {
		ids := append(previous(list), add[list]...)
		slices.Sort(ids)
		return ids
	}
	t.Cleanup(func() { knownStripIDs = previous })
}

// without returns the table's IDs of list minus drop, in table order.
func without(list string, drop ...string) []string {
	var out []string
	for _, id := range knownStripIDs(list) {
		if !slices.Contains(drop, id) {
			out = append(out, id)
		}
	}
	return out
}

func keepOf(t *testing.T, strip *StripSpec, list string) []string {
	t.Helper()
	for _, l := range strip.lists() {
		if l.field == list {
			if l.keep == nil {
				t.Fatalf("strip.%s is not in keep mode: %#v", list, strip)
			}
			return *l.keep
		}
	}
	t.Fatalf("no strip list %s", list)
	return nil
}

// TestStripKeepModeParses pins the keep-mode syntax: a keep list strips every
// other ID of its list, an empty keep list keeps nothing, one list is never
// in both modes, keep has no keep key, and keep IDs are checked like deny IDs.
func TestStripKeepModeParses(t *testing.T) {
	p, err := ParseBytes([]byte("name: core\nstrip:\n  commands: [/share]\n  keep:\n    tools: [read, bash, edit, write]\n    extensions: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := without(pigstrip.ListTools, "read", "bash", "edit", "write"); !slices.Equal(p.Strip.Tools, want) {
		t.Fatalf("strip.tools = %v, want %v", p.Strip.Tools, want)
	}
	if want := knownStripIDs(pigstrip.ListExtensions); !slices.Equal(p.Strip.Extensions, want) {
		t.Fatalf("strip.extensions = %v, want every built-in extension %v", p.Strip.Extensions, want)
	}
	if !slices.Equal(p.Strip.Commands, []string{"/share"}) || p.Strip.Features != nil {
		t.Fatalf("deny-mode lists changed: commands %v features %v", p.Strip.Commands, p.Strip.Features)
	}
	if got := p.Strip.KeepLists(); len(got) != 2 || got[0].Field() != "tools" || got[1].Field() != "extensions" || got[1].IDs == nil || len(got[1].IDs) != 0 {
		t.Fatalf("KeepLists = %#v", got)
	}
	for _, id := range p.Strip.StrippedIDs() {
		if id.Kind == StripKindExtension && id.Disposition != StripDispositionBinary {
			t.Fatalf("expanded %s has disposition %s", id.SlotID(), id.Disposition)
		}
	}

	t.Run("round trip keeps the intent", func(t *testing.T) {
		data, err := yaml.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "codemode") || !strings.Contains(string(data), "extensions: []") {
			t.Fatalf("marshalled Piglet writes the expansion, not the keep list:\n%s", data)
		}
		again, err := ParseBytes(data)
		if err != nil {
			t.Fatalf("marshalled keep-mode Piglet does not parse: %v\n%s", err, data)
		}
		if !slices.Equal(again.Strip.StrippedIDs(), p.Strip.StrippedIDs()) || !slices.EqualFunc(again.Strip.KeepLists(), p.Strip.KeepLists(), func(a, b StripIDList) bool {
			return a.Kind == b.Kind && slices.Equal(a.IDs, b.IDs)
		}) {
			t.Fatalf("round trip changed the strip spec: %#v -> %#v", p.Strip, again.Strip)
		}
	})

	for _, tc := range []struct{ name, strip, want string }{
		{"both modes", "  tools: [grep]\n  keep:\n    tools: [read]\n", "strip.tools and strip.keep.tools: a strip list is in deny mode or keep mode, not both"},
		{"keep.keep", "  keep:\n    keep:\n      tools: [read]\n", "field keep not found"},
		{"unknown keep ID", "  keep:\n    extensions: [memoryx]\n", `strip.keep.extensions[0]: unknown extension ID "memoryx"`},
		{"renamed keep ID", "  keep:\n    commands: [/sharex]\n", `strip.keep.commands[0]: unknown command ID "/sharex"`},
		{"duplicate keep ID", "  keep:\n    tools: [read, read]\n", `strip.keep.tools[1] duplicates "read"`},
		{"keep and skills", "  keep:\n    features: []\n", `strip.features: "skills" disables the declared skills`},
		// A null keep list would decode as deny mode and strip nothing, the
		// opposite of keeping nothing.
		{"null keep list", "  keep:\n    extensions:\n", "strip.keep.extensions must be a list of the IDs it keeps; write [] to keep nothing"},
		{"null keep", "  keep:\n", "strip.keep must be a mapping of keep-mode lists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "name: core\nstrip:\n" + tc.strip
			if tc.name == "keep and skills" {
				body = "name: core\nskills:\n  - name: review\n    content: hi\nstrip:\n" + tc.strip
			}
			if _, err := ParseBytes([]byte(body)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}

	t.Run("an unexpanded spec does not validate", func(t *testing.T) {
		keep := []string{"read"}
		unexpanded := &Piglet{Name: "core", Strip: &StripSpec{Keep: &StripKeep{Tools: &keep}}}
		if err := unexpanded.Validate(); err == nil || !strings.Contains(err.Error(), "strip.tools: keep mode is not expanded") {
			t.Fatalf("Validate() = %v", err)
		}
	})
}

// TestStripKeepModeStripsNewTableIDs pins the point of keep mode: an ID a
// later core adds to a keep-mode list's part of the table is stripped, while
// a deny-mode list leaves it enabled.
func TestStripKeepModeStripsNewTableIDs(t *testing.T) {
	withSyntheticStripIDs(t, map[string][]string{pigstrip.ListExtensions: {"memory"}, pigstrip.ListCommands: {"/plan"}})
	p, err := ParseBytes([]byte("name: core\nstrip:\n  commands: [/share]\n  keep:\n    extensions: [mcp]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.Strip.Extensions, "memory") || slices.Contains(p.Strip.Extensions, "mcp") {
		t.Fatalf("keep-mode extensions = %v, want the new memory stripped and mcp kept", p.Strip.Extensions)
	}
	if slices.Contains(p.Strip.Commands, "/plan") {
		t.Fatalf("deny-mode commands = %v strip the new /plan", p.Strip.Commands)
	}
}

// TestStripKeepModeExtendsAlgebra pins every row of the piglet-derivatives
// design table 2.3 and the keep-to-deny refusal.
func TestStripKeepModeExtendsAlgebra(t *testing.T) {
	dir := t.TempDir()
	writePigletSource(t, dir, "keepbase", "name: keepbase\nstrip:\n  keep:\n    extensions: [mcp, codemode]\n")
	writePigletSource(t, dir, "denybase", "name: denybase\nstrip:\n  extensions: [mcp]\n")
	resolve := func(t *testing.T, name, body string) (*StripSpec, error) {
		t.Helper()
		resolved, err := ResolveEffective(writePigletSource(t, dir, name, body))
		if err != nil {
			return nil, err
		}
		return resolved.Piglet.Strip, nil
	}
	wantKeep := func(t *testing.T, strip *StripSpec, keep ...string) {
		t.Helper()
		if got := keepOf(t, strip, pigstrip.ListExtensions); !slices.Equal(got, keep) {
			t.Fatalf("keep.extensions = %v, want %v", got, keep)
		}
		if want := without(pigstrip.ListExtensions, keep...); !slices.Equal(strip.Extensions, want) {
			t.Fatalf("strip.extensions = %v, want the expansion %v", strip.Extensions, want)
		}
	}

	t.Run("keep base, child strips x: keep K minus x", func(t *testing.T) {
		strip, err := resolve(t, "row1", "name: row1\nextends:\n  source: local:./keepbase.yaml\nstrip:\n  extensions: [mcp]\n")
		if err != nil {
			t.Fatal(err)
		}
		wantKeep(t, strip, "codemode")
	})
	t.Run("keep base, child keeps K2 inside K: keep K and K2", func(t *testing.T) {
		strip, err := resolve(t, "row2", "name: row2\nextends:\n  source: local:./keepbase.yaml\nstrip:\n  keep:\n    extensions: [mcp]\n")
		if err != nil {
			t.Fatal(err)
		}
		wantKeep(t, strip, "mcp")
	})
	t.Run("keep base, child keeps an ID outside K: error pointing to remove.strip", func(t *testing.T) {
		_, err := resolve(t, "row2err", "name: row2err\nextends:\n  source: local:./keepbase.yaml\nstrip:\n  keep:\n    extensions: [mcp, tool-search]\n")
		if err == nil || !strings.Contains(err.Error(), `strip.keep.extensions: "tool-search" is stripped by the base; re-enable it with extends.remove.strip.extensions and extends.allowWiden`) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("deny base D, child keeps K2: keep K2 minus D", func(t *testing.T) {
		strip, err := resolve(t, "row3", "name: row3\nextends:\n  source: local:./denybase.yaml\nstrip:\n  keep:\n    extensions: [codemode]\n")
		if err != nil {
			t.Fatal(err)
		}
		wantKeep(t, strip, "codemode")
	})
	t.Run("deny base D, child keeps an ID of D: same error", func(t *testing.T) {
		_, err := resolve(t, "row3err", "name: row3err\nextends:\n  source: local:./denybase.yaml\nstrip:\n  keep:\n    extensions: [mcp]\n")
		if err == nil || !strings.Contains(err.Error(), `strip.keep.extensions: "mcp" is stripped by the base; re-enable it with extends.remove.strip.extensions`) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("keep base, remove.strip x needs allowWiden", func(t *testing.T) {
		_, err := resolve(t, "row4narrow", "name: row4narrow\nextends:\n  source: local:./keepbase.yaml\n  remove:\n    strip:\n      extensions: [tool-search]\n")
		if err == nil || !strings.Contains(err.Error(), "strip.extensions/tool-search") || !strings.Contains(err.Error(), "allowWiden") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("keep base, remove.strip x with allowWiden: keep K plus x", func(t *testing.T) {
		strip, err := resolve(t, "row4", "name: row4\nextends:\n  source: local:./keepbase.yaml\n  allowWiden: true\n  remove:\n    strip:\n      extensions: [tool-search]\n")
		if err != nil {
			t.Fatal(err)
		}
		wantKeep(t, strip, "codemode", "mcp", "tool-search")
	})
	t.Run("keep base, remove.strip of a kept ID: absent", func(t *testing.T) {
		_, err := resolve(t, "row4absent", "name: row4absent\nextends:\n  source: local:./keepbase.yaml\n  allowWiden: true\n  remove:\n    strip:\n      extensions: [mcp]\n")
		if err == nil || !strings.Contains(err.Error(), `remove.strip.extensions: "mcp" is absent`) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("keep base, nothing in the child: keep inherited down the chain and new IDs stay stripped", func(t *testing.T) {
		writePigletSource(t, dir, "row5", "name: row5\nextends:\n  source: local:./keepbase.yaml\nstrip:\n  commands: [/share]\n")
		withSyntheticStripIDs(t, map[string][]string{pigstrip.ListExtensions: {"memory"}})
		strip, err := resolve(t, "row5grandchild", "name: row5grandchild\nextends:\n  source: local:./row5.yaml\n")
		if err != nil {
			t.Fatal(err)
		}
		wantKeep(t, strip, "codemode", "mcp")
		if !slices.Contains(strip.Extensions, "memory") || !slices.Equal(strip.Commands, []string{"/share"}) {
			t.Fatalf("grandchild strip = %#v, want the new memory extension stripped", strip)
		}
	})
	t.Run("a child can't return a keep-mode list to deny mode", func(t *testing.T) {
		for _, body := range []string{
			"name: back\nextends:\n  source: local:./keepbase.yaml\n  allowWiden: true\n  remove:\n    strip:\n      keep:\n        extensions: []\n",
			"name: back\nextends:\n  source: local:./keepbase.yaml\n  allowWiden: true\n  remove:\n    strip:\n      keep:\n        extensions: [mcp]\n",
		} {
			_, err := resolve(t, "back", body)
			if err == nil || !strings.Contains(err.Error(), "extends.remove.strip.keep.extensions: a child can't return the keep-mode list extensions to deny mode") {
				t.Fatalf("error = %v", err)
			}
		}
	})
}

// TestStripKeepModeMeetsTheFloor pins that the functional floor reads the
// expanded deny list: keeping no tool and stripping /quit is refused, in one
// file and through extends; keeping one tool is allowed.
func TestStripKeepModeMeetsTheFloor(t *testing.T) {
	want := "a Piglet can't strip every tool and /quit"
	if _, err := ParseBytes([]byte("name: core\nstrip:\n  commands: [/quit]\n  keep:\n    tools: []\n")); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("one file: error = %v", err)
	}
	if _, err := ParseBytes([]byte("name: core\nstrip:\n  commands: [/quit]\n  keep:\n    tools: [read]\n")); err != nil {
		t.Fatalf("keeping read: %v", err)
	}
	if _, err := ParseBytes([]byte("name: chat\nstrip:\n  keep:\n    tools: []\n")); err != nil {
		t.Fatalf("keeping no tool without stripping /quit: %v", err)
	}
	dir := t.TempDir()
	writePigletSource(t, dir, "chat", "name: chat\nstrip:\n  keep:\n    tools: []\n")
	if _, err := ResolveEffective(writePigletSource(t, dir, "noquit", "name: noquit\nextends:\n  source: local:./chat.yaml\nstrip:\n  commands: [/quit]\n")); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("through extends: error = %v", err)
	}
	writePigletSource(t, dir, "reads", "name: reads\nstrip:\n  commands: [/quit]\n  keep:\n    tools: [read]\n")
	if _, err := ResolveEffective(writePigletSource(t, dir, "noread", "name: noread\nextends:\n  source: local:./reads.yaml\nstrip:\n  tools: [read]\n")); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("child strips the base's last kept tool: error = %v", err)
	}
}

func tableRecordLists(table []StripIDList) []artifact.StripIDs {
	out := make([]artifact.StripIDs, len(table))
	for i, list := range table {
		out[i] = artifact.StripIDs{Kind: list.Kind, IDs: slices.Clone(list.IDs)}
	}
	return out
}

// TestStripDeltaReportNamesNewTableIDs pins the strip delta report with
// synthetic new IDs: the keep-mode lists leave them out, a deny-mode list now
// includes them, and the report reads the modes from the record when no
// current spec is at hand.
func TestStripDeltaReportNamesNewTableIDs(t *testing.T) {
	previous := &artifact.Binary{
		PigVersion: "0.4.2",
		StripTable: tableRecordLists(StripTable()),
		StripKeep:  []artifact.StripIDs{{Kind: StripKindExtension, IDs: []string{}}, {Kind: StripKindFeature, IDs: []string{"themes"}}},
	}
	if delta, ok := stripDeltaSince(previous, nil); !ok || !slices.Equal(delta.Lines(), []string{"Strip table: PiG 0.4.2 → " + pigversion.PigVersion + " adds no built-in IDs"}) {
		t.Fatalf("unchanged table: %v %v", ok, delta.Lines())
	}
	if _, ok := stripDeltaSince(&artifact.Binary{PigVersion: "0.4.2"}, nil); ok {
		t.Fatal("a record without a strip table reports a delta")
	}

	withSyntheticStripIDs(t, map[string][]string{
		pigstrip.ListExtensions: {"memory"},
		pigstrip.ListFeatures:   {"voice"},
		pigstrip.ListCommands:   {"/plan"},
	})
	current, err := ParseBytes([]byte("name: core\nstrip:\n  commands: [/share]\n  keep:\n    extensions: []\n    features: [themes]\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Strip table: PiG 0.4.2 → " + pigversion.PigVersion + " adds 3 built-in IDs",
		"  left out (keep mode):     extensions.memory, features.voice",
		"  now included (deny mode): commands./plan",
	}
	for name, spec := range map[string]*StripSpec{"current spec": current.Strip, "record modes": nil} {
		delta, ok := stripDeltaSince(previous, spec)
		if !ok || !slices.Equal(delta.Lines(), want) {
			t.Fatalf("%s: report =\n%s\nwant\n%s", name, strings.Join(delta.Lines(), "\n"), strings.Join(want, "\n"))
		}
	}
	// A deny-mode Piglet that already strips the new command does not
	// include it, and a keep list that keeps a new ID does not leave it out.
	current, err = ParseBytes([]byte("name: core\nstrip:\n  commands: [/share, /plan]\n  keep:\n    extensions: [memory]\n    features: [themes]\n"))
	if err != nil {
		t.Fatal(err)
	}
	delta, _ := stripDeltaSince(previous, current.Strip)
	if !slices.Equal(delta.LeftOut, []string{"features.voice"}) || len(delta.NowIncluded) != 0 || delta.Added != 3 {
		t.Fatalf("delta = %#v", delta)
	}
	if lines := delta.Lines(); lines[2] != "  now included (deny mode): none" {
		t.Fatalf("empty list renders %q", lines[2])
	}
}

// TestShowPrintsKeepModeAndTheStripDelta pins `pig piglet show` for a
// keep-mode Piglet with a Binary record: each keep-mode list above the
// expanded rows, the record's keep-mode lists, and the strip delta report
// against the record's table (built here without /trust, so this core adds
// it).
func TestShowPrintsKeepModeAndTheStripDelta(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Chdir(t.TempDir())
	table := tableRecordLists(StripTable())
	for i := range table {
		if table[i].Kind == StripKindCommand {
			table[i].IDs = slices.DeleteFunc(table[i].IDs, func(id string) bool { return id == "/trust" })
		}
	}
	pigletsDir := filepath.Join(home, "piglets")
	if err := os.MkdirAll(pigletsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(pigletsDir, "core.yaml")
	if err := os.WriteFile(source, []byte("name: core\nstrip:\n  keep:\n    tools: [read, bash, edit, write]\n    extensions: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, err := pigletSourceDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	writeRecordFixtureWith(t, home, "core", strings.TrimPrefix(digest, "sha256:"), func(input *artifact.BinaryInput) {
		input.StripKeep = []artifact.StripIDs{{Kind: StripKindExtension, IDs: []string{}}}
		input.StripTable = table
	})
	var stdout, stderr strings.Builder
	if code := RunCommand([]string{"piglet", "show", "core"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"\nSlots:\n  tools  keep([read, bash, edit, write])\n  extensions  keep([])\n  tools.find  stripped(runtime)\n",
		"  extensions.codemode  stripped(binary)\n",
		"\nStrip table: PiG 0.1.1 → " + pigversion.PigVersion + " adds 1 built-in ID\n  left out (keep mode):     none\n  now included (deny mode): commands./trust\n",
		"    Slots:\n      extensions  keep([])\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show output lacks %q:\n%s", want, out)
		}
	}
	stdout.Reset()
	if code := RunCommand([]string{"piglet", "show", "core", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	for _, want := range []string{`"stripDelta": [`, `"  now included (deny mode): commands./trust"`, `"stripKeep": [`, `"keep": {`} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("show --json lacks %q:\n%s", want, stdout.String())
		}
	}
}
